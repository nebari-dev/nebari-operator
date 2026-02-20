package naming

import (
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

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

// CertificateName generates the name for a cert-manager Certificate.
// Includes namespace to avoid collisions since Certificates live in the Gateway namespace.
// Pattern: <nebariapp-name>-<namespace>-cert
func CertificateName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-%s", nebariApp.Name, nebariApp.Namespace, constants.CertificateSuffix)
}

// CertificateSecretName generates the name for the TLS secret created by cert-manager.
// Pattern: <nebariapp-name>-<namespace>-tls
func CertificateSecretName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-tls", nebariApp.Name, nebariApp.Namespace)
}

// ListenerName generates the name for the per-app Gateway HTTPS listener.
// Pattern: tls-<nebariapp-name>-<namespace>
func ListenerName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("tls-%s-%s", nebariApp.Name, nebariApp.Namespace)
}
