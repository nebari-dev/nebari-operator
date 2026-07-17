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

package config

// TLSConfig holds TLS certificate management configuration for the operator.
type TLSConfig struct {
	// ClusterIssuerName is the name of the cert-manager ClusterIssuer to use.
	// When empty, the TLS reconciler will not create Certificate resources.
	ClusterIssuerName string

	// PerAppGateway selects the per-app Gateway strategy. When false (default),
	// the operator appends a per-app HTTPS listener to the shared cluster Gateway
	// in the Gateway namespace (legacy behavior). When true, the operator instead
	// creates a dedicated Gateway (and co-located Certificate) in the NebariApp's
	// own namespace, owner-referenced for garbage collection. The per-app Gateways
	// are merged onto a single Envoy dataplane by Envoy Gateway's mergeGateways,
	// which MUST be enabled cluster-wide; without it each Gateway would provision
	// its own dataplane/load balancer. See nebari-operator#484.
	PerAppGateway bool
}

// LoadTLSConfig loads TLS configuration from environment variables.
func LoadTLSConfig() TLSConfig {
	return TLSConfig{
		ClusterIssuerName: getEnv("TLS_CLUSTER_ISSUER_NAME", ""),
		PerAppGateway:     getEnvBool("TLS_PER_APP_GATEWAY", false),
	}
}
