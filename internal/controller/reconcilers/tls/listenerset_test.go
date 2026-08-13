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
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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

// lsProgrammed builds a ListenerSet whose set-level Programmed condition is True.
func lsProgrammed(app *appsv1.NebariApp) *gatewayv1.ListenerSet {
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.ListenerSetName(app), Namespace: app.Namespace},
		Status: gatewayv1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.ListenerSetConditionProgrammed), Status: metav1.ConditionTrue,
					Reason: string(gatewayv1.ListenerSetReasonProgrammed), LastTransitionTime: metav1.Now()},
			},
		},
	}
}

// lsNotAllowed builds a ListenerSet the Gateway refuses to attach (e.g.
// spec.allowedListeners unset): set-level Accepted/Programmed False with reason
// NotAllowed, and no per-listener status at all (Envoy Gateway does not evaluate
// the listeners in this state).
func lsNotAllowed(app *appsv1.NebariApp) *gatewayv1.ListenerSet {
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.ListenerSetName(app), Namespace: app.Namespace},
		Status: gatewayv1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.ListenerSetConditionAccepted), Status: metav1.ConditionFalse,
					Reason: string(gatewayv1.ListenerSetReasonNotAllowed), LastTransitionTime: metav1.Now()},
				{Type: string(gatewayv1.ListenerSetConditionProgrammed), Status: metav1.ConditionFalse,
					Reason: string(gatewayv1.ListenerSetReasonNotAllowed), LastTransitionTime: metav1.Now()},
			},
		},
	}
}

// lsHostnameConflict builds a ListenerSet blocked only by a hostname conflict with
// our own legacy listener: set-level ListenersNotValid, and the app's per-listener
// entry Conflicted=True/HostnameConflict with ResolvedRefs set per refsResolved.
func lsHostnameConflict(app *appsv1.NebariApp, refsResolved bool) *gatewayv1.ListenerSet {
	refs := metav1.ConditionTrue
	if !refsResolved {
		refs = metav1.ConditionFalse
	}
	return &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.ListenerSetName(app), Namespace: app.Namespace},
		Status: gatewayv1.ListenerSetStatus{
			Conditions: []metav1.Condition{
				{Type: string(gatewayv1.ListenerSetConditionAccepted), Status: metav1.ConditionFalse,
					Reason: string(gatewayv1.ListenerSetReasonListenersNotValid), LastTransitionTime: metav1.Now()},
			},
			Listeners: []gatewayv1.ListenerEntryStatus{
				{
					Name: gatewayv1.SectionName(naming.ListenerName(app)),
					Conditions: []metav1.Condition{
						{Type: string(gatewayv1.ListenerConditionConflicted), Status: metav1.ConditionTrue,
							Reason: string(gatewayv1.ListenerReasonHostnameConflict), LastTransitionTime: metav1.Now()},
						{Type: string(gatewayv1.ListenerConditionResolvedRefs), Status: refs,
							Reason: "R", LastTransitionTime: metav1.Now()},
					},
				},
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

// TestShouldCutOver covers the reason-aware cutover decision: cut over once the
// ListenerSet is Programmed, or when the only thing blocking it is a hostname
// conflict with our own legacy listener (with refs resolving); stay on the legacy
// listener otherwise, in particular when the Gateway refuses the attachment
// (NotAllowed) since removing the legacy listener there would strand the app.
func TestShouldCutOver(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()

	noStatus := &gatewayv1.ListenerSet{
		ObjectMeta: metav1.ObjectMeta{Name: naming.ListenerSetName(app), Namespace: app.Namespace},
	}

	tests := []struct {
		name string
		seed *gatewayv1.ListenerSet
		want bool
	}{
		{name: "missing ListenerSet", seed: nil, want: false},
		{name: "no status yet", seed: noStatus, want: false},
		{name: "programmed", seed: lsProgrammed(app), want: true},
		{name: "not allowed (allowedListeners unset)", seed: lsNotAllowed(app), want: false},
		{name: "hostname conflict with our legacy listener, refs resolved", seed: lsHostnameConflict(app, true), want: true},
		{name: "hostname conflict but refs unresolved", seed: lsHostnameConflict(app, false), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(scheme)
			if tt.seed != nil {
				b = b.WithObjects(tt.seed)
			}
			r := &TLSReconciler{Client: b.Build(), Scheme: scheme, Recorder: record.NewFakeRecorder(10)}
			got, err := r.shouldCutOver(context.Background(), app)
			if err != nil {
				t.Fatalf("shouldCutOver: %v", err)
			}
			if got != tt.want {
				t.Errorf("cutover = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestReconcileTLSAttachment_StaysLegacyBeforeStatus asserts that with a
// freshly-created ListenerSet carrying no status yet, the attachment keeps the
// legacy shared-Gateway listener in place and reports useListenerSet=false.
func TestReconcileTLSAttachment_StaysLegacyBeforeStatus(t *testing.T) {
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
		t.Fatal("expected useListenerSet=false before the ListenerSet reports usable status")
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
		t.Error("legacy shared-Gateway listener not present before cutover")
	}
}

// TestReconcileTLSAttachment_StaysLegacyWhenNotAllowed asserts that when the
// Gateway refuses the attachment (allowedListeners unset), the attachment keeps
// the legacy listener and does NOT cut over, so the app is not stranded.
func TestReconcileTLSAttachment_StaysLegacyWhenNotAllowed(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()
	legacy := gatewayv1.Listener{
		Name: gatewayv1.SectionName(naming.ListenerName(app)), Port: 443, Protocol: gatewayv1.HTTPSProtocolType,
	}
	gw := newGateway(naming.GatewayName(app), legacy)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, gw, lsNotAllowed(app)).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	useLS, err := r.reconcileTLSAttachment(context.Background(), app, naming.CertificateSecretName(app))
	if err != nil {
		t.Fatalf("reconcileTLSAttachment: %v", err)
	}
	if useLS {
		t.Fatal("expected useListenerSet=false when the Gateway refuses the attachment (NotAllowed)")
	}
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
		t.Error("legacy shared-Gateway listener must be kept when NotAllowed")
	}
}

// TestReconcileTLSAttachment_CutsOverWhenProgrammed asserts that once the
// ListenerSet is Programmed the attachment reports useListenerSet=true and
// removes the legacy shared-Gateway listener.
func TestReconcileTLSAttachment_CutsOverWhenProgrammed(t *testing.T) {
	assertCutover(t, lsProgrammed(lsTestApp()))
}

// TestReconcileTLSAttachment_CutsOverOnHostnameConflict asserts the migration
// case: when the ListenerSet is blocked only by a hostname conflict with our own
// legacy listener, the attachment removes that legacy listener (freeing the tuple
// so the ListenerSet can program) and reports useListenerSet=true.
func TestReconcileTLSAttachment_CutsOverOnHostnameConflict(t *testing.T) {
	assertCutover(t, lsHostnameConflict(lsTestApp(), true))
}

// assertCutover runs reconcileTLSAttachment against a shared Gateway that already
// carries the app's legacy listener plus the given seeded ListenerSet, and asserts
// the app cuts over: useListenerSet=true and the legacy listener removed.
func assertCutover(t *testing.T, seededLS *gatewayv1.ListenerSet) {
	t.Helper()
	scheme := newScheme()
	app := lsTestApp()
	legacy := gatewayv1.Listener{
		Name: gatewayv1.SectionName(naming.ListenerName(app)), Port: 443, Protocol: gatewayv1.HTTPSProtocolType,
	}
	gw := newGateway(naming.GatewayName(app), legacy)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, gw, seededLS).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	useLS, err := r.reconcileTLSAttachment(context.Background(), app, naming.CertificateSecretName(app))
	if err != nil {
		t.Fatalf("reconcileTLSAttachment: %v", err)
	}
	if !useLS {
		t.Fatal("expected useListenerSet=true on cutover")
	}
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

// TestReconcileUserProvidedTLS_RemovesStaleListenerSet asserts that switching an
// app to a user-provided TLS secret removes any ListenerSet a prior cert-manager
// reconcile cut over to (so it stops claiming the hostname and detaching the
// route), and reports UseListenerSet=false.
func TestReconcileUserProvidedTLS_RemovesStaleListenerSet(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()
	app.Spec.Routing = &appsv1.RoutingConfig{
		TLS: &appsv1.RoutingTLSConfig{SecretName: "user-tls"},
	}
	// A ListenerSet a prior cert-manager reconcile created and cut over to.
	staleLS := lsProgrammed(app)
	// The user-provided secret lives in the Gateway namespace.
	userSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "user-tls", Namespace: constants.GatewayNamespace},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{corev1.TLSCertKey: []byte("crt"), corev1.TLSPrivateKeyKey: []byte("key")},
	}
	gw := newGateway(naming.GatewayName(app))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, gw, staleLS, userSecret).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	res, err := r.ReconcileTLS(context.Background(), app)
	if err != nil {
		t.Fatalf("ReconcileTLS: %v", err)
	}
	if res == nil || res.UseListenerSet {
		t.Fatalf("expected non-nil result with UseListenerSet=false, got %+v", res)
	}
	// The stale ListenerSet must be deleted so it no longer claims the hostname.
	err = c.Get(context.Background(), types.NamespacedName{
		Name: naming.ListenerSetName(app), Namespace: app.Namespace,
	}, &gatewayv1.ListenerSet{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected stale ListenerSet to be deleted, got err=%v", err)
	}
}

// TestReconcileTLS_DisabledRemovesStaleListenerSet asserts that disabling TLS on
// an app that had cut over tears down the ListenerSet, so it stops terminating
// HTTPS and detaching the route from the shared Gateway.
func TestReconcileTLS_DisabledRemovesStaleListenerSet(t *testing.T) {
	scheme := newScheme()
	app := lsTestApp()
	disabled := false
	app.Spec.Routing = &appsv1.RoutingConfig{TLS: &appsv1.RoutingTLSConfig{Enabled: &disabled}}
	staleLS := lsProgrammed(app) // a ListenerSet from when TLS was enabled and cut over
	gw := newGateway(naming.GatewayName(app))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, gw, staleLS).Build()
	r := &TLSReconciler{Client: c, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if _, err := r.ReconcileTLS(context.Background(), app); err != nil {
		t.Fatalf("ReconcileTLS: %v", err)
	}
	err := c.Get(context.Background(), types.NamespacedName{
		Name: naming.ListenerSetName(app), Namespace: app.Namespace,
	}, &gatewayv1.ListenerSet{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected ListenerSet removed when TLS disabled, got err=%v", err)
	}
}
