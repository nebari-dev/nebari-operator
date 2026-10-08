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

package auth

import (
	"fmt"
	"sort"
	"strings"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/ptr"
)

const (
	// groupsJWTProviderName links the SecurityPolicy jwt provider to the
	// authorization rule. Envoy's RBAC filter reads jwt_authn metadata keyed by
	// this name, so both must use the same value.
	groupsJWTProviderName = "nebari-groups"
	groupsAllowRuleName   = "allow-groups"
	groupsClaimName       = "groups"
	// authorizedPartyClaimName is the azp claim Keycloak sets to the client that requested the token.
	authorizedPartyClaimName = "azp"

	// maxClaimValuesPerRule is Envoy Gateway's cap on JWTClaim.Values
	// (+kubebuilder:validation:MaxItems=128 at v1.6.3). Longer group lists are
	// split across several allow rules.
	maxClaimValuesPerRule = 128

	// groupsClaimFormatVersion is hashed into AuthConfigHash so that every app
	// reprovisions once when the groups claim format changes.
	groupsClaimFormatVersion = "full-path-v1"
)

// groupsMode describes how spec.auth.groups is enforced for an app.
type groupsMode int

const (
	// groupsModeNone: no groups listed. Any authenticated realm user is admitted.
	groupsModeNone groupsMode = iota
	// groupsModeGateway: the SecurityPolicy allows only listed groups.
	groupsModeGateway
	// groupsModeApplication: enforceAtGateway is false; the app checks the claim.
	groupsModeApplication
	// groupsModeRequireKeycloak: the provider cannot enforce groups; deny all.
	groupsModeRequireKeycloak
	// groupsModeClaimConflict: a custom mapper emits an unusable claim; deny all.
	groupsModeClaimConflict
)

// enforcedAtGateway reports whether the SecurityPolicy carries an
// authorization block in this mode.
func (m groupsMode) enforcedAtGateway() bool {
	return m == groupsModeGateway || m == groupsModeRequireKeycloak || m == groupsModeClaimConflict
}

// resolveGroupsMode decides how spec.auth.groups is enforced. A non-empty
// list always counts as set, even if every entry normalizes away, so that a
// malformed list denies access instead of admitting everyone.
func resolveGroupsMode(auth *appsv1.AuthConfig) groupsMode {
	if auth == nil || len(auth.Groups) == 0 {
		return groupsModeNone
	}
	if !shouldEnforceAtGateway(auth) {
		return groupsModeApplication
	}
	if auth.Provider != "" && auth.Provider != constants.ProviderKeycloak {
		return groupsModeRequireKeycloak
	}
	if err := providers.CheckGroupsClaim(auth.KeycloakConfig); err != nil {
		return groupsModeClaimConflict
	}
	return groupsModeGateway
}

// NormalizeGroupPaths converts spec.auth.groups entries to the full-path form
// Keycloak puts in the groups claim: a leading slash, no trailing slash.
// Empty entries are dropped, and the result is deduplicated and sorted.
func NormalizeGroupPaths(groups []string) []string {
	seen := make(map[string]struct{}, len(groups))
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		g = strings.Trim(strings.TrimSpace(g), "/")
		if g == "" {
			continue
		}
		g = "/" + g
		if _, ok := seen[g]; ok {
			continue
		}
		seen[g] = struct{}{}
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// groupsAccessTokenCookieName is the pinned oauth2 access-token cookie name.
// Envoy Gateway's default name is derived from the policy UID, which the
// operator cannot know when it builds the jwt provider that reads the cookie.
func groupsAccessTokenCookieName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("nebari-at-%s-%s", nebariApp.Namespace, nebariApp.Name)
}

// denyAllAuthorization denies every request. It is used when groups are set
// but cannot be enforced, so the route never falls back to admitting every
// authenticated user.
func denyAllAuthorization() *egv1alpha1.Authorization {
	return &egv1alpha1.Authorization{DefaultAction: ptr.To(egv1alpha1.AuthorizationActionDeny)}
}

// applyGroupsEnforcement adds the jwt provider and authorization rules that
// admit only members of groupPaths. The oauth2 filter runs before jwt_authn
// and validates its session cookies with an HMAC, so a modified token cookie
// fails the oauth2 check and the request is sent back to login; the jwt
// provider therefore reads the access token from the oauth2 cookie. Group
// paths are split into one allow rule per maxClaimValuesPerRule values. With
// no groupPaths the policy denies every request.
//
// Every allow rule also requires the azp claim to equal clientID. The oauth2
// HMAC secret is shared across all SecurityPolicies, so the cookie check alone
// does not bind a token to this app; azp ties it to this app's OIDC client.
// Claims in one principal are ANDed.
func applyGroupsEnforcement(spec *egv1alpha1.SecurityPolicySpec, cookieName, jwksURL, clientID string, groupPaths []string) {
	spec.OIDC.CookieNames = &egv1alpha1.OIDCCookieNames{AccessToken: ptr.To(cookieName)}
	spec.JWT = &egv1alpha1.JWT{
		Providers: []egv1alpha1.JWTProvider{{
			Name:        groupsJWTProviderName,
			RemoteJWKS:  &egv1alpha1.RemoteJWKS{URI: jwksURL},
			ExtractFrom: &egv1alpha1.JWTExtractor{Cookies: []string{cookieName}},
		}},
	}
	spec.Authorization = denyAllAuthorization()
	for i := 0; i < len(groupPaths); i += maxClaimValuesPerRule {
		end := i + maxClaimValuesPerRule
		if end > len(groupPaths) {
			end = len(groupPaths)
		}
		name := groupsAllowRuleName
		if i > 0 {
			name = fmt.Sprintf("%s-%d", groupsAllowRuleName, i/maxClaimValuesPerRule+1)
		}
		spec.Authorization.Rules = append(spec.Authorization.Rules, egv1alpha1.AuthorizationRule{
			Name:   ptr.To(name),
			Action: egv1alpha1.AuthorizationActionAllow,
			Principal: egv1alpha1.Principal{
				JWT: &egv1alpha1.JWTPrincipal{
					Provider: groupsJWTProviderName,
					Claims: []egv1alpha1.JWTClaim{
						{
							Name:      groupsClaimName,
							ValueType: ptr.To(egv1alpha1.JWTClaimValueTypeStringArray),
							Values:    groupPaths[i:end],
						},
						{
							Name:      authorizedPartyClaimName,
							ValueType: ptr.To(egv1alpha1.JWTClaimValueTypeString),
							Values:    []string{clientID},
						},
					},
				},
			},
		})
	}
}
