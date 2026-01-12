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

package constants

// Gateway configuration constants
const (
	// PublicGatewayName is the name of the public-facing gateway
	PublicGatewayName = "nic-public-gateway"

	// InternalGatewayName is the name of the internal gateway
	InternalGatewayName = "nic-internal-gateway"

	// GatewayNamespace is the namespace where gateways are deployed
	GatewayNamespace = "envoy-gateway-system"
)

// Resource naming suffixes
const (
	// HTTPRouteSuffix is appended to NicApp name for HTTPRoute resources
	HTTPRouteSuffix = "route"

	// SecurityPolicySuffix is appended to NicApp name for SecurityPolicy resources
	SecurityPolicySuffix = "security"

	// CertificateSuffix is appended to NicApp name for Certificate resources
	CertificateSuffix = "cert"

	// ClientSecretSuffix is appended to NicApp name for OIDC client secret resources
	ClientSecretSuffix = "oidc-client"
)

// Secret keys
const (
	// ClientSecretKey is the key name for OIDC client secret data
	ClientSecretKey = "client-secret"
)

// Event reasons for validation events
const (
	EventReasonValidationFailed  = "ValidationFailed"
	EventReasonValidationSuccess = "ValidationSuccess"
	EventReasonNamespaceNotOptIn = "NamespaceNotOptedIn"
	EventReasonServiceNotFound   = "ServiceNotFound"
)

// Event reasons for routing events
const (
	EventReasonHTTPRouteCreated = "HTTPRouteCreated"
	EventReasonHTTPRouteUpdated = "HTTPRouteUpdated"
	EventReasonHTTPRouteDeleted = "HTTPRouteDeleted"
	EventReasonGatewayNotFound  = "GatewayNotFound"
)

// Event reasons for TLS events
const (
	EventReasonTLSConfigured = "TLSConfigured"
	EventReasonTLSFailed     = "TLSFailed"
)

// Event reasons for auth events
const (
	EventReasonAuthConfigured        = "AuthConfigured"
	EventReasonAuthFailed            = "AuthFailed"
	EventReasonClientProvisioned     = "ClientProvisioned"
	EventReasonClientProvisionFailed = "ClientProvisionFailed"
	EventReasonSecurityPolicyCreated = "SecurityPolicyCreated"
	EventReasonSecurityPolicyUpdated = "SecurityPolicyUpdated"
)

// Finalizers
const (
	// NicAppFinalizer is the finalizer added to NicApp resources
	NicAppFinalizer = "apps.nic.nebari.dev/finalizer"
)
