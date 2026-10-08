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
	"reflect"
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/ptr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNormalizeGroupPaths(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   []string
	}{
		{name: "nil", groups: nil, want: []string{}},
		{name: "bare name gets leading slash", groups: []string{"finance"}, want: []string{"/finance"}},
		{name: "path kept", groups: []string{"/ops/oncall"}, want: []string{"/ops/oncall"}},
		{name: "bare and path forms deduplicate", groups: []string{"finance", "/finance"}, want: []string{"/finance"}},
		{name: "sorted", groups: []string{"/zeta", "alpha"}, want: []string{"/alpha", "/zeta"}},
		{name: "trailing slash trimmed", groups: []string{"/finance/"}, want: []string{"/finance"}},
		{name: "whitespace trimmed", groups: []string{"  finance "}, want: []string{"/finance"}},
		{name: "empty and slash-only dropped", groups: []string{"", "/", "  "}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeGroupPaths(tt.groups)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeGroupPaths(%q) = %q, want %q", tt.groups, got, tt.want)
			}
		})
	}
}

func TestResolveGroupsMode(t *testing.T) {
	conflicting := &appsv1.KeycloakClientConfig{
		ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{{
			Name:           "my-groups",
			ProtocolMapper: "oidc-group-membership-mapper",
			Config:         map[string]string{"claim.name": "groups", "full.path": "false", "access.token.claim": "true"},
		}},
	}
	tests := []struct {
		name string
		auth *appsv1.AuthConfig
		want groupsMode
	}{
		{name: "nil auth", auth: nil, want: groupsModeNone},
		{name: "no groups", auth: &appsv1.AuthConfig{Provider: constants.ProviderKeycloak}, want: groupsModeNone},
		{name: "keycloak with groups", auth: &appsv1.AuthConfig{Provider: constants.ProviderKeycloak, Groups: []string{"a"}}, want: groupsModeGateway},
		{name: "empty provider defaults to keycloak", auth: &appsv1.AuthConfig{Groups: []string{"a"}}, want: groupsModeGateway},
		{name: "enforceAtGateway false", auth: &appsv1.AuthConfig{Provider: constants.ProviderKeycloak, Groups: []string{"a"}, EnforceAtGateway: ptr.To(false)}, want: groupsModeApplication},
		{name: "generic-oidc with groups", auth: &appsv1.AuthConfig{Provider: constants.ProviderGenericOIDC, Groups: []string{"a"}}, want: groupsModeRequireKeycloak},
		{name: "generic-oidc with groups not at gateway", auth: &appsv1.AuthConfig{Provider: constants.ProviderGenericOIDC, Groups: []string{"a"}, EnforceAtGateway: ptr.To(false)}, want: groupsModeApplication},
		{name: "conflicting custom mapper", auth: &appsv1.AuthConfig{Provider: constants.ProviderKeycloak, Groups: []string{"a"}, KeycloakConfig: conflicting}, want: groupsModeClaimConflict},
		{name: "only empty entries still enforced", auth: &appsv1.AuthConfig{Provider: constants.ProviderKeycloak, Groups: []string{""}}, want: groupsModeGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveGroupsMode(tt.auth); got != tt.want {
				t.Errorf("resolveGroupsMode() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGroupsModeEnforcedAtGateway(t *testing.T) {
	tests := []struct {
		mode groupsMode
		want bool
	}{
		{groupsModeNone, false},
		{groupsModeApplication, false},
		{groupsModeGateway, true},
		{groupsModeRequireKeycloak, true},
		{groupsModeClaimConflict, true},
	}
	for _, tt := range tests {
		if got := tt.mode.enforcedAtGateway(); got != tt.want {
			t.Errorf("groupsMode(%d).enforcedAtGateway() = %v, want %v", tt.mode, got, tt.want)
		}
	}
}

func TestGroupsAccessTokenCookieName(t *testing.T) {
	tests := []struct {
		namespace, name, want string
	}{
		{"finance", "finance-dash", "nebari-at-finance-finance-dash"},
		{"default", "app", "nebari-at-default-app"},
	}
	for _, tt := range tests {
		app := &appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Namespace: tt.namespace, Name: tt.name}}
		if got := groupsAccessTokenCookieName(app); got != tt.want {
			t.Errorf("groupsAccessTokenCookieName() = %q, want %q", got, tt.want)
		}
	}
}
