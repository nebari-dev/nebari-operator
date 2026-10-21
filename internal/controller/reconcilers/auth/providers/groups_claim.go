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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

// ErrGroupsClaimConflict reports a protocol mapper that writes the groups claim
// into the access token from something other than group membership, so the
// gateway cannot trust the claim.
var ErrGroupsClaimConflict = errors.New("a protocol mapper other than a group-membership mapper writes the groups claim into the access token")

// GroupsClaimConflictError names the mappers on the app's Keycloak client that
// the operator does not manage and that write the groups claim from a source
// other than group membership. It wraps ErrGroupsClaimConflict.
type GroupsClaimConflictError struct {
	Mappers []string
}

func (e *GroupsClaimConflictError) Error() string {
	return fmt.Sprintf("%v: mapper(s) %s on the client (remove them, or emit the claim with an oidc-group-membership-mapper)",
		ErrGroupsClaimConflict, strings.Join(e.Mappers, ", "))
}

func (e *GroupsClaimConflictError) Unwrap() error { return ErrGroupsClaimConflict }

const (
	// mapperConfigTrue is the string Keycloak mapper config uses for boolean true.
	mapperConfigTrue = "true"
	// groupsClaim is the token claim name that carries group membership.
	groupsClaim               = "groups"
	groupMembershipMapperType = "oidc-group-membership-mapper"
	// defaultGroupsMapperName is the mapper created when no custom mappers are
	// set. Existing clients already carry a mapper with this name, so the
	// update-by-name path rewrites it in place.
	defaultGroupsMapperName = "group-membership"
	// appendedGroupsMapperName is used when the mapper is appended to custom
	// mappers, so it never overwrites a custom mapper named group-membership.
	// The name is reserved for the operator, which removes the mapper once it
	// is no longer desired.
	appendedGroupsMapperName = "nebari-group-membership"
	// managedMappersAttribute is the client attribute in which the operator
	// records, as a JSON array, the names of the client-level mappers it
	// manages. Only those are deleted when they leave the spec; mappers added
	// any other way are left in place.
	managedMappersAttribute = "nebari.dev/managed-protocol-mappers"
)

// groupMembershipMapper emits group full paths in the "groups" claim of the
// access token, which the gateway's jwt provider reads.
func groupMembershipMapper(name string) appsv1.KeycloakProtocolMapperConfig {
	return appsv1.KeycloakProtocolMapperConfig{
		Name:           name,
		ProtocolMapper: groupMembershipMapperType,
		Config: map[string]string{
			"claim.name":           groupsClaim,
			"full.path":            mapperConfigTrue,
			"id.token.claim":       mapperConfigTrue,
			"access.token.claim":   mapperConfigTrue,
			"userinfo.token.claim": mapperConfigTrue,
		},
	}
}

// writesGroupsToAccessToken reports whether a mapper with this config writes
// the "groups" claim into the access token, which is the token the gateway
// reads. Keycloak only includes a mapper in the access token when
// access.token.claim is "true".
func writesGroupsToAccessToken(cfg map[string]string) bool {
	return cfg["claim.name"] == groupsClaim && cfg["access.token.claim"] == mapperConfigTrue
}

// unsafeGroupsMapper reports whether a mapper of this type and config writes
// the access-token "groups" claim from something other than group membership.
// Keycloak always merges the values of group-membership mappers into the
// claim, so another one (with full.path false, say) only adds values that
// never match a full path. Any other mapper can inject arbitrary values, or,
// when it is single-valued, replace the claim.
func unsafeGroupsMapper(mapperType string, cfg map[string]string) bool {
	return writesGroupsToAccessToken(cfg) && mapperType != groupMembershipMapperType
}

// emitsFullPathGroups reports whether any mapper is a group-membership mapper
// that writes full paths into the access-token "groups" claim.
func emitsFullPathGroups(mappers []appsv1.KeycloakProtocolMapperConfig) bool {
	for _, m := range mappers {
		if m.ProtocolMapper == groupMembershipMapperType && writesGroupsToAccessToken(m.Config) &&
			m.Config["full.path"] == mapperConfigTrue {
			return true
		}
	}
	return false
}

// CheckGroupsClaim returns an error wrapping ErrGroupsClaimConflict when a
// custom mapper in the spec writes the access-token "groups" claim from
// something other than group membership.
func CheckGroupsClaim(kc *appsv1.KeycloakClientConfig) error {
	if kc == nil {
		return nil
	}
	for _, m := range kc.ProtocolMappers {
		if unsafeGroupsMapper(m.ProtocolMapper, m.Config) {
			return fmt.Errorf("%w: mapper %q", ErrGroupsClaimConflict, m.Name)
		}
	}
	return nil
}

// managedMapperNames reads the names recorded in managedMappersAttribute. An
// absent or unreadable attribute means the operator manages none yet.
func managedMapperNames(attrs map[string]string) []string {
	var names []string
	if raw := attrs[managedMappersAttribute]; raw != "" {
		if err := json.Unmarshal([]byte(raw), &names); err != nil {
			return nil
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// encodeMapperNames is the managedMappersAttribute value for names.
func encodeMapperNames(names []string) string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	data, _ := json.Marshal(slices.Compact(sorted))
	return string(data)
}

// desiredProtocolMappers returns the client-level mappers to sync.
//
// Custom mappers replace the default, as before. When spec.auth.groups is set
// and no custom mapper already emits full paths into the access-token groups
// claim, the operator's mapper is appended, because the gateway rule needs
// them. A custom group-membership mapper without full paths keeps working
// next to it: Keycloak merges both mappers' values into the claim.
// Conflicting custom mappers are returned unchanged; the reconciler denies
// access for them.
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

	if !groupsSet || emitsFullPathGroups(custom) {
		return custom
	}
	out := make([]appsv1.KeycloakProtocolMapperConfig, 0, len(custom)+1)
	out = append(out, custom...)
	return append(out, groupMembershipMapper(appendedGroupsMapperName))
}
