# Design Document: NIC Landing Page

**Status:** Draft
**Author:** [Author Name]
**Created:** 2026-01-12
**Last Updated:** 2026-01-12

## Table of Contents

1. [Background](#background)
2. [Goals](#goals)
3. [Non-Goals](#non-goals)
4. [Proposed Design](#proposed-design)
5. [Detailed Design](#detailed-design)
6. [API Specification](#api-specification)
7. [Alternatives Considered](#alternatives-considered)
8. [Security Considerations](#security-considerations)
9. [Testing Strategy](#testing-strategy)
10. [Rollout Plan](#rollout-plan)
11. [Open Questions](#open-questions)

---

## Background

The NIC (Nebari Infrastructure Core) ecosystem deploys various services on top of Kubernetes clusters. Currently, there is no centralized way for users to discover and access these services. Users must know the specific URLs for each service or navigate through multiple documentation sources.

The nic-operator already provides a framework for service onboarding via the `NicApp` Custom Resource, which handles routing (HTTPRoute), TLS (cert-manager), and SSO (Envoy Gateway). This proposal extends that system to include a landing page that automatically displays all registered services.

### Current State

- Services are deployed via Helm/ArgoCD
- Each service creates a `NicApp` CR for routing/TLS/auth configuration
- No unified service discovery or landing page exists
- Users must bookmark individual service URLs

### Problem Statement

Users need a single entry point to:
1. Discover all available services in the platform
2. Access services without memorizing individual URLs
3. See the health status of services at a glance
4. Filter and search through available services

---

## Goals

1. **Service Discovery**: Provide a central landing page that displays all services registered via `NicApp` CRs
2. **Real-time Updates**: Automatically update the landing page when services are added, modified, or removed
3. **Health Visibility**: Display health status for services that expose health endpoints
4. **Self-Service**: Services register themselves by including landing page metadata in their `NicApp` CR
5. **GitOps Compatible**: No manual registration required; everything is declarative via CRDs

---

## Non-Goals

1. **Service Management**: The landing page is read-only; it does not manage or configure services
2. **Authentication Portal**: This is not a replacement for Keycloak or identity provider UIs
3. **Monitoring Dashboard**: This is not a replacement for Grafana or similar monitoring tools
4. **API Gateway**: The landing page does not proxy requests to services

---

## Proposed Design

### Architecture Overview

```
┌─────────────────┐     creates      ┌─────────────────┐
│  Helm Chart /   │ ───────────────► │   NicApp CR     │
│  ArgoCD App     │                  │  (CRD instance) │
└─────────────────┘                  └────────┬────────┘
                                              │
                    ┌─────────────────────────┼─────────────────────────┐
                    │                         │                         │
                    ▼                         ▼                         ▼
           ┌───────────────┐         ┌───────────────┐         ┌───────────────┐
           │ nic-operator  │         │ nic-operator  │         │ Landing Page  │
           │ (Routing)     │         │ (TLS/Auth)    │         │ Service       │
           └───────────────┘         └───────────────┘         └───────┬───────┘
                    │                         │                         │
                    ▼                         ▼                    ┌────┴────┐
           ┌───────────────┐         ┌───────────────┐            ▼         ▼
           │  HTTPRoute    │         │  Certificate  │     ┌──────────┐ ┌──────────┐
           │  Gateway      │         │  Secret       │     │ Go API   │ │ React UI │
           └───────────────┘         └───────────────┘     │ Server   │ │  (SPA)   │
                                                           └──────────┘ └──────────┘
```

### Key Design Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Deployment Model | Separate from operator | Independent scaling, clearer separation of concerns, can restart without affecting operator |
| Frontend Stack | React SPA with TypeScript | Rich interactivity, type safety, large ecosystem, team familiarity |
| Real-time Updates | WebSocket | Low latency updates without polling, efficient for multiple clients |
| Health Checks | Server-side | Centralized checking avoids CORS issues, reduces client load |
| Data Source | Watch NicApp CRs | Native Kubernetes pattern, no additional data store needed |

### Component Summary

1. **NicApp CRD Extension**: Add `landingPage` field to existing NicApp spec
2. **Go API Server**: Watches NicApp resources, performs health checks, serves REST API
3. **React Frontend**: SPA that displays services with filtering, search, and real-time updates
4. **Kubernetes Manifests**: Deployment, Service, RBAC, and self-registration NicApp

---

## Detailed Design

### 1. NicApp CRD Extension

Extend the `NicApp` spec with landing page metadata:

```yaml
apiVersion: apps.nebari.dev/v1alpha1
kind: NicApp
metadata:
  name: jupyterhub
  namespace: nebari
spec:
  # Existing fields (routing, TLS, auth)
  hostname: jupyterhub.example.com
  tls:
    enabled: true
  auth:
    enabled: true

  # NEW: Landing page configuration
  landingPage:
    enabled: true                    # Whether to show on landing page (default: false)
    displayName: "JupyterHub"        # Human-readable name (required if enabled)
    description: "Interactive computing environment for data science"
    icon: "jupyter"                  # Icon identifier (see Icon System below)
    category: "Development"          # Grouping category
    priority: 10                     # Sort order within category (lower = higher)
    externalUrl: ""                  # Override URL (default: derived from hostname)
    healthCheck:
      enabled: true                  # Enable health checking (default: false)
      path: "/health"                # Health endpoint path
      intervalSeconds: 30            # Check interval (default: 30, min: 10, max: 300)
      timeoutSeconds: 5              # Request timeout (default: 5)
```

**Go Type Definition:**

```go
// LandingPageConfig defines how a service appears on the landing page
type LandingPageConfig struct {
    // Enabled determines if this service appears on the landing page
    // +kubebuilder:default=false
    Enabled bool `json:"enabled"`

    // DisplayName is the human-readable name shown on the landing page
    // +kubebuilder:validation:MaxLength=64
    DisplayName string `json:"displayName"`

    // Description provides additional context about the service
    // +kubebuilder:validation:MaxLength=256
    // +optional
    Description string `json:"description,omitempty"`

    // Icon is an identifier for the service icon (e.g., "jupyter", "grafana")
    // or a URL to a custom icon
    // +optional
    Icon string `json:"icon,omitempty"`

    // Category groups related services together
    // +optional
    Category string `json:"category,omitempty"`

    // Priority determines sort order within a category (lower = higher priority)
    // +kubebuilder:default=100
    // +kubebuilder:validation:Minimum=0
    // +kubebuilder:validation:Maximum=1000
    // +optional
    Priority int `json:"priority,omitempty"`

    // ExternalUrl overrides the default URL derived from hostname
    // +optional
    ExternalUrl string `json:"externalUrl,omitempty"`

    // HealthCheck configures health status monitoring
    // +optional
    HealthCheck *HealthCheckConfig `json:"healthCheck,omitempty"`
}

// HealthCheckConfig defines health check parameters
type HealthCheckConfig struct {
    // Enabled determines if health checks are performed
    // +kubebuilder:default=false
    Enabled bool `json:"enabled"`

    // Path is the HTTP path to check (e.g., "/health", "/healthz")
    // +kubebuilder:default="/health"
    Path string `json:"path"`

    // IntervalSeconds is how often to check health
    // +kubebuilder:default=30
    // +kubebuilder:validation:Minimum=10
    // +kubebuilder:validation:Maximum=300
    IntervalSeconds int `json:"intervalSeconds,omitempty"`

    // TimeoutSeconds is the request timeout
    // +kubebuilder:default=5
    // +kubebuilder:validation:Minimum=1
    // +kubebuilder:validation:Maximum=30
    TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}
```

### 2. Go API Server

**Location:** `cmd/landingpage/` and `internal/landingpage/`

**Responsibilities:**
- Watch NicApp resources across all namespaces
- Maintain in-memory cache of services with landing page enabled
- Perform periodic health checks for services with health checking enabled
- Serve REST API for the React frontend
- Serve the built React SPA static files
- Broadcast updates via WebSocket

**API Endpoints:**

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/v1/services` | List all registered services |
| GET | `/api/v1/services/{namespace}/{name}` | Get single service details |
| GET | `/api/v1/categories` | List unique categories |
| GET | `/api/v1/health` | Landing page health check |
| WS | `/api/v1/ws` | WebSocket for real-time updates |
| GET | `/*` | Serve React SPA (catch-all) |

**Key Components:**

```
internal/landingpage/
├── server.go           # HTTP server setup, routing
├── api/
│   ├── handlers.go     # REST endpoint handlers
│   └── types.go        # API request/response types
├── watcher/
│   └── watcher.go      # NicApp informer, cache management
├── health/
│   └── checker.go      # Health check scheduler and executor
└── websocket/
    └── hub.go          # WebSocket connection management, broadcasting
```

**Health Check Implementation:**

- Health checks run in a dedicated goroutine pool
- Results are cached and updated asynchronously
- Failed checks are retried with exponential backoff
- Status transitions (healthy → unhealthy) are broadcast via WebSocket

### 3. React Frontend

**Location:** `web/`

**Tech Stack:**
- React 18 with TypeScript
- Vite for build tooling
- TailwindCSS for styling
- React Query (TanStack Query) for data fetching and caching
- Native WebSocket API for real-time updates

**Component Hierarchy:**

```
App
├── Layout
│   ├── Header (logo, search bar)
│   └── Main
│       ├── CategoryFilter (filter chips)
│       └── ServiceGrid
│           └── ServiceCard (repeated)
│               ├── Icon
│               ├── Title/Description
│               ├── HealthBadge
│               └── Link
```

**Key Features:**

1. **Service Grid**: Responsive grid layout showing service cards
2. **Category Filtering**: Filter services by category (chips/tabs)
3. **Search**: Client-side filtering by name/description
4. **Health Status**: Visual indicators (green/red/gray badges)
5. **Real-time Updates**: Services appear/disappear without page reload
6. **Responsive Design**: Works on desktop and mobile

**State Management:**

```typescript
// React Query handles server state
const { data: services, isLoading } = useQuery({
  queryKey: ['services'],
  queryFn: fetchServices,
});

// WebSocket updates invalidate query cache
useWebSocket('/api/v1/ws', {
  onMessage: (event) => {
    const update = JSON.parse(event.data);
    queryClient.invalidateQueries(['services']);
  },
});
```

### 4. Icon System

The landing page supports both built-in icons and custom URLs:

**Built-in Icons** (stored in frontend assets):
- `jupyter` - JupyterHub/JupyterLab
- `grafana` - Grafana
- `prometheus` - Prometheus
- `keycloak` - Keycloak
- `argocd` - ArgoCD
- `default` - Generic application icon

**Custom Icons:**
- Provide a full URL in the `icon` field
- Supports PNG, SVG, or any web-accessible image

### 5. Deployment

**Kubernetes Resources:**

```yaml
# ServiceAccount
apiVersion: v1
kind: ServiceAccount
metadata:
  name: landing-page
  namespace: nic-operator-system

---
# ClusterRole (read-only access to NicApp resources)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: landing-page
rules:
- apiGroups: ["apps.nebari.dev"]
  resources: ["nicapps"]
  verbs: ["get", "list", "watch"]

---
# Deployment
apiVersion: apps/v1
kind: Deployment
metadata:
  name: landing-page
  namespace: nic-operator-system
spec:
  replicas: 2
  selector:
    matchLabels:
      app: landing-page
  template:
    spec:
      serviceAccountName: landing-page
      containers:
      - name: landing-page
        image: ghcr.io/nebari-dev/nic-landing-page:latest
        ports:
        - containerPort: 8080
        resources:
          requests:
            cpu: 50m
            memory: 64Mi
          limits:
            cpu: 200m
            memory: 128Mi
        livenessProbe:
          httpGet:
            path: /api/v1/health
            port: 8080
        readinessProbe:
          httpGet:
            path: /api/v1/health
            port: 8080

---
# Service
apiVersion: v1
kind: Service
metadata:
  name: landing-page
  namespace: nic-operator-system
spec:
  selector:
    app: landing-page
  ports:
  - port: 80
    targetPort: 8080
```

**Self-Registration:**

The landing page registers itself via NicApp:

```yaml
apiVersion: apps.nebari.dev/v1alpha1
kind: NicApp
metadata:
  name: landing-page
  namespace: nic-operator-system
spec:
  hostname: nebari.example.com  # Root domain
  tls:
    enabled: true
  auth:
    enabled: false  # Public access
  landingPage:
    enabled: true
    displayName: "Home"
    description: "Service directory"
    icon: "home"
    category: "Platform"
    priority: 0  # Always first
```

---

## API Specification

### GET /api/v1/services

Returns all services with landing page enabled.

**Response:**

```json
{
  "services": [
    {
      "name": "jupyterhub",
      "namespace": "nebari",
      "displayName": "JupyterHub",
      "description": "Interactive computing environment",
      "url": "https://jupyterhub.example.com",
      "icon": "jupyter",
      "category": "Development",
      "priority": 10,
      "health": {
        "status": "healthy",
        "lastCheck": "2026-01-12T10:30:00Z",
        "message": null
      }
    }
  ],
  "categories": ["Development", "Monitoring", "Platform"]
}
```

### WebSocket /api/v1/ws

Broadcasts service updates in real-time.

**Message Format:**

```json
{
  "type": "added" | "modified" | "deleted",
  "service": { /* Service object */ }
}
```

---

## Alternatives Considered

### 1. Embed Landing Page in Operator

**Pros:**
- Single deployment
- Shared Kubernetes client

**Cons:**
- Couples UI lifecycle to operator
- Harder to scale independently
- Operator restarts affect landing page availability

**Decision:** Rejected - separation of concerns is more important

### 2. Server-Side Rendering (Go templates + htmx)

**Pros:**
- No JavaScript build step
- Simpler deployment
- Lower client-side complexity

**Cons:**
- Less interactive UI
- Harder to implement complex filtering/search
- Team less familiar with htmx

**Decision:** Rejected - React provides better UX for this use case

### 3. Static Site Generator

**Pros:**
- Simple hosting
- No server needed

**Cons:**
- No real-time updates
- Requires regeneration on changes
- Adds complexity to deployment pipeline

**Decision:** Rejected - real-time updates are a key requirement

### 4. ConfigMap-Based Registration

**Pros:**
- No CRD changes needed
- Works without operator

**Cons:**
- Separate from routing/TLS configuration
- Potential for drift between ConfigMap and NicApp
- Less declarative

**Decision:** Rejected - extending NicApp provides a unified configuration point

---

## Security Considerations

### Authentication & Authorization

- The landing page itself may be public or behind SSO (configurable via NicApp auth field)
- The API server only has read access to NicApp resources
- No write operations are performed

### Network Policies

- Landing page only needs egress to:
  - Kubernetes API server (for watching NicApps)
  - Service health endpoints (for health checks)
- Ingress only from the gateway

### Health Check Security

- Health checks are performed server-side to avoid exposing internal endpoints
- Only the configured health path is accessed
- Timeouts prevent hanging connections

### Container Security

- Non-root user
- Read-only filesystem
- Dropped capabilities
- Restricted pod security standard

---

## Testing Strategy

### Unit Tests

- API handler logic
- Health check scheduling
- Service filtering and sorting
- React component rendering

### Integration Tests

- NicApp watcher correctly updates cache
- WebSocket broadcasts on changes
- API returns correct data format

### End-to-End Tests

```bash
# 1. Deploy landing page to Kind cluster
make deploy-landingpage

# 2. Create test NicApp
kubectl apply -f - <<EOF
apiVersion: apps.nebari.dev/v1alpha1
kind: NicApp
metadata:
  name: test-service
  namespace: default
spec:
  hostname: test.example.com
  landingPage:
    enabled: true
    displayName: "Test Service"
    category: "Testing"
EOF

# 3. Verify service appears in API
curl http://localhost:8080/api/v1/services | jq '.services[] | select(.name=="test-service")'

# 4. Delete NicApp and verify removal
kubectl delete nicapp test-service
curl http://localhost:8080/api/v1/services | jq '.services | length'
```

---

## Rollout Plan

### Phase 1: CRD Extension
1. Add `landingPage` field to NicApp types
2. Run `make manifests` to regenerate CRD
3. Update existing NicApp samples with landing page examples
4. Release new CRD version

### Phase 2: Backend Implementation
1. Implement Go API server
2. Add unit and integration tests
3. Create Dockerfile
4. Add CI workflow

### Phase 3: Frontend Implementation
1. Scaffold React application
2. Implement components
3. Add component tests
4. Integrate with backend

### Phase 4: Deployment & Documentation
1. Create Kubernetes manifests
2. Add to Kustomize overlays
3. Write user documentation
4. Create sample NicApps for common services

### Phase 5: Production Rollout
1. Deploy to staging environment
2. Validate with test services
3. Deploy to production
4. Monitor and iterate

---

## Open Questions

1. **Branding**: Should the landing page support custom branding (logo, colors, title)?

2. **Service Grouping**: Should we support nested categories or tags in addition to single category?

3. **Favorites**: Should users be able to "favorite" services for quick access?

4. **Announcements**: Should the landing page support system-wide announcements/banners?

5. **External Services**: Should we support registering services that don't have NicApp CRs (e.g., external SaaS tools)?

---

## Appendix

### File Structure

```
nic-operator/
├── api/v1alpha1/
│   ├── groupversion_info.go
│   ├── nicapp_types.go              # NicApp CRD with landingPage field
│   └── zz_generated.deepcopy.go
├── cmd/
│   ├── main.go                      # Operator entry point
│   └── landingpage/
│       └── main.go                  # Landing page entry point
├── internal/
│   ├── controller/
│   │   └── nicapp_controller.go
│   └── landingpage/
│       ├── server.go
│       ├── api/
│       │   ├── handlers.go
│       │   └── types.go
│       ├── watcher/
│       │   └── watcher.go
│       ├── health/
│       │   └── checker.go
│       └── websocket/
│           └── hub.go
├── web/
│   ├── package.json
│   ├── vite.config.ts
│   ├── tailwind.config.js
│   ├── src/
│   │   ├── main.tsx
│   │   ├── App.tsx
│   │   ├── api/
│   │   ├── components/
│   │   └── hooks/
│   └── dist/
├── config/
│   ├── crd/bases/
│   │   └── apps.nebari.dev_nicapps.yaml
│   ├── landingpage/
│   │   ├── deployment.yaml
│   │   ├── service.yaml
│   │   ├── serviceaccount.yaml
│   │   ├── rbac.yaml
│   │   └── kustomization.yaml
│   └── samples/
│       └── landingpage-nicapp.yaml
├── Dockerfile
├── Dockerfile.landingpage
└── Makefile
```

### References

- [nic-operator README](../../README.md)
- [Kubernetes Operator Pattern](https://kubernetes.io/docs/concepts/extend-kubernetes/operator/)
- [React Query Documentation](https://tanstack.com/query/latest)
- [Vite Documentation](https://vitejs.dev/)
