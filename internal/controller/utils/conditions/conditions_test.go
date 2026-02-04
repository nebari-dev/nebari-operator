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

package conditions

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

func TestSetCondition(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-app",
			Namespace:  "default",
			Generation: 1,
		},
	}

	SetCondition(nebariApp, "Ready", metav1.ConditionTrue, "AllGood", "Everything is working")

	if len(nebariApp.Status.Conditions) != 1 {
		t.Errorf("expected 1 condition, got %d", len(nebariApp.Status.Conditions))
	}

	cond := nebariApp.Status.Conditions[0]
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
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-app",
			Namespace:  "default",
			Generation: 1,
		},
		Status: appsv1.NebariAppStatus{
			Conditions: []metav1.Condition{
				{
					Type:   "Ready",
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	cond := GetCondition(nebariApp, "Ready")
	if cond == nil {
		t.Error("expected to find condition, got nil")
	}
	if cond.Type != "Ready" {
		t.Errorf("expected type 'Ready', got '%s'", cond.Type)
	}

	cond = GetCondition(nebariApp, "NonExistent")
	if cond != nil {
		t.Error("expected nil for non-existent condition")
	}
}

func TestIsConditionTrue(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Status: appsv1.NebariAppStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Progressing", Status: metav1.ConditionFalse},
			},
		},
	}

	if !IsConditionTrue(nebariApp, "Ready") {
		t.Error("expected Ready condition to be true")
	}
	if IsConditionTrue(nebariApp, "Progressing") {
		t.Error("expected Progressing condition to not be true")
	}
	if IsConditionTrue(nebariApp, "NonExistent") {
		t.Error("expected NonExistent condition to not be true")
	}
}

func TestIsConditionFalse(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Status: appsv1.NebariAppStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Progressing", Status: metav1.ConditionFalse},
			},
		},
	}

	if !IsConditionFalse(nebariApp, "Progressing") {
		t.Error("expected Progressing condition to be false")
	}
	if IsConditionFalse(nebariApp, "Ready") {
		t.Error("expected Ready condition to not be false")
	}
	if IsConditionFalse(nebariApp, "NonExistent") {
		t.Error("expected NonExistent condition to not be false")
	}
}

func TestIsConditionUnknown(t *testing.T) {
	nebariApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Status: appsv1.NebariAppStatus{
			Conditions: []metav1.Condition{
				{Type: "Ready", Status: metav1.ConditionTrue},
				{Type: "Unknown", Status: metav1.ConditionUnknown},
			},
		},
	}

	if !IsConditionUnknown(nebariApp, "Unknown") {
		t.Error("expected Unknown condition to be unknown")
	}
	if IsConditionUnknown(nebariApp, "Ready") {
		t.Error("expected Ready condition to not be unknown")
	}
}
