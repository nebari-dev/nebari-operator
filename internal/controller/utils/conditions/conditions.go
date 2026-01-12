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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

// SetCondition sets or updates a condition in the NicApp status.
// If a condition with the same type already exists, it will be updated.
// The LastTransitionTime is automatically set to the current time.
func SetCondition(nicApp *appsv1.NicApp, conditionType string,
	status metav1.ConditionStatus, reason, message string) {

	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: nicApp.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	}

	meta.SetStatusCondition(&nicApp.Status.Conditions, condition)
}

// GetCondition returns the condition with the given type from the NicApp status.
// Returns nil if the condition does not exist.
func GetCondition(nicApp *appsv1.NicApp, conditionType string) *metav1.Condition {
	return meta.FindStatusCondition(nicApp.Status.Conditions, conditionType)
}

// IsConditionTrue checks if a condition exists and is set to True.
func IsConditionTrue(nicApp *appsv1.NicApp, conditionType string) bool {
	condition := GetCondition(nicApp, conditionType)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// IsConditionFalse checks if a condition exists and is set to False.
func IsConditionFalse(nicApp *appsv1.NicApp, conditionType string) bool {
	condition := GetCondition(nicApp, conditionType)
	return condition != nil && condition.Status == metav1.ConditionFalse
}

// IsConditionUnknown checks if a condition exists and is set to Unknown.
func IsConditionUnknown(nicApp *appsv1.NicApp, conditionType string) bool {
	condition := GetCondition(nicApp, conditionType)
	return condition != nil && condition.Status == metav1.ConditionUnknown
}
