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

package namespace

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsManaged(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name      string
		labels    map[string]string
		missing   bool
		want      bool
		wantError bool
	}{
		{name: "labeled true", labels: map[string]string{ManagedNamespaceLabel: "true"}, want: true},
		{name: "labeled false", labels: map[string]string{ManagedNamespaceLabel: "false"}},
		{name: "no labels"},
		{name: "namespace missing", missing: true, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(scheme)
			if !tt.missing {
				b = b.WithObjects(&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "ns", Labels: tt.labels},
				})
			}

			got, err := IsManaged(context.Background(), b.Build(), "ns")
			if (err != nil) != tt.wantError {
				t.Fatalf("error: got %v, want error=%v", err, tt.wantError)
			}
			if got != tt.want {
				t.Errorf("managed: got %v, want %v", got, tt.want)
			}
		})
	}
}
