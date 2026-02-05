# Development Environment

This directory contains scripts and tools for setting up a local development environment for the NIC Operator.

## Directory Structure

```
dev/
├── Makefile                    # Main automation interface
├── README.md                   # This file
├── scripts/                    # Development automation scripts
│   ├── cluster/               # Cluster lifecycle management
│   │   ├── create.sh          # Create Kind cluster with MetalLB
│   │   └── delete.sh          # Delete Kind cluster
│   ├── services/              # Service installation
│   │   ├── install.sh         # Install Envoy Gateway, cert-manager, Gateway
│   │   └── uninstall.sh       # Uninstall all services
│   ├── networking/            # Network configuration
│   │   ├── update-hosts.sh    # Manage /etc/hosts entries for NebariApps
│   │   └── port-forward.sh    # Setup port forwarding for local access
│   └── testing/               # Testing utilities
│       └── test-connectivity.sh  # Test HTTP/HTTPS connectivity to apps
└── examples/                   # Example manifests for local development
    ├── app-deployment.yaml     # Test application deployment (nginx)
    └── nebariapp.yaml          # Simple NebariApp example with TLS and routing
```

## Quick Start

```bash
# Create cluster and install all services
make setup

# Check status
make status

# Teardown everything
make teardown
```

## Available Commands

```bash
make help                # Show all available commands
make cluster-create      # Create Kind cluster with MetalLB
make services-install    # Install Envoy Gateway, cert-manager, etc.
make setup              # Full setup (cluster + services)
make teardown           # Full cleanup
make status             # Check environment status
make update-hosts       # Update /etc/hosts with all NebariApp hostnames
make test-connectivity  # Test HTTP/HTTPS connectivity to an app
                        # Usage: make test-connectivity APP=<name> NS=<namespace>
make port-forward       # Setup port forwarding for local access
```

## What Gets Installed

### 1. Kind Cluster
- Name: `nic-operator-dev` (configurable via `CLUSTER_NAME`)
- 1 control-plane node + 2 worker nodes
- MetalLB for LoadBalancer services
- Port forwarding: 80, 443

### 2. Envoy Gateway (v1.2.4)
- Installed via Helm
- Namespace: `envoy-gateway-system`
- Provides Gateway API implementation
- GatewayClass: `envoy-gateway`

### 3. cert-manager (v1.16.2)
- Installed via Helm with Gateway API support
- Namespace: `cert-manager`
- Self-signed CA for development
- Automatic certificate management

### 4. Gateway Resources
- **Gateway**: `nebari-gateway` in `envoy-gateway-system`
  - HTTP listener on port 80
  - HTTPS listener on port 443
  - Allows routes from all namespaces
- **Wildcard Certificate**: `*.nebari.local`
  - Secret: `nebari-gateway-tls`
  - Self-signed for development

## Usage

### Local Development

> **Note**: The `examples/` directory contains simplified manifests for quick local development.
> For comprehensive test variations (HTTP-only, multiple paths, TLS disabled, etc.), see the
> E2E test files in `test/e2e/` which create these programmatically.

1. **Setup environment**:
   ```bash
   cd dev
   make setup
   ```

2. **Deploy operator**:
   ```bash
   cd ..
   make deploy
   ```

3. **Deploy test application and NebariApp**:
   ```bash
   # Deploy the test app (nginx)
   kubectl apply -f examples/app-deployment.yaml

   # Deploy the NebariApp to expose it via the Gateway
   kubectl apply -f examples/nebariapp.yaml

   # Wait for the app to be ready
   kubectl wait --for=condition=Ready nebariapp/sample-app -n dev-test --timeout=60s

   # Update /etc/hosts for local access
   make update-hosts
   ```

4. **Test connectivity**:
   ```bash
   # Test the app
   curl -k https://sample-app.nebari.local

   # Or use the test script
   make test-connectivity APP=sample-app NS=dev-test
   ```

### E2E Tests

The e2e tests can use this pre-configured environment:

```bash
cd dev
make setup

cd ..
make test-e2e
```

Or let the tests manage everything:

```bash
# Tests will create cluster if needed
make test-e2e
```

## Environment Variables

- `CLUSTER_NAME`: Name of the Kind cluster (default: `nic-operator-dev`)
- `KUBECONFIG`: Path to kubeconfig file (default: `~/.kube/config`)

## Accessing Services

### DNS Configuration

The setup automatically configures `/etc/hosts` to resolve `*.nebari.local` domains to the Gateway's LoadBalancer IP.

#### Automatic Setup (during `make setup`)

The `services-install` script automatically:
1. Gets the Gateway's LoadBalancer IP from MetalLB
2. Adds a base entry: `<GATEWAY_IP> nebari.local # nebari-gateway`

#### Adding App-Specific Hostnames

After creating NebariApp resources, add their hostnames to `/etc/hosts`:

```bash
# Scan and add all NebariApp hostnames automatically
./scripts/networking/update-hosts.sh

# Or add specific app hostname
./scripts/networking/update-hosts.sh sample-app
```

This adds entries like:
```
172.18.255.200 sample-app.nebari.local # nebari-gateway
```

#### Manual Configuration

If needed, you can manually add entries:

```bash
# Get Gateway IP
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway \
  -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}')

# Add entry
echo "${GATEWAY_IP} my-app.nebari.local # nebari-gateway" | sudo tee -a /etc/hosts
```

### Testing Routes

Once DNS is configured, test your apps:

```bash
# HTTP (redirects to HTTPS if TLS enabled)
curl http://sample-app.nebari.local

# HTTPS (use -k for self-signed cert)
curl -k https://sample-app.nebari.local

# View headers and follow redirects
curl -v -L -k https://sample-app.nebari.local

# Test from browser
# Open: https://sample-app.nebari.local
# (Accept the self-signed certificate warning)
```

**Automated Testing**

Use the test-connectivity script to check if an app is reachable:

```bash
# Test the sample app from examples/
make test-connectivity APP=sample-app NS=dev-test

# Or use the script directly
./scripts/testing/test-connectivity.sh sample-app dev-test
```

This will:
- Check if the NebariApp exists and is ready
- Test HTTP and HTTPS connectivity
- Show curl commands for manual testing
- Check if hostname is in /etc/hosts

### Gateway LoadBalancer IP

To view the Gateway's external IP:

```bash
kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway
```

## Troubleshooting

### Check cluster health

```bash
make status
```

### View Gateway status

```bash
kubectl get gateway nebari-gateway -n envoy-gateway-system -o yaml
kubectl get gatewayclass envoy-gateway -o yaml
```

### Check certificate

```bash
kubectl get certificate -n envoy-gateway-system
kubectl describe certificate nebari-gateway-cert -n envoy-gateway-system
```

### View Envoy Gateway logs

```bash
kubectl logs -n envoy-gateway-system deployment/envoy-gateway
```

### Recreate everything

```bash
make teardown
make setup
```

## Production Differences

This development setup differs from production in these ways:

1. **Certificates**: Uses self-signed CA instead of Let's Encrypt
2. **LoadBalancer**: Uses MetalLB instead of cloud provider LB
3. **DNS**: Uses `/etc/hosts` instead of real DNS
4. **Scale**: Single replica deployments instead of HA setup

For production setup with ArgoCD, see the main documentation.
