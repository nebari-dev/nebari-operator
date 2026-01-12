# NIC Operator - Quick Development Workflow

This guide shows the complete workflow for developing and testing the NIC operator.

## Initial Setup

```bash
# 1. Create cluster with all services
./nic-dev setup

# Or step by step:
./nic-dev cluster:create    # Creates KIND cluster + MetalLB
./nic-dev services:install  # Installs Envoy Gateway, cert-manager, Keycloak
                            # + Sets up gateway forwarding (macOS)
```

## Build and Deploy Operator

```bash
# Build and deploy in one command
./nic-dev operator:deploy

# Or use Makefile
make docker-build
make kind-load
make deploy
```

## Deploy Sample Applications

### Simple App (No Authentication)

```bash
# Deploy
./nic-dev samples:deploy
# or: make sample-deploy

# Add hostname
./dev/local-cluster/add-host.sh nginx-demo.nic.local

# Test
curl http://nginx-demo.nic.local
open http://nginx-demo.nic.local
```

### App with Authentication (Keycloak Login)

```bash
# Deploy
./nic-dev samples:deploy-auth
# or: make sample-deploy-auth

# Add hostname
./dev/local-cluster/add-host.sh nginx-demo-auth.nic.local

# Test - should redirect to Keycloak login
open http://nginx-demo-auth.nic.local
```

## Verify Everything Works

```bash
# Run comprehensive verification
./nic-dev verify

# Check operator logs
./nic-dev operator:logs

# Check NicApp resources
kubectl get nicapp -A
kubectl describe nicapp -n demo-app nginx-demo

# Check generated HTTPRoutes
kubectl get httproute -A
```

## Development Workflow

### Making Changes

```bash
# 1. Edit code (Go files in internal/controller/)

# 2. Rebuild and redeploy
./nic-dev operator:deploy

# 3. Watch logs
./nic-dev operator:logs

# 4. Test with samples
kubectl apply -f dev/local-cluster/sample-apps/nginx-demo-nicapp.yaml
```

### Testing Different Configurations

```bash
# Edit the NicApp spec
kubectl edit nicapp -n demo-app nginx-demo

# Enable authentication
kubectl patch nicapp nginx-demo -n demo-app --type=merge -p '{"spec":{"auth":{"enabled":true}}}'

# Watch operator reconcile
./nic-dev operator:logs
```

## Cleanup

```bash
# Remove samples
./nic-dev samples:delete
# or: make sample-undeploy

# Remove operator
./nic-dev operator:delete

# Delete entire cluster
./nic-dev clean
# or: make playground-teardown
```

## Useful Commands

### Check Gateway Status

```bash
# Get LoadBalancer IP
./nic-dev gateway:ip

# Check gateway services
kubectl get svc -n envoy-gateway-system
kubectl get gateway -n envoy-gateway-system
```

### Check Operator Status

```bash
# Pod status
kubectl get pods -n nic-operator-system

# Logs
kubectl logs -n nic-operator-system deployment/nic-operator-controller-manager -f

# Or use shortcut
./nic-dev operator:logs
```

### Check Application Status

```bash
# NicApp resources
kubectl get nicapp -A

# Detailed status
kubectl get nicapp -n demo-app nginx-demo -o yaml

# Conditions
kubectl get nicapp -n demo-app nginx-demo -o jsonpath='{.status.conditions}' | jq
```

### Debug HTTPRoutes

```bash
# List routes
kubectl get httproute -A

# Check route details
kubectl describe httproute -n demo-app nginx-demo-route

# Check gateway attachment
kubectl get httproute -n demo-app nginx-demo-route -o jsonpath='{.spec.parentRefs}'
```

## Common Issues

### Can't access apps via hostname

```bash
# 1. Check gateway forwarding (macOS)
./nic-dev gateway:setup

# 2. Verify /etc/hosts
grep "\.nic\.local" /etc/hosts

# 3. Check socat in control plane
docker exec nic-operator-dev-control-plane ps aux | grep socat
```

### Operator not reconciling

```bash
# Check operator logs
./nic-dev operator:logs

# Check for errors
kubectl get events -n nic-operator-system --sort-by='.lastTimestamp'

# Restart operator
kubectl rollout restart deployment -n nic-operator-system nic-operator-controller-manager
```

### Authentication not working

```bash
# Check Keycloak is running
kubectl get pods -n keycloak

# Check NicApp auth status
kubectl get nicapp -n demo-app <name> -o jsonpath='{.status.conditions[?(@.type=="AuthReady")]}'

# Check if client was created (manual verification)
# Access Keycloak UI at http://keycloak.nic.local (if configured)
```

## Quick Reference

| Task | Command |
|------|---------|
| Setup everything | `./nic-dev setup` |
| Deploy operator | `./nic-dev operator:deploy` |
| Deploy simple sample | `./nic-dev samples:deploy` |
| Deploy auth sample | `./nic-dev samples:deploy-auth` |
| Add hostname | `./dev/local-cluster/add-host.sh <hostname>` |
| View logs | `./nic-dev operator:logs` |
| Verify setup | `./nic-dev verify` |
| Clean up | `./nic-dev clean` |

## Makefile Targets

All nic-dev commands also available via make:

```bash
make help                    # Show all targets
make playground-setup        # Initial setup
make docker-build           # Build operator image
make deploy                 # Deploy operator
make sample-deploy          # Deploy simple sample
make sample-deploy-auth     # Deploy auth sample
make playground-teardown    # Clean up everything
```
