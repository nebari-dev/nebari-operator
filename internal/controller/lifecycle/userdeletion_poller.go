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
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
	"github.com/nebari-dev/nebari-operator/internal/keycloak"
)

// DeletionEventsSource lists user deletions recorded by the identity provider.
// Implemented by *keycloak.Client.
type DeletionEventsSource interface {
	ListUserDeletions(ctx context.Context, since time.Time) ([]keycloak.UserDeletionEvent, error)
}

// KeycloakDeletionPoller polls Keycloak for user deletions and creates a
// UserDeletion for each one. It is a manager Runnable, not a controller: no
// object in the cluster triggers it, time does. It only runs on the leader so
// two replicas do not poll and advance the cursor concurrently.
type KeycloakDeletionPoller struct {
	client.Client
	Events          DeletionEventsSource
	PollInterval    time.Duration
	EventRetention  time.Duration
	CursorName      string
	CursorNamespace string
}

// Compile-time checks that the poller satisfies the manager interfaces.
var (
	_ manager.Runnable               = &KeycloakDeletionPoller{}
	_ manager.LeaderElectionRunnable = &KeycloakDeletionPoller{}
)

// Start polls once immediately, then on every tick, until the context is
// cancelled. A failed poll is logged and retried on the next tick rather than
// returned, since returning an error stops the manager.
func (p *KeycloakDeletionPoller) Start(ctx context.Context) error {
	log := logf.Log.WithName("userdeletion-poller")
	log.Info("starting", "interval", p.PollInterval)

	ticker := time.NewTicker(p.PollInterval)
	defer ticker.Stop()

	for {
		if err := p.poll(ctx); err != nil {
			log.Error(err, "poll failed, will retry on next tick")
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			log.Info("stopping")
			return nil
		}
	}
}

// NeedLeaderElection makes the manager start the poller only on the leader.
func (p *KeycloakDeletionPoller) NeedLeaderElection() bool {
	return true
}

// cursorKey is the ConfigMap data key holding the last processed event time.
const cursorKey = "lastEventTime"

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;create;update,namespace=nebari-operator-system
// +kubebuilder:rbac:groups=lifecycle.nebari.dev,resources=userdeletions,verbs=create

// poll reads the cursor, fetches deletions since it, creates a UserDeletion
// per event, and advances the cursor to the newest event seen.
func (p *KeycloakDeletionPoller) poll(ctx context.Context) error {
	log := logf.Log.WithName("userdeletion-poller")

	since, found, err := p.readCursor(ctx)
	if err != nil {
		return err
	}
	// Never ask for events older than the retention window. Without a cursor
	// that is the whole window. With one, this keeps the newest event, which
	// sits exactly at the cursor and is re-read every poll, from recreating its
	// marker once the tombstone is gone and Keycloak still has the event.
	oldest := time.Now().Add(-p.EventRetention)
	if !found {
		since = oldest
		log.Info("no cursor found, using the event retention window", "since", since)
	} else if since.Before(oldest) {
		since = oldest
	}

	events, err := p.Events.ListUserDeletions(ctx, since)
	if err != nil {
		return err
	}

	newest := since
	for _, e := range events {
		err := p.createUserDeletion(ctx, e)
		// A marker the API server rejects would be rejected on every poll and
		// hold the cursor back, hiding every newer deletion behind it. Log it
		// and move on. Anything else is retried on the next poll.
		if apierrors.IsInvalid(err) {
			log.Error(err, "skipping a deletion whose marker the API server rejects, it will not be cleaned up",
				"userID", e.UserID, "eventID", e.AdminEventID)
		} else if err != nil {
			return err
		}
		if e.DeletedAt.After(newest) {
			newest = e.DeletedAt
		}
	}

	if len(events) > 0 {
		log.Info("processed user deletions", "count", len(events), "newest", newest)
	}

	return p.writeCursor(ctx, newest)
}

// readCursor returns the last processed event time. found is false when the
// ConfigMap or its key does not exist, which is the first run or a cursor that
// was deleted manually. A value that does not parse is an error rather than a
// silent reset.
func (p *KeycloakDeletionPoller) readCursor(ctx context.Context) (time.Time, bool, error) {
	var cm corev1.ConfigMap
	key := client.ObjectKey{Namespace: p.CursorNamespace, Name: p.CursorName}
	if err := p.Get(ctx, key, &cm); err != nil {
		if apierrors.IsNotFound(err) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("failed to read cursor %s: %w", key, err)
	}

	raw, ok := cm.Data[cursorKey]
	if !ok || raw == "" {
		return time.Time{}, false, nil
	}

	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("cursor %s has invalid %s %q: %w", key, cursorKey, raw, err)
	}
	return t, true, nil
}

// writeCursor stores t as the last processed event time, creating the
// ConfigMap if it does not exist.
func (p *KeycloakDeletionPoller) writeCursor(ctx context.Context, t time.Time) error {
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: p.CursorName, Namespace: p.CursorNamespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, p.Client, cm, func() error {
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		// Keycloak timestamps are milliseconds. Storing them whole keeps the
		// re-fetch window on the next poll to the events at this exact instant.
		cm.Data[cursorKey] = t.UTC().Format(time.RFC3339Nano)
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to write cursor %s/%s: %w", p.CursorNamespace, p.CursorName, err)
	}
	return nil
}

// createUserDeletion creates the marker for one event. The marker is named by
// the Keycloak user id, so a replayed event collides with the existing marker
// and AlreadyExists is treated as success.
func (p *KeycloakDeletionPoller) createUserDeletion(ctx context.Context, e keycloak.UserDeletionEvent) error {
	marker := &lifecyclev1alpha1.UserDeletion{
		ObjectMeta: metav1.ObjectMeta{Name: e.UserID},
		Spec: lifecyclev1alpha1.UserDeletionSpec{
			UserID:    e.UserID,
			Username:  e.Username,
			DeletedAt: metav1.NewTime(e.DeletedAt),
			DeletedBy: &lifecyclev1alpha1.DeletionActor{
				ClientID:  e.DeletedBy.ClientID,
				UserID:    e.DeletedBy.UserID,
				IPAddress: e.DeletedBy.IPAddress,
			},
			AdminEventID: e.AdminEventID,
		},
	}

	err := p.Create(ctx, marker)
	if apierrors.IsAlreadyExists(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to create UserDeletion %s: %w", e.UserID, err)
	}
	return nil
}
