package naming

import (
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

// maxKubernetesNameLength is the maximum length for Kubernetes resource names
// and Gateway API SectionName values.
const maxKubernetesNameLength = 253

// ValidateResourceNames checks that all derived resource names for a NebariApp
// fit within Kubernetes naming limits (253 characters for object names and SectionName).
func ValidateResourceNames(nebariApp *appsv1.NebariApp) error {
	checks := []struct {
		label string
		value string
	}{
		{"HTTPRoute", HTTPRouteName(nebariApp)},
		{"PublicHTTPRoute", PublicHTTPRouteName(nebariApp)},
		{"SecurityPolicy", SecurityPolicyName(nebariApp)},
		{"Certificate", CertificateName(nebariApp)},
		{"CertificateSecret", CertificateSecretName(nebariApp)},
		{"GatewayListener", ListenerName(nebariApp)},
		{"OIDCClientSecret", ClientSecretName(nebariApp)},
	}

	for _, c := range checks {
		if len(c.value) > maxKubernetesNameLength {
			return fmt.Errorf("%s name %q exceeds maximum length of %d characters (%d chars)",
				c.label, c.value, maxKubernetesNameLength, len(c.value))
		}
	}
	return nil
}

// ResourceName generates a consistent resource name for NebariApp-owned resources.
// Pattern: <nebariapp-name>-<resource-type>
//
// Examples:
//   - ResourceName(nebariApp, "route") -> "my-app-route"
//   - ResourceName(nebariApp, "security") -> "my-app-security"
//   - ResourceName(nebariApp, "certificate") -> "my-app-certificate"
func ResourceName(nebariApp *appsv1.NebariApp, resourceType string) string {
	return fmt.Sprintf("%s-%s", nebariApp.Name, resourceType)
}

// SecurityPolicyName generates the name for a SecurityPolicy.
// Pattern: <nebariapp-name>-security
func SecurityPolicyName(nebariApp *appsv1.NebariApp) string {
	return ResourceName(nebariApp, constants.SecurityPolicySuffix)
}

// HTTPRouteName generates the name for an HTTPRoute.
// Pattern: <nebariapp-name>-route
func HTTPRouteName(nebariApp *appsv1.NebariApp) string {
	return ResourceName(nebariApp, constants.HTTPRouteSuffix)
}

// PublicHTTPRouteName generates the name for the public (unauthenticated) HTTPRoute.
// Pattern: <nebariapp-name>-public-route
func PublicHTTPRouteName(nebariApp *appsv1.NebariApp) string {
	return ResourceName(nebariApp, constants.PublicHTTPRouteSuffix)
}

// ClientSecretName generates the name for the OIDC client secret.
// Pattern: <nebariapp-name>-oidc-client
func ClientSecretName(nebariApp *appsv1.NebariApp) string {
	return ResourceName(nebariApp, constants.ClientSecretSuffix)
}

// ClientID generates the OIDC client ID for a NebariApp.
// Pattern: <namespace>-<nebariapp-name>
// This ensures uniqueness across namespaces.
func ClientID(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s", nebariApp.Namespace, nebariApp.Name)
}

// DeviceFlowClientID generates the OIDC device flow client ID for a NebariApp.
// Pattern: <namespace>-<nebariapp-name>-device
func DeviceFlowClientID(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-device", nebariApp.Namespace, nebariApp.Name)
}

// CertificateName generates the name for a cert-manager Certificate.
// Includes namespace to avoid collisions since Certificates live in the Gateway namespace.
// Pattern: <nebariapp-name>-<namespace>-cert
func CertificateName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-%s", nebariApp.Name, nebariApp.Namespace, constants.CertificateSuffix)
}

// CertificateSecretName generates the name for the TLS secret created by cert-manager.
// Pattern: <nebariapp-name>-<namespace>-tls
func CertificateSecretName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-%s", nebariApp.Name, nebariApp.Namespace, constants.CertificateSecretSuffix)
}

// ListenerName generates the name for the per-app Gateway HTTPS listener.
// Pattern: tls-<nebariapp-name>-<namespace>
func ListenerName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("tls-%s-%s", nebariApp.Name, nebariApp.Namespace)
}

// GatewayName returns the Gateway name for a NebariApp based on its gateway spec.
// Returns the internal gateway name when spec.gateway is "internal",
// otherwise returns the public gateway name.
func GatewayName(nebariApp *appsv1.NebariApp) string {
	if nebariApp.Spec.Gateway == "internal" {
		return constants.InternalGatewayName
	}
	return constants.PublicGatewayName
}
