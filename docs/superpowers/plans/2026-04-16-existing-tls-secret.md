# User-Provided TLS Secret Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a NebariApp reference a pre-existing Kubernetes TLS secret (`routing.tls.secretName`) instead of forcing cert-manager to issue a fresh certificate for every app.

**Architecture:** Add one optional string field to `RoutingTLSConfig`. When set, the TLS reconciler skips `Certificate` creation, points the per-app Gateway HTTPS listener at the user's secret in `envoy-gateway-system`, and reports `TLSReady` based on a best-effort existence/type check. When unset, existing cert-manager behavior is unchanged. On reconcile, an owned `Certificate` left behind from a previous cert-manager configuration is cleaned up.

**Tech Stack:** Go 1.22+, controller-runtime (Kubebuilder v4), Gateway API v1, cert-manager v1, Ginkgo/Gomega (e2e).

**Spec:** [`docs/superpowers/specs/2026-04-16-existing-tls-secret-design.md`](../specs/2026-04-16-existing-tls-secret-design.md) — issue [#90](https://github.com/nebari-dev/nebari-operator/issues/90).

**File map:**

- Modify: `api/v1/nebariapp_types.go` — new `SecretName` field on `RoutingTLSConfig`; new condition reason + event reason constants.
- Regenerated: `api/v1/zz_generated.deepcopy.go`, `config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml`, `docs/api-reference.md` — via `make manifests generate docs`.
- Modify: `internal/controller/reconcilers/tls/reconciler.go` — branch in `ReconcileTLS`; new helpers `cleanupOwnedCertificate` and `checkUserProvidedSecret`; `reconcileGatewayListener` takes a secret name parameter.
- Modify: `internal/controller/reconcilers/tls/reconciler_test.go` — table-driven unit cases covering all user-provided-secret scenarios plus regression cases.
- Create: `test/e2e/tls_user_secret_test.go` — end-to-end test with a pre-created self-signed secret.
- Modify: `docs/configuration-reference.md` and `docs/reconcilers/routing.md` — document the new field.

**RBAC:** No RBAC changes required. The existing controller has `secrets: get;list;watch;create;update;patch` cluster-wide (`internal/controller/nebariapp_controller.go:65`), which covers the new `Get` on secrets in `envoy-gateway-system`.

**Conventions enforced throughout:**

- Unit tests are table-driven (project convention; see existing `reconciler_test.go`).
- Functions take interfaces and return concrete types.
- No em dashes in any code, doc, commit, or comment content.
- Commits contain no AI attribution (no "Co-Authored-By: Claude", no "Generated with Claude Code").
- Never commit the `.claude/` directory.
- After each task, run the relevant tests **before committing** and confirm they pass.

---

## Task 1: Add `SecretName` field to `RoutingTLSConfig` and regenerate manifests

**Files:**

- Modify: `api/v1/nebariapp_types.go:145-154`
- Regenerated: `api/v1/zz_generated.deepcopy.go`, `config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml`, `docs/api-reference.md`

- [ ] **Step 1: Edit `RoutingTLSConfig` to add the `SecretName` field**

Replace the struct body at `api/v1/nebariapp_types.go:145-154` with:

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

	// SecretName optionally references a pre-existing Kubernetes TLS secret
	// (type kubernetes.io/tls) in the Gateway's namespace (envoy-gateway-system).
	// When set, the operator will NOT create a cert-manager Certificate; instead
	// it points the per-app HTTPS listener at this secret. Use this for
	// pre-provisioned or externally managed certificates (for example, wildcard
	// certs or air-gapped environments without ACME access). The secret must be
	// created and maintained by the user in the envoy-gateway-system namespace.
	// Mutually exclusive with cert-manager-managed certificates: when set, the
	// operator will not create or update a Certificate for this NebariApp and
	// will clean up any existing owned Certificate.
	// Ignored when enabled is false.
	// +optional
	// +kubebuilder:validation:MinLength=1
	SecretName string `json:"secretName,omitempty"`
}
```

- [ ] **Step 2: Regenerate CRD manifests, deepcopy, and API reference docs**

Run:

```bash
make manifests generate docs
```

Expected: three files modified (no errors): `config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml`, `api/v1/zz_generated.deepcopy.go`, `docs/api-reference.md`. The CRD yaml should contain a new `secretName` property under `spec.routing.tls`.

- [ ] **Step 3: Verify the generated CRD shows the new field**

Run:

```bash
grep -A 20 'tls:' config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml | grep -A 10 properties | head -25
```

Expected: output includes a `secretName` entry with `minLength: 1` and `type: string`.

- [ ] **Step 4: Build the project to confirm the API change compiles**

Run:

```bash
make build
```

Expected: clean build, no errors.

- [ ] **Step 5: Commit**

```bash
git add api/v1/nebariapp_types.go api/v1/zz_generated.deepcopy.go config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml docs/api-reference.md
git commit -m "feat(api): add routing.tls.secretName to RoutingTLSConfig

Adds an optional SecretName field to allow referencing a pre-existing
Kubernetes TLS secret instead of creating a cert-manager Certificate.
The field is ignored when enabled=false. Regenerates CRD manifests,
deepcopy, and the API reference doc."
```

---

## Task 2: Add condition reason and event reason constants

**Files:**

- Modify: `api/v1/nebariapp_types.go` — condition reasons block ending at line 642; event reasons block ending at line 711.

- [ ] **Step 1: Add three condition reason constants after `ReasonGatewayListenerConflict`**

In `api/v1/nebariapp_types.go`, after the line `ReasonGatewayListenerConflict = "GatewayListenerConflict"` (line 641) and before the closing `)` of the reasons block (line 642), add:

```go

	// ReasonUserProvidedSecretReady indicates a user-provided TLS secret exists and is valid.
	ReasonUserProvidedSecretReady = "UserProvidedSecretReady"

	// ReasonUserProvidedSecretNotFound indicates the user-provided TLS secret does not exist.
	ReasonUserProvidedSecretNotFound = "UserProvidedSecretNotFound"

	// ReasonUserProvidedSecretInvalidType indicates the user-provided secret is not type kubernetes.io/tls.
	ReasonUserProvidedSecretInvalidType = "UserProvidedSecretInvalidType"
```

- [ ] **Step 2: Add three event reason constants after `EventReasonGatewayListenerConflict`**

In `api/v1/nebariapp_types.go`, after the line `EventReasonGatewayListenerConflict = "GatewayListenerConflict"` (line 710) and before the closing `)` of the event reasons block (line 711), add:

```go

	// EventReasonUserProvidedSecretInUse is used when a user-provided TLS secret is successfully attached.
	EventReasonUserProvidedSecretInUse = "UserProvidedSecretInUse"

	// EventReasonUserProvidedSecretNotFound is used when a referenced user-provided TLS secret is missing.
	EventReasonUserProvidedSecretNotFound = "UserProvidedSecretNotFound"

	// EventReasonUserProvidedSecretInvalid is used when a referenced TLS secret is not type kubernetes.io/tls.
	EventReasonUserProvidedSecretInvalid = "UserProvidedSecretInvalid"
```

- [ ] **Step 3: Build to confirm constants compile**

Run:

```bash
make build
```

Expected: clean build.

- [ ] **Step 4: Commit**

```bash
git add api/v1/nebariapp_types.go
git commit -m "feat(api): add condition and event reasons for user-provided TLS secrets

Introduces three condition reasons (UserProvidedSecretReady, NotFound,
InvalidType) and three event reasons (UserProvidedSecretInUse, NotFound,
Invalid) that the TLS reconciler will emit when a NebariApp uses
routing.tls.secretName."
```

---

## Task 3: Refactor `reconcileGatewayListener` to take the secret name as a parameter

**Files:**

- Modify: `internal/controller/reconcilers/tls/reconciler.go:234-317`
- Regression: `internal/controller/reconcilers/tls/reconciler_test.go`

**Goal:** Prepare the listener writer so both the cert-manager path and the user-provided-secret path can share it. No behavior change. Existing tests must pass unchanged.

- [ ] **Step 1: Run existing TLS tests first to capture baseline**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -v
```

Expected: all tests pass.

- [ ] **Step 2: Change the signature of `reconcileGatewayListener` to accept `secretName`**

In `internal/controller/reconcilers/tls/reconciler.go`:

Replace the function header at line 234 from:

```go
func (r *TLSReconciler) reconcileGatewayListener(ctx context.Context, nebariApp *appsv1.NebariApp) error {
```

to:

```go
func (r *TLSReconciler) reconcileGatewayListener(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) error {
```

Delete the body line that reads `secretName := naming.CertificateSecretName(nebariApp)` (currently line 239). The parameter now supplies it.

- [ ] **Step 3: Update the single existing caller to pass the cert-manager secret name**

In `internal/controller/reconcilers/tls/reconciler.go` at line 113, replace:

```go
	if err := r.reconcileGatewayListener(ctx, nebariApp); err != nil {
```

with:

```go
	if err := r.reconcileGatewayListener(ctx, nebariApp, naming.CertificateSecretName(nebariApp)); err != nil {
```

- [ ] **Step 4: Run the TLS tests again to confirm no regressions**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -v
```

Expected: all existing tests pass without modification.

- [ ] **Step 5: Run vet and lint to catch any issues**

Run:

```bash
make vet lint
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/reconcilers/tls/reconciler.go
git commit -m "refactor(tls): parameterize secret name in reconcileGatewayListener

Pulls the TLS secret name out of reconcileGatewayListener into a
caller-supplied argument so the user-provided-secret path (issue #90)
can reuse the same listener-writer logic. Pure refactor; no behavior
change."
```

---

## Task 4: Add `cleanupOwnedCertificate` helper with unit test

**Files:**

- Modify: `internal/controller/reconcilers/tls/reconciler.go` — add helper.
- Modify: `internal/controller/reconcilers/tls/reconciler_test.go` — add new `TestCleanupOwnedCertificate` test.

**Goal:** Idempotently delete any `Certificate` owned by this NebariApp (identified by the labels `nebari.dev/nebariapp-name` and `nebari.dev/nebariapp-namespace` that `reconcileCertificate` writes). Used on the user-provided-secret path to reclaim resources when a user migrates away from cert-manager.

- [ ] **Step 1: Write the failing test**

Append this new test function to `internal/controller/reconcilers/tls/reconciler_test.go` (after `TestIsCertificateReady`):

```go
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
						"app.kubernetes.io/managed-by":    "nebari-operator",
						"nebari.dev/nebariapp-name":       "migrate-app",
						"nebari.dev/nebariapp-namespace":  "default",
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -run TestCleanupOwnedCertificate -v
```

Expected: FAIL with `reconciler.cleanupOwnedCertificate undefined` (or similar compile error).

- [ ] **Step 3: Implement `cleanupOwnedCertificate`**

Add this method to `internal/controller/reconcilers/tls/reconciler.go` (place it right after `deleteCertificate`, at the end of the file):

```go
// cleanupOwnedCertificate deletes the cert-manager Certificate for this NebariApp
// if it exists and is labeled as owned by this NebariApp. This is used when a
// NebariApp switches from cert-manager to a user-provided TLS secret via
// routing.tls.secretName. The function is idempotent: missing Certificates and
// Certificates with mismatched ownership labels are left alone.
//
// Ownership is verified via the labels written by reconcileCertificate:
//   - nebari.dev/nebariapp-name      == nebariApp.Name
//   - nebari.dev/nebariapp-namespace == nebariApp.Namespace
func (r *TLSReconciler) cleanupOwnedCertificate(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)
	certName := naming.CertificateName(nebariApp)

	cert := &certmanagerv1.Certificate{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      certName,
		Namespace: constants.GatewayNamespace,
	}, cert); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Certificate for cleanup check: %w", err)
	}

	if cert.Labels["nebari.dev/nebariapp-name"] != nebariApp.Name ||
		cert.Labels["nebari.dev/nebariapp-namespace"] != nebariApp.Namespace {
		logger.V(1).Info("Certificate exists with mismatched ownership labels, leaving it alone",
			"name", certName, "namespace", constants.GatewayNamespace)
		return nil
	}

	if err := client.IgnoreNotFound(r.Client.Delete(ctx, cert)); err != nil {
		return fmt.Errorf("failed to delete owned Certificate during migration: %w", err)
	}

	logger.Info("Deleted owned Certificate during migration to user-provided secret",
		"name", certName, "namespace", constants.GatewayNamespace)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonCertificateDeleted,
		fmt.Sprintf("Deleted cert-manager Certificate %s/%s after switch to user-provided secret", constants.GatewayNamespace, certName))
	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -run TestCleanupOwnedCertificate -v
```

Expected: PASS for all four subtests.

- [ ] **Step 5: Run vet and lint**

Run:

```bash
make vet lint
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/reconcilers/tls/reconciler.go internal/controller/reconcilers/tls/reconciler_test.go
git commit -m "feat(tls): add cleanupOwnedCertificate helper

Adds an idempotent helper that deletes the cert-manager Certificate
owned by a NebariApp, identified via its operator-written ownership
labels. Used on the user-provided-secret path to clean up the prior
cert-manager resource when a user migrates."
```

---

## Task 5: Add `checkUserProvidedSecret` helper with unit test

**Files:**

- Modify: `internal/controller/reconcilers/tls/reconciler.go` — add helper.
- Modify: `internal/controller/reconcilers/tls/reconciler_test.go` — add `TestCheckUserProvidedSecret`.

**Goal:** Look up a secret in `envoy-gateway-system` and classify it as ready, missing, or wrong-type. Returns `(conditionStatus, reason, message)` so the caller can both set the condition and emit a matching event.

- [ ] **Step 1: Write the failing test**

Append this new test function to `internal/controller/reconcilers/tls/reconciler_test.go`:

```go
func TestCheckUserProvidedSecret(t *testing.T) {
	scheme := newScheme()
	_ = corev1.AddToScheme(scheme)

	tests := []struct {
		name           string
		secretName     string
		existingSecret *corev1.Secret
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
			},
			expectStatus: metav1.ConditionTrue,
			expectReason: appsv1.ReasonUserProvidedSecretReady,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingSecret != nil {
				builder = builder.WithObjects(tt.existingSecret)
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
```

Also add the import `corev1 "k8s.io/api/core/v1"` to the test file's import block if it is not already present (check the existing imports around line 29; `metav1` is imported but `corev1` may not be — add it).

- [ ] **Step 2: Run the test to verify it fails**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -run TestCheckUserProvidedSecret -v
```

Expected: FAIL with `reconciler.checkUserProvidedSecret undefined`.

- [ ] **Step 3: Implement `checkUserProvidedSecret`**

Append this method to `internal/controller/reconcilers/tls/reconciler.go` (after `cleanupOwnedCertificate`):

```go
// checkUserProvidedSecret inspects a user-supplied TLS secret in the Gateway
// namespace and returns a condition (status, reason, message) tuple describing
// its readiness. The check is best-effort: a missing or malformed secret yields
// ConditionFalse but does not error, so the caller can still proceed to attach
// the listener. Envoy Gateway will surface the actual attachment error
// downstream once the secret appears.
func (r *TLSReconciler) checkUserProvidedSecret(ctx context.Context, secretName string) (metav1.ConditionStatus, string, string) {
	secret := &corev1.Secret{}
	err := r.Client.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: constants.GatewayNamespace,
	}, secret)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return metav1.ConditionFalse,
				appsv1.ReasonUserProvidedSecretNotFound,
				fmt.Sprintf("TLS secret %s/%s not found; create it and the listener will pick it up",
					constants.GatewayNamespace, secretName)
		}
		return metav1.ConditionFalse,
			appsv1.ReasonUserProvidedSecretNotFound,
			fmt.Sprintf("failed to check TLS secret %s/%s: %v", constants.GatewayNamespace, secretName, err)
	}

	if secret.Type != corev1.SecretTypeTLS {
		return metav1.ConditionFalse,
			appsv1.ReasonUserProvidedSecretInvalidType,
			fmt.Sprintf("TLS secret %s/%s is type %s, expected kubernetes.io/tls",
				constants.GatewayNamespace, secretName, secret.Type)
	}

	return metav1.ConditionTrue,
		appsv1.ReasonUserProvidedSecretReady,
		fmt.Sprintf("using pre-provisioned TLS secret %s/%s", constants.GatewayNamespace, secretName)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -run TestCheckUserProvidedSecret -v
```

Expected: PASS for all three subtests.

- [ ] **Step 5: Run vet and lint**

Run:

```bash
make vet lint
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/reconcilers/tls/reconciler.go internal/controller/reconcilers/tls/reconciler_test.go
git commit -m "feat(tls): add checkUserProvidedSecret helper

Adds a best-effort checker that classifies a user-supplied TLS secret
as Ready, NotFound, or InvalidType. Returns a (status, reason, message)
tuple so callers can set the TLSReady condition and emit a matching
event in one lookup."
```

---

## Task 6: Wire the user-provided-secret branch into `ReconcileTLS`

**Files:**

- Modify: `internal/controller/reconcilers/tls/reconciler.go` — branch logic in `ReconcileTLS`.
- Modify: `internal/controller/reconcilers/tls/reconciler_test.go` — five new table-driven cases on `TestReconcileTLS`.

**Goal:** Make `ReconcileTLS` honor `spec.Routing.TLS.SecretName`. When set: bypass the ClusterIssuer check, skip Certificate creation, clean up any owned Certificate, attach the listener pointing at the user's secret, and set `TLSReady` from `checkUserProvidedSecret`. When unset: existing cert-manager path.

- [ ] **Step 1: Write the failing test cases**

Open `internal/controller/reconcilers/tls/reconciler_test.go`. In the `tests` slice inside `TestReconcileTLS` (starting around line 65), append the following five new cases before the closing `}` of the slice:

```go
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
			clusterIssuerName: "letsencrypt-prod",
			gateway:           newGateway(constants.PublicGatewayName),
			expectError:       false,
			expectNilResult:   true,
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
			},
			existingCert: &certmanagerv1.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      naming.CertificateName(&appsv1.NebariApp{ObjectMeta: metav1.ObjectMeta{Name: "migrate", Namespace: "default"}}),
					Namespace: constants.GatewayNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by":    "nebari-operator",
						"nebari.dev/nebariapp-name":       "migrate",
						"nebari.dev/nebariapp-namespace":  "default",
					},
				},
			},
			expectError:        false,
			expectNilResult:    false,
			validateCertAbsent: true,
		},
```

In the same file, update the struct definition at the top of `TestReconcileTLS` (around line 65) to add the two new fields and the existing `existingSecret` support. Replace the struct literal field list (lines 65-77):

```go
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
```

Then update the runner (around line 471-548) to wire the new `existingSecret` and `validateCertAbsent` fields. Inside the `for _, tt := range tests` block, find the section that builds the fake client (line 473-484) and insert secret handling after the existingCert handling:

```go
			if tt.existingSecret != nil {
				builder = builder.WithObjects(tt.existingSecret)
			}
```

Add a new validation block after the existing `validateCert` block (around line 527) that asserts the Certificate is absent when `validateCertAbsent` is true:

```go
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
```

Also add `corev1` to the test imports if not already present (it was added in Task 5; confirm).

Finally, register `corev1` on the test scheme. In `newScheme()` (around line 41), add a line:

```go
	_ = corev1.AddToScheme(scheme)
```

- [ ] **Step 2: Run the tests to verify the new ones fail**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -run TestReconcileTLS -v
```

Expected: FAIL for the five new cases (the reconciler still takes the cert-manager path or errors on empty ClusterIssuer). Existing cases still pass.

- [ ] **Step 3: Implement the branch in `ReconcileTLS`**

In `internal/controller/reconcilers/tls/reconciler.go`, replace the body of `ReconcileTLS` (lines 82-151). The new body:

```go
func (r *TLSReconciler) ReconcileTLS(ctx context.Context, nebariApp *appsv1.NebariApp) (*TLSResult, error) {
	logger := log.FromContext(ctx)

	// Step 1: Check if TLS is disabled
	if !isTLSEnabled(nebariApp) {
		logger.Info("TLS not enabled, skipping TLS reconciliation")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"TLSDisabled", "TLS is not enabled for this app")
		return nil, nil
	}

	userSecret := ""
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil {
		userSecret = nebariApp.Spec.Routing.TLS.SecretName
	}

	// Step 2: Branch on whether the user supplied a TLS secret
	if userSecret != "" {
		return r.reconcileUserProvidedTLS(ctx, nebariApp, userSecret)
	}

	// Step 3: cert-manager path: validate ClusterIssuerName is configured
	if r.ClusterIssuerName == "" {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"ClusterIssuerNotConfigured", "No ClusterIssuer configured for TLS certificate management")
		return nil, fmt.Errorf("ClusterIssuerName is not configured; set TLS_CLUSTER_ISSUER_NAME environment variable")
	}

	logger.Info("Reconciling TLS",
		"hostname", nebariApp.Spec.Hostname,
		"clusterIssuer", r.ClusterIssuerName,
		"gateway", naming.GatewayName(nebariApp))

	// Step 4: Create/update cert-manager Certificate
	if err := r.reconcileCertificate(ctx, nebariApp); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"CertificateFailed", fmt.Sprintf("Failed to reconcile Certificate: %v", err))
		return nil, err
	}

	// Step 5: Patch Gateway to add per-app HTTPS listener
	if err := r.reconcileGatewayListener(ctx, nebariApp, naming.CertificateSecretName(nebariApp)); err != nil {
		if containsListenerConflict(err) {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				appsv1.ReasonGatewayListenerConflict,
				fmt.Sprintf("Gateway listener conflict: Multiple NebariApps cannot share hostname %s with per-app TLS. "+
					"Set routing.tls.enabled=false to use shared wildcard listener, or use unique hostnames.",
					nebariApp.Spec.Hostname))
		} else {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				"GatewayListenerFailed", fmt.Sprintf("Failed to reconcile Gateway listener: %v", err))
		}
		return nil, err
	}

	// Step 6: Check Certificate readiness
	certReady, err := r.isCertificateReady(ctx, nebariApp)
	if err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"CertificateCheckFailed", fmt.Sprintf("Failed to check Certificate readiness: %v", err))
		return nil, err
	}

	// Step 7: Set TLSReady condition
	if certReady {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionTrue,
			"TLSConfigured", "TLS certificate is ready and Gateway listener is configured")
	} else {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			appsv1.ReasonCertificateNotReady, "Waiting for cert-manager Certificate to become ready")
	}

	return &TLSResult{
		ListenerName: naming.ListenerName(nebariApp),
		SecretName:   naming.CertificateSecretName(nebariApp),
		CertReady:    certReady,
	}, nil
}

// reconcileUserProvidedTLS handles the path where routing.tls.secretName references
// a pre-existing Kubernetes TLS secret in the Gateway namespace. The operator does
// not create a cert-manager Certificate; it cleans up any owned Certificate left
// over from a previous cert-manager configuration, attaches the per-app listener
// to the user's secret, and sets TLSReady from a best-effort secret check.
func (r *TLSReconciler) reconcileUserProvidedTLS(ctx context.Context, nebariApp *appsv1.NebariApp, secretName string) (*TLSResult, error) {
	logger := log.FromContext(ctx)
	logger.Info("Using user-provided TLS secret",
		"secret", secretName,
		"namespace", constants.GatewayNamespace,
		"hostname", nebariApp.Spec.Hostname)

	// Migration: remove any previously owned cert-manager Certificate.
	if err := r.cleanupOwnedCertificate(ctx, nebariApp); err != nil {
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
			"CertificateCleanupFailed", fmt.Sprintf("Failed to clean up owned Certificate during migration: %v", err))
		return nil, err
	}

	// Attach the listener pointing at the user's secret. Do this even if the
	// secret is missing or malformed so Envoy will pick it up once fixed.
	if err := r.reconcileGatewayListener(ctx, nebariApp, secretName); err != nil {
		if containsListenerConflict(err) {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				appsv1.ReasonGatewayListenerConflict,
				fmt.Sprintf("Gateway listener conflict: Multiple NebariApps cannot share hostname %s with per-app TLS. "+
					"Set routing.tls.enabled=false to use shared wildcard listener, or use unique hostnames.",
					nebariApp.Spec.Hostname))
		} else {
			conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, metav1.ConditionFalse,
				"GatewayListenerFailed", fmt.Sprintf("Failed to reconcile Gateway listener: %v", err))
		}
		return nil, err
	}

	// Best-effort secret check for status reporting.
	status, reason, msg := r.checkUserProvidedSecret(ctx, secretName)
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeTLSReady, status, reason, msg)

	switch reason {
	case appsv1.ReasonUserProvidedSecretReady:
		r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonUserProvidedSecretInUse, msg)
	case appsv1.ReasonUserProvidedSecretNotFound:
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonUserProvidedSecretNotFound, msg)
	case appsv1.ReasonUserProvidedSecretInvalidType:
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonUserProvidedSecretInvalid, msg)
	}

	return &TLSResult{
		ListenerName: naming.ListenerName(nebariApp),
		SecretName:   secretName,
		CertReady:    status == metav1.ConditionTrue,
	}, nil
}
```

- [ ] **Step 4: Run all TLS tests to verify everything passes**

Run:

```bash
go test ./internal/controller/reconcilers/tls/... -v
```

Expected: every subtest in `TestReconcileTLS`, `TestCleanupTLS`, `TestIsCertificateReady`, `TestCleanupOwnedCertificate`, and `TestCheckUserProvidedSecret` passes.

- [ ] **Step 5: Run vet and lint**

Run:

```bash
make vet lint
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/reconcilers/tls/reconciler.go internal/controller/reconcilers/tls/reconciler_test.go
git commit -m "feat(tls): reconcile routing.tls.secretName for pre-provisioned TLS

When routing.tls.secretName is set on a NebariApp, the TLS reconciler
now:

- Bypasses the ClusterIssuer check (supports operator installs with no
  ClusterIssuer when every app brings its own secret).
- Cleans up any owned cert-manager Certificate from a prior
  configuration.
- Attaches the per-app Gateway HTTPS listener directly to the user's
  secret in envoy-gateway-system.
- Emits TLSReady with UserProvidedSecretReady / NotFound /
  InvalidType based on a best-effort secret check, and emits a
  matching Kubernetes event.

The listener is attached regardless of secret validity so Envoy Gateway
picks it up as soon as the user creates or fixes the secret.

Closes #90."
```

---

## Task 7: Controller integration test for user-provided secret

**Files:**

- Modify: `internal/controller/nebariapp_controller_test.go`

**Goal:** Verify at the controller level that a NebariApp with `routing.tls.secretName` produces a Gateway listener pointed at the user's secret and no `Certificate` resource.

**Context for the engineer:** The file uses Ginkgo with a top-level `Describe("NebariApp Controller")` wrapping a `Context("When reconciling a resource")`. The existing `BeforeEach` creates a shared `test-resource` NebariApp and a `test-service`. The shared `nebariapp` var and the `BeforeEach`/`AfterEach` lifecycle only touch `test-resource`, so your new `It` can create its own independent NebariApp with a distinct name without colliding. The file uses `reconcilersv1` as the alias for `api/v1` (not `appsv1`). API: `reconcilersv1.RoutingTLSConfig{Enabled: boolPtr(true), SecretName: ...}`.

- [ ] **Step 1: Add the imports**

At `internal/controller/nebariapp_controller_test.go:19-36`, add the following imports if they are not already present (the diff will be inside the existing single-line imports or the grouped block; leave groupings alone):

```go
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
```

The package already imports `corev1`, `errors` (aliased as `apierrors` below - confirm), `types`, `metav1`, and `reconcilersv1`. If `errors` is aliased differently in the file (e.g., imported as plain `errors`), use that alias consistently; do not duplicate.

Add a helper at the bottom of the file (once, after the closing `})` of the `Describe`):

```go
func boolPtr(b bool) *bool { return &b }
```

If `boolPtr` is already defined in another `_test.go` file in the same package, reuse it instead.

- [ ] **Step 2: Add the integration test case inside the existing `Context`**

After the closing `})` of the final `It(...)` block in the `Context("When reconciling a resource")`, add a new `It`:

```go
		It("uses a user-provided TLS secret when routing.tls.secretName is set", func() {
			const appName = "byo-secret"
			const userSecretName = "my-user-tls"

			// Pre-create the user's TLS secret in the Gateway namespace.
			gwNS := &corev1.Namespace{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: constants.GatewayNamespace}, gwNS); errors.IsNotFound(err) {
				Expect(k8sClient.Create(ctx, &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: constants.GatewayNamespace},
				})).To(Succeed())
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: userSecretName, Namespace: constants.GatewayNamespace},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					"tls.crt": []byte("dummy-cert"),
					"tls.key": []byte("dummy-key"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, secret)
			})

			app := &reconcilersv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{Name: appName, Namespace: "default"},
				Spec: reconcilersv1.NebariAppSpec{
					Hostname: "byo.example.com",
					Service:  reconcilersv1.ServiceReference{Name: "test-service", Port: 8080},
					Routing: &reconcilersv1.RoutingConfig{
						TLS: &reconcilersv1.RoutingTLSConfig{
							Enabled:    boolPtr(true),
							SecretName: userSecretName,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, app)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, app)
			})

			By("triggering reconcile")
			reconciler := newTestReconciler()
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: appName, Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			By("verifying no Certificate was created for this NebariApp")
			cert := &certmanagerv1.Certificate{}
			getErr := k8sClient.Get(ctx, types.NamespacedName{
				Name:      naming.CertificateName(app),
				Namespace: constants.GatewayNamespace,
			}, cert)
			Expect(errors.IsNotFound(getErr)).To(BeTrue(),
				"no Certificate should exist on the user-provided-secret path")

			By("verifying TLSReady=True/UserProvidedSecretReady")
			got := &reconcilersv1.NebariApp{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: appName, Namespace: "default"}, got)).To(Succeed())
			c := conditions.GetCondition(got, reconcilersv1.ConditionTypeTLSReady)
			Expect(c).NotTo(BeNil())
			Expect(c.Status).To(Equal(metav1.ConditionTrue))
			Expect(c.Reason).To(Equal(reconcilersv1.ReasonUserProvidedSecretReady))
		})
```

**Open choice for the engineer: the `Reconcile` call.** The existing `It` blocks in this file invoke the reconciler via a local helper or by calling a reconciler that is constructed inline. Open the file and mirror the construction the other `It`s use. Specifically: search for the first `Reconcile(` call inside the `Describe` block and copy the reconciler-construction boilerplate that precedes it (expect to see something like `controllerReconciler := &NebariAppReconciler{...}`), then reuse it. If it extracts to a helper, factor `newTestReconciler()` out of that helper so both tests can share it. If the existing tests inline the construction, inline it here too. Do not introduce a helper that did not already exist.

Also ensure the test creates the Gateway in `envoy-gateway-system` if earlier `It`s have not already done so. The TLS reconciler's listener-patch step requires the Gateway to exist (see `reconcileGatewayListener` at `internal/controller/reconcilers/tls/reconciler.go:272-283`). If prior tests create the Gateway, rely on their setup; otherwise add a minimal Gateway creation (with `DeferCleanup`) at the start of the `It`.

- [ ] **Step 3: Run the envtest-backed suite**

Run:

```bash
make test
```

Expected: all integration tests pass, including the new `It`. If envtest binaries are missing, run `make setup-envtest` first.

- [ ] **Step 4: Run vet and lint**

Run:

```bash
make vet lint
```

Expected: no errors.

- [ ] **Step 3: Run the controller integration test**

Run:

```bash
make test
```

Expected: all envtest-backed integration tests pass, including the new `It` case. If `make test` requires `setup-envtest` binaries that aren't installed, run `make setup-envtest` first.

- [ ] **Step 4: Run vet and lint**

Run:

```bash
make vet lint
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/nebariapp_controller_test.go
git commit -m "test(controller): verify user-provided TLS secret path end-to-end

Adds an envtest-backed integration test confirming that when a NebariApp
sets routing.tls.secretName: the operator attaches the per-app Gateway
listener to the user's secret, does not create a cert-manager
Certificate, and reports TLSReady=True/UserProvidedSecretReady."
```

---

## Task 8: E2E test against a real Kind cluster

**Files:**

- Create: `test/e2e/tls_user_secret_test.go`

**Goal:** On the full Kind-based e2e stack (MetalLB + Envoy Gateway + cert-manager), verify a self-signed user secret is served for a NebariApp hostname.

**Context for the engineer:** The only helper in `test/utils` that every e2e test uses is `utils.Run(cmd *exec.Cmd) (string, error)`. There is no `CreateNamespace`, `LabelNamespace`, `ApplyManifest`, or `RunErr` helper in this repo. Use `exec.Command(...)` + `utils.Run` directly, matching the pattern in `test/e2e/nebariapp_test.go`. Also note that `namespace` is a package-level constant in `e2e_utils.go:40` pinned to `"nebari-operator-system"`; use a different local name for your test's application namespace to avoid shadowing.

- [ ] **Step 1: Create the e2e test file**

Write this file at `test/e2e/tls_user_secret_test.go`:

```go
//go:build e2e
// +build e2e

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

package e2e

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

var _ = Describe("NebariApp with routing.tls.secretName", Ordered, func() {
	const appNS = "tls-byo-ns"
	const appName = "byo-tls-app"
	const userSecretName = "byo-tls"
	const hostname = "byo-tls.nebari.local"

	BeforeAll(func() {
		By("creating the application namespace")
		_, err := utils.Run(exec.Command("kubectl", "create", "namespace", appNS))
		if err != nil && !strings.Contains(err.Error(), "AlreadyExists") {
			Fail(fmt.Sprintf("failed to create namespace %s: %v", appNS, err))
		}
		_, err = utils.Run(exec.Command("kubectl", "label", "namespace", appNS,
			"nebari.dev/managed=true", "--overwrite"))
		Expect(err).NotTo(HaveOccurred())

		By("generating a self-signed TLS cert for the test hostname")
		// The cert's chain is not validated; we only verify the operator attaches it.
		_, err = utils.Run(exec.Command("bash", "-c", fmt.Sprintf(
			"openssl req -x509 -nodes -newkey rsa:2048 -days 1 "+
				"-subj '/CN=%s' -addext 'subjectAltName=DNS:%s' "+
				"-keyout /tmp/%s.key -out /tmp/%s.crt",
			hostname, hostname, userSecretName, userSecretName,
		)))
		Expect(err).NotTo(HaveOccurred())

		By("creating the user-provided TLS secret in envoy-gateway-system")
		_, err = utils.Run(exec.Command("kubectl", "create", "secret", "tls", userSecretName,
			"-n", "envoy-gateway-system",
			fmt.Sprintf("--cert=/tmp/%s.crt", userSecretName),
			fmt.Sprintf("--key=/tmp/%s.key", userSecretName)))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "nebariapp", appName, "-n", appNS, "--ignore-not-found=true"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "secret", userSecretName, "-n", "envoy-gateway-system", "--ignore-not-found=true"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", appNS, "--ignore-not-found=true"))
	})

	It("attaches the user secret to the per-app listener", func() {
		By("applying a NebariApp with routing.tls.secretName")
		manifest := fmt.Sprintf(`apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: echo-backend
    port: 8080
  routing:
    tls:
      enabled: true
      secretName: %s
`, appName, appNS, hostname, userSecretName)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for TLSReady=True with reason UserProvidedSecretReady")
		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "nebariapp", appName,
				"-n", appNS,
				"-o", `jsonpath={.status.conditions[?(@.type=="TLSReady")].reason}`))
			return strings.TrimSpace(out)
		}, 2*time.Minute, 5*time.Second).Should(Equal("UserProvidedSecretReady"))

		By("verifying no Certificate was created for this NebariApp")
		out, _ := utils.Run(exec.Command("kubectl", "get", "certificate",
			"-n", "envoy-gateway-system",
			"-l", fmt.Sprintf("nebari.dev/nebariapp-name=%s", appName),
			"-o", "name"))
		Expect(strings.TrimSpace(out)).To(BeEmpty(),
			"expected no cert-manager Certificate for user-provided-secret NebariApp")

		By("verifying the Gateway listener references the user's secret")
		Eventually(func() string {
			jsonpath := fmt.Sprintf(`jsonpath={.spec.listeners[?(@.name=="tls-%s-%s")].tls.certificateRefs[0].name}`,
				appName, appNS)
			out, _ := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
				"-n", "envoy-gateway-system",
				"-o", jsonpath))
			return strings.TrimSpace(out)
		}, 1*time.Minute, 5*time.Second).Should(Equal(userSecretName))
	})
})
```

- [ ] **Step 2: Run the e2e suite locally against a Kind cluster**

Prerequisite: `cd dev && make setup` has produced a working cluster, and the operator image has been loaded.

Run:

```bash
make test-e2e
```

Expected: all e2e scenarios pass, including the new `NebariApp with routing.tls.secretName` block. Expect this to take 10 to 15 minutes.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/tls_user_secret_test.go
git commit -m "test(e2e): cover routing.tls.secretName end-to-end

Pre-creates a self-signed TLS secret in envoy-gateway-system, creates a
NebariApp referencing it via routing.tls.secretName, and asserts that
TLSReady flips to UserProvidedSecretReady, no Certificate is created,
and the Gateway listener's certificateRefs point at the user's secret."
```

---

## Task 9: Documentation updates

**Files:**

- Modify: `docs/configuration-reference.md`
- Modify: `docs/reconcilers/routing.md`

- [ ] **Step 1: Locate the TLS section in `docs/configuration-reference.md`**

Run:

```bash
grep -n -A 5 "tls:" docs/configuration-reference.md | head -40
```

Use the output to find the section that currently documents `routing.tls.enabled`.

- [ ] **Step 2: Add a `secretName` subsection**

After the `enabled` field documentation in `docs/configuration-reference.md`, append:

```markdown
### `routing.tls.secretName`

**Type:** string (optional)
**Default:** unset (cert-manager manages the certificate)

When set, the operator will not create a cert-manager `Certificate` for
this NebariApp. Instead, the per-app HTTPS listener on the shared
Gateway points at the named TLS secret, which you must create and
maintain yourself.

**Requirements:**

- The secret must live in the `envoy-gateway-system` namespace (the
  Gateway's namespace).
- The secret must be of type `kubernetes.io/tls` with `tls.crt` and
  `tls.key` keys.
- The operator checks for the secret on every reconcile. A missing or
  wrong-type secret does not block reconciliation: the listener is
  still attached so Envoy picks the secret up as soon as you create or
  fix it. The `TLSReady` condition will report
  `UserProvidedSecretNotFound` or `UserProvidedSecretInvalidType` until
  the secret is valid.

**Example:**

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: myapp
  namespace: myteam
spec:
  hostname: myapp.example.com
  service:
    name: myapp-backend
    port: 8080
  routing:
    tls:
      enabled: true
      secretName: myteam-wildcard-tls
```

**When to use this instead of cert-manager:**

- You have a wildcard certificate managed outside the cluster.
- Your environment is air-gapped and cannot reach ACME providers.
- Your organization rotates certificates via external tooling.
- You cannot (or do not wish to) run cert-manager.

**Migration:** Switching an existing NebariApp from cert-manager to
`secretName` on the fly is supported. The operator will delete the
owned cert-manager `Certificate` on the next reconcile and re-point
the listener.
```

- [ ] **Step 3: Add a note to `docs/reconcilers/routing.md`**

In `docs/reconcilers/routing.md`, find the TLS discussion (search for `tls`). Add a short subsection or bullet:

```markdown
### User-provided TLS secrets

Set `spec.routing.tls.secretName` to bypass cert-manager and use a
pre-existing Kubernetes TLS secret. The secret must live in the
`envoy-gateway-system` namespace and be of type `kubernetes.io/tls`.
See `docs/configuration-reference.md` for details. When `secretName`
is set, the operator does not create or own a `Certificate` resource
for this NebariApp, and it cleans up any previously owned Certificate.
```

- [ ] **Step 4: Preview the rendered docs locally (optional)**

If the repo has a docs preview target, run it. Otherwise, read the changed files back and confirm the formatting is sensible.

- [ ] **Step 5: Commit**

```bash
git add docs/configuration-reference.md docs/reconcilers/routing.md
git commit -m "docs: document routing.tls.secretName

Adds a reference entry for the new SecretName field with requirements,
an example, and migration notes, and a cross-reference from the routing
reconciler doc."
```

---

## Task 10: Full test + lint + build sweep

**Files:** none (verification only).

- [ ] **Step 1: Run the full non-envtest unit test suite**

Run:

```bash
go test ./internal/controller/reconcilers/... ./internal/controller/utils/... ./internal/config/...
```

Expected: all pass.

- [ ] **Step 2: Run the envtest-backed controller suite**

Run:

```bash
make test
```

Expected: all pass. (`make setup-envtest` first if binaries are missing.)

- [ ] **Step 3: Run lint**

Run:

```bash
make lint
```

Expected: no violations. Fix any inline and re-run.

- [ ] **Step 4: Rebuild the binary and regenerate to confirm no drift**

Run:

```bash
make manifests generate docs build
```

Expected: no files change (everything was already regenerated in Task 1) and build succeeds.

- [ ] **Step 5: Verify no stray changes were left uncommitted**

Run:

```bash
git status
```

Expected: working tree clean except for the `.claude/` directory (never commit that).

- [ ] **Step 6: Push the branch**

Run:

```bash
git push -u origin feat/tls-existing-secret-design
```

Expected: branch pushed. Open a pull request against `main` referencing issue #90; paste the relevant bits of the spec into the PR description (do not add any AI attribution).
