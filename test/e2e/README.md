# E2E Tests

End-to-end tests for the Nebari Operator.

## Running Tests

### Option 1: Use Pre-configured Environment (Recommended)

Setup infrastructure first, then run tests:

```bash
# Setup infrastructure
cd dev
make setup

# Run tests
cd ..
make test-e2e USE_EXISTING_CLUSTER=true
```

### Option 2: Let Tests Setup Infrastructure

Tests will create cluster and install services:

```bash
make test-e2e SETUP_INFRASTRUCTURE=true
```

### Option 3: Completely Managed (CI)

Tests manage everything including cleanup:

```bash
make test-e2e
```

## Environment Variables

- `USE_EXISTING_CLUSTER=true`: Use existing cluster, don't create/delete Kind cluster
- `SETUP_INFRASTRUCTURE=true`: Run `dev/install-services.sh` to setup Envoy Gateway, cert-manager, etc.
- `SKIP_SETUP=true`: Skip all setup, assume cluster and infrastructure exist

## Test Structure

### Infrastructure Validation Tests

Tests that verify the foundational infrastructure is properly configured:

- **Gateway and cert-manager Integration**: Validates Gateway API resources, GatewayClass, wildcard TLS certificate
- Skipped if Gateway API CRDs not found

### NebariApp Reconciliation Tests

Tests that validate the operator's core functionality:

- Create NebariApp resources
- Verify HTTPRoute creation and configuration
- Validate routing to backend services
- Test deletion and cleanup

### Routing Schema Variation Tests

Comprehensive tests covering all routing schema variations (**NEW**):

- **TLS Configuration**: HTTP vs HTTPS listeners, default TLS behavior
- **Path-Based Routing**: PathPrefix and Exact match types, multiple path rules
- **Combined Scenarios**: TLS + path routing combinations
- **Hostname Variations**: Different hostname formats
- **Edge Cases**: No routing config, TLS-only, root path, etc.

See [ROUTING_TEST_COVERAGE.md](./ROUTING_TEST_COVERAGE.md) for detailed test coverage matrix.

### NebariApp Authentication Tests

Comprehensive tests for OIDC authentication integration (**NEW**):

- **Operator Configuration Validation**: Verifies operator has correct Keycloak environment variables and can access
  admin credentials
- **Authentication Disabled**: Validates that SecurityPolicy is not created when auth is disabled (default)
- **Keycloak Integration**:
  - Automatic OIDC client provisioning
  - SecurityPolicy creation with correct issuer, scopes, and secrets
  - Client secret storage in Kubernetes secrets
  - Cleanup of provisioned clients on deletion
  - Validation failures when credentials are missing
- **Generic OIDC Provider**:
  - Manual client provisioning workflow
  - Custom issuer URL configuration
  - SecurityPolicy creation with external providers (Google, Azure AD, Okta, etc.)
  - Validation of required issuerURL field

**Configuration Testing**: Tests verify that the operator config objects (`internal/config`) properly load:
- Keycloak URL, realm, and admin credentials from environment variables and secrets
- Default values when optional configuration is not provided
- Priority: Kubernetes secret > environment variables
- Error handling when required credentials are missing

These tests require Keycloak to be installed with the `nebari` realm configured. The dev setup scripts
(`dev/install-keycloak.sh` and `dev/setup-keycloak-realm.sh`) handle this automatically.

### HTTP/HTTPS Connectivity Tests

End-to-end connectivity tests:

- HTTP connectivity via Gateway IP
- HTTPS connectivity with TLS via Gateway IP
- Response validation

## Prerequisites

The tests assume the following infrastructure exists:

1. **Gateway API CRDs**: Installed by Envoy Gateway
2. **Envoy Gateway**: Running in `envoy-gateway-system` namespace
3. **cert-manager**: With Gateway API support enabled
4. **Gateway Resource**: `nebari-gateway` in `envoy-gateway-system`
   - HTTP listener on port 80
   - HTTPS listener on port 443
   - Wildcard TLS certificate
5. **GatewayClass**: `envoy-gateway` (created by Envoy Gateway)
6. **Keycloak** (for auth tests): Running in `keycloak` namespace with `nebari` realm configured
   - Secret: `keycloak-admin-credentials` containing master realm admin credentials (admin/admin)
   - Secret: `nebari-realm-admin-credentials` containing nebari realm admin credentials (admin/nebari-admin)
   - Accessible at: `http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth`
   - **Memory Requirements**: Keycloak requires ~2Gi memory in dev mode. Ensure your Kind cluster has sufficient
     resources.

**Note**: The dev setup scripts (`make services-install`) automatically install all prerequisites including Keycloak.

**Keycloak Memory Configuration**: If Keycloak pods are OOMKilled, the install script has been configured with:
- Memory request: 1Gi
- Memory limit: 2Gi
- JVM heap: 512m-1536m

## Local Development Workflow

1. **Setup once**:
   ```bash
   cd dev
   make setup
   cd ..
   ```

2. **Iterate on operator code**:
   ```bash
   # Make changes to operator...

   # Run tests against existing infrastructure
   make test-e2e USE_EXISTING_CLUSTER=true
   ```

3. **Cleanup when done**:
   ```bash
   cd dev
   make teardown
   cd ..
   ```

## CI Workflow

CI should use the automatic setup mode:

```yaml
- name: Run E2E Tests
  run: |
    make test-e2e SETUP_INFRASTRUCTURE=true
```

This will:
1. Create Kind cluster (if needed)
2. Install foundational services via dev scripts
3. Build and deploy operator
4. Run all tests
5. Cleanup everything

## Troubleshooting

### Tests are skipped

If you see "Gateway API CRDs not installed - skipping tests", the infrastructure is missing:

```bash
# Check what's installed
cd dev
make status

# Install infrastructure
make services-install
```

### Gateway not found

Verify the Gateway exists:

```bash
kubectl get gateway nebari-gateway -n envoy-gateway-system
```

If missing, reinstall services:

```bash
cd dev
make services-install
```

### Operator not deploying

Check the operator deployment:

```bash
kubectl get pods -n nebari-operator-system
kubectl logs -n nebari-operator-system deployment/nebari-operator-controller-manager
```

### Keycloak OOMKilled or CrashLoopBackOff

If Keycloak pods are failing due to memory issues:

```bash
# Check Keycloak pod status
kubectl get pods -n keycloak
kubectl describe pod -n keycloak -l app.kubernetes.io/name=keycloakx

# Uninstall and reinstall with updated memory settings
helm uninstall keycloak -n keycloak
cd dev
./install-keycloak.sh

# Verify Keycloak is running
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=keycloakx -n keycloak --timeout=5m

# Check if master realm admin secret exists (used by operator)
kubectl get secret keycloak-admin-credentials -n keycloak

# Check if nebari realm admin secret exists
kubectl get secret nebari-realm-admin-credentials -n keycloak
```

**Note**: Keycloak requires ~2Gi memory to run in dev mode. If your Kind cluster has insufficient resources, consider:
- Increasing Docker Desktop memory allocation
- Using a lighter authentication solution for testing
- Skipping auth tests: `go run github.com/onsi/ginkgo/v2/ginkgo -v -tags=e2e --skip="Authentication" test/e2e/`

### Clean slate

Delete and recreate everything:

```bash
cd dev
make teardown
make setup
cd ..
make test-e2e USE_EXISTING_CLUSTER=true
```
