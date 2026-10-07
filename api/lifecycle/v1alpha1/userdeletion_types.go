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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// DeletionActor identifies who performed the deletion in Keycloak.
type DeletionActor struct {
	// clientId is the Keycloak client the deletion was made through.
	// +optional
	ClientID string `json:"clientId,omitempty"`

	// userId is the Keycloak id of the admin who deleted the user. Not the deleted user.
	// +optional
	UserID string `json:"userId,omitempty"`

	// ipAddress is the address the deletion request came from.
	// +optional
	IPAddress string `json:"ipAddress,omitempty"`
}

// UserDeletionSpec records a user deletion in Keycloak. It is written once by the
// poller from the admin event and never changes.
type UserDeletionSpec struct {
	// userId is the Keycloak id of the deleted user, same as the OIDC sub claim.
	// It is also the name of this object.
	// +required
	UserID string `json:"userId"`

	// username is the deleted user's username. Empty when the Keycloak event did not carry it.
	// +optional
	Username string `json:"username,omitempty"`

	// deletedAt is when the user was deleted in Keycloak. The grace period counts from here.
	// +required
	DeletedAt metav1.Time `json:"deletedAt"`

	// deletedBy identifies who performed the deletion. For audit only.
	// +optional
	DeletedBy *DeletionActor `json:"deletedBy,omitempty"`

	// adminEventId is the id of the Keycloak admin event this deletion was created from.
	// +optional
	AdminEventID string `json:"adminEventId,omitempty"`
}

// UserDeletionPhase summarizes where a UserDeletion is in its life.
// +kubebuilder:validation:Enum=Pending;InProgress;Completed
type UserDeletionPhase string

const (
	// UserDeletionPending means no cleanup Job has been created yet.
	UserDeletionPending UserDeletionPhase = "Pending"
	// UserDeletionInProgress means at least one hook has a Job and at least one is not terminal.
	UserDeletionInProgress UserDeletionPhase = "InProgress"
	// UserDeletionCompleted means every hook entry is terminal. The marker is kept
	// as a tombstone so a replayed Keycloak event does not run cleanup twice.
	UserDeletionCompleted UserDeletionPhase = "Completed"
)

// HookState is the state of one hook's cleanup for one UserDeletion.
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Skipped
type HookState string

const (
	// HookPending means the Job has not been created, either because the stage
	// is not due yet or because the reconciler has not got to it.
	HookPending HookState = "Pending"
	// HookRunning means the Job exists and has not finished.
	HookRunning HookState = "Running"
	// HookSucceeded means the Job completed.
	HookSucceeded HookState = "Succeeded"
	// HookFailed means the Job failed, or disappeared before finishing.
	HookFailed HookState = "Failed"
	// HookSkipped means the Job was never created, for example because the hook
	// was removed before its stage came due.
	HookSkipped HookState = "Skipped"
)

// IsTerminal reports whether the state can no longer change.
func (s HookState) IsTerminal() bool {
	return s == HookSucceeded || s == HookFailed || s == HookSkipped
}

// HookStatus records the cleanup of one UserCleanupHook for this user.
type HookStatus struct {
	// name of the UserCleanupHook.
	// +required
	Name string `json:"name"`

	// namespace of the UserCleanupHook. Jobs run there.
	// +required
	Namespace string `json:"namespace"`

	// stage the hook subscribed to, copied so the entry stays meaningful after the hook is gone.
	// +required
	Stage CleanupStage `json:"stage"`

	// state of the cleanup.
	// +required
	State HookState `json:"state"`

	// job is the name of the Job created for this hook, once created.
	// +optional
	Job string `json:"job,omitempty"`

	// startedAt is when the Job started.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// firstFailedAt is when creating the Job first failed for a reason that may
	// clear on its own. Retries continue for a bounded window from this time,
	// then the entry becomes Failed. Cleared when a create succeeds.
	// +optional
	FirstFailedAt *metav1.Time `json:"firstFailedAt,omitempty"`

	// finishedAt is when the Job reached a terminal state, or when the entry was skipped.
	// +optional
	FinishedAt *metav1.Time `json:"finishedAt,omitempty"`

	// reason is a CamelCase word explaining a Failed or Skipped state, or why a
	// Pending entry has not advanced, for example a Job create that keeps failing.
	// +optional
	Reason string `json:"reason,omitempty"`

	// message is a human readable explanation to go with reason.
	// +optional
	Message string `json:"message,omitempty"`
}

// UserDeletionStatus is the observed progress of the cleanup. It is owned by the
// UserDeletion controller and can be rebuilt from the Jobs that exist.
type UserDeletionStatus struct {
	// observedGeneration is the spec generation this status was computed from.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// phase summarizes the cleanup: Pending, InProgress or Completed.
	// +optional
	Phase UserDeletionPhase `json:"phase,omitempty"`

	// dueAt is when the "delete" stage may run: deletedAt plus the cluster-wide
	// grace period at the time the marker was first reconciled. It is frozen so
	// a later change to the grace period does not move markers already in flight.
	// +optional
	DueAt *metav1.Time `json:"dueAt,omitempty"`

	// completedAt is when every hook entry became terminal. The tombstone is
	// removed some time after this.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`

	// hooks has one entry per UserCleanupHook seen while this deletion was in flight.
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	// +optional
	Hooks []HookStatus `json:"hooks,omitempty"`

	// conditions:
	// - "IdentifiersComplete": the username is known. False when the Keycloak
	//   event carried no representation, so packs keyed on username may do nothing.
	// - "HooksSucceeded": every hook entry is terminal and none failed or was skipped.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for UserDeletion.
const (
	// ConditionTypeIdentifiersComplete is True when the marker carries a username.
	ConditionTypeIdentifiersComplete = "IdentifiersComplete"
	// ConditionTypeHooksSucceeded is True when every hook ran and succeeded.
	ConditionTypeHooksSucceeded = "HooksSucceeded"
)

// Condition reasons for UserDeletion.
const (
	ReasonUsernameKnown   = "UsernameKnown"
	ReasonUsernameMissing = "UsernameMissing"
	ReasonNoHooks         = "NoHooks"
	ReasonHooksPending    = "HooksPending"
	ReasonAllSucceeded    = "AllSucceeded"
	ReasonHookFailed      = "HookFailed"
	ReasonHookSkipped     = "HookSkipped"
)

// Reasons recorded on a HookStatus entry.
const (
	// HookReasonJobLost means the Job disappeared before the controller saw it finish.
	HookReasonJobLost = "JobLost"
	// HookReasonHookRemoved means the UserCleanupHook was deleted before its stage came due.
	HookReasonHookRemoved = "HookRemoved"
	// HookReasonJobCreateFailed means the last attempt to create the Job failed for a
	// reason that may clear on its own. The entry stays Pending and is retried.
	HookReasonJobCreateFailed = "JobCreateFailed"
	// HookReasonJobCreateRejected means the API server rejected the Job in a way
	// that retrying cannot fix, for example admission or validation.
	HookReasonJobCreateRejected = "JobCreateRejected"
	// HookReasonJobCreateTimedOut means the Job could not be created within the
	// retry window after the stage came due.
	HookReasonJobCreateTimedOut = "JobCreateTimedOut"
)

// Event reasons emitted on a UserDeletion that have no HookStatus counterpart.
// The HookReason* constants above double as event reasons for their failures.
const (
	// EventReasonCompleted is emitted once when the marker reaches Completed.
	EventReasonCompleted = "Completed"
	// EventReasonJobCreated is emitted for every Job the controller creates.
	EventReasonJobCreated = "JobCreated"
	// EventReasonJobFailed is emitted when a Job finished with the Failed condition.
	EventReasonJobFailed = "JobFailed"
)

// UserDeletionFinalizer keeps a marker until its running Jobs have finished.
const UserDeletionFinalizer = "lifecycle.nebari.dev/in-flight-jobs"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Username",type=string,JSONPath=`.spec.username`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Deleted",type=date,JSONPath=`.spec.deletedAt`
// +kubebuilder:printcolumn:name="Due",type=date,JSONPath=`.status.dueAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// UserDeletion marks that a user was deleted in Keycloak. The poller creates one per
// deleted user and the UserDeletion controller runs the registered cleanup hooks for it.
type UserDeletion struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of UserDeletion
	// +required
	Spec UserDeletionSpec `json:"spec"`

	// status defines the observed state of UserDeletion
	// +optional
	Status UserDeletionStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// UserDeletionList contains a list of UserDeletion
type UserDeletionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []UserDeletion `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &UserDeletion{}, &UserDeletionList{})
		return nil
	})
}
