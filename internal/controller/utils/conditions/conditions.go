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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

// SetCondition sets or updates a condition in the NebariApp status.
// If a condition with the same type already exists, it will be updated.
// The LastTransitionTime is only updated when the status changes.
func SetCondition(nebariApp *appsv1.NebariApp, conditionType string,
	status metav1.ConditionStatus, reason, message string) {

	// Check if condition exists and if status is changing
	existingCondition := meta.FindStatusCondition(nebariApp.Status.Conditions, conditionType)

	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: nebariApp.Generation,
		Reason:             reason,
		Message:            message,
	}

	// Only set LastTransitionTime if condition doesn't exist or status is changing
	// When status is not changing, leave LastTransitionTime unset (zero value)
	// and meta.SetStatusCondition will preserve the existing value
	if existingCondition == nil || existingCondition.Status != status {
		condition.LastTransitionTime = metav1.Now()
	}

	meta.SetStatusCondition(&nebariApp.Status.Conditions, condition)
}

// GetCondition returns the condition with the given type from the NebariApp status.
// Returns nil if the condition does not exist.
func GetCondition(nebariApp *appsv1.NebariApp, conditionType string) *metav1.Condition {
	return meta.FindStatusCondition(nebariApp.Status.Conditions, conditionType)
}

// IsConditionTrue checks if a condition exists and is set to True.
func IsConditionTrue(nebariApp *appsv1.NebariApp, conditionType string) bool {
	condition := GetCondition(nebariApp, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// IsConditionFalse checks if a condition exists and is set to False.
func IsConditionFalse(nebariApp *appsv1.NebariApp, conditionType string) bool {
	condition := GetCondition(nebariApp, conditionType)
	return condition != nil && condition.Status == metav1.ConditionFalse
}

// IsConditionUnknown checks if a condition exists and is set to Unknown.
func IsConditionUnknown(nebariApp *appsv1.NebariApp, conditionType string) bool {
	condition := GetCondition(nebariApp, conditionType)
	return condition != nil && condition.Status == metav1.ConditionUnknown
}
