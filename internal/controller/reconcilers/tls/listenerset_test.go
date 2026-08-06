/*
Copyright 2026, OpenTeams.

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

package tls

import (
	"context"
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func lsTestApp() *appsv1.NebariApp {
	return &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "team-a", UID: "uid-myapp"},
		Spec:       appsv1.NebariAppSpec{Hostname: "myapp.example.com"},
	}
}

// listenerSetWithConditions builds a ListenerSet carrying the given Accepted /
// Programmed condition statuses, used to seed the fake client's status.
func listenerSetWithConditions(app *appsv1.NebariApp, accepted, programmed metav1.ConditionStatus) *gatewayv1.ListenerSet {
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.ListenerSetName(app), Namespace: app.Namespace},
		Status: gatewayv1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{Type: listenerSetConditionAccepted, Status: accepted, Reason: "T", LastTransitionTime: metav1.Now()},
				{Type: listenerSetConditionProgrammed, Status: programmed, Reason: "T", LastTransitionTime: metav1.Now()},
			},
		},
	}
}

func TestReconcileListenerSet(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	secret := naming.CertificateSecretName(app)
	if err := r.reconcileListenerSet(context.Background(), app, secret); err != nil {
		t.Fatalf("reconcileListenerSet: %v", err)
	}

	ls := &gatewayv1.ListenerSet{}
	if err := c.Get(context.Background(), types.NamespacedName{
		Name: naming.ListenerSetName(app), Namespace: app.Namespace,
	}, ls); err != nil {
		t.Fatalf("ListenerSet not created: %v", err)
	}

	// Lives in the app namespace.
	if ls.Namespace != "team-a" {
		t.Errorf("ListenerSet namespace = %q, want team-a", ls.Namespace)
	}
	// parentRef points at the shared Gateway in the Gateway namespace.
	if string(ls.Spec.ParentRef.Name) != naming.GatewayName(app) {
		t.Errorf("parentRef.name = %q, want %q", ls.Spec.ParentRef.Name, naming.GatewayName(app))
	}
	if ls.Spec.ParentRef.Namespace == nil || string(*ls.Spec.ParentRef.Namespace) != constants.GatewayNamespace {
		t.Errorf("parentRef.namespace = %v, want %q", ls.Spec.ParentRef.Namespace, constants.GatewayNamespace)
	}
	// Single HTTPS Terminate listener with a same-namespace cert ref.
	if len(ls.Spec.Listeners) != 1 {
		t.Fatalf("listeners = %d, want 1", len(ls.Spec.Listeners))
	}
	l := ls.Spec.Listeners[0]
	if string(l.Name) != naming.ListenerName(app) {
		t.Errorf("listener name = %q, want %q", l.Name, naming.ListenerName(app))
	}
	if l.Port != 443 || l.Protocol != gatewayv1.HTTPSProtocolType {
		t.Errorf("listener = %d/%s, want 443/HTTPS", l.Port, l.Protocol)
	}
	if l.TLS == nil || l.TLS.Mode == nil || *l.TLS.Mode != gatewayv1.TLSModeTerminate {
		t.Errorf("listener TLS mode not Terminate: %+v", l.TLS)
	}
	if len(l.TLS.CertificateRefs) != 1 || string(l.TLS.CertificateRefs[0].Name) != secret {
		t.Errorf("cert ref = %+v, want name %q", l.TLS.CertificateRefs, secret)
	}
	if l.TLS.CertificateRefs[0].Namespace != nil {
		t.Errorf("cert ref must be same-namespace (no explicit namespace), got %v", *l.TLS.CertificateRefs[0].Namespace)
	}
	// Owner-referenced to the NebariApp so GC removes it with the app.
	if len(ls.OwnerReferences) != 1 || ls.OwnerReferences[0].Name != app.Name {
		t.Errorf("ownerReferences = %+v, want single ref to %q", ls.OwnerReferences, app.Name)
	}
}

func TestIsListenerSetProgrammed(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()

	tests := []struct {
		name string
		seed *gatewayv1.ListenerSet
		want bool
	}{
		{name: "missing ListenerSet", seed: nil, want: false},
		{name: "accepted only", seed: listenerSetWithConditions(app, metav1.ConditionTrue, metav1.ConditionFalse), want: false},
		{name: "programmed only", seed: listenerSetWithConditions(app, metav1.ConditionFalse, metav1.ConditionTrue), want: false},
		{name: "accepted and programmed", seed: listenerSetWithConditions(app, metav1.ConditionTrue, metav1.ConditionTrue), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(scheme)
			if tt.seed != nil {
				b = b.WithObjects(tt.seed)
			}
			r := &TLSReconciler{Client: b.Build(), Scheme: scheme, Recorder: record.NewFakeRecorder(10)}
			got, err := r.isListenerSetProgrammed(context.Background(), app)
			if err != nil {
				t.Fatalf("isListenerSetProgrammed: %v", err)
			}
			if got != tt.want {
				t.Errorf("programmed = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReconcileTLSAttachment_StaysLegacyUntilProgrammed asserts the load-bearing
// safety property: while the ListenerSet is not Programmed, the attachment keeps
// the legacy shared-Gateway listener in place and reports useListenerSet=false.
func TestReconcileTLSAttachment_StaysLegacyUntilProgrammed(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()
	gw := newGateway(naming.GatewayName(app)) // shared Gateway, no per-app listener yet
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, gw).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	useLS, err := r.reconcileTLSAttachment(context.Background(), app, naming.CertificateSecretName(app))
	if err != nil {
		t.Fatalf("reconcileTLSAttachment: %v", err)
	}
	if useLS {
		t.Fatal("expected useListenerSet=false while ListenerSet not Programmed")
	}
	// Legacy shared-Gateway listener must have been added.
	got := &gatewayv1.Gateway{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: gw.Name, Namespace: gw.Namespace}, got); err != nil {
		t.Fatalf("get gateway: %v", err)
	}
	found := false
	for _, l := range got.Spec.Listeners {
		if string(l.Name) == naming.ListenerName(app) {
			found = true
		}
	}
	if !found {
		t.Error("legacy shared-Gateway listener not present in Phase A")
	}
}

// TestReconcileTLSAttachment_CutsOverWhenProgrammed asserts that once the
// ListenerSet is Programmed the attachment reports useListenerSet=true and
// removes the legacy shared-Gateway listener.
func TestReconcileTLSAttachment_CutsOverWhenProgrammed(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()

	// Shared Gateway already carries this app's legacy listener.
	legacy := gatewayv1.Listener{
		Name: gatewayv1.SectionName(naming.ListenerName(app)), Port: 443, Protocol: gatewayv1.HTTPSProtocolType,
	}
	gw := newGateway(naming.GatewayName(app), legacy)
	programmed := listenerSetWithConditions(app, metav1.ConditionTrue, metav1.ConditionTrue)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, gw, programmed).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	useLS, err := r.reconcileTLSAttachment(context.Background(), app, naming.CertificateSecretName(app))
	if err != nil {
		t.Fatalf("reconcileTLSAttachment: %v", err)
	}
	if !useLS {
		t.Fatal("expected useListenerSet=true once ListenerSet Programmed")
	}
	// Legacy shared-Gateway listener must have been removed on cutover.
	got := &gatewayv1.Gateway{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: gw.Name, Namespace: gw.Namespace}, got); err != nil {
		t.Fatalf("get gateway: %v", err)
	}
	for _, l := range got.Spec.Listeners {
		if string(l.Name) == naming.ListenerName(app) {
			t.Error("legacy shared-Gateway listener should be removed after cutover")
		}
	}
}
