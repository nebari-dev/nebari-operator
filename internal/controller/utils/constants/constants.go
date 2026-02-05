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

package constants

// Gateway configuration constants
// These match the foundational infrastructure setup via ArgoCD
const (
	// PublicGatewayName is the name of the public-facing gateway
	// Corresponds to the nebari-gateway Gateway resource in envoy-gateway-system
	PublicGatewayName = "nebari-gateway"

	// InternalGatewayName is the name of the internal gateway (if deployed)
	InternalGatewayName = "nebari-internal-gateway"

	// GatewayNamespace is the namespace where gateways are deployed
	GatewayNamespace = "envoy-gateway-system"

	// GatewayClassName is the GatewayClass used by the gateway
	GatewayClassName = "envoy-gateway"

	// DefaultTLSSecretName is the wildcard certificate used by the gateway
	// This corresponds to the nebari-gateway-tls secret created by cert-manager
	DefaultTLSSecretName = "nebari-gateway-tls"
)

// Resource naming suffixes
const (
	// HTTPRouteSuffix is appended to NebariApp name for HTTPRoute resources
	HTTPRouteSuffix = "route"

	// SecurityPolicySuffix is appended to NebariApp name for SecurityPolicy resources
	SecurityPolicySuffix = "security"

	// CertificateSuffix is appended to NebariApp name for Certificate resources
	CertificateSuffix = "cert"

	// ClientSecretSuffix is appended to NebariApp name for OIDC client secret resources
	ClientSecretSuffix = "oidc-client"
)

// Secret keys
const (
	// ClientSecretKey is the key name for OIDC client secret data
	ClientSecretKey = "client-secret"
)

// Finalizers
const (
	// NebariAppFinalizer is the finalizer added to NebariApp resources
	NebariAppFinalizer = "apps.nebari.dev/finalizer"
)
