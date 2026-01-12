/*
Copyright 2025.

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

package labels

import (
	"reflect"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

func TestStandardLabels(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	result := StandardLabels(nicApp)

	expected := map[string]string{
		"app.kubernetes.io/name":       "nicapp",
		"app.kubernetes.io/instance":   "test-app",
		"app.kubernetes.io/managed-by": "nic-operator",
		"app.kubernetes.io/component":  "application",
	}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestLabelsWithComponent(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
	}

	result := LabelsWithComponent(nicApp, "routing")

	expected := map[string]string{
		"app.kubernetes.io/name":       "nicapp",
		"app.kubernetes.io/instance":   "test-app",
		"app.kubernetes.io/managed-by": "nic-operator",
		"app.kubernetes.io/component":  "routing",
	}

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("expected %v, got %v", expected, result)
	}
}

func TestMergeLabels(t *testing.T) {
	tests := []struct {
		name     string
		inputs   []map[string]string
		expected map[string]string
	}{
		{
			name: "Merge with empty",
			inputs: []map[string]string{
				{},
				{"key1": "value1"},
			},
			expected: map[string]string{"key1": "value1"},
		},
		{
			name: "Merge with override",
			inputs: []map[string]string{
				{"key1": "value1", "key2": "value2"},
				{"key2": "newvalue2", "key3": "value3"},
			},
			expected: map[string]string{"key1": "value1", "key2": "newvalue2", "key3": "value3"},
		},
		{
			name: "Merge multiple",
			inputs: []map[string]string{
				{"key1": "value1"},
				{"key2": "value2"},
				{"key3": "value3"},
			},
			expected: map[string]string{"key1": "value1", "key2": "value2", "key3": "value3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MergeLabels(tt.inputs...)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
