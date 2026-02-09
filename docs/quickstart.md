# Quick Start Guide

Get started with the Nebari Operator in 5 minutes. This guide walks you through setting up a local development
environment and deploying your first application.

## Prerequisites

- Docker
- kubectl
- make
- Kind (for local development)

## Step 1: Set Up Development Environment

Use the automated development setup to create a fully configured cluster:

```bash
cd dev
make setup
```

This command:
1. Creates a Kind cluster (`nebari-operator-dev`) with MetalLB
2. Installs Envoy Gateway v1.2.4
3. Installs cert-manager v1.16.2 with Gateway API support
4. Creates a shared Gateway (`nebari-gateway`) with HTTP/HTTPS listeners
5. Provisions a wildcard TLS certificate for `*.nebari.local`
6. Optionally installs Keycloak for authentication testing

Verify the environment is ready:

```bash
make status
```

**Expected output:**
```
✅ Cluster 'nebari-operator-dev' exists

Checking services...
...

Gateway API resources:
NAMESPACE              NAME              CLASS            ADDRESS          PROGRAMMED   AGE
envoy-gateway-system   nebari-gateway    envoy-gateway    172.18.255.200   True         1m
```

## Step 2: Install the Operator

From the repository root, build and deploy the operator:

```bash
# Return to root directory
cd ..

# Build the operator image
make docker-build IMG=quay.io/nebari/nebari-operator:dev

# Load image into Kind cluster
kind load docker-image quay.io/nebari/nebari-operator:dev --name nebari-operator-dev

# Install CRDs
make install

# Deploy operator to cluster
make deploy IMG=quay.io/nebari/nebari-operator:dev
```

Verify the operator is running:

```bash
kubectl get pods -n nebari-operator-system

# Expected output:
# NAME                                                    READY   STATUS    RESTARTS   AGE
# nebari-operator-controller-manager-xxxxxxxxxx-xxxxx       2/2     Running   0          30s
```

## Step 3: Deploy Your First Application

### 3.1 Create a Test Application

Create a namespace and deploy a simple test application:

```bash
kubectl create namespace demo
kubectl label namespace demo nebari.dev/managed=true

kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hello-app
  namespace: demo
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hello
  template:
    metadata:
      labels:
        app: hello
    spec:
      containers:
      - name: hello
        image: hashicorp/http-echo
        args:
          - "-text=Hello from Nebari Operator!"
        ports:
        - containerPort: 5678
---
apiVersion: v1
kind: Service
metadata:
  name: hello-service
  namespace: demo
spec:
  selector:
    app: hello
  ports:
  - port: 80
    targetPort: 5678
EOF
```

### 3.2 Create a NebariApp Resource

Create a NebariApp to expose your service:

```bash
kubectl apply -f - <<EOF
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: hello-app
  namespace: demo
spec:
  hostname: hello.nebari.local
  service:
    name: hello-service
    port: 80
  routing:
    tls:
      enabled: true
EOF
```

### 3.3 Verify the Configuration

Check the NebariApp status:

```bash
kubectl get nebariapp -n demo
kubectl describe nebariapp hello-app -n demo
```

You should see conditions indicating success:
- `Ready=True`
- `RoutingReady=True`

Verify the HTTPRoute was created:

```bash
kubectl get httproute -n demo
```

## Step 4: Test Your Application

The dev setup automatically configures `/etc/hosts` entries for `*.nebari.local`. Test your application:

### HTTP Test

```bash
curl http://hello.nebari.local
```

### HTTPS Test

```bash
curl -k https://hello.nebari.local
```

**Expected output:**
```
Hello from Nebari Operator!
```

> **Note:** The `-k` flag skips certificate validation since we're using self-signed certificates in development.

## Step 5: Enable Authentication (Optional)

Add authentication to your application with Keycloak:

### 5.1 Update NebariApp with Auth

```bash
kubectl apply -f - <<EOF
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: hello-app
  namespace: demo
spec:
  hostname: hello.nebari.local
  service:
    name: hello-service
    port: 80
  routing:
    tls:
      enabled: true
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    scopes:
      - openid
      - profile
      - email
EOF
```

### 5.2 Test Authenticated Access

Access your application - you'll be redirected to Keycloak for authentication:

```bash
open https://hello.nebari.local
```

**Default Keycloak credentials:**
- Username: `admin`
- Password: `admin`

## Common Issues

### Gateway Has No IP Address

**Problem:** Gateway shows `ADDRESS: <none>`

**Solution:**
```bash
cd dev
make cluster-create  # Recreates cluster with MetalLB
```

### Certificate Not Ready

**Problem:** TLS not working, certificate shows `Ready: False`

**Solution:**
```bash
kubectl get certificate -n envoy-gateway-system
kubectl describe certificate nebari-gateway-cert -n envoy-gateway-system

# Force certificate renewal
kubectl delete certificate nebari-gateway-cert -n envoy-gateway-system
cd dev && make services-install
```

### Service Not Found Error

**Problem:** NebariApp shows `ServiceNotFound` condition

**Solution:**
- Verify the service exists: `kubectl get svc hello-service -n demo`
- Check the port matches: `kubectl get svc hello-service -n demo -o jsonpath='{.spec.ports[0].port}'`

### Namespace Not Opted In

**Problem:** NebariApp shows `NamespaceNotOptedIn` condition

**Solution:**
```bash
kubectl label namespace demo nebari.dev/managed=true
```

## Cleanup

Remove the test application:

```bash
kubectl delete nebariapp hello-app -n demo
kubectl delete namespace demo
```

Teardown the development environment:

```bash
cd dev
make teardown
```

## Next Steps

- **[Platform Setup](platform-setup.md)** - Production infrastructure setup
- **[Configuration Reference](configuration-reference.md)** - Complete NebariApp CRD documentation
- **[Reconciler Architecture](reconcilers/README.md)** - How the operator works internally
- **[Authentication Guide](reconcilers/authentication.md)** - OIDC integration details

## Additional Resources

### Development Makefile Targets

From the `dev/` directory:
- `make help` - Show all available commands
- `make cluster-create` - Create Kind cluster only
- `make services-install` - Install infrastructure services only
- `make status` - Check environment status
- `make teardown` - Complete cleanup

### Operator Makefile Targets

From the repository root:
- `make help` - Show all available commands
- `make build` - Build manager binary
- `make docker-build` - Build Docker image
- `make install` - Install CRDs
- `make deploy` - Deploy operator
- `make undeploy` - Remove operator
- `make test` - Run unit tests
- `make test-e2e` - Run end-to-end tests

See [docs/makefile-reference.md](makefile-reference.md) for complete documentation.
