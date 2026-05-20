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

package routing

import (
	"context"
	"testing"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

func newStreamingScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = appsv1.AddToScheme(s)
	_ = egv1alpha1.AddToScheme(s)
	return s
}

func newNebariApp(streaming bool, publicRoutes []appsv1.RouteMatch) *appsv1.NebariApp {
	const (
		name      = "app"
		namespace = "default"
	)
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			UID:       types.UID("uid-" + name),
		},
		Spec: appsv1.NebariAppSpec{
			Hostname: name + ".example.com",
			Service:  appsv1.ServiceReference{Name: name + "-svc", Port: 80},
			Routing: &appsv1.RoutingConfig{
				Streaming:    streaming,
				PublicRoutes: publicRoutes,
			},
		},
	}
	return app
}

func TestStreamingReconciler_DisabledCreatesNoPolicy(t *testing.T) {
	scheme := newStreamingScheme()
	app := newNebariApp(false, nil)

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r := &StreamingReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if err := r.Reconcile(context.Background(), app); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	policy := &egv1alpha1.BackendTrafficPolicy{}
	err := client.Get(context.Background(), types.NamespacedName{
		Name:      naming.BackendTrafficPolicyName(app),
		Namespace: app.Namespace,
	}, policy)
	if err == nil {
		t.Fatal("expected no BackendTrafficPolicy when streaming is disabled")
	}

	if cond := conditions.GetCondition(app, appsv1.ConditionTypeStreamingReady); cond != nil {
		t.Errorf("expected no StreamingReady condition when streaming is disabled, got %+v", cond)
	}
}

func TestStreamingReconciler_EnabledCreatesPolicy(t *testing.T) {
	scheme := newStreamingScheme()
	app := newNebariApp(true, nil)

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r := &StreamingReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if err := r.Reconcile(context.Background(), app); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	policy := &egv1alpha1.BackendTrafficPolicy{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name:      naming.BackendTrafficPolicyName(app),
		Namespace: app.Namespace,
	}, policy); err != nil {
		t.Fatalf("expected BackendTrafficPolicy to be created: %v", err)
	}

	if got, want := len(policy.Spec.TargetRefs), 1; got != want {
		t.Errorf("expected %d targetRefs, got %d", want, got)
	}
	if string(policy.Spec.TargetRefs[0].Name) != naming.HTTPRouteName(app) {
		t.Errorf("expected targetRef to point at %s, got %s", naming.HTTPRouteName(app), policy.Spec.TargetRefs[0].Name)
	}
	if policy.Spec.Timeout == nil || policy.Spec.Timeout.HTTP == nil {
		t.Fatalf("expected HTTP timeout on policy spec")
	}
	if got := string(*policy.Spec.Timeout.HTTP.RequestTimeout); got != streamingRequestTimeout {
		t.Errorf("expected requestTimeout=%q, got %q", streamingRequestTimeout, got)
	}
	if got := string(*policy.Spec.Timeout.HTTP.ConnectionIdleTimeout); got != streamingIdleTimeout {
		t.Errorf("expected connectionIdleTimeout=%q, got %q", streamingIdleTimeout, got)
	}

	if !conditions.IsConditionTrue(app, appsv1.ConditionTypeStreamingReady) {
		t.Errorf("expected StreamingReady=True, got %+v",
			conditions.GetCondition(app, appsv1.ConditionTypeStreamingReady))
	}
}

func TestStreamingReconciler_EnabledWithPublicRoutesTargetsBoth(t *testing.T) {
	scheme := newStreamingScheme()
	app := newNebariApp(true, []appsv1.RouteMatch{
		{PathPrefix: "/healthz"},
	})

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r := &StreamingReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if err := r.Reconcile(context.Background(), app); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	policy := &egv1alpha1.BackendTrafficPolicy{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name:      naming.BackendTrafficPolicyName(app),
		Namespace: app.Namespace,
	}, policy); err != nil {
		t.Fatalf("expected BackendTrafficPolicy to be created: %v", err)
	}

	if got, want := len(policy.Spec.TargetRefs), 2; got != want {
		t.Fatalf("expected %d targetRefs (main + public), got %d", want, got)
	}
	wantNames := []string{naming.HTTPRouteName(app), naming.PublicHTTPRouteName(app)}
	for i, ref := range policy.Spec.TargetRefs {
		if string(ref.Name) != wantNames[i] {
			t.Errorf("targetRef %d: expected %s, got %s", i, wantNames[i], ref.Name)
		}
	}
}

func TestStreamingReconciler_TransitionToDisabledDeletesPolicy(t *testing.T) {
	scheme := newStreamingScheme()
	app := newNebariApp(true, nil)

	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app).Build()
	r := &StreamingReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	// First reconcile: streaming on, policy exists.
	if err := r.Reconcile(context.Background(), app); err != nil {
		t.Fatalf("unexpected error on enable: %v", err)
	}

	// Flip to disabled and reconcile again.
	app.Spec.Routing.Streaming = false
	if err := r.Reconcile(context.Background(), app); err != nil {
		t.Fatalf("unexpected error on disable: %v", err)
	}

	policy := &egv1alpha1.BackendTrafficPolicy{}
	err := client.Get(context.Background(), types.NamespacedName{
		Name:      naming.BackendTrafficPolicyName(app),
		Namespace: app.Namespace,
	}, policy)
	if err == nil {
		t.Fatal("expected BackendTrafficPolicy to be deleted after streaming was disabled")
	}

	if cond := conditions.GetCondition(app, appsv1.ConditionTypeStreamingReady); cond != nil {
		t.Errorf("expected StreamingReady condition removed after disable, got %+v", cond)
	}
}

func TestStreamingReconciler_RefusesForeignPolicy(t *testing.T) {
	scheme := newStreamingScheme()
	app := newNebariApp(true, nil)

	// Pre-existing policy with NO owner reference to this NebariApp.
	foreign := &egv1alpha1.BackendTrafficPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.BackendTrafficPolicyName(app),
			Namespace: app.Namespace,
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(app, foreign).Build()
	r := &StreamingReconciler{Client: client, Scheme: scheme, Recorder: record.NewFakeRecorder(10)}

	if err := r.Reconcile(context.Background(), app); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cond := conditions.GetCondition(app, appsv1.ConditionTypeStreamingReady)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "ForeignPolicyExists" {
		t.Errorf("expected StreamingReady=False ForeignPolicyExists, got %+v", cond)
	}

	// Confirm we did NOT overwrite the foreign policy with our spec.
	got := &egv1alpha1.BackendTrafficPolicy{}
	if err := client.Get(context.Background(), types.NamespacedName{
		Name:      naming.BackendTrafficPolicyName(app),
		Namespace: app.Namespace,
	}, got); err != nil {
		t.Fatalf("foreign policy should still exist: %v", err)
	}
	if len(got.Spec.TargetRefs) != 0 {
		t.Errorf("expected foreign policy spec untouched (empty targetRefs), got %d entries", len(got.Spec.TargetRefs))
	}
}
