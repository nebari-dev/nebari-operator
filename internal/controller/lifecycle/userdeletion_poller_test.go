/*
Copyright (c) 2026, OpenTeams

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
	"github.com/nebari-dev/nebari-operator/internal/keycloak"
)

// fakeDeletionSource stands in for the Keycloak client. It returns canned
// events and records the since it was asked for, so tests can assert on the
// cursor the poller derived.
type fakeDeletionSource struct {
	events     []keycloak.UserDeletionEvent
	err        error
	calledWith []time.Time
}

func (f *fakeDeletionSource) ListUserDeletions(_ context.Context, since time.Time) ([]keycloak.UserDeletionEvent, error) {
	f.calledWith = append(f.calledWith, since)
	if f.err != nil {
		return nil, f.err
	}
	return f.events, nil
}

var _ = Describe("KeycloakDeletionPoller", func() {
	const retention = 7 * 24 * time.Hour

	var (
		source *fakeDeletionSource
		poller *KeycloakDeletionPoller
	)

	// Timestamps inside the retention window so a first-run poll, whose since
	// is now minus retention, sees them as new. Second precision, since
	// metav1.Time drops sub-seconds.
	older := time.Now().Add(-2 * time.Hour).Truncate(time.Second).UTC()
	newer := time.Now().Add(-1 * time.Hour).Truncate(time.Second).UTC()

	newEvent := func(userID string, deletedAt time.Time) keycloak.UserDeletionEvent {
		return keycloak.UserDeletionEvent{
			UserID:    userID,
			Username:  "user-" + userID,
			DeletedAt: deletedAt,
			DeletedBy: keycloak.DeletionActor{
				ClientID:  "admin-cli",
				UserID:    "admin-id",
				IPAddress: "10.0.0.1",
			},
			AdminEventID: "event-" + userID,
		}
	}

	// registerCleanup deletes the markers the poller is expected to create,
	// since UserDeletion is cluster-scoped and names must not leak between specs.
	registerCleanup := func(userIDs ...string) {
		DeferCleanup(func() {
			for _, id := range userIDs {
				marker := &lifecyclev1alpha1.UserDeletion{ObjectMeta: metav1.ObjectMeta{Name: id}}
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, marker))).To(Succeed())
			}
		})
	}

	getCursor := func() (time.Time, bool) {
		var cm corev1.ConfigMap
		key := types.NamespacedName{Name: poller.CursorName, Namespace: poller.CursorNamespace}
		err := k8sClient.Get(ctx, key, &cm)
		if apierrors.IsNotFound(err) {
			return time.Time{}, false
		}
		Expect(err).NotTo(HaveOccurred())
		t, err := time.Parse(time.RFC3339Nano, cm.Data[cursorKey])
		Expect(err).NotTo(HaveOccurred())
		return t, true
	}

	seedCursor := func(t time.Time) {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: poller.CursorName, Namespace: poller.CursorNamespace},
			Data:       map[string]string{cursorKey: t.Format(time.RFC3339Nano)},
		}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())
	}

	BeforeEach(func() {
		source = &fakeDeletionSource{}
		poller = &KeycloakDeletionPoller{
			Client:         k8sClient,
			Events:         source,
			PollInterval:   time.Minute,
			EventRetention: retention,
			// Unique per spec so cursors from one spec never leak into another.
			CursorName:      fmt.Sprintf("cursor-%d-%d", GinkgoParallelProcess(), time.Now().UnixNano()),
			CursorNamespace: testNamespace,
		}

		DeferCleanup(func() {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: poller.CursorName, Namespace: poller.CursorNamespace}}
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, cm))).To(Succeed())
		})
	})

	It("creates a marker per event and advances the cursor to the newest one", func() {
		source.events = []keycloak.UserDeletionEvent{
			newEvent("poller-a", newer),
			newEvent("poller-b", older),
		}
		registerCleanup("poller-a", "poller-b")

		Expect(poller.poll(ctx)).To(Succeed())

		var marker lifecyclev1alpha1.UserDeletion
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "poller-b"}, &marker)).To(Succeed())
		Expect(marker.Spec.UserID).To(Equal("poller-b"))
		Expect(marker.Spec.Username).To(Equal("user-poller-b"))
		Expect(marker.Spec.DeletedAt.Time).To(BeTemporally("==", older))
		Expect(marker.Spec.AdminEventID).To(Equal("event-poller-b"))
		Expect(marker.Spec.DeletedBy).NotTo(BeNil())
		Expect(marker.Spec.DeletedBy.UserID).To(Equal("admin-id"))

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "poller-a"}, &marker)).To(Succeed())

		cursor, found := getCursor()
		Expect(found).To(BeTrue())
		Expect(cursor).To(BeTemporally("==", newer), "cursor must be the newest event, regardless of order")
	})

	It("treats a replayed event as already processed", func() {
		source.events = []keycloak.UserDeletionEvent{newEvent("poller-replay", older)}
		registerCleanup("poller-replay")

		Expect(poller.poll(ctx)).To(Succeed())
		Expect(poller.poll(ctx)).To(Succeed(), "AlreadyExists must not fail the poll")

		var list lifecyclev1alpha1.UserDeletionList
		Expect(k8sClient.List(ctx, &list)).To(Succeed())
		count := 0
		for _, m := range list.Items {
			if m.Name == "poller-replay" {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})

	It("looks back over the retention window when there is no cursor", func() {
		before := time.Now()
		Expect(poller.poll(ctx)).To(Succeed())

		Expect(source.calledWith).To(HaveLen(1))
		Expect(source.calledWith[0]).To(BeTemporally("~", before.Add(-retention), 5*time.Second))
	})

	It("asks for events since the stored cursor", func() {
		seedCursor(older)

		Expect(poller.poll(ctx)).To(Succeed())

		Expect(source.calledWith).To(HaveLen(1))
		Expect(source.calledWith[0]).To(BeTemporally("==", older))
	})

	It("never asks for events older than the retention window", func() {
		// A cursor this old means the newest event Keycloak has is past the
		// retention and its tombstone may be gone. Asking since the cursor
		// would replay it.
		stale := time.Now().Add(-2 * retention)
		seedCursor(stale)
		before := time.Now()

		Expect(poller.poll(ctx)).To(Succeed())

		Expect(source.calledWith).To(HaveLen(1))
		Expect(source.calledWith[0]).To(BeTemporally("~", before.Add(-retention), 5*time.Second))
	})

	It("keeps the cursor where it was when no events are returned", func() {
		seedCursor(older)

		Expect(poller.poll(ctx)).To(Succeed())

		cursor, found := getCursor()
		Expect(found).To(BeTrue())
		Expect(cursor).To(BeTemporally("==", older))
	})

	It("fails on a corrupt cursor without calling the source", func() {
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: poller.CursorName, Namespace: poller.CursorNamespace},
			Data:       map[string]string{cursorKey: "yesterday"},
		}
		Expect(k8sClient.Create(ctx, cm)).To(Succeed())

		Expect(poller.poll(ctx)).NotTo(Succeed())
		Expect(source.calledWith).To(BeEmpty())
	})

	It("leaves the cursor untouched when the source fails", func() {
		seedCursor(older)
		source.err = errors.New("keycloak unreachable")

		Expect(poller.poll(ctx)).NotTo(Succeed())

		cursor, found := getCursor()
		Expect(found).To(BeTrue())
		Expect(cursor).To(BeTemporally("==", older))
	})

	It("keeps millisecond precision in the cursor", func() {
		// Keycloak stamps events in milliseconds. A cursor truncated to seconds
		// would re-fetch every event in the same second as the newest one.
		precise := time.Now().Add(-time.Hour).Truncate(time.Millisecond).UTC()
		if precise.Nanosecond() == 0 {
			precise = precise.Add(time.Millisecond)
		}
		source.events = []keycloak.UserDeletionEvent{newEvent("poller-precise", precise)}
		registerCleanup("poller-precise")

		Expect(poller.poll(ctx)).To(Succeed())

		cursor, found := getCursor()
		Expect(found).To(BeTrue())
		Expect(cursor).To(BeTemporally("==", precise))
		Expect(cursor.Nanosecond()).NotTo(BeZero(), "sub-second part must survive the round trip")
	})

	It("does not advance the cursor when a marker cannot be created", func() {
		// An uppercase name violates DNS subdomain rules, so the create is
		// rejected by the API server and the poll must stop before the cursor.
		source.events = []keycloak.UserDeletionEvent{newEvent("Not-A-Valid-Name", newer)}

		Expect(poller.poll(ctx)).NotTo(Succeed())

		_, found := getCursor()
		Expect(found).To(BeFalse())
	})
})
