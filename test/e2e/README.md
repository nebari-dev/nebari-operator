# E2E Tests

End-to-end tests for the NIC Operator.

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

### Clean slate

Delete and recreate everything:

```bash
cd dev
make teardown
make setup
cd ..
make test-e2e USE_EXISTING_CLUSTER=true
```
