# Development Guide

This guide will help you set up a local development environment for the nic-operator.

## Prerequisites

Ensure you have the following tools installed:

- **Go** 1.24+ - [Install Go](https://golang.org/doc/install)
- **Docker** - [Install Docker](https://docs.docker.com/get-docker/)
- **kubectl** - [Install kubectl](https://kubernetes.io/docs/tasks/tools/)
- **kind** (Kubernetes IN Docker) - [Install kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- **Helm** 3+ - [Install Helm](https://helm.sh/docs/intro/install/)
- **make** - Usually pre-installed on macOS/Linux

Optional:
- **istioctl** - [Install Istio](https://istio.io/latest/docs/setup/getting-started/) (only if you want to test with
  Istio)

## Quick Start

### 1. Set Up the Playground Environment

The fastest way to get started is to use our automated playground setup:

```bash
# Create KIND cluster and install all foundational services
make playground-setup
```

This will:
- Create a multi-node KIND cluster named `nic-operator-dev`
- Install Gateway API CRDs
- Install Envoy Gateway (ingress controller)
- Install cert-manager with self-signed CA
- Install Keycloak with PostgreSQL (for SSO testing)
- Create shared Gateway resources
- Generate wildcard TLS certificates

**Note:** The setup will prompt you to optionally install Istio. This is not required for basic operator development.

### 2. Build and Deploy the Operator

```bash
# Generate CRDs and manifests
make manifests generate

# Build the operator Docker image
make docker-build

# Load image into KIND cluster
kind load docker-image controller:latest --name nic-operator-dev

# Deploy the operator
make deploy
```

### 3. Deploy Sample Applications

```bash
# Deploy sample nginx app with manual HTTPRoute
make sample-deploy

# Add DNS entry to /etc/hosts
echo "127.0.0.1  nginx-demo.nic.local" | sudo tee -a /etc/hosts

# Test access via command line
curl http://nginx-demo.nic.local

# For browser access, set up port forwarding in a separate terminal:
kubectl port-forward -n envoy-gateway-system \
  svc/$(kubectl get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=nic-public-gateway -o name | cut -d/ -f2) \
  8080:80

# Then open in your browser:
# http://localhost:8080 (with Host header set via browser extension)
# OR better: http://nginx-demo.nic.local:8080
```

**Note on Browser Access:** Due to KIND's networking limitations, the Gateway isn't directly accessible on port 80. Use
port-forwarding (8080) or access via the NodePort shown in `kubectl get svc -n envoy-gateway-system`.

### 4. Check Playground Status

```bash
# View status of all components
make playground-status

# Check operator logs
kubectl logs -n nic-operator-system deployment/nic-operator-controller-manager -f
```

### 5. Teardown

```bash
# Remove sample apps
make sample-undeploy

# Remove operator
make undeploy

# Delete entire playground environment
make playground-teardown
```

## Development Workflow

### Making Changes to the Operator

1. **Modify the code** in `internal/controller/` or `api/v1/`

2. **Regenerate code and manifests** (if you changed the API):
   ```bash
   make manifests generate
   ```

3. **Run tests**:
   ```bash
   make test
   ```

4. **Rebuild and redeploy**:
   ```bash
   make docker-build
   kind load docker-image controller:latest --name nic-operator-dev
   kubectl rollout restart deployment/nic-operator-controller-manager -n nic-operator-system
   ```

5. **Watch logs**:
   ```bash
   kubectl logs -n nic-operator-system deployment/nic-operator-controller-manager -f
   ```

### Running the Operator Locally (Outside Cluster)

For faster iteration during development, you can run the operator on your local machine:

```bash
# Ensure CRDs are installed
make install

# Run the operator locally
make run
```

This runs the operator outside the cluster but connected to your KIND cluster.

### Testing Changes

```bash
# Run unit tests
make test

# Run integration tests with envtest
make test-integration

# Run E2E tests (requires cluster)
make test-e2e

# Generate coverage report
make test-coverage
```

### Code Quality

```bash
# Format code
make fmt

# Run linters
make lint

# Fix linting issues automatically
make lint-fix
```

## Directory Structure

```
nic-operator/
├── api/v1/                          # API definitions (CRDs)
│   ├── nicapp_types.go              # NicApp CRD (to be created)
│   └── ...
├── cmd/
│   └── main.go                      # Operator entrypoint
├── config/                          # Kubernetes manifests
│   ├── crd/                         # CRD definitions
│   ├── default/                     # Default deployment configuration
│   ├── manager/                     # Operator deployment
│   ├── rbac/                        # RBAC rules
│   └── samples/                     # Sample CRs
├── docs/                            # Documentation
├── dev/                             # Development tools and scripts
│   └── local-cluster/               # Local development environment
│       ├── create-cluster.sh
│       ├── delete-cluster.sh
│       ├── setup-foundational-services.sh
│       └── sample-apps/
├── internal/
│   └── controller/                  # Controller implementation
│       ├── nicapp_controller.go     # Main controller
│       └── reconcilers/             # Domain-specific reconcilers
│           ├── core/                # Core logic (validation, status)
│           ├── routing/             # HTTPRoute generation
│           ├── tls/                 # TLS/cert-manager integration
│           ├── auth/                # Auth policy (Envoy SecurityPolicy)
│           └── keycloak/            # Keycloak client provisioning
├── test/
│   ├── e2e/                         # End-to-end tests
│   └── utils/                       # Test utilities
├── Dockerfile                       # Operator container image
├── Makefile                         # Build automation
├── go.mod                           # Go dependencies
└── TODO.md                          # Implementation roadmap
```

## Accessing Playground Services

### Keycloak Admin Console

```bash
# URL: http://localhost:30080
# Username: admin
# Password: admin
```

### Gateway Information

The playground creates two shared gateways:

1. **Public Gateway** (`nic-public-gateway`):
   - Namespace: `envoy-gateway-system`
   - HTTP: port 80
   - HTTPS: port 443 (with wildcard TLS)
   - Used for public-facing applications

2. **Internal Gateway** (`nic-internal-gateway`):
   - Namespace: `envoy-gateway-system`
   - HTTP: port 8080
   - Used for internal-only applications

### TLS Certificates

The playground creates a wildcard certificate for `*.nic.local`:
- Certificate name: `nic-wildcard-certificate`
- Secret name: `nic-wildcard-tls`
- Namespace: `envoy-gateway-system`

To trust the self-signed CA in your browser:
```bash
# Export the CA certificate
kubectl get secret nic-ca-secret -n cert-manager -o jsonpath='{.data.ca\.crt}' | base64 -d > /tmp/nic-ca.crt

# Import into your system (macOS example)
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain /tmp/nic-ca.crt
```

## Troubleshooting

### Cluster creation fails

```bash
# Check if another cluster with the same name exists
kind get clusters

# Delete existing cluster
kind delete cluster --name nic-operator-dev

# Try again
make playground-setup
```

### Foundational services fail to install

```bash
# Check if Helm repos are accessible
helm repo list
helm repo update

# Check cluster status
kubectl get nodes
kubectl get pods -A
```

### Operator fails to start

```bash
# Check operator logs
kubectl logs -n nic-operator-system deployment/nic-operator-controller-manager

# Check RBAC permissions
kubectl auth can-i --list --as=system:serviceaccount:nic-operator-system:nic-operator-controller-manager

# Reinstall CRDs
make uninstall install
```

### Sample app not accessible

```bash
# Check if HTTPRoute is created
kubectl get httproute -n demo-app

# Check Gateway status
kubectl get gateway -n envoy-gateway-system

# Check if pods are running
kubectl get pods -n demo-app

# Verify DNS entry in /etc/hosts
cat /etc/hosts | grep nic.local

# Check Envoy proxy logs
kubectl logs -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=nic-public-gateway
```

### Port conflicts

If ports 80, 443, or 30080 are already in use:

```bash
# Check what's using the ports
lsof -i :80
lsof -i :443
lsof -i :30080

# Either stop the conflicting service or modify dev/local-cluster/kind-config.yaml
# to use different host ports
```

## Advanced Topics

### Debugging Reconciliation

Add verbose logging:

```bash
# Edit config/manager/manager.yaml to add --zap-log-level=debug
# Then redeploy
make deploy
```

Or run locally with debug logging:

```bash
make run -- --zap-log-level=debug
```

### Testing with Multiple Namespaces

```bash
# Create and label multiple test namespaces
kubectl create namespace test-app-1
kubectl create namespace test-app-2
kubectl label namespace test-app-1 nic.nebari.dev/managed=true
kubectl label namespace test-app-2 nic.nebari.dev/managed=true

# Deploy apps to different namespaces
kubectl apply -f dev/local-cluster/sample-apps/nginx-demo.yaml -n test-app-1
```

### Using a Different KIND Cluster

```bash
# Set custom cluster name
CLUSTER_NAME=my-custom-cluster make kind-create

# All subsequent commands will need the same env var
CLUSTER_NAME=my-custom-cluster make playground-setup
```

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the development workflow
4. Add tests for new functionality
5. Ensure all tests pass: `make test`
6. Run linters: `make lint`
7. Commit your changes (follow conventional commits)
8. Push to your fork and create a pull request

## Getting Help

- Check the [TODO.md](../TODO.md) for the implementation roadmap
- Review existing issues on GitHub
- Read the [Architecture documentation](./ARCHITECTURE.md) (coming soon)
- Check operator logs for errors

## Resources

- [Kubebuilder Book](https://book.kubebuilder.io/)
- [Gateway API Documentation](https://gateway-api.sigs.k8s.io/)
- [cert-manager Documentation](https://cert-manager.io/docs/)
- [Envoy Gateway Documentation](https://gateway.envoyproxy.io/)
- [Keycloak Documentation](https://www.keycloak.org/documentation)
- [Controller Runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime)
