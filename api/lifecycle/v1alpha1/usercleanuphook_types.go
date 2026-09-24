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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// CleanupStage identifies the moment in a user's deletion when a hook runs.
// +kubebuilder:validation:Enum=disable;delete
type CleanupStage string

const (
	// CleanupStageDisable runs as soon as the deletion is detected.
	CleanupStageDisable CleanupStage = "disable"
	// CleanupStageDelete runs once the grace period has elapsed.
	CleanupStageDelete CleanupStage = "delete"
)

// UserCleanupHookSpec defines the desired state of UserCleanupHook
type UserCleanupHookSpec struct {
	// stage is the moment in the user's deletion when this hook runs.
	// "disable" runs as soon as the deletion is detected. "delete" runs once the grace period has elapsed.
	// +required
	Stage CleanupStage `json:"stage"`

	// template is the pod the operator wraps in a Job when the stage is due.
	// The operator injects the user identifiers as env vars into every container.
	// restartPolicy defaults to Never when omitted.
	// +required
	// +kubebuilder:validation:XValidation:rule="!has(self.spec.restartPolicy) || self.spec.restartPolicy in ['Never', 'OnFailure']",message="template.spec.restartPolicy must be Never or OnFailure"
	// +kubebuilder:validation:XValidation:rule="size(self.spec.containers) > 0",message="template.spec.containers must not be empty"
	// +kubebuilder:validation:XValidation:rule="self.spec.containers.all(c, has(c.image) && c.image != '')",message="every container needs an image"
	Template corev1.PodTemplateSpec `json:"template"`

	// backoffLimit is the number of failed pods the Job tolerates before it is marked Failed.
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=0
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// activeDeadlineSeconds is the maximum time the Job may run before it is killed and marked Failed.
	// +optional
	// +kubebuilder:default=900
	// +kubebuilder:validation:Minimum=1
	ActiveDeadlineSeconds *int64 `json:"activeDeadlineSeconds,omitempty"`

	// ttlSecondsAfterFinished is how long a finished Job and its pods are kept before Kubernetes deletes them.
	// The floor of one hour ensures the operator records the Job outcome before the Job disappears.
	// +optional
	// +kubebuilder:default=604800
	// +kubebuilder:validation:Minimum=3600
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// dryRun is passed to the script as NEBARI_CLEANUP_DRY_RUN. When true the script should
	// log what it would do without doing it.
	// +optional
	// +kubebuilder:default=false
	DryRun bool `json:"dryRun,omitempty"`
}

// Condition types for UserCleanupHook
const (
	// ConditionTypeAccepted indicates whether the pod template renders to a valid Job.
	// True means the API server accepted a dry-run create of the rendered Job.
	// False means it was rejected and the reason and message carry the API server error.
	// Unknown means the operator could not evaluate the template.
	ConditionTypeAccepted = "Accepted"
)

// Condition reasons for UserCleanupHook
const (
	// ReasonTemplateValid is set when the rendered Job passed API server validation.
	ReasonTemplateValid = "TemplateValid"

	// ReasonTemplateInvalid is set when the API server rejected the rendered Job.
	ReasonTemplateInvalid = "TemplateInvalid"

	// ReasonValidationUnavailable is set when the dry-run request itself failed,
	// for example because the API server was unreachable, so the template could
	// not be evaluated either way.
	ReasonValidationUnavailable = "ValidationUnavailable"
)

// UserCleanupHookStatus defines the observed state of UserCleanupHook.
type UserCleanupHookStatus struct {
	// conditions represent the current state of the UserCleanupHook resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Condition types:
	// - "Accepted": the pod template renders to a valid Job
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed for this UserCleanupHook.
	// It corresponds to the UserCleanupHook's generation, which is updated on mutation by the API Server.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Stage",type=string,JSONPath=`.spec.stage`
// +kubebuilder:printcolumn:name="Accepted",type=string,JSONPath=`.status.conditions[?(@.type=="Accepted")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// UserCleanupHook is the Schema for the usercleanuphooks API
type UserCleanupHook struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of UserCleanupHook
	// +required
	Spec UserCleanupHookSpec `json:"spec"`

	// status defines the observed state of UserCleanupHook
	// +optional
	Status UserCleanupHookStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// UserCleanupHookList contains a list of UserCleanupHook
type UserCleanupHookList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []UserCleanupHook `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &UserCleanupHook{}, &UserCleanupHookList{})
		return nil
	})
}
