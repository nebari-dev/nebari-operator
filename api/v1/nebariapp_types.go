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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NebariAppSpec defines the desired state of NebariApp
type NebariAppSpec struct {
	// Hostname is the fully qualified domain name where the application should be accessible.
	// This will be used to generate HTTPRoute.
	// Example: "myapp.nebari.local" or "api.example.com"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Hostname string `json:"hostname"`

	// Service defines the backend Kubernetes Service that should receive traffic.
	// +kubebuilder:validation:Required
	Service ServiceReference `json:"service"`

	// Routing configures routing behavior including path-based rules and TLS.
	// +optional
	Routing *RoutingConfig `json:"routing,omitempty"`

	// Auth configures authentication/authorization for the application.
	// When enabled, the application will require OIDC authentication via supporting OIDC Provider.
	// +optional
	Auth *AuthConfig `json:"auth,omitempty"`

	// Gateway specifies which shared Gateway to use for routing.
	// Valid values are "public" (default) or "internal".
	// +kubebuilder:validation:Enum=public;internal
	// +kubebuilder:default=public
	// +optional
	Gateway string `json:"gateway,omitempty"`
}

// ServiceReference identifies the Kubernetes Service that backs this application.
type ServiceReference struct {
	// Name is the name of the Kubernetes Service in the same namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Port is the port number on the Service to route traffic to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

// RoutingConfig configures routing behavior for the application.
type RoutingConfig struct {
	// Routes defines path-based routing rules for the application.
	// If not specified, all traffic to the hostname will be routed to the service.
	// When specified, only traffic matching these path prefixes will be routed.
	// Example: ["/app-1", "/api/v1"]
	// +optional
	Routes []RouteMatch `json:"routes,omitempty"`

	// TLS configures TLS termination behavior for the HTTPRoute.
	// Note: The operator does not manage TLS certificates or Gateway TLS configuration.
	// cert-manager and envoy-gateway handle certificate provisioning and TLS termination.
	// This setting only controls whether the HTTPRoute should reference HTTPS listeners.
	// +optional
	TLS *RoutingTLSConfig `json:"tls,omitempty"`
}

// RouteMatch defines a path-based routing rule.
type RouteMatch struct {
	// PathPrefix specifies the path prefix to match for routing.
	// Traffic matching this prefix will be routed to the service.
	// Must start with "/". Example: "/app-1", "/api/v1"
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^/.*`
	PathPrefix string `json:"pathPrefix"`

	// PathType specifies how the path should be matched.
	// Valid values:
	//   - "PathPrefix" (default): Match requests with the specified path prefix
	//   - "Exact": Match requests with the exact path
	// +kubebuilder:validation:Enum=PathPrefix;Exact
	// +kubebuilder:default=PathPrefix
	// +optional
	PathType string `json:"pathType,omitempty"`
}

// RoutingTLSConfig controls TLS termination for the HTTPRoute.
type RoutingTLSConfig struct {
	// Enabled determines whether TLS termination should be used.
	// When nil or true, the HTTPRoute will reference HTTPS listeners on the Gateway.
	// When explicitly set to false, only HTTP listeners will be used.
	// Note: The Gateway's TLS certificates are managed by cert-manager, not by this operator.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// AuthConfig specifies authentication/authorization configuration.
type AuthConfig struct {
	// Enabled determines whether authentication should be enforced for this application.
	// When true, users must authenticate via OIDC before accessing the application.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Provider specifies the authentication provider to use.
	// Currently only "keycloak" is supported.
	// +kubebuilder:validation:Enum=keycloak
	// +kubebuilder:default=keycloak
	// +optional
	Provider string `json:"provider,omitempty"`

	// ClientSecretRef references a Kubernetes Secret containing OIDC client credentials.
	// The secret must be in the same namespace as the NebariApp and contain:
	//   - client-id: The OIDC client ID
	//   - client-secret: The OIDC client secret
	// If not specified and Keycloak provisioning is enabled, the operator will create
	// a secret named "<nebariapp-name>-oidc-client".
	// +optional
	ClientSecretRef *string `json:"clientSecretRef,omitempty"`

	// Scopes defines the OIDC scopes to request during authentication.
	// Common scopes: openid, profile, email, roles
	// +optional
	Scopes []string `json:"scopes,omitempty"`

	// ProvisionClient determines whether the operator should automatically provision
	// an OIDC client in Keycloak. When true, the operator will create a Keycloak client
	// and store the credentials in a Secret.
	// +kubebuilder:default=true
	// +optional
	ProvisionClient bool `json:"provisionClient,omitempty"`
}

// NebariAppStatus defines the observed state of NebariApp.
type NebariAppStatus struct {
	// For Kubernetes API conventions, see:
	// https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#typical-status-properties

	// Conditions represent the current state of the NebariApp resource.
	// Standard condition types:
	//   - "RoutingReady": HTTPRoute has been created and is functioning
	//   - "TLSReady": TLS certificate is available and configured
	//   - "AuthReady": Authentication policy is configured (if auth is enabled)
	//   - "Ready": All components are ready (aggregate condition)
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// ObservedGeneration is the most recent generation observed for this NebariApp.
	// It corresponds to the NebariApp's generation, which is updated on mutation by the API Server.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Hostname is the actual hostname where the application is accessible.
	// This mirrors the spec.hostname for easy reference.
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// GatewayRef identifies the Gateway resource that routes traffic to this application.
	// +optional
	GatewayRef *GatewayReference `json:"gatewayRef,omitempty"`

	// ClientSecretRef identifies the Secret containing OIDC client credentials.
	// +optional
	ClientSecretRef *ResourceReference `json:"clientSecretRef,omitempty"`
}

// GatewayReference identifies a Gateway resource.
type GatewayReference struct {
	// Name of the Gateway.
	Name string `json:"name"`

	// Namespace of the Gateway.
	Namespace string `json:"namespace"`
}

// ResourceReference identifies a Kubernetes resource.
type ResourceReference struct {
	// Name of the resource.
	Name string `json:"name"`

	// Namespace of the resource (if namespaced).
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// Condition types for NebariApp
const (
	// ConditionTypeRoutingReady indicates that the HTTPRoute has been created
	// and the Gateway is routing traffic to the application.
	ConditionTypeRoutingReady = "RoutingReady"

	// ConditionTypeTLSReady indicates that TLS termination is functioning.
	// This verifies that the Gateway's TLS listeners are accessible and working.
	// Note: TLS certificates are managed by cert-manager, not by this operator.
	ConditionTypeTLSReady = "TLSReady"

	// ConditionTypeAuthReady indicates that authentication is properly configured.
	// This includes the SecurityPolicy being created and the client secret being available.
	ConditionTypeAuthReady = "AuthReady"

	// ConditionTypeReady is an aggregate condition indicating all components are ready.
	ConditionTypeReady = "Ready"
)

// Condition reasons
const (
	// ReasonReconciling indicates reconciliation is in progress
	ReasonReconciling = "Reconciling"

	// ReasonAvailable indicates the resource is functioning correctly
	ReasonAvailable = "Available"

	// ReasonReconcileSuccess indicates successful reconciliation
	ReasonReconcileSuccess = "ReconcileSuccess"

	// ReasonValidationSuccess indicates validation passed successfully
	ReasonValidationSuccess = "ValidationSuccess"

	// ReasonFailed indicates reconciliation failed
	ReasonFailed = "Failed"

	// ReasonNamespaceNotOptedIn indicates the namespace doesn't have the required label
	ReasonNamespaceNotOptedIn = "NamespaceNotOptedIn"

	// ReasonServiceNotFound indicates the referenced service doesn't exist
	ReasonServiceNotFound = "ServiceNotFound"

	// ReasonSecretNotFound indicates the referenced secret doesn't exist
	ReasonSecretNotFound = "SecretNotFound"

	// ReasonGatewayNotFound indicates the target gateway doesn't exist
	ReasonGatewayNotFound = "GatewayNotFound"

	// ReasonCertificateNotReady indicates the cert-manager Certificate is not ready
	ReasonCertificateNotReady = "CertificateNotReady"
)

// Event reasons for recording Kubernetes events
const (
	// EventReasonValidationFailed is used when validation fails
	EventReasonValidationFailed = "ValidationFailed"

	// EventReasonValidationSuccess is used when validation succeeds
	EventReasonValidationSuccess = "ValidationSuccess"

	// EventReasonNamespaceNotOptIn is used when namespace is not opted-in
	EventReasonNamespaceNotOptIn = "NamespaceNotOptedIn"

	// EventReasonServiceNotFound is used when referenced service doesn't exist
	EventReasonServiceNotFound = "ServiceNotFound"

	// EventReasonHTTPRouteCreated is used when HTTPRoute is created
	EventReasonHTTPRouteCreated = "HTTPRouteCreated"

	// EventReasonHTTPRouteUpdated is used when HTTPRoute is updated
	EventReasonHTTPRouteUpdated = "HTTPRouteUpdated"

	// EventReasonHTTPRouteDeleted is used when HTTPRoute is deleted
	EventReasonHTTPRouteDeleted = "HTTPRouteDeleted"

	// EventReasonGatewayNotFound is used when target gateway doesn't exist
	EventReasonGatewayNotFound = "GatewayNotFound"

	// EventReasonTLSConfigured is used when TLS is successfully configured
	EventReasonTLSConfigured = "TLSConfigured"

	// EventReasonTLSFailed is used when TLS configuration fails
	EventReasonTLSFailed = "TLSFailed"

	// EventReasonAuthConfigured is used when auth is successfully configured
	EventReasonAuthConfigured = "AuthConfigured"

	// EventReasonAuthFailed is used when auth configuration fails
	EventReasonAuthFailed = "AuthFailed"

	// EventReasonClientProvisioned is used when OIDC client is provisioned
	EventReasonClientProvisioned = "ClientProvisioned"

	// EventReasonClientProvisionFailed is used when OIDC client provisioning fails
	EventReasonClientProvisionFailed = "ClientProvisionFailed"

	// EventReasonSecurityPolicyCreated is used when SecurityPolicy is created
	EventReasonSecurityPolicyCreated = "SecurityPolicyCreated"

	// EventReasonSecurityPolicyUpdated is used when SecurityPolicy is updated
	EventReasonSecurityPolicyUpdated = "SecurityPolicyUpdated"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=nebariapp

// NebariApp is the Schema for the nebariapps API
// It represents an application onboarding intent, specifying how an application
// should be exposed (routing), secured (TLS), and protected (authentication).
type NebariApp struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of NebariApp
	// +required
	Spec NebariAppSpec `json:"spec"`

	// status defines the observed state of NebariApp
	// +optional
	Status NebariAppStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NebariAppList contains a list of NebariApp
type NebariAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NebariApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NebariApp{}, &NebariAppList{})
}
