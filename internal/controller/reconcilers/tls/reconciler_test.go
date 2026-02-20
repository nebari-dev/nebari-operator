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
	"testing"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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

func TestReconcileTLS(t *testing.T) {
	scheme := newScheme()

	tests := []struct {
		name               string
		nebariApp          *appsv1.NebariApp
		clusterIssuerName  string
		gateway            *gatewayv1.Gateway
		existingCert       *certmanagerv1.Certificate
		expectError        bool
		expectNilResult    bool
		validateResult     func(*testing.T, *TLSResult)
		validateCert       func(*testing.T, *certmanagerv1.Certificate)
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
				found := false
				for _, c := range app.Status.Conditions {
					if c.Type == appsv1.ConditionTypeTLSReady {
						found = true
						if c.Status != metav1.ConditionFalse {
							t.Errorf("expected TLSReady=False, got %s", c.Status)
						}
						if c.Reason != "TLSDisabled" {
							t.Errorf("expected reason TLSDisabled, got %s", c.Reason)
						}
					}
				}
				if !found {
					t.Error("expected TLSReady condition to be set")
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
				if cert.Labels["nebari.dev/nebariapp-namespace"] != "default" {
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
				// Verify AllowedRoutes from All
				if listener.AllowedRoutes == nil || listener.AllowedRoutes.Namespaces == nil ||
					listener.AllowedRoutes.Namespaces.From == nil ||
					*listener.AllowedRoutes.Namespaces.From != gatewayv1.NamespacesFromAll {
					t.Error("expected AllowedRoutes with namespaces from All")
				}
			},
		},
		{
			name: "TLS enabled with nil Enabled (default true) creates Certificate",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "app-default-tls",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "app-default.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					// No Routing config at all - TLS defaults to enabled
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       false,
			expectNilResult:   false,
			validateResult: func(t *testing.T, result *TLSResult) {
				if result == nil {
					t.Fatal("expected non-nil result for default TLS-enabled app")
				}
			},
			validateCert: func(t *testing.T, cert *certmanagerv1.Certificate) {
				if len(cert.Spec.DNSNames) != 1 || cert.Spec.DNSNames[0] != "app-default.example.com" {
					t.Errorf("expected dnsNames [app-default.example.com], got %v", cert.Spec.DNSNames)
				}
			},
		},
		{
			name: "No ClusterIssuer configured returns error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app",
					Namespace: "default",
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
				},
			},
			clusterIssuerName: "", // Empty - no ClusterIssuer configured
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       true,
			expectNilResult:   true,
			validateConditions: func(t *testing.T, app *appsv1.NebariApp) {
				found := false
				for _, c := range app.Status.Conditions {
					if c.Type == appsv1.ConditionTypeTLSReady {
						found = true
						if c.Status != metav1.ConditionFalse {
							t.Errorf("expected TLSReady=False, got %s", c.Status)
						}
					}
				}
				if !found {
					t.Error("expected TLSReady condition to be set")
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
				found := false
				for _, c := range app.Status.Conditions {
					if c.Type == appsv1.ConditionTypeTLSReady {
						found = true
						if c.Status != metav1.ConditionTrue {
							t.Errorf("expected TLSReady=True, got %s", c.Status)
						}
					}
				}
				if !found {
					t.Error("expected TLSReady condition to be set")
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
					t.Errorf("expected 1 listener (updated, not duplicated), got %d", len(gw.Spec.Listeners))
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

			// Validate Gateway was patched
			if tt.validateGateway != nil {
				gw := &gatewayv1.Gateway{}
				gwName := constants.PublicGatewayName
				if tt.nebariApp.Spec.Gateway == "internal" {
					gwName = constants.InternalGatewayName
				}
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
				gwName := constants.PublicGatewayName
				if tt.nebariApp.Spec.Gateway == "internal" {
					gwName = constants.InternalGatewayName
				}
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
