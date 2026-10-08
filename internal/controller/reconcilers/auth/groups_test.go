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
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	egv1alpha1 "github.com/envoyproxy/gateway/api/v1alpha1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/auth/providers"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/ptr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestBuildSecurityPolicySpec_Groups(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = egv1alpha1.AddToScheme(scheme)

	const jwks = "http://kc.keycloak.svc.cluster.local/auth/realms/nebari/protocol/openid-connect/certs"
	const cookie = "nebari-at-finance-finance-dash"
	deny := egv1alpha1.AuthorizationActionDeny
	stringArray := egv1alpha1.JWTClaimValueTypeStringArray

	allowRule := func(values ...string) []egv1alpha1.AuthorizationRule {
		return []egv1alpha1.AuthorizationRule{{
			Name:   ptr.To("allow-groups"),
			Action: egv1alpha1.AuthorizationActionAllow,
			Principal: egv1alpha1.Principal{JWT: &egv1alpha1.JWTPrincipal{
				Provider: "nebari-groups",
				Claims: []egv1alpha1.JWTClaim{{
					Name: "groups", ValueType: &stringArray, Values: values,
				}},
			}},
		}}
	}
	// genGroups returns n sorted paths /g000../g<n-1>.
	genGroups := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("/g%03d", i)
		}
		return out
	}
	ruleNamed := func(name string, values []string) egv1alpha1.AuthorizationRule {
		r := allowRule(values...)[0]
		r.Name = ptr.To(name)
		return r
	}
	conflicting := &appsv1.KeycloakClientConfig{ProtocolMappers: []appsv1.KeycloakProtocolMapperConfig{{
		Name: "g", ProtocolMapper: "oidc-group-membership-mapper",
		Config: map[string]string{"claim.name": "groups", "full.path": "false", "access.token.claim": "true"},
	}}}

	tests := []struct {
		name       string
		auth       *appsv1.AuthConfig
		wantCookie *string
		wantJWT    bool
		wantAuthz  *egv1alpha1.Authorization
	}{
		{
			name: "no groups: OIDC only",
			auth: &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderKeycloak},
		},
		{
			name:       "keycloak with groups: pinned cookie, jwt, allow rule",
			auth:       &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderKeycloak, Groups: []string{"finance", "/ops/oncall", "/finance"}},
			wantCookie: ptr.To(cookie),
			wantJWT:    true,
			wantAuthz:  &egv1alpha1.Authorization{DefaultAction: &deny, Rules: allowRule("/finance", "/ops/oncall")},
		},
		{
			name:       "exactly 128 groups: one rule",
			auth:       &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderKeycloak, Groups: genGroups(128)},
			wantCookie: ptr.To(cookie),
			wantJWT:    true,
			wantAuthz:  &egv1alpha1.Authorization{DefaultAction: &deny, Rules: allowRule(genGroups(128)...)},
		},
		{
			name:       "129 groups: split into two rules",
			auth:       &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderKeycloak, Groups: genGroups(129)},
			wantCookie: ptr.To(cookie),
			wantJWT:    true,
			wantAuthz: &egv1alpha1.Authorization{DefaultAction: &deny, Rules: []egv1alpha1.AuthorizationRule{
				ruleNamed("allow-groups", genGroups(129)[:128]),
				ruleNamed("allow-groups-2", genGroups(129)[128:]),
			}},
		},
		{
			name:       "only empty entries: deny all, no allow rule",
			auth:       &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderKeycloak, Groups: []string{"", "/"}},
			wantCookie: ptr.To(cookie),
			wantJWT:    true,
			wantAuthz:  &egv1alpha1.Authorization{DefaultAction: &deny},
		},
		{
			name:      "generic-oidc with groups: deny all",
			auth:      &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderGenericOIDC, Groups: []string{"finance"}},
			wantAuthz: &egv1alpha1.Authorization{DefaultAction: &deny},
		},
		{
			name:      "conflicting custom mapper: deny all",
			auth:      &appsv1.AuthConfig{Enabled: true, Provider: constants.ProviderKeycloak, Groups: []string{"finance"}, KeycloakConfig: conflicting},
			wantAuthz: &egv1alpha1.Authorization{DefaultAction: &deny},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &AuthReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
			app := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "finance-dash", Namespace: "finance"},
				Spec:       appsv1.NebariAppSpec{Hostname: "dash.example.com", Auth: tt.auth},
			}
			provider := &mockProvider{issuerURL: "https://kc.example.com/realms/nebari", clientID: "c", jwksURL: jwks}

			spec, err := r.buildSecurityPolicySpec(context.Background(), app, provider)
			if err != nil {
				t.Fatalf("buildSecurityPolicySpec() error = %v", err)
			}

			var gotCookie *string
			if spec.OIDC.CookieNames != nil {
				gotCookie = spec.OIDC.CookieNames.AccessToken
			}
			if !reflect.DeepEqual(gotCookie, tt.wantCookie) {
				t.Errorf("cookieNames.accessToken = %v, want %v", gotCookie, tt.wantCookie)
			}
			if spec.OIDC.CookieNames != nil && spec.OIDC.CookieNames.IDToken != nil {
				t.Errorf("cookieNames.idToken should stay unset, got %q", *spec.OIDC.CookieNames.IDToken)
			}

			if tt.wantJWT {
				want := &egv1alpha1.JWT{Providers: []egv1alpha1.JWTProvider{{
					Name:        "nebari-groups",
					RemoteJWKS:  &egv1alpha1.RemoteJWKS{URI: jwks},
					ExtractFrom: &egv1alpha1.JWTExtractor{Cookies: []string{cookie}},
				}}}
				if !reflect.DeepEqual(spec.JWT, want) {
					t.Errorf("jwt = %+v, want %+v", spec.JWT, want)
				}
			} else if spec.JWT != nil {
				t.Errorf("jwt should be nil, got %+v", spec.JWT)
			}

			if !reflect.DeepEqual(spec.Authorization, tt.wantAuthz) {
				t.Errorf("authorization = %+v, want %+v", spec.Authorization, tt.wantAuthz)
			}
		})
	}
}

type oidcOnlyProvider struct{ providers.OIDCProvider }

func TestBuildSecurityPolicySpec_GroupsProviderErrors(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = egv1alpha1.AddToScheme(scheme)

	tests := []struct {
		name     string
		provider providers.OIDCProvider
	}{
		{
			name:     "provider does not implement GroupsClaimProvider",
			provider: oidcOnlyProvider{&mockProvider{issuerURL: "https://kc.example.com/realms/nebari", clientID: "c"}},
		},
		{
			name:     "GetJWKSURL fails",
			provider: &mockProvider{issuerURL: "https://kc.example.com/realms/nebari", clientID: "c", jwksError: errors.New("boom")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &AuthReconciler{Client: fake.NewClientBuilder().WithScheme(scheme).Build(), Scheme: scheme}
			app := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "finance-dash", Namespace: "finance"},
				Spec: appsv1.NebariAppSpec{Hostname: "dash.example.com", Auth: &appsv1.AuthConfig{
					Enabled: true, Provider: constants.ProviderKeycloak, Groups: []string{"finance"},
				}},
			}
			spec, err := r.buildSecurityPolicySpec(context.Background(), app, tt.provider)
			if err == nil {
				t.Fatal("buildSecurityPolicySpec() error = nil, want error")
			}
			if !reflect.DeepEqual(spec, egv1alpha1.SecurityPolicySpec{}) {
				t.Errorf("spec should be zero on error, got %+v", spec)
			}
		})
	}
}
