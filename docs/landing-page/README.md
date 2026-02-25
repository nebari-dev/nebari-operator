# Nebari Landing Page

A service discovery portal for Nebari that provides centralized access to all deployed applications.

## Overview

The Landing Page is a standalone application that automatically discovers and displays NebariApp resources configured
with landing page visibility. It provides:

- **Service Discovery**: Automatically displays all services configured in NebariApps
- **Role-Based Access**: Shows different services based on user authentication and group membership
- **Health Monitoring**: Displays real-time health status of services (when configured)
- **Modern UI**: Responsive React-based frontend with clean, card-based design
- **OIDC Authentication**: Integrates with Keycloak for user authentication using PKCE flow

## Architecture

### Backend (Go)
- **Service Cache**: In-memory cache of NebariApp resources with landing page enabled
- **Kubernetes Watcher**: Watches NebariApp resources via informer pattern
- **JWT Validator**: Validates Keycloak JWT tokens and extracts user groups
- **REST API**: Provides `/api/v1/services` endpoint with visibility filtering
- **Health Checker**: Periodically checks service health (placeholder implementation)

### Frontend (HTML/JS)
- **Public Landing Page**: No authentication required to view public services
- **Client-Side OIDC**: Uses PKCE flow for secure authentication without client secret
- **Group-Based Filtering**: Shows private services only to users in required groups
- **Responsive Design**: Mobile-friendly card-based layout

### Data Flow

```
NebariApp CRD → Kubernetes Informer → Service Cache → REST API → Frontend
                                                          ↓
                                               JWT Validation (optional)
```

## Deployment

### Prerequisites
- Kubernetes cluster with nebari-operator installed
- Keycloak instance (for authentication)
- cert-manager (for TLS certificates)

### Install

```bash
# Build the landing page image
docker build -f Dockerfile.landingpage -t ghcr.io/nebari-dev/nebari-landing-page:latest .

# Push to registry (or load into Kind)
docker push ghcr.io/nebari-dev/nebari-landing-page:latest
# OR for Kind:
kind load docker-image ghcr.io/nebari-dev/nebari-landing-page:latest --name nebari-operator-dev

# Deploy using kustomize
kubectl apply -k config/landingpage/

# Verify deployment
kubectl get pods -n nebari-system -l app=landing-page
kubectl get svc -n nebari-system landing-page
kubectl get nebariapp -n nebari-system landing-page
```

### Configuration

Edit [config/landingpage/deployment.yaml](../../config/landingpage/deployment.yaml) to configure:

```yaml
env:
  - name: KEYCLOAK_URL
    value: "https://keycloak.nebari.example.com"  # Your Keycloak URL
  - name: KEYCLOAK_REALM
    value: "nebari"                                # Your realm name
  - name: PORT
    value: "8080"
  - name: ENABLE_AUTH
    value: "true"                                  # Enable JWT validation
  - name: LOG_LEVEL
    value: "info"
```

### Keycloak Client Setup

Create a Keycloak client for the landing page frontend:

1. Go to Keycloak Admin Console → Clients → Create Client
2. **Client ID**: `landing-page`
3. **Client authentication**: Disabled (public client)
4. **Valid redirect URIs**: `https://landing.nebari.example.com/*`
5. **Web origins**: `https://landing.nebari.example.com`
6. **Advanced Settings**:
   - Proof Key for Code Exchange (PKCE): `S256` required
7. **Client scopes**: Add `groups` scope to access user groups in JWT

## Configuring Services

To make a NebariApp appear on the landing page, add the `landingPage` configuration:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: jupyter
  namespace: nebari
spec:
  hostname: jupyter.nebari.example.com
  service:
    name: jupyterhub
    port: 8000

  # Landing page configuration
  landingPage:
    enabled: true
    displayName: "JupyterLab"
    description: "Interactive Python notebooks for data science"
    icon: "jupyter"                    # Built-in icon or URL
    category: "Data Science"
    priority: 10                       # Lower = higher priority
    visibility: "authenticated"        # public|authenticated|private
    requiredGroups:                    # Only for visibility: private
      - "data-scientists"
      - "admin"
    healthCheck:
      enabled: true
      path: "/hub/health"
      intervalSeconds: 30
      timeoutSeconds: 5
```

### Visibility Levels

| Visibility | Description | Who Can See |
|------------|-------------|-------------|
| `public` | Visible to everyone | Unauthenticated AND authenticated users |
| `authenticated` | Requires sign-in | Any authenticated user |
| `private` | Requires specific groups | Users in `requiredGroups` only |

### Built-in Icons

Supported icon identifiers:
- `jupyter` 📓
- `grafana` 📊
- `prometheus` 📈
- `keycloak` 🔐
- `argocd` 🔄
- `kubernetes` ☸️
- `dashboard` 🏠
- `database` 💾
- `api` 🔌

Or use a custom icon URL: `icon: "https://example.com/my-icon.png"`

## API Reference

### GET /api/v1/services

Returns all services filtered by user's authentication status and group membership.

**Headers:**
- `Authorization: Bearer <JWT>` (optional, for authenticated access)

**Response:**
```json
{
  "services": {
    "public": [
      {
        "uid": "abc123",
        "name": "docs",
        "namespace": "nebari",
        "displayName": "Documentation",
        "description": "Nebari documentation site",
        "url": "https://docs.nebari.example.com",
        "icon": "📚",
        "category": "Platform",
        "priority": 5,
        "visibility": "public",
        "health": {
          "status": "healthy",
          "lastCheck": "2026-02-24T17:00:00Z"
        }
      }
    ],
    "authenticated": [ /* services for authenticated users */ ],
    "private": [ /* services user has group access to */ ]
  },
  "categories": ["Data Science", "Platform", "Monitoring"],
  "user": {
    "authenticated": true,
    "username": "john.doe",
    "name": "John Doe",
    "groups": ["users", "data-scientists"]
  }
}
```

### GET /api/v1/health

Health check endpoint for the landing page service.

**Response:**
```json
{
  "status": "healthy",
  "timestamp": "2026-02-24T17:00:00Z",
  "cachedServices": 15
}
```

## Testing

### Unit Tests

```bash
# Run all landing page unit tests
go test ./internal/landingpage/... -v

# Run specific package tests
go test ./internal/landingpage/cache -v
go test ./internal/landingpage/auth -v
go test ./internal/landingpage/api -v
```

Note: Current unit tests are structural placeholders and may require refinement to match exact implementation details.

### E2E Tests

```bash
# Run E2E tests (requires cluster setup)
make test-e2e

# Run only landing page E2E tests
go test -v -tags=e2e ./test/e2e -ginkgo.focus="Landing Page"
```

### Manual Testing

1. **Deploy landing page**:
   ```bash
   kubectl apply -k config/landingpage/
   ```

2. **Port-forward to access locally**:
   ```bash
   kubectl port-forward -n nebari-system svc/landing-page 8080:80
   ```

3. **Open browser**:
   ```
   http://localhost:8080
   ```

4. **Create test services**:
   ```bash
   kubectl apply -f - <<EOF
   apiVersion: reconcilers.nebari.dev/v1
   kind: NebariApp
   metadata:
     name: test-public
     namespace: default
   spec:
     hostname: test.example.com
     service:
       name: test
       port: 80
     landingPage:
       enabled: true
       displayName: "Test Public Service"
       visibility: "public"
       category: "Testing"
   EOF
   ```

5. **Verify service appears**:
   - Refresh landing page
   - Should see "Test Public Service" in Public Services section

## Troubleshooting

### Services Not Appearing
1. Check if landing page pod is running:
   ```bash
   kubectl get pods -n nebari-system -l app=landing-page
   kubectl logs -n nebari-system -l app=landing-page
   ```

2. Verify NebariApp has `landingPage.enabled: true`
   ```bash
   kubectl get nebariapp -A -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.landingPage.enabled}{"\n"}{end}'
   ```

3. Check watcher logs for errors:
   ```bash
   kubectl logs -n nebari-system -l app=landing-page | grep -i watch
   ```

### Authentication Not Working
1. Verify Keycloak configuration:
   - Check `KEYCLOAK_URL` and `KEYCLOAK_REALM` in deployment
   - Ensure Keycloak client exists with correct redirect URIs
   - Verify PKCE is enabled on client

2. Check browser console for errors during login

3. Verify JWT structure includes `groups` claim:
   ```bash
   # Decode JWT from browser localStorage
   echo "<token>" | cut -d. -f2 | base64 -d | jq .
   ```

### Permission Issues
Landing page needs permissions to read NebariApps across all namespaces:

```bash
kubectl get clusterrole landing-page-reader -o yaml
kubectl get clusterrolebinding landing-page-reader -o yaml
```

## Development

### Local Development

```bash
# Run backend locally (requires kubeconfig)
go run ./cmd/landingpage \
  --kubeconfig ~/.kube/config \
  --keycloak-url http://localhost:8180 \
  --keycloak-realm nebari \
  --enable-auth=false \
  --port 8080

# Frontend development (served by backend)
# Edit files in web/static/
# Refresh browser to see changes
```

### Building

```bash
# Build binary
go build -o bin/landingpage ./cmd/landingpage

# Build Docker image
docker build -f Dockerfile.landingpage -t landing-page:dev .

# Run container locally
docker run -p 8080:8080 \
  -v ~/.kube/config:/kubeconfig \
  -e KUBECONFIG=/kubeconfig \
  landing-page:dev
```

## Future Enhancements

- [ ] Actual health check implementation (HTTP probes to services)
- [ ] Service categories auto-detection from annotations
- [ ] Custom themes/branding
- [ ] Service favoriting/bookmarks (user preferences)
- [ ] Search and filtering in frontend
- [ ] Service usage analytics
- [ ] Admin dashboard for managing landing page configuration
- [ ] Multi-tenancy support (namespace-based filtering)
- [ ] Webhook notifications for service status changes
- [ ] Integration with monitoring systems (Prometheus metrics)

## Contributing

See [CONTRIBUTING.md](../../CONTRIBUTING.md) for development guidelines.

## License

Apache License 2.0 - see [LICENSE](../../LICENSE)
