# TLS Certificate Management Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a TLS reconciler that creates per-app cert-manager Certificates and per-app Gateway HTTPS listeners so NebariApp hostnames get TLS automatically.

**Architecture:** New `TLSReconciler` in the pipeline between Core and Routing. It creates a cert-manager `Certificate` in the Gateway namespace, patches the shared Gateway to add a per-hostname HTTPS listener, and returns the listener name to the Routing reconciler for HTTPRoute attachment. Cleanup is explicit via the existing finalizer since the Certificate is cross-namespace.

**Tech Stack:** Go, controller-runtime, cert-manager API types, Gateway API

**Design doc:** `docs/plans/2026-02-20-tls-certificate-management-design.md`

---

### Task 1: Add cert-manager Go dependency

**Files:**
- Modify: `go.mod`
- Modify: `cmd/main.go:54-61` (scheme registration)

**Step 1: Add cert-manager dependency**

Run:
```bash
cd /home/chuck/devel/nebari-operator
go get github.com/cert-manager/cert-manager@latest
```

**Step 2: Register cert-manager scheme in main.go**

In `cmd/main.go`, add import:
```go
certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
```

In `init()` function (after line 59), add:
```go
utilruntime.Must(certmanagerv1.AddToScheme(scheme))
```

**Step 3: Verify it compiles**

Run: `go build ./...`
Expected: Clean build with no errors.

**Step 4: Commit**

```bash
git add go.mod go.sum cmd/main.go
git commit -m "feat: add cert-manager dependency and register scheme"
```

---

### Task 2: Add TLS configuration

**Files:**
- Create: `internal/config/tls.go`
- Create: `internal/config/tls_test.go`

**Step 1: Write the test for TLS config loading**

Create `internal/config/tls_test.go`:
```go
package config

import (
	"os"
	"testing"
)

func TestLoadTLSConfig(t *testing.T) {
	tests := []struct {
		name                  string
		envVars               map[string]string
		expectedIssuerName    string
	}{
		{
			name:               "Default values",
			envVars:            map[string]string{},
			expectedIssuerName: "",
		},
		{
			name: "Custom issuer name",
			envVars: map[string]string{
				"TLS_CLUSTER_ISSUER_NAME": "letsencrypt-prod",
			},
			expectedIssuerName: "letsencrypt-prod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear env vars
			os.Unsetenv("TLS_CLUSTER_ISSUER_NAME")

			// Set test env vars
			for k, v := range tt.envVars {
				os.Setenv(k, v)
				defer os.Unsetenv(k)
			}

			config := LoadTLSConfig()

			if config.ClusterIssuerName != tt.expectedIssuerName {
				t.Errorf("expected ClusterIssuerName %q, got %q", tt.expectedIssuerName, config.ClusterIssuerName)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -v -run TestLoadTLSConfig`
Expected: FAIL - `LoadTLSConfig` not defined.

**Step 3: Write TLS config implementation**

Create `internal/config/tls.go`:
```go
package config

// TLSConfig holds TLS certificate management configuration for the operator.
type TLSConfig struct {
	// ClusterIssuerName is the name of the cert-manager ClusterIssuer to use.
	// When empty, the TLS reconciler will not create Certificate resources.
	ClusterIssuerName string
}

// LoadTLSConfig loads TLS configuration from environment variables.
func LoadTLSConfig() TLSConfig {
	return TLSConfig{
		ClusterIssuerName: getEnv("TLS_CLUSTER_ISSUER_NAME", ""),
	}
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v -run TestLoadTLSConfig`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/tls.go internal/config/tls_test.go
git commit -m "feat: add TLS configuration with ClusterIssuer env var"
```

---

### Task 3: Add TLS naming utilities

**Files:**
- Modify: `internal/controller/utils/naming/naming.go`
- Modify: `internal/controller/utils/naming/naming_test.go` (if exists, else create)

**Step 1: Write tests for TLS naming functions**

Add to or create `internal/controller/utils/naming/naming_test.go`:
```go
package naming

import (
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestCertificateName(t *testing.T) {
	tests := []struct {
		name     string
		app      *appsv1.NebariApp
		expected string
	}{
		{
			name: "Standard name",
			app: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
			},
			expected: "my-app-default-cert",
		},
		{
			name: "Different namespace",
			app: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "hub", Namespace: "data-science"},
			},
			expected: "hub-data-science-cert",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CertificateName(tt.app)
			if got != tt.expected {
				t.Errorf("CertificateName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestCertificateSecretName(t *testing.T) {
	tests := []struct {
		name     string
		app      *appsv1.NebariApp
		expected string
	}{
		{
			name: "Standard name",
			app: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
			},
			expected: "my-app-default-tls",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CertificateSecretName(tt.app)
			if got != tt.expected {
				t.Errorf("CertificateSecretName() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestListenerName(t *testing.T) {
	tests := []struct {
		name     string
		app      *appsv1.NebariApp
		expected string
	}{
		{
			name: "Standard name",
			app: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "my-app", Namespace: "default"},
			},
			expected: "tls-my-app-default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ListenerName(tt.app)
			if got != tt.expected {
				t.Errorf("ListenerName() = %q, want %q", got, tt.expected)
			}
		})
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/controller/utils/naming/ -v -run TestCertificate`
Expected: FAIL - functions not defined.

**Step 3: Add naming functions**

Add to `internal/controller/utils/naming/naming.go` (after line 37):
```go
// CertificateName generates the name for a cert-manager Certificate.
// Includes namespace to avoid collisions since Certificates live in the Gateway namespace.
// Pattern: <nebariapp-name>-<namespace>-cert
func CertificateName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-%s", nebariApp.Name, nebariApp.Namespace, constants.CertificateSuffix)
}

// CertificateSecretName generates the name for the TLS secret created by cert-manager.
// Pattern: <nebariapp-name>-<namespace>-tls
func CertificateSecretName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("%s-%s-tls", nebariApp.Name, nebariApp.Namespace)
}

// ListenerName generates the name for the per-app Gateway HTTPS listener.
// Pattern: tls-<nebariapp-name>-<namespace>
func ListenerName(nebariApp *appsv1.NebariApp) string {
	return fmt.Sprintf("tls-%s-%s", nebariApp.Name, nebariApp.Namespace)
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/controller/utils/naming/ -v`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/controller/utils/naming/
git commit -m "feat: add TLS certificate and listener naming utilities"
```

---

### Task 4: Add TLS event reason constants

**Files:**
- Modify: `api/v1/nebariapp_types.go:228-329`

**Step 1: Update condition and event constants**

In `api/v1/nebariapp_types.go`, update the `ConditionTypeTLSReady` doc comment (line 234-237) to remove the "Note: TLS certificates are managed by cert-manager, not by this operator" comment:
```go
// ConditionTypeTLSReady indicates that the TLS certificate has been provisioned
// and the Gateway listener is configured for this application's hostname.
ConditionTypeTLSReady = "TLSReady"
```

Also update the `RoutingTLSConfig` doc comment (lines 104-113) to reflect new behavior:
```go
// RoutingTLSConfig controls TLS termination for the HTTPRoute.
type RoutingTLSConfig struct {
	// Enabled determines whether TLS termination should be used.
	// When nil or true, the operator will create a cert-manager Certificate
	// for the application's hostname and configure a per-app Gateway HTTPS listener.
	// When explicitly set to false, only HTTP listeners will be used.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}
```

Add new event reason constants (after line 313):
```go
// EventReasonCertificateCreated is used when cert-manager Certificate is created
EventReasonCertificateCreated = "CertificateCreated"

// EventReasonCertificateUpdated is used when cert-manager Certificate is updated
EventReasonCertificateUpdated = "CertificateUpdated"

// EventReasonCertificateDeleted is used when cert-manager Certificate is deleted
EventReasonCertificateDeleted = "CertificateDeleted"

// EventReasonGatewayListenerAdded is used when a per-app listener is added to the Gateway
EventReasonGatewayListenerAdded = "GatewayListenerAdded"

// EventReasonGatewayListenerRemoved is used when a per-app listener is removed from the Gateway
EventReasonGatewayListenerRemoved = "GatewayListenerRemoved"
```

**Step 2: Regenerate CRDs**

Run: `make manifests generate`
Expected: CRDs regenerated, no errors.

**Step 3: Verify build**

Run: `go build ./...`
Expected: Clean build.

**Step 4: Commit**

```bash
git add api/v1/nebariapp_types.go config/crd/ config/rbac/
git commit -m "feat: update TLS condition docs and add certificate event constants"
```

---

### Task 5: Create TLS reconciler - Certificate management

This is the core task. We build the TLS reconciler in stages: Certificate management first, Gateway patching second.

**Files:**
- Create: `internal/controller/reconcilers/tls/reconciler.go`
- Create: `internal/controller/reconcilers/tls/reconciler_test.go`

**Step 1: Write failing tests for Certificate creation**

Create `internal/controller/reconcilers/tls/reconciler_test.go`:
```go
package tls

import (
	"context"
	"testing"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
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

func newTestScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = certmanagerv1.AddToScheme(scheme)
	_ = gatewayv1.Install(scheme)
	return scheme
}

func newTestGateway() *gatewayv1.Gateway {
	return &gatewayv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{
			Name:      constants.PublicGatewayName,
			Namespace: constants.GatewayNamespace,
		},
		Spec: gatewayv1.GatewaySpec{
			GatewayClassName: gatewayv1.ObjectName(constants.GatewayClassName),
			Listeners: []gatewayv1.Listener{
				{
					Name:     "http",
					Protocol: gatewayv1.HTTPProtocolType,
					Port:     80,
				},
				{
					Name:     "https",
					Protocol: gatewayv1.HTTPSProtocolType,
					Port:     443,
				},
			},
		},
	}
}

func TestReconcileTLS(t *testing.T) {
	scheme := newTestScheme()

	tests := []struct {
		name              string
		nebariApp         *appsv1.NebariApp
		clusterIssuerName string
		gateway           *gatewayv1.Gateway
		expectError       bool
		expectCert        bool
		expectListener    bool
		validateResult    func(*testing.T, *TLSResult)
	}{
		{
			name: "TLS disabled - skip reconciliation",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: boolPtr(false)},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newTestGateway(),
			expectError:       false,
			expectCert:        false,
			expectListener:    false,
			validateResult: func(t *testing.T, result *TLSResult) {
				if result != nil {
					t.Error("expected nil result when TLS is disabled")
				}
			},
		},
		{
			name: "TLS enabled - creates Certificate and listener",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: boolPtr(true)},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newTestGateway(),
			expectError:       false,
			expectCert:        true,
			expectListener:    true,
			validateResult: func(t *testing.T, result *TLSResult) {
				if result == nil {
					t.Fatal("expected non-nil result")
				}
				if result.ListenerName != "tls-test-app-default" {
					t.Errorf("expected listener name tls-test-app-default, got %s", result.ListenerName)
				}
				if result.SecretName != "test-app-default-tls" {
					t.Errorf("expected secret name test-app-default-tls, got %s", result.SecretName)
				}
			},
		},
		{
			name: "TLS enabled with nil (default true) - creates Certificate",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newTestGateway(),
			expectError:       false,
			expectCert:        true,
			expectListener:    true,
		},
		{
			name: "No ClusterIssuer configured - error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: boolPtr(true)},
					},
				},
			},
			clusterIssuerName: "",
			gateway:           newTestGateway(),
			expectError:       true,
			expectCert:        false,
			expectListener:    false,
		},
		{
			name: "Gateway not found - error",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: boolPtr(true)},
					},
				},
			},
			clusterIssuerName: "letsencrypt-prod",
			gateway:           nil,
			expectError:       true,
			expectCert:        false,
			expectListener:    false,
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

			client := builder.Build()

			reconciler := &TLSReconciler{
				Client:            client,
				Scheme:            scheme,
				Recorder:          record.NewFakeRecorder(10),
				ClusterIssuerName: tt.clusterIssuerName,
			}

			result, err := reconciler.ReconcileTLS(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			// Verify Certificate creation
			if tt.expectCert {
				cert := &certmanagerv1.Certificate{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      naming.CertificateName(tt.nebariApp),
					Namespace: constants.GatewayNamespace,
				}, cert)
				if err != nil {
					t.Errorf("expected Certificate to be created, got error: %v", err)
				} else {
					// Verify dnsNames
					if len(cert.Spec.DNSNames) != 1 || cert.Spec.DNSNames[0] != tt.nebariApp.Spec.Hostname {
						t.Errorf("expected dnsNames [%s], got %v", tt.nebariApp.Spec.Hostname, cert.Spec.DNSNames)
					}
					// Verify issuerRef
					if cert.Spec.IssuerRef.Name != tt.clusterIssuerName {
						t.Errorf("expected issuerRef name %s, got %s", tt.clusterIssuerName, cert.Spec.IssuerRef.Name)
					}
					if cert.Spec.IssuerRef.Kind != "ClusterIssuer" {
						t.Errorf("expected issuerRef kind ClusterIssuer, got %s", cert.Spec.IssuerRef.Kind)
					}
				}
			}

			// Verify Gateway listener
			if tt.expectListener && tt.gateway != nil {
				gw := &gatewayv1.Gateway{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      constants.PublicGatewayName,
					Namespace: constants.GatewayNamespace,
				}, gw)
				if err != nil {
					t.Fatalf("failed to get Gateway: %v", err)
				}
				listenerFound := false
				expectedName := gatewayv1.SectionName(naming.ListenerName(tt.nebariApp))
				for _, l := range gw.Spec.Listeners {
					if l.Name == expectedName {
						listenerFound = true
						if l.Protocol != gatewayv1.HTTPSProtocolType {
							t.Errorf("expected HTTPS protocol, got %s", l.Protocol)
						}
						if l.Port != 443 {
							t.Errorf("expected port 443, got %d", l.Port)
						}
						hostname := gatewayv1.Hostname(tt.nebariApp.Spec.Hostname)
						if l.Hostname == nil || *l.Hostname != hostname {
							t.Errorf("expected hostname %s, got %v", hostname, l.Hostname)
						}
					}
				}
				if !listenerFound {
					t.Errorf("expected listener %s not found in Gateway", expectedName)
				}
			}

			if tt.validateResult != nil {
				tt.validateResult(t, result)
			}
		})
	}
}

func TestCleanupTLS(t *testing.T) {
	scheme := newTestScheme()

	tests := []struct {
		name        string
		nebariApp   *appsv1.NebariApp
		gateway     *gatewayv1.Gateway
		certificate *certmanagerv1.Certificate
		expectError bool
	}{
		{
			name: "Cleanup removes Certificate and listener",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Gateway:  "public",
				},
			},
			gateway: func() *gatewayv1.Gateway {
				gw := newTestGateway()
				hostname := gatewayv1.Hostname("test.example.com")
				tlsMode := gatewayv1.TLSModeTerminate
				gw.Spec.Listeners = append(gw.Spec.Listeners, gatewayv1.Listener{
					Name:     "tls-test-app-default",
					Protocol: gatewayv1.HTTPSProtocolType,
					Port:     443,
					Hostname: &hostname,
					TLS: &gatewayv1.GatewayTLSConfig{
						Mode: &tlsMode,
						CertificateRefs: []gatewayv1.SecretObjectReference{
							{Name: "test-app-default-tls"},
						},
					},
				})
				return gw
			}(),
			certificate: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-app-default-cert",
					Namespace: constants.GatewayNamespace,
				},
			},
			expectError: false,
		},
		{
			name: "Cleanup when Certificate already deleted",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Gateway:  "public",
				},
			},
			gateway:     newTestGateway(),
			certificate: nil,
			expectError: false,
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
			if tt.certificate != nil {
				builder = builder.WithObjects(tt.certificate)
			}

			client := builder.Build()

			reconciler := &TLSReconciler{
				Client:   client,
				Scheme:   scheme,
				Recorder: record.NewFakeRecorder(10),
			}

			err := reconciler.CleanupTLS(context.Background(), tt.nebariApp)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got: %v", err)
			}

			// Verify Certificate is deleted
			if tt.certificate != nil && !tt.expectError {
				cert := &certmanagerv1.Certificate{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      tt.certificate.Name,
					Namespace: tt.certificate.Namespace,
				}, cert)
				if err == nil {
					t.Error("expected Certificate to be deleted, but it still exists")
				}
			}

			// Verify listener is removed from Gateway
			if tt.gateway != nil && !tt.expectError {
				gw := &gatewayv1.Gateway{}
				err := client.Get(context.Background(), types.NamespacedName{
					Name:      constants.PublicGatewayName,
					Namespace: constants.GatewayNamespace,
				}, gw)
				if err == nil {
					expectedName := gatewayv1.SectionName(naming.ListenerName(tt.nebariApp))
					for _, l := range gw.Spec.Listeners {
						if l.Name == expectedName {
							t.Error("expected listener to be removed, but it still exists")
						}
					}
				}
			}
		})
	}
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/controller/reconcilers/tls/ -v`
Expected: FAIL - package/functions don't exist.

**Step 3: Write the TLS reconciler implementation**

Create `internal/controller/reconcilers/tls/reconciler.go`:
```go
package tls

import (
	"context"
	"fmt"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// TLSResult contains information about the TLS configuration for use by the routing reconciler.
type TLSResult struct {
	// ListenerName is the per-app Gateway listener name (e.g., "tls-myapp-default")
	ListenerName string
	// SecretName is the TLS secret name created by cert-manager
	SecretName string
	// CertReady indicates whether the Certificate has Ready=True
	CertReady bool
}

// TLSReconciler manages cert-manager Certificates and Gateway HTTPS listeners for NebariApps.
type TLSReconciler struct {
	Client            client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	ClusterIssuerName string
}

// ReconcileTLS creates or updates the cert-manager Certificate and Gateway listener for a NebariApp.
// Returns a TLSResult with listener info for the routing reconciler, or nil if TLS is disabled.
func (r *TLSReconciler) ReconcileTLS(ctx context.Context, nebariApp *appsv1.NebariApp) (*TLSResult, error) {
	logger := log.FromContext(ctx)

	// Check if TLS is enabled (default: true when not explicitly set to false)
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil &&
		nebariApp.Spec.Routing.TLS.Enabled != nil && !*nebariApp.Spec.Routing.TLS.Enabled {
		logger.Info("TLS disabled, skipping TLS reconciliation")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"TLSDisabled", "TLS termination is disabled for this app")
		return nil, nil
	}

	// Validate ClusterIssuer is configured
	if r.ClusterIssuerName == "" {
		err := fmt.Errorf("TLS is enabled but TLS_CLUSTER_ISSUER_NAME is not configured")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			appsv1.ReasonFailed, err.Error())
		return nil, err
	}

	logger.Info("Reconciling TLS", "hostname", nebariApp.Spec.Hostname, "issuer", r.ClusterIssuerName)

	// Determine gateway name
	gatewayName := constants.PublicGatewayName
	if nebariApp.Spec.Gateway == "internal" {
		gatewayName = constants.InternalGatewayName
	}

	// Create/update Certificate
	if err := r.reconcileCertificate(ctx, nebariApp); err != nil {
		return nil, err
	}

	// Patch Gateway to add per-app listener
	if err := r.reconcileGatewayListener(ctx, nebariApp, gatewayName); err != nil {
		return nil, err
	}

	// Check Certificate readiness
	certReady := r.isCertificateReady(ctx, nebariApp)

	if certReady {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionTrue,
			appsv1.ReasonAvailable, fmt.Sprintf("TLS certificate is ready for %s", nebariApp.Spec.Hostname))
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonTLSConfigured,
			fmt.Sprintf("TLS configured for %s", nebariApp.Spec.Hostname))
	} else {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			appsv1.ReasonCertificateNotReady,
			fmt.Sprintf("Waiting for cert-manager Certificate to become ready for %s", nebariApp.Spec.Hostname))
	}

	return &TLSResult{
		ListenerName: naming.ListenerName(nebariApp),
		SecretName:   naming.CertificateSecretName(nebariApp),
		CertReady:    certReady,
	}, nil
}

// CleanupTLS removes the Certificate and Gateway listener for a NebariApp.
func (r *TLSReconciler) CleanupTLS(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	// Determine gateway name
	gatewayName := constants.PublicGatewayName
	if nebariApp.Spec.Gateway == "internal" {
		gatewayName = constants.InternalGatewayName
	}

	// Remove listener from Gateway
	if err := r.removeGatewayListener(ctx, nebariApp, gatewayName); err != nil {
		logger.Error(err, "Failed to remove Gateway listener")
		return err
	}

	// Delete Certificate
	certName := naming.CertificateName(nebariApp)
	cert := &certmanagerv1.Certificate{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      certName,
		Namespace: constants.GatewayNamespace,
	}, cert)

	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Certificate already deleted", "name", certName)
			return nil
		}
		return fmt.Errorf("failed to get Certificate: %w", err)
	}

	if err := r.Client.Delete(ctx, cert); err != nil {
		return fmt.Errorf("failed to delete Certificate: %w", err)
	}

	logger.Info("Deleted Certificate", "name", certName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateDeleted,
		fmt.Sprintf("Deleted Certificate %s", certName))

	return nil
}

// reconcileCertificate creates or updates the cert-manager Certificate for a NebariApp.
func (r *TLSReconciler) reconcileCertificate(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	certName := naming.CertificateName(nebariApp)
	secretName := naming.CertificateSecretName(nebariApp)

	desired := &certmanagerv1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certName,
			Namespace: constants.GatewayNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by":   "nebari-operator",
				"nebari.dev/nebariapp-name":      nebariApp.Name,
				"nebari.dev/nebariapp-namespace": nebariApp.Namespace,
			},
		},
		Spec: certmanagerv1.CertificateSpec{
			SecretName: secretName,
			DNSNames:   []string{nebariApp.Spec.Hostname},
			IssuerRef: cmmeta.ObjectReference{
				Name: r.ClusterIssuerName,
				Kind: "ClusterIssuer",
			},
		},
	}

	// Check if it exists
	existing := &certmanagerv1.Certificate{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      certName,
		Namespace: constants.GatewayNamespace,
	}, existing)

	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Client.Create(ctx, desired); err != nil {
				return fmt.Errorf("failed to create Certificate: %w", err)
			}
			logger.Info("Created Certificate", "name", certName, "hostname", nebariApp.Spec.Hostname)
			r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateCreated,
				fmt.Sprintf("Created Certificate %s for %s", certName, nebariApp.Spec.Hostname))
			return nil
		}
		return fmt.Errorf("failed to get Certificate: %w", err)
	}

	// Update existing
	existing.Spec = desired.Spec
	existing.Labels = desired.Labels
	if err := r.Client.Update(ctx, existing); err != nil {
		if errors.IsConflict(err) {
			logger.V(1).Info("Certificate update conflict, will retry", "name", certName)
			return nil
		}
		return fmt.Errorf("failed to update Certificate: %w", err)
	}

	logger.Info("Updated Certificate", "name", certName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateUpdated,
		fmt.Sprintf("Updated Certificate %s", certName))

	return nil
}

// reconcileGatewayListener adds a per-app HTTPS listener to the Gateway.
func (r *TLSReconciler) reconcileGatewayListener(ctx context.Context, nebariApp *appsv1.NebariApp, gatewayName string) error {
	logger := log.FromContext(ctx)

	gw := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      gatewayName,
		Namespace: constants.GatewayNamespace,
	}, gw); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("gateway %s not found in namespace %s", gatewayName, constants.GatewayNamespace)
		}
		return fmt.Errorf("failed to get Gateway: %w", err)
	}

	listenerName := gatewayv1.SectionName(naming.ListenerName(nebariApp))
	hostname := gatewayv1.Hostname(nebariApp.Spec.Hostname)
	secretName := gatewayv1.ObjectName(naming.CertificateSecretName(nebariApp))
	tlsMode := gatewayv1.TLSModeTerminate
	fromAll := gatewayv1.NamespacesFromAll

	newListener := gatewayv1.Listener{
		Name:     listenerName,
		Protocol: gatewayv1.HTTPSProtocolType,
		Port:     443,
		Hostname: &hostname,
		AllowedRoutes: &gatewayv1.AllowedRoutes{
			Namespaces: &gatewayv1.RouteNamespaces{
				From: &fromAll,
			},
		},
		TLS: &gatewayv1.GatewayTLSConfig{
			Mode: &tlsMode,
			CertificateRefs: []gatewayv1.SecretObjectReference{
				{Name: secretName},
			},
		},
	}

	// Check if listener already exists, update or add
	found := false
	for i, l := range gw.Spec.Listeners {
		if l.Name == listenerName {
			gw.Spec.Listeners[i] = newListener
			found = true
			break
		}
	}
	if !found {
		gw.Spec.Listeners = append(gw.Spec.Listeners, newListener)
	}

	if err := r.Client.Update(ctx, gw); err != nil {
		return fmt.Errorf("failed to update Gateway with listener: %w", err)
	}

	if !found {
		logger.Info("Added listener to Gateway", "listener", listenerName, "gateway", gatewayName)
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonGatewayListenerAdded,
			fmt.Sprintf("Added HTTPS listener %s to Gateway %s", listenerName, gatewayName))
	} else {
		logger.Info("Updated listener on Gateway", "listener", listenerName, "gateway", gatewayName)
	}

	return nil
}

// removeGatewayListener removes the per-app listener from the Gateway.
func (r *TLSReconciler) removeGatewayListener(ctx context.Context, nebariApp *appsv1.NebariApp, gatewayName string) error {
	logger := log.FromContext(ctx)

	gw := &gatewayv1.Gateway{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      gatewayName,
		Namespace: constants.GatewayNamespace,
	}, gw); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Gateway not found, skipping listener removal", "gateway", gatewayName)
			return nil
		}
		return fmt.Errorf("failed to get Gateway: %w", err)
	}

	listenerName := gatewayv1.SectionName(naming.ListenerName(nebariApp))
	filtered := make([]gatewayv1.Listener, 0, len(gw.Spec.Listeners))
	removed := false
	for _, l := range gw.Spec.Listeners {
		if l.Name == listenerName {
			removed = true
			continue
		}
		filtered = append(filtered, l)
	}

	if !removed {
		logger.Info("Listener not found on Gateway, nothing to remove", "listener", listenerName)
		return nil
	}

	gw.Spec.Listeners = filtered
	if err := r.Client.Update(ctx, gw); err != nil {
		return fmt.Errorf("failed to update Gateway: %w", err)
	}

	logger.Info("Removed listener from Gateway", "listener", listenerName, "gateway", gatewayName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonGatewayListenerRemoved,
		fmt.Sprintf("Removed listener %s from Gateway %s", listenerName, gatewayName))

	return nil
}

// isCertificateReady checks if the cert-manager Certificate has Ready=True.
func (r *TLSReconciler) isCertificateReady(ctx context.Context, nebariApp *appsv1.NebariApp) bool {
	cert := &certmanagerv1.Certificate{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.CertificateName(nebariApp),
		Namespace: constants.GatewayNamespace,
	}, cert)
	if err != nil {
		return false
	}

	for _, cond := range cert.Status.Conditions {
		if cond.Type == certmanagerv1.CertificateConditionReady && cond.Status == cmmeta.ConditionTrue {
			return true
		}
	}
	return false
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/controller/reconcilers/tls/ -v`
Expected: PASS

**Step 5: Run all existing tests to check for regressions**

Run: `go test ./internal/controller/reconcilers/... ./internal/controller/utils/... ./internal/config/...`
Expected: All PASS

**Step 6: Commit**

```bash
git add internal/controller/reconcilers/tls/
git commit -m "feat: add TLS reconciler with Certificate and Gateway listener management"
```

---

### Task 6: Update RBAC and integrate TLS into the pipeline

**Files:**
- Modify: `internal/controller/nebariapp_controller.go`
- Modify: `cmd/main.go`

**Step 1: Update RBAC marker for Gateway write access**

In `internal/controller/nebariapp_controller.go`, change line 62:
```go
// FROM:
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch
// TO:
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=gateways,verbs=get;list;watch;update;patch
```

**Step 2: Add TLSReconciler to NebariAppReconciler struct**

In `internal/controller/nebariapp_controller.go`, update the struct (lines 44-52):
```go
type NebariAppReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          record.EventRecorder
	CoreReconciler    *core.CoreReconciler
	RoutingReconciler *routing.RoutingReconciler
	AuthReconciler    *auth.AuthReconciler
	TLSReconciler     *tls.TLSReconciler
}
```

Add import:
```go
"github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/tls"
```

**Step 3: Integrate TLS reconciliation into Reconcile method**

After the routing reconciliation block (after line 177) and before auth reconciliation (line 179), add TLS reconciliation. The TLS reconciler also needs to pass its result to the routing reconciler so the HTTPRoute uses the per-app listener name.

Update the routing block to accept a listener name override. Between core validation and routing (after line 157), insert:

```go
// Reconcile TLS certificates and Gateway listener
var tlsListenerName string
if r.TLSReconciler != nil && nebariApp.Spec.Routing != nil {
	tlsResult, err := r.TLSReconciler.ReconcileTLS(ctx, nebariApp)
	if err != nil {
		logger.Error(err, "TLS reconciliation failed")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
			appsv1.ReasonFailed, fmt.Sprintf("TLS reconciliation failed: %v", err))
		if err := r.Status().Update(ctx, nebariApp); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Minute}, nil
	}
	if tlsResult != nil {
		tlsListenerName = tlsResult.ListenerName
		if !tlsResult.CertReady {
			// Certificate not ready yet - continue but requeue
			logger.Info("TLS Certificate not ready yet, will requeue")
		}
	}
	logger.Info("TLS reconciled successfully", "nebariapp", nebariApp.Name)
}
```

**Step 4: Add cleanup for TLS in the cleanup method**

In the `cleanup` method (after routing cleanup, before auth cleanup), add:
```go
// Cleanup TLS resources (Certificate + Gateway listener)
if r.TLSReconciler != nil {
	if err := r.TLSReconciler.CleanupTLS(ctx, nebariApp); err != nil {
		logger.Error(err, "Failed to cleanup TLS resources")
		return err
	}
}
```

**Step 5: Initialize TLS reconciler in main.go**

In `cmd/main.go`, add import:
```go
"github.com/nebari-dev/nebari-operator/internal/config"
tlsreconciler "github.com/nebari-dev/nebari-operator/internal/controller/reconcilers/tls"
```

After auth reconciler initialization (after line 227), add:
```go
// Load TLS configuration
tlsConfig := config.LoadTLSConfig()

// Initialize TLS reconciler if ClusterIssuer is configured
var tlsReconciler *tlsreconciler.TLSReconciler
if tlsConfig.ClusterIssuerName != "" {
	tlsReconciler = &tlsreconciler.TLSReconciler{
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		Recorder:          mgr.GetEventRecorderFor("nebariapp-tls"),
		ClusterIssuerName: tlsConfig.ClusterIssuerName,
	}
	setupLog.Info("TLS reconciler initialized", "clusterIssuer", tlsConfig.ClusterIssuerName)
} else {
	setupLog.Info("TLS reconciler disabled - TLS_CLUSTER_ISSUER_NAME not set")
}
```

Update the NebariAppReconciler initialization (lines 229-236) to include TLS:
```go
if err := (&controller.NebariAppReconciler{
	Client:         mgr.GetClient(),
	Scheme:         mgr.GetScheme(),
	AuthReconciler: authReconciler,
	TLSReconciler:  tlsReconciler,
}).SetupWithManager(mgr); err != nil {
```

**Step 6: Regenerate RBAC**

Run: `make manifests`
Expected: RBAC updated with Gateway write permissions.

**Step 7: Run all tests**

Run: `go test ./internal/controller/reconcilers/... ./internal/controller/utils/... ./internal/config/... && go build ./...`
Expected: All PASS, build succeeds.

**Step 8: Commit**

```bash
git add internal/controller/nebariapp_controller.go cmd/main.go config/rbac/
git commit -m "feat: integrate TLS reconciler into pipeline with RBAC for Gateway patching"
```

---

### Task 7: Update routing reconciler to accept TLS listener name

**Files:**
- Modify: `internal/controller/reconcilers/routing/httproute.go`
- Modify: `internal/controller/reconcilers/routing/httproute_test.go`

**Step 1: Write failing tests for listener name override**

Add to `internal/controller/reconcilers/routing/httproute_test.go`:
```go
func TestBuildHTTPRouteWithTLSListener(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = gatewayv1.Install(scheme)
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name                string
		nebariApp           *appsv1.NebariApp
		tlsListenerName     string
		expectedSectionName string
	}{
		{
			name: "TLS listener provided",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: func() *bool { b := true; return &b }()},
					},
				},
			},
			tlsListenerName:     "tls-test-app-default",
			expectedSectionName: "tls-test-app-default",
		},
		{
			name: "No TLS listener - falls back to https",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: func() *bool { b := true; return &b }()},
					},
				},
			},
			tlsListenerName:     "",
			expectedSectionName: "https",
		},
		{
			name: "TLS disabled - uses http",
			nebariApp: &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: "test-app", Namespace: "default"},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test.example.com",
					Service:  appsv1.ServiceReference{Name: "test-svc", Port: 8080},
					Routing: &appsv1.RoutingConfig{
						TLS: &appsv1.RoutingTLSConfig{Enabled: func() *bool { b := false; return &b }()},
					},
				},
			},
			tlsListenerName:     "",
			expectedSectionName: "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reconciler := &RoutingReconciler{
				Scheme: scheme,
			}
			route := reconciler.buildHTTPRoute(tt.nebariApp, constants.PublicGatewayName, tt.tlsListenerName)

			if len(route.Spec.ParentRefs) == 0 {
				t.Fatal("expected at least one ParentRef")
			}

			sectionName := string(*route.Spec.ParentRefs[0].SectionName)
			if sectionName != tt.expectedSectionName {
				t.Errorf("expected sectionName %q, got %q", tt.expectedSectionName, sectionName)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/controller/reconcilers/routing/ -v -run TestBuildHTTPRouteWithTLSListener`
Expected: FAIL - wrong number of arguments to `buildHTTPRoute`.

**Step 3: Update buildHTTPRoute to accept listener name**

In `internal/controller/reconcilers/routing/httproute.go`, update `buildHTTPRoute` signature (line 151):

```go
func (r *RoutingReconciler) buildHTTPRoute(nebariApp *appsv1.NebariApp, gatewayName string, tlsListenerName string) *gatewayv1.HTTPRoute {
```

Update the sectionName logic (lines 155-162):
```go
// Determine which Gateway listener to use
// Priority: tlsListenerName (from TLS reconciler) > TLS enabled ("https") > TLS disabled ("http")
sectionName := gatewayv1.SectionName("https")
tlsEnabled := true
if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil && nebariApp.Spec.Routing.TLS.Enabled != nil && !*nebariApp.Spec.Routing.TLS.Enabled {
	sectionName = gatewayv1.SectionName("http")
	tlsEnabled = false
}
if tlsListenerName != "" && tlsEnabled {
	sectionName = gatewayv1.SectionName(tlsListenerName)
}
```

Update `ReconcileRouting` signature to accept the listener name:
```go
func (r *RoutingReconciler) ReconcileRouting(ctx context.Context, nebariApp *appsv1.NebariApp, tlsListenerName string) error {
```

Update the call to `buildHTTPRoute` inside `ReconcileRouting` (line 64):
```go
desiredRoute := r.buildHTTPRoute(nebariApp, gatewayName, tlsListenerName)
```

**Step 4: Update call sites in nebariapp_controller.go**

In `internal/controller/nebariapp_controller.go`, update the call to `ReconcileRouting` (line 162):
```go
if err := r.RoutingReconciler.ReconcileRouting(ctx, nebariApp, tlsListenerName); err != nil {
```

**Step 5: Fix any existing tests that call buildHTTPRoute or ReconcileRouting**

Update existing tests in `internal/controller/reconcilers/routing/httproute_test.go` to pass the extra `""` argument where `buildHTTPRoute` or `ReconcileRouting` is called.

**Step 6: Run all tests**

Run: `go test ./internal/controller/reconcilers/... ./internal/controller/utils/... ./internal/config/...`
Expected: All PASS

**Step 7: Commit**

```bash
git add internal/controller/reconcilers/routing/ internal/controller/nebariapp_controller.go
git commit -m "feat: update routing reconciler to accept TLS listener name override"
```

---

### Task 8: Update dev setup and E2E tests

**Files:**
- Modify: `dev/install-services.sh` (add ClusterIssuer env var to operator deployment)
- Create: `test/e2e/tls_test.go`
- Modify: `test/e2e/testdata/` (add TLS test data if needed)

**Step 1: Add TLS_CLUSTER_ISSUER_NAME to dev operator deployment**

In `dev/install-services.sh`, find where the operator deployment environment variables are configured and add:
```bash
TLS_CLUSTER_ISSUER_NAME=nebari-ca-issuer
```

**Step 2: Create E2E TLS test**

Create `test/e2e/tls_test.go` following the existing e2e test patterns. Test:
- NebariApp with TLS enabled creates a Certificate in envoy-gateway-system
- Certificate has correct dnsNames and issuerRef
- Gateway has the per-app listener
- HTTPRoute references the per-app listener section name
- Deleting NebariApp removes the Certificate and listener

**Step 3: Run E2E smoke tests (if dev cluster available)**

Run: `make test-e2e-smoke` (only if local dev cluster is set up)

**Step 4: Commit**

```bash
git add dev/ test/e2e/
git commit -m "feat: add TLS E2E tests and dev environment configuration"
```

---

### Task 9: Run linting and final verification

**Files:** None (verification only)

**Step 1: Run linter**

Run: `make lint`
Expected: No errors

**Step 2: Run all unit tests**

Run: `go test ./internal/controller/reconcilers/... ./internal/controller/utils/... ./internal/config/...`
Expected: All PASS

**Step 3: Build**

Run: `go build ./...`
Expected: Clean build

**Step 4: Regenerate manifests**

Run: `make manifests generate`
Expected: No changes (already up to date)

---

### Task 10: Update documentation

**Files:**
- Modify: `README.md` (update TLS section to reflect actual behavior)

**Step 1: Update README TLS documentation**

Update the README to accurately describe the TLS management behavior:
- The operator creates per-app cert-manager Certificates
- The operator adds per-app Gateway HTTPS listeners
- Requires `TLS_CLUSTER_ISSUER_NAME` environment variable
- Certificates are created in the Gateway namespace

**Step 2: Commit**

```bash
git add README.md
git commit -m "docs: update README with accurate TLS certificate management documentation"
```
