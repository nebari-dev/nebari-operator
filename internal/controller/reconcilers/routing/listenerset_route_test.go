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
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TestRouteParentRef_TargetSelection verifies that generated HTTPRoutes attach to
// the per-app ListenerSet (in the NebariApp namespace) once cut over, and to the
// shared Gateway (in the Gateway namespace) otherwise.
func TestRouteParentRef_TargetSelection(t *testing.T) {
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "team-a"},
		Spec:       appsv1.NebariAppSpec{Hostname: "myapp.example.com"},
	}
	section := gatewayv1.SectionName(naming.ListenerName(app))

	t.Run("legacy shared Gateway", func(t *testing.T) {
		ref := routeParentRef(app, naming.GatewayName(app), section, false)
		if string(ref.Name) != naming.GatewayName(app) {
			t.Errorf("name = %q, want %q", ref.Name, naming.GatewayName(app))
		}
		if ref.Namespace == nil || string(*ref.Namespace) != constants.GatewayNamespace {
			t.Errorf("namespace = %v, want %q", ref.Namespace, constants.GatewayNamespace)
		}
		if ref.Kind != nil && string(*ref.Kind) != "Gateway" {
			t.Errorf("kind = %v, want Gateway/nil", ref.Kind)
		}
	})

	t.Run("per-app ListenerSet after cutover", func(t *testing.T) {
		ref := routeParentRef(app, naming.GatewayName(app), section, true)
		if ref.Kind == nil || string(*ref.Kind) != "ListenerSet" {
			t.Errorf("kind = %v, want ListenerSet", ref.Kind)
		}
		if string(ref.Name) != naming.ListenerSetName(app) {
			t.Errorf("name = %q, want %q", ref.Name, naming.ListenerSetName(app))
		}
		if ref.Namespace == nil || string(*ref.Namespace) != app.Namespace {
			t.Errorf("namespace = %v, want %q (app namespace)", ref.Namespace, app.Namespace)
		}
		if ref.SectionName == nil || string(*ref.SectionName) != string(section) {
			t.Errorf("sectionName = %v, want %q", ref.SectionName, section)
		}
	})
}
