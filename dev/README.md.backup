# Development Environment

This directory contains scripts and tools for setting up a local development environment for the NIC Operator.

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

3. **Create test application**:
   ```bash
   kubectl create namespace test-app
   kubectl label namespace test-app nebari.dev/managed=true

   kubectl apply -f - <<EOF
   apiVersion: reconcilers.nebari.dev/v1
   kind: NebariApp
   metadata:
     name: my-app
     namespace: test-app
   spec:
     hostname: my-app.nebari.local
     service:
       name: my-app-service
       port: 8080
     routing:
       tls:
         enabled: true
   EOF
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

### Gateway LoadBalancer IP

Get the external IP assigned by MetalLB:

```bash
kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway
```

### Testing Routes

Add to `/etc/hosts` (macOS/Linux):

```bash
# Get Gateway IP
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system \
  -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway \
  -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}')

# Add entries
echo "${GATEWAY_IP} my-app.nebari.local" | sudo tee -a /etc/hosts
```

Then test:

```bash
curl http://my-app.nebari.local
curl -k https://my-app.nebari.local  # -k for self-signed cert
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
