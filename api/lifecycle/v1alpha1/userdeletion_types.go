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

// UserDeletionStatus defines the observed state of UserDeletion.
type UserDeletionStatus struct {
	// conditions represent the current state of the UserDeletion resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="Username",type=string,JSONPath=`.spec.username`
// +kubebuilder:printcolumn:name="Deleted",type=date,JSONPath=`.spec.deletedAt`
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
