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

package tls

import (
	"context"
	"fmt"
	"strings"
	"testing"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func boolPtr(b bool) *bool {
	return &b
}

func newScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = certmanagerv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
	_ = corev1.AddToScheme(scheme)
	return scheme
}

func newGateway(name string, listeners ...gatewayv1.Listener) *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: constants.GatewayNamespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(constants.GatewayClassName),
			Listeners:        listeners,
		},
	}
}

func TestReconcileTLS(t *testing.T) { //nolint:gocyclo // table-driven test with inline validation
	scheme := newScheme()

	tests := []struct {
		name               string
		nebariApp          *appsv1.NebariApp
		clusterIssuerName  string
		gateway            *gatewayv1.Gateway
		existingCert       *certmanagerv1.Certificate
		existingSecret     *corev1.Secret
		expectError        bool
		expectNilResult    bool
		validateResult     func(*testing.T, *TLSResult)
		validateCert       func(*testing.T, *certmanagerv1.Certificate)
		validateCertAbsent bool
		validateGateway    func(*testing.T, *gatewayv1.Gateway)
		validateConditions func(*testing.T, *appsv1.NebariApp)
	}{
		{
			name: "TLS disabled returns nil result and no Certificate created",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled: boolPtr(false),
						},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       false,
			expectNilResult:   true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil {
					t.Fatal("expected TLSReady condition to be set")
				}
				if c.Status != metav1.ConditionFalse {
					t.Errorf("expected TLSReady=False, got %s", c.Status)
				}
				if c.Reason != "TLSDisabled" {
					t.Errorf("expected reason TLSDisabled, got %s", c.Reason)
				}
			},
		},
		{
			name: "TLS enabled creates Certificate with correct spec",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "myapp",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "myapp.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing:  &appsv1.RoutingConfig{}, // Explicitly enable routing (TLS defaults to enabled)
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       false,
			expectNilResult:   false,
			validateResult: func(t *testing.T, result *TLSResult) {
				expectedListenerName := naming.ListenerName(&appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"},
				})
				if result.ListenerName != expectedListenerName {
					t.Errorf("expected listener name %s, got %s", expectedListenerName, result.ListenerName)
				}
				expectedSecretName := naming.CertificateSecretName(&appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"},
				})
				if result.SecretName != expectedSecretName {
					t.Errorf("expected secret name %s, got %s", expectedSecretName, result.SecretName)
				}
				// Certificate is not ready yet (no Ready condition on cert)
				if result.CertReady {
					t.Error("expected CertReady=false for newly created Certificate")
				}
			},
			validateCert: func(t *testing.T, cert *certmanagerv1.Certificate) {
				if len(cert.Spec.DNSNames) != 1 || cert.Spec.DNSNames[0] != "myapp.example.com" {
					t.Errorf("expected dnsNames [myapp.example.com], got %v", cert.Spec.DNSNames)
				}
				if cert.Spec.IssuerRef.Name != "letsencrypt-prod" {
					t.Errorf("expected issuerRef name letsencrypt-prod, got %s", cert.Spec.IssuerRef.Name)
				}
				if cert.Spec.IssuerRef.Kind != "ClusterIssuer" {
					t.Errorf("expected issuerRef kind ClusterIssuer, got %s", cert.Spec.IssuerRef.Kind)
				}
				expectedSecretName := naming.CertificateSecretName(&appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"},
				})
				if cert.Spec.SecretName != expectedSecretName {
					t.Errorf("expected secretName %s, got %s", expectedSecretName, cert.Spec.SecretName)
				}
				// Check labels
				if cert.Labels["app.kubernetes.io/managed-by"] != "nebari-operator" {
					t.Errorf("expected managed-by label, got %v", cert.Labels)
				}
				if cert.Labels["nebari.dev/nebariapp-name"] != "myapp" {
					t.Errorf("expected nebariapp-name label, got %v", cert.Labels)
				}
				if cert.Labels["nebari.dev/nebariapp-namespace"] != "default" { //nolint:goconst // test data
					t.Errorf("expected nebariapp-namespace label, got %v", cert.Labels)
				}
			},
			validateGateway: func(t *testing.T, gw *gatewayv1.Gateway) {
				if len(gw.Spec.Listeners) != 1 {
					t.Fatalf("expected 1 listener, got %d", len(gw.Spec.Listeners))
				}
				listener := gw.Spec.Listeners[0]
				expectedListenerName := naming.ListenerName(&appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"},
				})
				if string(listener.Name) != expectedListenerName {
					t.Errorf("expected listener name %s, got %s", expectedListenerName, listener.Name)
				}
				if listener.Protocol != gatewayv1.HTTPSProtocolType {
					t.Errorf("expected HTTPS protocol, got %s", listener.Protocol)
				}
				if listener.Port != 443 {
					t.Errorf("expected port 443, got %d", listener.Port)
				}
				if listener.Hostname == nil || string(*listener.Hostname) != "myapp.example.com" {
					t.Errorf("expected hostname myapp.example.com, got %v", listener.Hostname)
				}
				if listener.TLS == nil {
					t.Fatal("expected TLS config on listener")
				}
				if listener.TLS.Mode == nil || *listener.TLS.Mode != gatewayv1.TLSModeTerminate {
					t.Errorf("expected TLS mode Terminate, got %v", listener.TLS.Mode)
				}
				if len(listener.TLS.CertificateRefs) != 1 {
					t.Fatalf("expected 1 certificate ref, got %d", len(listener.TLS.CertificateRefs))
				}
				expectedSecret := naming.CertificateSecretName(&appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{Name: "myapp", Namespace: "default"},
				})
				if string(listener.TLS.CertificateRefs[0].Name) != expectedSecret {
					t.Errorf("expected cert ref name %s, got %s", expectedSecret, listener.TLS.CertificateRefs[0].Name)
				}
				// Verify AllowedRoutes restricts to the NebariApp's namespace
				if listener.AllowedRoutes == nil || listener.AllowedRoutes.Namespaces == nil ||
					listener.AllowedRoutes.Namespaces.From == nil ||
					*listener.AllowedRoutes.Namespaces.From != gatewayv1.NamespacesFromSelector {
					t.Error("expected AllowedRoutes with NamespacesFromSelector")
				}
				if listener.AllowedRoutes.Namespaces.Selector == nil ||
					listener.AllowedRoutes.Namespaces.Selector.MatchLabels["kubernetes.io/metadata.name"] != "default" {
					t.Error("expected namespace selector matching 'default'")
				}
			},
		},
		{
			name: "TLS disabled when routing is nil (externally managed)",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-no-routing",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "app-no-routing.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					// No Routing config - TLS disabled for externally managed routing
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       false,
			expectNilResult:   true, // TLS disabled when routing is nil
			validateResult: func(t *testing.T, result *TLSResult) {
				if result != nil {
					t.Fatal("expected nil result when routing is nil (TLS disabled)")
				}
			},
		},
		{
			name: "No ClusterIssuer configured and no secretName: skip TLS, fall back to shared listener",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing:  &appsv1.RoutingConfig{},
				},
			},
			clusterIssuerName: "", // Empty - no ClusterIssuer configured
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       false,
			expectNilResult:   true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil {
					t.Fatal("expected TLSReady condition to be set")
				}
				if c.Status != metav1.ConditionFalse {
					t.Errorf("expected TLSReady=False, got %s", c.Status)
				}
				if c.Reason != "ClusterIssuerNotConfigured" {
					t.Errorf("expected reason ClusterIssuerNotConfigured, got %s", c.Reason)
				}
			},
		},
		{
			name: "Gateway not found returns error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing:  &appsv1.RoutingConfig{},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           nil, // No gateway exists
			expectError:       true,
			expectNilResult:   true,
		},
		{
			name: "TLS enabled with internal gateway patches internal gateway",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "internal-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "internal.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Gateway:  "internal",
					Routing:  &appsv1.RoutingConfig{},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.InternalGatewayName),
			expectError:       false,
			expectNilResult:   false,
			validateGateway: func(t *testing.T, gw *gatewayv1.Gateway) {
				if len(gw.Spec.Listeners) != 1 {
					t.Fatalf("expected 1 listener, got %d", len(gw.Spec.Listeners))
				}
				if gw.Name != constants.InternalGatewayName {
					t.Errorf("expected internal gateway, got %s", gw.Name)
				}
			},
		},
		{
			name: "Certificate already ready sets TLSReady True",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ready-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "ready.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing:  &appsv1.RoutingConfig{},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "ready-app", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
				},
				Spec: certmanagerv1.CertificateSpec{
					SecretName: naming.CertificateSecretName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "ready-app", Namespace: "default"}}),
					DNSNames:   []string{"ready.example.com"},
					IssuerRef: cmmeta.ObjectReference{
						Name: "letsencrypt-prod",
						Kind: "ClusterIssuer",
					},
				},
				Status: certmanagerv1.CertificateStatus{
					Conditions: []certmanagerv1.CertificateCondition{
						{
							Type:   certmanagerv1.CertificateConditionReady,
							Status: cmmeta.ConditionTrue,
						},
					},
				},
			},
			expectError:     false,
			expectNilResult: false,
			validateResult: func(t *testing.T, result *TLSResult) {
				if !result.CertReady {
					t.Error("expected CertReady=true when Certificate has Ready=True")
				}
			},
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil {
					t.Fatal("expected TLSReady condition to be set")
				}
				if c.Status != metav1.ConditionTrue {
					t.Errorf("expected TLSReady=True, got %s", c.Status)
				}
			},
		},
		{
			name: "Existing listener is updated rather than duplicated",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "update-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "update.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing:  &appsv1.RoutingConfig{},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway: newGateway(constants.PublicGatewayName, gatewayv1.Listener{
				Name:     gatewayv1.SectionName(naming.ListenerName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "update-app", Namespace: "default"}})),
				Port:     443,
				Protocol: gatewayv1.HTTPSProtocolType,
			}),
			expectError:     false,
			expectNilResult: false,
			validateGateway: func(t *testing.T, gw *gatewayv1.Gateway) {
				// Should still have exactly 1 listener, not 2
				if len(gw.Spec.Listeners) != 1 {
					t.Fatalf("expected 1 listener (updated, not duplicated), got %d", len(gw.Spec.Listeners))
				}
				listener := gw.Spec.Listeners[0]
				// Verify the listener was actually updated with full spec, not just the original stub
				if listener.Hostname == nil || string(*listener.Hostname) != "update.example.com" {
					t.Errorf("expected hostname update.example.com, got %v", listener.Hostname)
				}
				if listener.TLS == nil {
					t.Fatal("expected TLS config on updated listener")
				}
				if listener.TLS.Mode == nil || *listener.TLS.Mode != gatewayv1.TLSModeTerminate {
					t.Errorf("expected TLS mode Terminate, got %v", listener.TLS.Mode)
				}
				if listener.AllowedRoutes == nil || listener.AllowedRoutes.Namespaces == nil ||
					listener.AllowedRoutes.Namespaces.From == nil ||
					*listener.AllowedRoutes.Namespaces.From != gatewayv1.NamespacesFromSelector {
					t.Error("expected AllowedRoutes with NamespacesFromSelector")
				}
				if listener.AllowedRoutes.Namespaces.Selector == nil ||
					listener.AllowedRoutes.Namespaces.Selector.MatchLabels["kubernetes.io/metadata.name"] != "default" {
					t.Error("expected namespace selector matching 'default'")
				}
			},
		},
		{
			name: "Certificate update via CreateOrUpdate preserves existing cert",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "update-cert-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "update-cert.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing:  &appsv1.RoutingConfig{},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "update-cert-app", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
				},
				Spec: certmanagerv1.CertificateSpec{
					SecretName: "old-secret-name",
					DNSNames:   []string{"old.example.com"},
					IssuerRef: cmmeta.ObjectReference{
						Name: "old-issuer",
						Kind: "ClusterIssuer",
					},
				},
			},
			expectError:     false,
			expectNilResult: false,
			validateCert: func(t *testing.T, cert *certmanagerv1.Certificate) {
				// Cert should be updated with new spec values
				expectedSecretName := naming.CertificateSecretName(&appsv1.NebariApp{
					ObjectMeta: metav1.ObjectMeta{Name: "update-cert-app", Namespace: "default"},
				})
				if cert.Spec.SecretName != expectedSecretName {
					t.Errorf("expected secretName %s, got %s", expectedSecretName, cert.Spec.SecretName)
				}
				if len(cert.Spec.DNSNames) != 1 || cert.Spec.DNSNames[0] != "update-cert.example.com" {
					t.Errorf("expected dnsNames [update-cert.example.com], got %v", cert.Spec.DNSNames)
				}
				if cert.Spec.IssuerRef.Name != "letsencrypt-prod" {
					t.Errorf("expected issuerRef name letsencrypt-prod, got %s", cert.Spec.IssuerRef.Name)
				}
				// Labels should be set
				if cert.Labels["nebari.dev/nebariapp-name"] != "update-cert-app" {
					t.Errorf("expected nebariapp-name label, got %v", cert.Labels)
				}
			},
		},
		{
			name: "secretName set with valid TLS secret: listener added, no Certificate, TLSReady=True",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "bring-your-own", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "byo.example.com",
					Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: "my-wildcard-tls",
						},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "my-wildcard-tls", Namespace: constants.GatewayNamespace},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("stubcert"),
					corev1.TLSPrivateKeyKey: []byte("stubkey"),
				},
			},
			expectError:     false,
			expectNilResult: false,
			validateResult: func(t *testing.T, result *TLSResult) {
				if result.SecretName != "my-wildcard-tls" {
					t.Errorf("expected result.SecretName = my-wildcard-tls, got %s", result.SecretName)
				}
				if !result.CertReady {
					t.Error("expected CertReady=true for a valid user-provided secret")
				}
			},
			validateGateway: func(t *testing.T, gw *gatewayv1.Gateway) {
				if len(gw.Spec.Listeners) != 1 {
					t.Fatalf("expected 1 listener, got %d", len(gw.Spec.Listeners))
				}
				refs := gw.Spec.Listeners[0].TLS.CertificateRefs
				if len(refs) != 1 || string(refs[0].Name) != "my-wildcard-tls" {
					t.Errorf("expected listener cert ref my-wildcard-tls, got %+v", refs)
				}
			},
			validateCertAbsent: true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil || c.Status != metav1.ConditionTrue || c.Reason != appsv1.ReasonUserProvidedSecretReady {
					t.Errorf("expected TLSReady=True/UserProvidedSecretReady, got %+v", c)
				}
			},
		},
		{
			name: "secretName set with missing secret: listener still added, TLSReady=False/NotFound",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-secret", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "missing.example.com",
					Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: "absent-tls",
						},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			existingSecret:    nil,
			expectError:       false,
			expectNilResult:   false,
			validateGateway: func(t *testing.T, gw *gatewayv1.Gateway) {
				if len(gw.Spec.Listeners) != 1 {
					t.Fatalf("expected listener to be added even when secret missing, got %d", len(gw.Spec.Listeners))
				}
			},
			validateCertAbsent: true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil || c.Status != metav1.ConditionFalse || c.Reason != appsv1.ReasonUserProvidedSecretNotFound {
					t.Errorf("expected TLSReady=False/UserProvidedSecretNotFound, got %+v", c)
				}
			},
		},
		{
			name: "secretName set with wrong-type secret: TLSReady=False/InvalidType",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-type", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "badtype.example.com",
					Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: "opaque-secret",
						},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "opaque-secret", Namespace: constants.GatewayNamespace},
				Type:       corev1.SecretTypeOpaque,
			},
			expectError:        false,
			expectNilResult:    false,
			validateCertAbsent: true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil || c.Status != metav1.ConditionFalse || c.Reason != appsv1.ReasonUserProvidedSecretInvalidType {
					t.Errorf("expected TLSReady=False/UserProvidedSecretInvalidType, got %+v", c)
				}
			},
		},
		{
			name: "secretName set with empty ClusterIssuerName: reconcile succeeds",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "no-issuer", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "noissuer.example.com",
					Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: "my-tls",
						},
					},
				},
			},
			clusterIssuerName: "",
			gateway:           newGateway(constants.PublicGatewayName),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: constants.GatewayNamespace},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("stubcert"),
					corev1.TLSPrivateKeyKey: []byte("stubkey"),
				},
			},
			expectError:        false,
			expectNilResult:    false,
			validateCertAbsent: true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil || c.Status != metav1.ConditionTrue {
					t.Errorf("expected TLSReady=True when secretName supplied without ClusterIssuer, got %+v", c)
				}
			},
		},
		{
			name: "enabled=false with secretName set: HTTP-only, secretName ignored",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "disabled-with-secret", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "disabled.example.com",
					Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled:    boolPtr(false),
							SecretName: "should-be-ignored",
						},
					},
				},
			},
			clusterIssuerName:  "letsencrypt-prod",
			gateway:            newGateway(constants.PublicGatewayName),
			expectError:        false,
			expectNilResult:    true,
			validateCertAbsent: true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady)
				if c == nil || c.Status != metav1.ConditionFalse || c.Reason != "TLSDisabled" {
					t.Errorf("expected TLSReady=False/TLSDisabled (secretName must be ignored when disabled), got %+v", c)
				}
			},
		},
		{
			name: "secretName set with pre-existing owned Certificate: Certificate is cleaned up",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "migrate.example.com",
					Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: "my-tls",
						},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "my-tls", Namespace: constants.GatewayNamespace},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("stubcert"),
					corev1.TLSPrivateKeyKey: []byte("stubkey"),
				},
			},
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by":   "nebari-operator",
						"nebari.dev/nebariapp-name":      "migrate",
						"nebari.dev/nebariapp-namespace": "default",
					},
				},
			},
			expectError:        false,
			expectNilResult:    false,
			validateCertAbsent: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp)

			if tt.gateway != nil {
				builder = builder.WithObjects(tt.gateway)
			}
			if tt.existingCert != nil {
				builder = builder.WithObjects(tt.existingCert)
			}
			if tt.existingSecret != nil {
				builder = builder.WithObjects(tt.existingSecret)
			}

			fakeClient := builder.Build()
			reconciler := &TLSReconciler{
				Client:            fakeClient,
				Scheme:            scheme,
				Recorder:          record.NewFakeRecorder(10),
				ClusterIssuerName: tt.clusterIssuerName,
			}

			result, err := reconciler.ReconcileTLS(context.Background(), tt.nebariApp)

			// Check error expectation
			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			// Check nil result expectation
			if tt.expectNilResult && result != nil {
				t.Errorf("expected nil result, got %+v", result)
			}
			if !tt.expectNilResult && result == nil {
				t.Error("expected non-nil result, got nil")
			}

			// Run result validation
			if tt.validateResult != nil && result != nil {
				tt.validateResult(t, result)
			}

			// Validate Certificate was created
			if tt.validateCert != nil {
				cert := &certmanagerv1.Certificate{}
				certName := naming.CertificateName(tt.nebariApp)
				err := fakeClient.Get(context.Background(), types.NamespacedName{
					Name:      certName,
					Namespace: constants.GatewayNamespace,
				}, cert)
				if err != nil {
					t.Fatalf("expected Certificate to exist, got error: %v", err)
				}
				tt.validateCert(t, cert)
			}

			if tt.validateCertAbsent {
				cert := &certmanagerv1.Certificate{}
				err := fakeClient.Get(context.Background(), types.NamespacedName{
					Name:      naming.CertificateName(tt.nebariApp),
					Namespace: constants.GatewayNamespace,
				}, cert)
				if err == nil {
					t.Error("expected Certificate to be absent (user-provided secret path), but it exists")
				}
			}

			// Validate Gateway was patched
			if tt.validateGateway != nil {
				gw := &gatewayv1.Gateway{}
				gwName := naming.GatewayName(tt.nebariApp)
				err := fakeClient.Get(context.Background(), types.NamespacedName{
					Name:      gwName,
					Namespace: constants.GatewayNamespace,
				}, gw)
				if err != nil {
					t.Fatalf("expected Gateway to exist, got error: %v", err)
				}
				tt.validateGateway(t, gw)
			}

			// Validate conditions
			if tt.validateConditions != nil {
				tt.validateConditions(t, tt.nebariApp)
			}
		})
	}
}

func TestCleanupTLS(t *testing.T) {
	scheme := newScheme()

	tests := []struct {
		name         string
		nebariApp    *appsv1.NebariApp
		gateway      *gatewayv1.Gateway
		existingCert *certmanagerv1.Certificate
		expectError  bool
		validateGW   func(*testing.T, *gatewayv1.Gateway)
	}{
		{
			name: "Cleanup removes Certificate and listener from Gateway",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cleanup-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "cleanup.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			gateway: newGateway(constants.PublicGatewayName, gatewayv1.Listener{
				Name:     gatewayv1.SectionName(naming.ListenerName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "cleanup-app", Namespace: "default"}})),
				Port:     443,
				Protocol: gatewayv1.HTTPSProtocolType,
			}, gatewayv1.Listener{
				Name:     "other-listener",
				Port:     443,
				Protocol: gatewayv1.HTTPSProtocolType,
			}),
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "cleanup-app", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
					Labels: map[string]string{
						"nebari.dev/nebariapp-name":      "cleanup-app",
						"nebari.dev/nebariapp-namespace": "default",
					},
				},
			},
			expectError: false,
			validateGW: func(t *testing.T, gw *gatewayv1.Gateway) {
				// Only the "other-listener" should remain
				if len(gw.Spec.Listeners) != 1 {
					t.Fatalf("expected 1 remaining listener, got %d", len(gw.Spec.Listeners))
				}
				if string(gw.Spec.Listeners[0].Name) != "other-listener" {
					t.Errorf("expected other-listener to remain, got %s", gw.Spec.Listeners[0].Name)
				}
			},
		},
		{
			name: "Cleanup when Certificate already deleted does not error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "already-gone",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "gone.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			gateway:      newGateway(constants.PublicGatewayName),
			existingCert: nil, // Certificate already deleted
			expectError:  false,
		},
		{
			name: "Cleanup when Gateway not found does not error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-gw-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "no-gw.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			gateway:      nil, // No gateway
			existingCert: nil,
			expectError:  false,
		},
		{
			name: "Cleanup with internal gateway",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "internal-cleanup",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "internal-cleanup.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Gateway:  "internal",
				},
			},
			gateway: newGateway(constants.InternalGatewayName, gatewayv1.Listener{
				Name:     gatewayv1.SectionName(naming.ListenerName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "internal-cleanup", Namespace: "default"}})),
				Port:     443,
				Protocol: gatewayv1.HTTPSProtocolType,
			}),
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "internal-cleanup", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
					Labels: map[string]string{
						"nebari.dev/nebariapp-name":      "internal-cleanup",
						"nebari.dev/nebariapp-namespace": "default",
					},
				},
			},
			expectError: false,
			validateGW: func(t *testing.T, gw *gatewayv1.Gateway) {
				if len(gw.Spec.Listeners) != 0 {
					t.Errorf("expected 0 listeners after cleanup, got %d", len(gw.Spec.Listeners))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.nebariApp)

			if tt.gateway != nil {
				builder = builder.WithObjects(tt.gateway)
			}
			if tt.existingCert != nil {
				builder = builder.WithObjects(tt.existingCert)
			}

			fakeClient := builder.Build()
			reconciler := &TLSReconciler{
				Client:            fakeClient,
				Scheme:            scheme,
				Recorder:          record.NewFakeRecorder(10),
				ClusterIssuerName: "letsencrypt-prod",
			}

			err := reconciler.CleanupTLS(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			// Verify Certificate was deleted
			if tt.existingCert != nil && !tt.expectError {
				cert := &certmanagerv1.Certificate{}
				err := fakeClient.Get(context.Background(), types.NamespacedName{
					Name:      tt.existingCert.Name,
					Namespace: tt.existingCert.Namespace,
				}, cert)
				if err == nil {
					t.Error("expected Certificate to be deleted, but it still exists")
				}
			}

			// Validate Gateway state
			if tt.validateGW != nil {
				gw := &gatewayv1.Gateway{}
				gwName := naming.GatewayName(tt.nebariApp)
				err := fakeClient.Get(context.Background(), types.NamespacedName{
					Name:      gwName,
					Namespace: constants.GatewayNamespace,
				}, gw)
				if err != nil {
					t.Fatalf("expected Gateway to exist, got error: %v", err)
				}
				tt.validateGW(t, gw)
			}
		})
	}
}

func TestCleanupOwnedCertificate(t *testing.T) {
	scheme := newScheme()

	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "migrate-app", Namespace: "default"},
		Spec: appsv1.NebariAppSpec{
			Hostname: "migrate.example.com",
			Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
		},
	}
	certName := naming.CertificateName(app)

	tests := []struct {
		name          string
		existingCert  *certmanagerv1.Certificate
		expectError   bool
		expectDeleted bool
	}{
		{
			name: "Owned Certificate is deleted",
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: constants.GatewayNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by":   "nebari-operator",
						"nebari.dev/nebariapp-name":      "migrate-app",
						"nebari.dev/nebariapp-namespace": "default",
					},
				},
			},
			expectError:   false,
			expectDeleted: true,
		},
		{
			name:          "Absent Certificate is a no-op",
			existingCert:  nil,
			expectError:   false,
			expectDeleted: false,
		},
		{
			name: "Certificate with mismatched labels is preserved",
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: constants.GatewayNamespace,
					Labels: map[string]string{
						"nebari.dev/nebariapp-name":      "some-other-app",
						"nebari.dev/nebariapp-namespace": "default",
					},
				},
			},
			expectError:   false,
			expectDeleted: false,
		},
		{
			name: "Certificate with no labels is preserved",
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: constants.GatewayNamespace,
				},
			},
			expectError:   false,
			expectDeleted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingCert != nil {
				builder = builder.WithObjects(tt.existingCert)
			}
			fakeClient := builder.Build()
			reconciler := &TLSReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.cleanupOwnedCertificate(context.Background(), app)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if tt.existingCert != nil {
				cert := &certmanagerv1.Certificate{}
				getErr := fakeClient.Get(context.Background(), types.NamespacedName{
					Name:      certName,
					Namespace: constants.GatewayNamespace,
				}, cert)
				if tt.expectDeleted && getErr == nil {
					t.Error("expected Certificate to be deleted but it still exists")
				}
				if !tt.expectDeleted && getErr != nil {
					t.Errorf("expected Certificate to be preserved but got error: %v", getErr)
				}
			}
		})
	}
}

func TestCheckUserProvidedSecret(t *testing.T) {
	scheme := newScheme()

	tests := []struct {
		name           string
		secretName     string
		existingSecret *corev1.Secret
		getErr         error
		expectStatus   metav1.ConditionStatus
		expectReason   string
	}{
		{
			name:       "Valid kubernetes.io/tls secret yields Ready=True",
			secretName: "my-tls",
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-tls",
					Namespace: constants.GatewayNamespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       []byte("-----BEGIN CERTIFICATE-----\nstub\n-----END CERTIFICATE-----\n"),
					corev1.TLSPrivateKeyKey: []byte("-----BEGIN PRIVATE KEY-----\nstub\n-----END PRIVATE KEY-----\n"),
				},
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: appsv1.ReasonUserProvidedSecretReady,
		},
		{
			name:       "kubernetes.io/tls secret with empty data yields InvalidType",
			secretName: "empty-tls",
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "empty-tls",
					Namespace: constants.GatewayNamespace,
				},
				Type: corev1.SecretTypeTLS,
				// Data intentionally nil: simulates `kubectl apply -f` of a
				// kubernetes.io/tls secret without the required PEM keys.
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: appsv1.ReasonUserProvidedSecretInvalidType,
		},
		{
			name:       "kubernetes.io/tls secret missing tls.key yields InvalidType",
			secretName: "no-key-tls",
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "no-key-tls",
					Namespace: constants.GatewayNamespace,
				},
				Type: corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey: []byte("-----BEGIN CERTIFICATE-----\nstub\n-----END CERTIFICATE-----\n"),
				},
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: appsv1.ReasonUserProvidedSecretInvalidType,
		},
		{
			name:           "Missing secret yields NotFound",
			secretName:     "absent-tls",
			existingSecret: nil,
			expectStatus:   metav1.ConditionFalse,
			expectReason:   appsv1.ReasonUserProvidedSecretNotFound,
		},
		{
			name:       "Opaque secret yields InvalidType",
			secretName: "opaque-tls",
			existingSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "opaque-tls",
					Namespace: constants.GatewayNamespace,
				},
				Type: corev1.SecretTypeOpaque,
			},
			expectStatus: metav1.ConditionFalse,
			expectReason: appsv1.ReasonUserProvidedSecretInvalidType,
		},
		{
			name:         "Transient API error yields CheckFailed (not NotFound)",
			secretName:   "any-tls",
			getErr:       fmt.Errorf("etcdserver: request timed out"),
			expectStatus: metav1.ConditionFalse,
			expectReason: appsv1.ReasonUserProvidedSecretCheckFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingSecret != nil {
				builder = builder.WithObjects(tt.existingSecret)
			}
			if tt.getErr != nil {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(_ context.Context, _ client.WithWatch, _ client.ObjectKey, _ client.Object, _ ...client.GetOption) error {
						return tt.getErr
					},
				})
			}
			fakeClient := builder.Build()
			reconciler := &TLSReconciler{
				Client:   fakeClient,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			status, reason, msg := reconciler.checkUserProvidedSecret(context.Background(), tt.secretName)
			if status != tt.expectStatus {
				t.Errorf("expected status %s, got %s", tt.expectStatus, status)
			}
			if reason != tt.expectReason {
				t.Errorf("expected reason %s, got %s", tt.expectReason, reason)
			}
			if msg == "" {
				t.Error("expected non-empty message")
			}
		})
	}
}

// TestCleanupTLSLeavesUnownedCertificateAlone verifies that CleanupTLS does not
// delete a Certificate whose name collides with this NebariApp's derived name
// but whose ownership labels point at a different NebariApp. The previous
// implementation deleted by name only, which could remove another app's
// Certificate in the unlikely event of a name collision.
func TestCleanupTLSLeavesUnownedCertificateAlone(t *testing.T) {
	scheme := newScheme()

	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "collide-app", Namespace: "default"},
		Spec: appsv1.NebariAppSpec{
			Hostname: "collide.example.com",
			Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
		},
	}
	certName := naming.CertificateName(app)

	// A Certificate at the same derived name, but labeled as owned by a
	// different NebariApp ("other-app"/"other-ns"). CleanupTLS for `app` must
	// leave this Certificate in place.
	foreignCert := &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certName,
			Namespace: constants.GatewayNamespace,
			Labels: map[string]string{
				"nebari.dev/nebariapp-name":      "other-app",
				"nebari.dev/nebariapp-namespace": "other-ns",
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app, foreignCert, newGateway(constants.PublicGatewayName)).
		Build()
	reconciler := &TLSReconciler{
		Client:            fakeClient,
		Scheme:            scheme,
		Recorder:          record.NewFakeRecorder(10),
		ClusterIssuerName: "letsencrypt-prod",
	}

	if err := reconciler.CleanupTLS(context.Background(), app); err != nil {
		t.Fatalf("CleanupTLS returned error: %v", err)
	}

	got := &certmanagerv1.Certificate{}
	err := fakeClient.Get(context.Background(), types.NamespacedName{
		Name:      certName,
		Namespace: constants.GatewayNamespace,
	}, got)
	if err != nil {
		t.Fatalf("expected unowned Certificate to be preserved, but Get returned: %v", err)
	}
	if got.Labels["nebari.dev/nebariapp-name"] != "other-app" {
		t.Errorf("expected preserved Certificate's owner label to remain 'other-app', got %q",
			got.Labels["nebari.dev/nebariapp-name"])
	}
}

// TestUserProvidedSecretEventDedup verifies that user-provided-secret events are
// emitted only when the TLSReady condition reason transitions, not on every
// reconcile pass. This protects against event spam (~30s-1m reconcile cadence)
// drowning out actual state transitions in `kubectl describe`.
func TestUserProvidedSecretEventDedup(t *testing.T) {
	scheme := newScheme()

	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{Name: "byo-event-dedup", Namespace: "default"},
		Spec: appsv1.NebariAppSpec{
			Hostname: "byo-event-dedup.example.com",
			Service:  appsv1.ServiceReference{Name: "svc", Port: 8080},
			Routing: &appsv1.RoutingConfig{
				TLS: &appsv1.RoutingTLSConfig{
					Enabled:    boolPtr(true),
					SecretName: "byo-tls",
				},
			},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "byo-tls", Namespace: constants.GatewayNamespace},
		Type:       corev1.SecretTypeTLS,
		Data: map[string][]byte{
			corev1.TLSCertKey:       []byte("stubcert"),
			corev1.TLSPrivateKeyKey: []byte("stubkey"),
		},
	}
	gw := newGateway(constants.PublicGatewayName)

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(app, secret, gw).
		Build()
	recorder := record.NewFakeRecorder(20)
	reconciler := &TLSReconciler{
		Client:            fakeClient,
		Scheme:            scheme,
		Recorder:          recorder,
		ClusterIssuerName: "letsencrypt-prod",
	}

	// First reconcile: condition transitions from absent -> UserProvidedSecretReady.
	// Expect one InUse event.
	if _, err := reconciler.ReconcileTLS(context.Background(), app); err != nil {
		t.Fatalf("first reconcile: unexpected error: %v", err)
	}
	if got := countEventsWithReason(recorder, appsv1.EventReasonUserProvidedSecretInUse); got != 1 {
		t.Errorf("after first reconcile: expected exactly 1 UserProvidedSecretInUse event, got %d", got)
	}

	// Drain the recorder so the next assertion is independent.
	drainRecorder(recorder)

	// Second reconcile with the secret unchanged: reason should be the same and
	// no new InUse event should fire.
	if _, err := reconciler.ReconcileTLS(context.Background(), app); err != nil {
		t.Fatalf("second reconcile: unexpected error: %v", err)
	}
	if got := countEventsWithReason(recorder, appsv1.EventReasonUserProvidedSecretInUse); got != 0 {
		t.Errorf("after no-op reconcile: expected 0 UserProvidedSecretInUse events (dedup), got %d", got)
	}

	// Delete the secret. Next reconcile should transition reason to NotFound
	// and fire exactly one NotFound event.
	if err := fakeClient.Delete(context.Background(), secret); err != nil {
		t.Fatalf("delete secret: %v", err)
	}
	drainRecorder(recorder)
	if _, err := reconciler.ReconcileTLS(context.Background(), app); err != nil {
		t.Fatalf("third reconcile: unexpected error: %v", err)
	}
	if got := countEventsWithReason(recorder, appsv1.EventReasonUserProvidedSecretNotFound); got != 1 {
		t.Errorf("after secret delete: expected 1 UserProvidedSecretNotFound event, got %d", got)
	}
	// The condition itself must have moved off Ready.
	if c := conditions.GetCondition(app, appsv1.ConditionTypeTLSReady); c == nil ||
		c.Reason != appsv1.ReasonUserProvidedSecretNotFound {
		t.Errorf("expected TLSReady reason UserProvidedSecretNotFound after secret delete, got %+v", c)
	}
}

// countEventsWithReason drains the FakeRecorder's channel and counts how many
// of the buffered events contain the given reason substring. FakeRecorder
// formats each event as "<type> <reason> <message>", so a substring match on
// the reason is reliable.
func countEventsWithReason(r *record.FakeRecorder, reason string) int {
	count := 0
	for {
		select {
		case ev := <-r.Events:
			if strings.Contains(ev, reason) {
				count++
			}
		default:
			return count
		}
	}
}

func drainRecorder(r *record.FakeRecorder) {
	for {
		select {
		case <-r.Events:
		default:
			return
		}
	}
}

func TestIsCertificateReady(t *testing.T) {
	scheme := newScheme()

	tests := []struct {
		name        string
		nebariApp   *appsv1.NebariApp
		cert        *certmanagerv1.Certificate
		expectReady bool
		expectError bool
	}{
		{
			name: "Returns true when Certificate has Ready=True",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "ready-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "ready.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			cert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "ready-app", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
				},
				Status: certmanagerv1.CertificateStatus{
					Conditions: []certmanagerv1.CertificateCondition{
						{Type: certmanagerv1.CertificateConditionReady, Status: cmmeta.ConditionTrue},
					},
				},
			},
			expectReady: true,
			expectError: false,
		},
		{
			name: "Returns false when Certificate has Ready=False",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "not-ready-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "not-ready.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			cert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "not-ready-app", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
				},
				Status: certmanagerv1.CertificateStatus{
					Conditions: []certmanagerv1.CertificateCondition{
						{Type: certmanagerv1.CertificateConditionReady, Status: cmmeta.ConditionFalse},
					},
				},
			},
			expectReady: false,
			expectError: false,
		},
		{
			name: "Returns error when Certificate does not exist",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "missing-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "missing.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			cert:        nil, // No certificate exists
			expectReady: false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().
				WithScheme(scheme)

			if tt.cert != nil {
				builder = builder.WithObjects(tt.cert)
			}

			fakeClient := builder.Build()
			reconciler := &TLSReconciler{
				Client: fakeClient,
				Scheme: scheme,
			}

			ready, err := reconciler.isCertificateReady(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}
			if ready != tt.expectReady {
				t.Errorf("expected ready=%v, got ready=%v", tt.expectReady, ready)
			}
		})
	}
}
