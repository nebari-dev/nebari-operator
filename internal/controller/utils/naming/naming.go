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
