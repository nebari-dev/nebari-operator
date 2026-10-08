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

package providers

import (
	"errors"
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

// ErrGroupsClaimConflict reports a custom protocol mapper that emits the groups
// claim in a form the gateway authorization rule cannot match.
var ErrGroupsClaimConflict = errors.New("custom protocol mapper emits the groups claim without full paths in the access token")

const (
	groupMembershipMapperType = "oidc-group-membership-mapper"
	// defaultGroupsMapperName is the mapper created when no custom mappers are
	// set. Existing clients already carry a mapper with this name, so the
	// update-by-name path rewrites it in place.
	defaultGroupsMapperName = "group-membership"
	// appendedGroupsMapperName is used when the mapper is appended to custom
	// mappers, so it never overwrites a custom mapper named group-membership.
	appendedGroupsMapperName = "nebari-group-membership"
)

// groupMembershipMapper emits group full paths in the "groups" claim of the
// access token, which the gateway's jwt provider reads.
func groupMembershipMapper(name string) appsv1.KeycloakProtocolMapperConfig {
	return appsv1.KeycloakProtocolMapperConfig{
		Name:           name,
		ProtocolMapper: groupMembershipMapperType,
		Config: map[string]string{
			"claim.name":           "groups",
			"full.path":            "true",
			"id.token.claim":       "true",
			"access.token.claim":   "true",
			"userinfo.token.claim": "true",
		},
	}
}

// emitsGroupsClaim reports whether any mapper writes the "groups" claim.
func emitsGroupsClaim(mappers []appsv1.KeycloakProtocolMapperConfig) bool {
	for _, m := range mappers {
		if m.Config["claim.name"] == "groups" {
			return true
		}
	}
	return false
}

// CheckGroupsClaim returns an error wrapping ErrGroupsClaimConflict when a
// custom mapper writes the "groups" claim as anything other than full group
// paths in the access token.
func CheckGroupsClaim(kc *appsv1.KeycloakClientConfig) error {
	if kc == nil {
		return nil
	}
	for _, m := range kc.ProtocolMappers {
		if m.Config["claim.name"] != "groups" {
			continue
		}
		if m.ProtocolMapper != groupMembershipMapperType ||
			m.Config["full.path"] != "true" ||
			m.Config["access.token.claim"] != "true" {
			return fmt.Errorf("%w: mapper %q", ErrGroupsClaimConflict, m.Name)
		}
	}
	return nil
}

// desiredProtocolMappers returns the client-level mappers to sync.
//
// Custom mappers replace the default, as before. When spec.auth.groups is set
// and no custom mapper emits the groups claim, the operator's mapper is
// appended, because the gateway rule needs the claim. Conflicting custom
// mappers are returned unchanged; the reconciler denies access for them.
func desiredProtocolMappers(nebariApp *appsv1.NebariApp) []appsv1.KeycloakProtocolMapperConfig {
	auth := nebariApp.Spec.Auth
	groupsSet := len(auth.Groups) > 0

	var custom []appsv1.KeycloakProtocolMapperConfig
	if auth.KeycloakConfig != nil {
		custom = auth.KeycloakConfig.ProtocolMappers
	}

	if len(custom) == 0 {
		if groupsSet || hasScope(nebariApp, "groups") {
			return []appsv1.KeycloakProtocolMapperConfig{groupMembershipMapper(defaultGroupsMapperName)}
		}
		return nil
	}

	if !groupsSet || emitsGroupsClaim(custom) {
		return custom
	}
	out := make([]appsv1.KeycloakProtocolMapperConfig, 0, len(custom)+1)
	out = append(out, custom...)
	return append(out, groupMembershipMapper(appendedGroupsMapperName))
}
