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

package conditions

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

func TestSetCondition(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-app",
			Namespace:  "default",
			Generation: 1,
		},
	}

	SetCondition(nicApp, "Ready", metav1.ConditionTrue, "AllGood", "Everything is working")

	if len(nicApp.Status.Conditions) != 1 {
		t.Errorf("expected 1 condition, got %d", len(nicApp.Status.Conditions))
	}

	cond := nicApp.Status.Conditions[0]
	if cond.Type != "Ready" {
		t.Errorf("expected type 'Ready', got '%s'", cond.Type)
	}
	if cond.Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %s", cond.Status)
	}
	if cond.Reason != "AllGood" {
		t.Errorf("expected reason 'AllGood', got '%s'", cond.Reason)
	}
}

func TestGetCondition(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-app",
			Namespace:  "default",
			Generation: 1,
		},
		Status: appsv1.NicAppStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	cond := GetCondition(nicApp, "Ready")
	if cond == nil {
		t.Error("expected to find condition, got nil")
	}
	if cond.Type != "Ready" {
		t.Errorf("expected type 'Ready', got '%s'", cond.Type)
	}

	cond = GetCondition(nicApp, "NonExistent")
	if cond != nil {
		t.Error("expected nil for non-existent condition")
	}
}

func TestIsConditionTrue(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Status: appsv1.NicAppStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Progressing", Status: metav1.ConditionFalse},
			},
		},
	}

	if !IsConditionTrue(nicApp, "Ready") {
		t.Error("expected Ready condition to be true")
	}
	if IsConditionTrue(nicApp, "Progressing") {
		t.Error("expected Progressing condition to not be true")
	}
	if IsConditionTrue(nicApp, "NonExistent") {
		t.Error("expected NonExistent condition to not be true")
	}
}

func TestIsConditionFalse(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Status: appsv1.NicAppStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Progressing", Status: metav1.ConditionFalse},
			},
		},
	}

	if !IsConditionFalse(nicApp, "Progressing") {
		t.Error("expected Progressing condition to be false")
	}
	if IsConditionFalse(nicApp, "Ready") {
		t.Error("expected Ready condition to not be false")
	}
	if IsConditionFalse(nicApp, "NonExistent") {
		t.Error("expected NonExistent condition to not be false")
	}
}

func TestIsConditionUnknown(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Status: appsv1.NicAppStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Unknown", Status: metav1.ConditionUnknown},
			},
		},
	}

	if !IsConditionUnknown(nicApp, "Unknown") {
		t.Error("expected Unknown condition to be unknown")
	}
	if IsConditionUnknown(nicApp, "Ready") {
		t.Error("expected Ready condition to not be unknown")
	}
}
