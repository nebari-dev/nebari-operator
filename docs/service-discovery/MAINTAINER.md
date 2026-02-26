# Service Discovery API — Maintainer Testing Guide

This guide shows how to build, deploy, and test the Service Discovery API locally using a Kind cluster.

## Prerequisites

- [Go](https://go.dev/dl/) ≥ 1.23
- [Docker](https://docs.docker.com/get-docker/) (or Podman)
- [kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation) ≥ 0.20
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [make](https://www.gnu.org/software/make/)

## Quick Start

### 1. Create or re-use a Kind cluster

```bash
kind create cluster --name nebari-operator-dev
```

> If the cluster already exists this command will fail; skip it.

### 2. Build and deploy (one command)

```bash
make dev-svc-api KIND_CLUSTER=nebari-operator-dev
```

This will:
1. Build `Dockerfile.service-discovery` and tag it `service-discovery-api:dev`
2. Load the image into your Kind cluster (no registry push needed)
3. Create the `nebari-system` namespace if necessary
4. Apply all manifests from `deploy/service-discovery/` via kustomize
5. Patch the deployment to use the freshly loaded local image with `imagePullPolicy: Never`

### 3. Port-forward

```bash
make svc-api-pf
# → kubectl port-forward -n nebari-system svc/service-discovery 8080:8080
```

Leave this running in one terminal window.



## Testing the API

### Health

```bash
curl -s http://localhost:8080/api/v1/health | jq .
```

Expected:
```json
{"status":"healthy","checks":{"watcher":"ok"}}
```

### Services list (public; no auth required)

```bash
curl -s http://localhost:8080/api/v1/services | jq .
```

Returns services grouped by visibility: `public`, `authenticated`, `private`.

### Categories

```bash
curl -s http://localhost:8080/api/v1/categories | jq .
```

### Individual service

```bash
curl -s http://localhost:8080/api/v1/services/nebari-system/service-discovery | jq .
```

### WebSocket (live push updates)

With `wscat` (install via `npm i -g wscat`):

```bash
wscat -c ws://localhost:8080/api/v1/ws
```

Or with plain `curl`:

```bash
curl --no-buffer -H "Connection: Upgrade" -H "Upgrade: websocket" \
     -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
     -H "Sec-WebSocket-Version: 13" \
     http://localhost:8080/api/v1/ws
```

Create or update a NebariApp CR to see the WebSocket stream update in real-time:

```bash
kubectl apply -f dev/examples/nebariapp.yaml
```



## Unit Tests

```bash
make test-svc-api
```

Runs tests for `internal/servicediscovery/api/`, `auth/`, and `cache/` with coverage.

Generate HTML coverage report:

```bash
make test-svc-api-html
```



## Testing with a Real NebariApp CR

Apply the sample from `dev/examples/`:

```bash
kubectl apply -f dev/examples/nebariapp.yaml
```

Or inline:

```bash
kubectl apply -f - <<EOF
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: my-app
  namespace: nebari-system
spec:
  hostname: my-app.nebari.example.com
  service:
    name: my-app
    port: 8080
  routing:
    tls:
      enabled: false
  auth:
    enabled: false
  landingPage:
    enabled: true
    displayName: "My Test App"
    description: "A locally deployed test service"
    category: "Testing"
    visibility: "public"
    priority: 10
EOF
```

The watcher will pick this up and expose it at `/api/v1/services` within a few seconds.



## Iterating on Code Changes

After editing Go source:

```bash
make dev-svc-api KIND_CLUSTER=nebari-operator-dev
```

This rebuilds and reloads the image, then restarts the deployment automatically.



## Cleanup

```bash
make undeploy-svc-api                    # remove the kustomize stack
kind delete cluster --name nebari-operator-dev
```
