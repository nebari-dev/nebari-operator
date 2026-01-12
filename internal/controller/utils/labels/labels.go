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
	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

// StandardLabels returns the standard set of labels for NicApp-owned resources.
// These labels follow Kubernetes recommended label conventions.
func StandardLabels(nicApp *appsv1.NicApp) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "nicapp",
		"app.kubernetes.io/instance":   nicApp.Name,
		"app.kubernetes.io/managed-by": "nic-operator",
		"app.kubernetes.io/component":  "application",
	}
}

// LabelsWithComponent returns standard labels with a custom component value.
func LabelsWithComponent(nicApp *appsv1.NicApp, component string) map[string]string {
	labels := StandardLabels(nicApp)
	labels["app.kubernetes.io/component"] = component
	return labels
}

// MergeLabels merges multiple label maps, with later maps taking precedence.
func MergeLabels(labelMaps ...map[string]string) map[string]string {
	result := make(map[string]string)
	for _, labels := range labelMaps {
		for k, v := range labels {
			result[k] = v
		}
	}
	return result
}
