# nic-operator

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

  subgraph Operator[nic-operator]
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

## Quick Start

For detailed local development setup with full OIDC authentication support, see:
- **[Local Development Guide](docs/local-development.md)** - Complete setup instructions
- **[Quick Reference](docs/quick-reference.md)** - Command cheat sheet

### Using the Development Tool

The `nic-dev` tool provides a unified interface for all development tasks:

```bash
# Full setup from scratch (cluster + services)
./nic-dev setup

# Or step-by-step:
./nic-dev cluster:create        # Create KIND cluster
./nic-dev services:install      # Install Envoy, cert-manager, Keycloak
./nic-dev gateway:setup         # Setup gateway access (macOS only)
./nic-dev operator:deploy       # Build and deploy operator
./nic-dev samples:deploy        # Deploy sample apps
./nic-dev verify                # Verify everything works

# Other useful commands:
./nic-dev services:status       # Check service status
./nic-dev operator:logs         # View operator logs
./nic-dev gateway:ip            # Get gateway LoadBalancer IP
./nic-dev samples:deploy-auth   # Deploy sample with authentication
./nic-dev clean                 # Delete everything

# See all commands:
./nic-dev help
```

### Using Make

Traditional Makefile targets are also available:

```bash
make kind-create                # Create cluster
make playground-setup           # Full setup (cluster + services)
make docker-build deploy        # Build and deploy operator
make sample-deploy              # Deploy sample apps
make sample-deploy-auth         # Deploy sample with auth
make kind-delete                # Clean up cluster
```

### Accessing Applications (macOS)

On macOS, Docker runs in a VM, so you need to set up port forwarding to access LoadBalancer services:

```bash
# Setup gateway access (run once after cluster creation)
./nic-dev gateway:setup

# Add application hostnames to /etc/hosts (required for DNS resolution)
echo "127.0.0.1  nginx-demo.nic.local nginx-demo-auth.nic.local" | sudo tee -a /etc/hosts

# Access applications
curl http://nginx-demo.nic.local
curl -k https://nginx-demo-auth.nic.local  # Redirects to Keycloak
```

The `gateway:setup` command configures port forwarding but doesn't modify `/etc/hosts` (requires sudo). You need to add
the hostnames manually.

For more details, see [`dev/MACOS_NETWORK_ACCESS.md`](dev/MACOS_NETWORK_ACCESS.md).
