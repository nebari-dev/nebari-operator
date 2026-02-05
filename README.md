# nebari-operator

Kubernetes Operator designed to streamline and centralize the configuration of **routing**, **TLS certificates**, and
**SSO authentication** within the NIC ecosystem.

This project targets a GitOps-friendly platform where:
- **Argo CD** deploys application Helm charts (the “workloads/apps”) -
  [NS/EW traffic management](https://devcookies.medium.com/north-south-vs-east-west-traffic-in-microservices-a-complete-guide-0e458fe4e605):
  - **Envoy Gateway (Gateway API)** provides north/south traffic entry
  - **Istio** provides mesh capabilities (east/west, optional policies)
- **cert-manager** provisions/renews TLS certificates
- **Keycloak** provides authentication / user & client management

The operator’s purpose is to enable self-service onboarding: > “When a new app is installed via Helm/Argo CD, the
platform automatically wires DNS/TLS, routes, and SSO.”


## Goals

- Provide a single onboarding __contract__ for apps (via a CRD or annotation-based intent).
- Automatically reconcile:
  - **Gateway API routes** (e.g., `HTTPRoute`)
  - **TLS** (cert-manager driven)
  - **SSO/OIDC enforcement** at the edge (Envoy Gateway policies)
  - **Keycloak client provisioning** for each onboarded app
- Be **GitOps-compatible**:
  - Users/app charts define intent
  - Operator renders/owns generated platform resources
  - Changes are declarative and continuously reconciled

```mermaid
flowchart TB
  Helm[Helm install app] --> K8s[(Kubernetes API)]
  K8s --> AppCR[NicApp CR]

  subgraph Operator[nebari-operator]
    D[Intent Reconciler]
    R[Routing Reconciler]
    T[TLS Reconciler]
    A[Auth Reconciler]
    K[Keycloak Reconciler optional]
  end

  AppCR --> D
  D --> R --> HTTPRoute[HTTPRoute]
  HTTPRoute --> Gateway[Gateway]

  D --> T --> Certs[cert-manager Certificate or shared wildcard secret]
  Certs --> Gateway

  D --> A --> SecPol[EnvoyGateway SecurityPolicy OIDC]
  Secret[OIDC client secret in K8s Secret] --> SecPol

  D --> K --> KC[Keycloak client]
  KC --> Secret

```

See the Architectural decision issue for more information. [soon]
## Installation

### Using kubectl (Recommended)

Install the latest release directly from GitHub:

```bash
kubectl apply -f https://github.com/nebari-dev/nebari-operator/releases/latest/download/install.yaml
```

Or install a specific version:

```bash
kubectl apply -f https://github.com/nebari-dev/nebari-operator/releases/download/v1.0.0/install.yaml
```

### Using Kustomize

Clone the repository and use kustomize:

```bash
git clone https://github.com/nebari-dev/nebari-operator.git
cd nebari-operator
make deploy IMG=quay.io/nebari/nebari-operator:latest
```

### Verify Installation

Check that the operator is running:

```bash
kubectl get pods -n nebari-operator-system
kubectl logs -n nebari-operator-system deployment/nebari-operator-controller-manager
```

## Quick Start

Create a sample NebariApp to expose your service:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: my-app
  namespace: default
spec:
  hostname: my-app.nebari.local
  service:
    name: my-service
    port: 8080
  tls:
    enabled: true
    mode: wildcard
  auth:
    enabled: false
```

Apply the configuration:

```bash
kubectl apply -f my-app.yaml
```

For more examples and configuration options, see the [Configuration Reference](docs/configuration-reference.md).

## Documentation

- [Configuration Reference](docs/configuration-reference.md) - Complete reference for all NebariApp options
- [Makefile Reference](docs/makefile-reference.md) - Guide to Makefile targets and CI/CD integration
- [Release Process](docs/release-process.md) - How releases are created and managed
- [Release Checklist](docs/release-checklist.md) - Step-by-step checklist for releases
- [GitHub Secrets Setup](docs/github-secrets-setup.md) - Configure secrets for automated releases

## Development

### Prerequisites

- Go 1.24 or later
- Docker or Podman
- kubectl
- A Kubernetes cluster (kind, minikube, etc.)
- make (for using the Makefile)

### Using the Makefile

The project uses a Kubebuilder-generated Makefile that provides convenient targets for development. See the
[Makefile Reference](docs/makefile-reference.md) for complete documentation.

**Common commands**:
```bash
make help          # Show all available targets
make fmt           # Format code
make vet           # Run static analysis
make test          # Run tests
make lint          # Run linter
make manifests     # Generate CRDs and RBAC
make generate      # Generate DeepCopy code
make build         # Build binary
make docker-build  # Build Docker image
```

### Running Locally

```bash
# Install dependencies and generate code
make manifests
make generate

# Run tests
make test

# Run linter
make lint

# Run the operator locally
make run
```

### Manual Testing with Development Environment

The `dev/` folder contains scripts and sample resources for manual testing of the operator.

#### 1. Setup Development Cluster

Create a Kind cluster with all required infrastructure (Envoy Gateway, cert-manager, Gateway):

```bash
# Create cluster and install all services
cd dev
make setup
cd ..
```

This will:
- Create a Kind cluster named `nic-operator-dev`
- Install Envoy Gateway with Gateway API support
- Install cert-manager with self-signed certificates
- Deploy a shared Gateway (`nebari-gateway`) with HTTP/HTTPS listeners
- Configure TLS with wildcard certificate for `*.nebari.local`

#### 2. Build and Deploy the Operator

Build the operator image and deploy it to the cluster:

```bash
# Build and load operator image to Kind
make docker-build IMG=quay.io/nebari/nebari-operator:dev
kind load docker-image quay.io/nebari/nebari-operator:dev --name nic-operator-dev

# Install CRDs and deploy operator
make install
make deploy IMG=quay.io/nebari/nebari-operator:dev
```

Verify the operator is running:

```bash
kubectl get pods -n nebari-operator-system
kubectl logs -n nebari-operator-system -l control-plane=controller-manager -f
```

#### 3. Deploy Sample Application

Use the provided samples to test the operator:

```bash
# Deploy sample app (nginx) with service
kubectl apply -f dev/sample-app-deployment.yaml

# Test basic NebariApp (no routing)
kubectl apply -f dev/sample-nebariapp-basic.yaml

# Check status
kubectl get nebariapp -n dev-test
kubectl describe nebariapp sample-app-basic -n dev-test

# Test NebariApp with HTTPS routing
kubectl apply -f dev/sample-nebariapp-with-routing.yaml

# Verify HTTPRoute was created
kubectl get httproute -n dev-test
kubectl describe httproute sample-app-routing-route -n dev-test

# Test HTTP-only routing (TLS disabled)
kubectl apply -f dev/sample-nebariapp-http-only.yaml

# Verify HTTP listener is used
kubectl get httproute sample-app-http-route -n dev-test -o jsonpath='{.spec.parentRefs[0].sectionName}'
# Should output: http

# Test advanced routing (multiple paths)
kubectl apply -f dev/sample-nebariapp-advanced.yaml
```

#### 4. Verify Routing

Check that the operator created the HTTPRoute correctly:

```bash
# View all HTTPRoutes
kubectl get httproute -n dev-test

# Check specific HTTPRoute details
kubectl get httproute sample-app-routing-route -n dev-test -o yaml

# Verify Gateway reference
kubectl get httproute sample-app-routing-route -n dev-test \
  -o jsonpath='{.spec.parentRefs[0]}' | jq

# Check NebariApp status conditions
kubectl get nebariapp sample-app-routing -n dev-test \
  -o jsonpath='{.status.conditions}' | jq
```

#### 5. Test Changes

To test your code changes:

```bash
# Rebuild and reload image
make docker-build IMG=quay.io/nebari/nebari-operator:dev
kind load docker-image quay.io/nebari/nebari-operator:dev --name nic-operator-dev

# Restart the operator to pick up new image
kubectl rollout restart deployment nebari-operator-controller-manager -n nebari-operator-system

# Watch logs
kubectl logs -n nebari-operator-system -l control-plane=controller-manager -f
```

#### 6. Cleanup

Remove test resources and tear down the environment:

```bash
# Delete sample applications
kubectl delete -f dev/sample-nebariapp-advanced.yaml
kubectl delete -f dev/sample-nebariapp-http-only.yaml
kubectl delete -f dev/sample-nebariapp-with-routing.yaml
kubectl delete -f dev/sample-nebariapp-basic.yaml
kubectl delete -f dev/sample-app-deployment.yaml

# Or delete entire namespace
kubectl delete namespace dev-test

# Undeploy operator
make undeploy

# Teardown development environment
cd dev
make teardown
cd ..
```

#### Available Sample Resources

The `dev/` folder includes:

- **`sample-app-deployment.yaml`** - Basic nginx deployment with service
- **`sample-nebariapp-basic.yaml`** - Minimal NebariApp (no routing)
- **`sample-nebariapp-with-routing.yaml`** - NebariApp with HTTPS routing
- **`sample-nebariapp-http-only.yaml`** - NebariApp with HTTP routing (TLS disabled)
- **`sample-nebariapp-advanced.yaml`** - NebariApp with multiple path rules

See [dev/README.md](dev/README.md) for detailed testing scenarios.

### Building

```bash
# Build the binary
make build

# Build Docker image
make docker-build IMG=quay.io/nebari/nebari-operator:dev

# Push Docker image
make docker-push IMG=quay.io/nebari/nebari-operator:dev
```

## Contributing

We welcome contributions! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Add tests
5. Run `make fmt`, `make vet`, `make test`, and `make lint`
6. Submit a pull request

### Pull Request Builds

When you open a pull request:
- ✅ Tests run automatically
- ✅ Code is validated with linters
- ✅ Docker images are built with your branch name as tag
- ✅ A comment is added to your PR with the image details

You can use the PR image to test your changes:
```bash
kubectl set image deployment/nebari-operator-controller-manager \
  manager=quay.io/nebari/nebari-operator:your-branch-name \
  -n nebari-operator-system
```

## Releases

Releases are automated via GitHub Actions. When a new release is created on GitHub:

1. Tests are run automatically
2. Docker images are built for multiple architectures
3. Go binaries are built for multiple platforms
4. Kubernetes manifests are generated
5. All artifacts are attached to the release

See the [Release Process](docs/release-process.md) for detailed information.

### Latest Release

Check the [Releases page](https://github.com/nebari-dev/nebari-operator/releases) for the latest version.

### Container Images

Container images are available at:
```
quay.io/nebari/nebari-operator:latest
quay.io/nebari/nebari-operator:<version>
```

## License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## Support

- Report issues: [GitHub Issues](https://github.com/nebari-dev/nebari-operator/issues)
- Documentation: [docs/](docs/)
