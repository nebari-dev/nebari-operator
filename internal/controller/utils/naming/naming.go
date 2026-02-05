package naming

import (
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

// ResourceName generates a consistent resource name for NebariApp-owned resources.
// Pattern: <nicapp-name>-<resource-type>
//
// Examples:
//   - ResourceName(nebariApp, "route") -> "my-app-route"
//   - ResourceName(nebariApp, "security") -> "my-app-security"
//   - ResourceName(nebariApp, "certificate") -> "my-app-certificate"
func ResourceName(nebariApp *appsv1.NebariApp, resourceType string) string {
	return fmt.Sprintf("%s-%s", nebariApp.Name, resourceType)
}

// SecurityPolicyName generates the name for a SecurityPolicy.
// Pattern: <nicapp-name>-security
func SecurityPolicyName(nebariApp *appsv1.NebariApp) string {
	return ResourceName(nebariApp, "security")
}

// HTTPRouteName generates the name for an HTTPRoute.
// Pattern: <nicapp-name>-route
func HTTPRouteName(nebariApp *appsv1.NebariApp) string {
	return ResourceName(nebariApp, "route")
}
