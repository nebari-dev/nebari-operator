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

package naming

import (
	"fmt"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

// ResourceName generates a consistent resource name for NicApp-owned resources.
// Pattern: <nicapp-name>-<resource-type>
//
// Examples:
//   - ResourceName(nicApp, "route") -> "my-app-route"
//   - ResourceName(nicApp, "security") -> "my-app-security"
//   - ResourceName(nicApp, "certificate") -> "my-app-certificate"
func ResourceName(nicApp *appsv1.NicApp, resourceType string) string {
	return fmt.Sprintf("%s-%s", nicApp.Name, resourceType)
}

// ClientID generates a Keycloak client ID for a NicApp.
// Pattern: <nicapp-name>-<namespace>-client
//
// This ensures uniqueness across namespaces while maintaining readability.
func ClientID(nicApp *appsv1.NicApp) string {
	return fmt.Sprintf("%s-%s-client", nicApp.Name, nicApp.Namespace)
}

// ClientSecretName generates the name for a Keycloak client secret.
// Pattern: <nicapp-name>-oidc-client
func ClientSecretName(nicApp *appsv1.NicApp) string {
	return fmt.Sprintf("%s-oidc-client", nicApp.Name)
}

// SecurityPolicyName generates the name for a SecurityPolicy.
// Pattern: <nicapp-name>-security
func SecurityPolicyName(nicApp *appsv1.NicApp) string {
	return ResourceName(nicApp, "security")
}

// HTTPRouteName generates the name for an HTTPRoute.
// Pattern: <nicapp-name>-route
func HTTPRouteName(nicApp *appsv1.NicApp) string {
	return ResourceName(nicApp, "route")
}

// CertificateName generates the name for a Certificate.
// Pattern: <nicapp-name>-cert
func CertificateName(nicApp *appsv1.NicApp) string {
	return ResourceName(nicApp, "cert")
}
