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
	"reflect"
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/ptr"
)

func fullPathMapper(name string) appsv1.KeycloakProtocolMapperConfig {
	return appsv1.KeycloakProtocolMapperConfig{
		Name:           name,
		ProtocolMapper: "oidc-group-membership-mapper",
		Config: map[string]string{
			"claim.name":           "groups",
			"full.path":            "true",
			"id.token.claim":       "true",
			"access.token.claim":   "true",
			"userinfo.token.claim": "true",
		},
	}
}

func TestCheckGroupsClaim(t *testing.T) {
	audience := appsv1.KeycloakProtocolMapperConfig{
		Name: "aud", ProtocolMapper: "oidc-audience-mapper",
		Config: map[string]string{"included.client.audience": "x"},
	}
	bareNames := fullPathMapper("my-groups")
	bareNames.Config = map[string]string{"claim.name": "groups", "full.path": "false", "access.token.claim": "true"}
	idTokenOnly := fullPathMapper("my-groups")
	idTokenOnly.Config = map[string]string{"claim.name": "groups", "full.path": "true", "access.token.claim": "false"}
	scriptMapper := appsv1.KeycloakProtocolMapperConfig{
		Name: "my-groups", ProtocolMapper: "oidc-hardcoded-claim-mapper",
		Config: map[string]string{"claim.name": "groups", "access.token.claim": "true"},
	}
	// A mapper that only writes lightweight access tokens.
	lightweight := appsv1.KeycloakProtocolMapperConfig{
		Name: "my-groups", ProtocolMapper: "oidc-hardcoded-claim-mapper",
		Config: map[string]string{"claim.name": "groups", "access.token.claim": "false", "lightweight.claim": "true"},
	}
	// Keycloak strips backslashes from claim.name and splits it on dots, so
	// these all land in the top-level groups claim.
	named := func(claim string) appsv1.KeycloakProtocolMapperConfig {
		return appsv1.KeycloakProtocolMapperConfig{
			Name: "my-groups", ProtocolMapper: "oidc-hardcoded-claim-mapper",
			Config: map[string]string{"claim.name": claim, "access.token.claim": "true"},
		}
	}
	// Passes every config check, so only the mapper type rejects it. A user
	// attribute mapper could let users write their own groups claim.
	attributeMapper := appsv1.KeycloakProtocolMapperConfig{
		Name: "my-groups", ProtocolMapper: "oidc-usermodel-attribute-mapper",
		Config: map[string]string{"claim.name": "groups", "full.path": "true", "access.token.claim": "true"},
	}

	tests := []struct {
		name     string
		kc       *appsv1.KeycloakClientConfig
		conflict bool
	}{
		{name: "nil config", kc: nil},
		{name: "no mappers", kc: &appsv1.KeycloakClientConfig{}},
		{name: "unrelated mapper", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{audience}}},
		{name: "compatible groups mapper", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{fullPathMapper("my-groups")}}},
		// It emits group names, and Keycloak lets a group be named "/admins".
		{name: "bare names", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{bareNames}}, conflict: true},
		{name: "not in access token", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{idTokenOnly}}},
		{name: "other mapper type emitting groups", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{scriptMapper}}, conflict: true},
		{name: "other mapper type with full paths in the access token", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{attributeMapper}}, conflict: true},
		{name: "lightweight access token claim", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{lightweight}}, conflict: true},
		{name: "claim name with a trailing dot", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{named(`groups.`)}}, conflict: true},
		{name: "claim name with an escaped first character", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{named(`\groups`)}}, conflict: true},
		{name: "claim name with an escape inside", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{named(`gr\oups`)}}, conflict: true},
		{name: "unrelated claim with a groups suffix", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{named(`usergroups`)}}},
		{name: "one compatible and one conflicting", kc: &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{fullPathMapper("a"), attributeMapper}}, conflict: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckGroupsClaim(tt.kc)
			if tt.conflict != errors.Is(err, ErrGroupsClaimConflict) {
				t.Errorf("CheckGroupsClaim() error = %v, want conflict %v", err, tt.conflict)
			}
			if !tt.conflict && err != nil {
				t.Errorf("CheckGroupsClaim() unexpected error %v", err)
			}
		})
	}
}

func TestAppUnsafeGroupsMapper(t *testing.T) {
	tests := []struct {
		name       string
		mapperType string
		cfg        map[string]string
		want       bool
	}{
		{"hardcoded groups in userinfo only", "oidc-hardcoded-claim-mapper", map[string]string{"claim.name": "groups", "userinfo.token.claim": "true"}, true},
		{"hardcoded groups in the ID token only", "oidc-hardcoded-claim-mapper", map[string]string{"claim.name": "groups", "id.token.claim": "true"}, true},
		{"attribute mapper in the access token", "oidc-usermodel-attribute-mapper", map[string]string{"claim.name": "groups", "access.token.claim": "true"}, true},
		{"bare-name membership mapper", "oidc-group-membership-mapper", map[string]string{"claim.name": "groups", "full.path": "false", "userinfo.token.claim": "true"}, true},
		{"full-path membership mapper", "oidc-group-membership-mapper", map[string]string{"claim.name": "groups", "full.path": "true", "userinfo.token.claim": "true"}, false},
		{"groups in no token", "oidc-hardcoded-claim-mapper", map[string]string{"claim.name": "groups"}, false},
		{"another claim", "oidc-hardcoded-claim-mapper", map[string]string{"claim.name": "department", "userinfo.token.claim": "true"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := appUnsafeGroupsMapper(tt.mapperType, tt.cfg); got != tt.want {
				t.Errorf("appUnsafeGroupsMapper() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDesiredProtocolMappers(t *testing.T) {
	audience := appsv1.KeycloakProtocolMapperConfig{
		Name: "aud", ProtocolMapper: "oidc-audience-mapper",
		Config: map[string]string{"included.client.audience": "x"},
	}
	sameNameOtherClaim := appsv1.KeycloakProtocolMapperConfig{
		Name: "group-membership", ProtocolMapper: "oidc-usermodel-attribute-mapper",
		Config: map[string]string{"claim.name": "dept"},
	}
	app := func(scopes, groups []string, mappers []appsv1.KeycloakProtocolMapperConfig) *appsv1.NebariApp {
		a := &appsv1.NebariApp{Spec: appsv1.NebariAppSpec{Auth: &appsv1.AuthConfig{Scopes: scopes, Groups: groups}}}
		if mappers != nil {
			a.Spec.Auth.KeycloakConfig = &appsv1.KeycloakClientConfig{ProtocolMappers: mappers}
		}
		return a
	}

	bareNameMapper := fullPathMapper("mine")
	bareNameMapper.Config = map[string]string{"claim.name": "groups", "full.path": "false", "access.token.claim": "true"}

	idTokenOnlyMapper := fullPathMapper("mine")
	idTokenOnlyMapper.Config = map[string]string{"claim.name": "groups", "full.path": "true", "access.token.claim": "false", "id.token.claim": "true"}
	appEnforced := func(a *appsv1.NebariApp) *appsv1.NebariApp {
		a.Spec.Auth.EnforceAtGateway = ptr.To(false)
		return a
	}

	tests := []struct {
		name string
		app  *appsv1.NebariApp
		want []appsv1.KeycloakProtocolMapperConfig
	}{
		{name: "nothing requested", app: app([]string{"openid"}, nil, nil), want: nil},
		{name: "groups scope gets full-path default", app: app([]string{"openid", "groups"}, nil, nil), want: []appsv1.KeycloakProtocolMapperConfig{fullPathMapper("group-membership")}},
		{name: "groups set without scope gets full-path default", app: app([]string{"openid"}, []string{"finance"}, nil), want: []appsv1.KeycloakProtocolMapperConfig{fullPathMapper("group-membership")}},
		{name: "custom mappers without groups keep today's behavior", app: app([]string{"groups"}, nil, []appsv1.KeycloakProtocolMapperConfig{audience}), want: []appsv1.KeycloakProtocolMapperConfig{audience}},
		{name: "custom mappers with groups set get operator mapper appended", app: app(nil, []string{"finance"}, []appsv1.KeycloakProtocolMapperConfig{audience}), want: []appsv1.KeycloakProtocolMapperConfig{audience, fullPathMapper("nebari-group-membership")}},
		{name: "compatible custom groups mapper used as-is", app: app(nil, []string{"finance"}, []appsv1.KeycloakProtocolMapperConfig{fullPathMapper("mine")}), want: []appsv1.KeycloakProtocolMapperConfig{fullPathMapper("mine")}},
		// A bare-name mapper conflicts; the operator mapper is still appended, and the reconciler denies access.
		{name: "custom bare-name groups mapper gets operator mapper appended", app: app(nil, []string{"finance"}, []appsv1.KeycloakProtocolMapperConfig{bareNameMapper}), want: []appsv1.KeycloakProtocolMapperConfig{bareNameMapper, fullPathMapper("nebari-group-membership")}},
		// The ID-token-only mapper gives the gateway nothing, so the operator mapper is still needed.
		{name: "custom full-path mapper only in the ID token gets operator mapper appended", app: app(nil, []string{"finance"}, []appsv1.KeycloakProtocolMapperConfig{idTokenOnlyMapper}), want: []appsv1.KeycloakProtocolMapperConfig{idTokenOnlyMapper, fullPathMapper("nebari-group-membership")}},
		// The application reads the claim its own mapper emits; only the gateway needs full paths.
		{name: "application-enforced app keeps its own groups mapper", app: appEnforced(app(nil, []string{"finance"}, []appsv1.KeycloakProtocolMapperConfig{bareNameMapper})), want: []appsv1.KeycloakProtocolMapperConfig{bareNameMapper}},
		{name: "appended mapper does not reuse a custom mapper's name", app: app(nil, []string{"finance"}, []appsv1.KeycloakProtocolMapperConfig{sameNameOtherClaim}), want: []appsv1.KeycloakProtocolMapperConfig{sameNameOtherClaim, fullPathMapper("nebari-group-membership")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := desiredProtocolMappers(tt.app)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("desiredProtocolMappers() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}
