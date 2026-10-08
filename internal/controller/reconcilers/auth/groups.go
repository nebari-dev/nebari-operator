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

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
)

const (
	// groupsJWTProviderName links the SecurityPolicy jwt provider to the
	// authorization rule. Envoy's RBAC filter reads jwt_authn metadata keyed by
	// this name, so both must use the same value.
	groupsJWTProviderName = "nebari-groups"
	groupsAllowRuleName   = "allow-groups"
	groupsClaimName       = "groups"

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
