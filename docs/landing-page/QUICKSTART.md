# Landing Page Quick Start Guide

This guide will help you quickly deploy and test the Nebari Landing Page.

## Prerequisites

- Kubernetes cluster (Kind, Minikube, or real cluster)
- Keycloak instance running (or use existing)
- `kubectl` configured
- Docker or Podman

## Step 1: Build the Landing Page

```bash
# Build the Docker image
make docker-build-landingpage

# For Kind clusters, load the image
kind load docker-image ghcr.io/nebari-dev/nebari-landing-page:latest --name <your-cluster-name>
```

## Step 2: Configure Keycloak

### Create a Client

1. Go to Keycloak Admin Console
2. Select your realm (e.g., `nebari`)
3. Navigate to **Clients** → **Create client**

**Settings:**
```
Client ID: landing-page
Client type: OpenID Connect
Client authentication: OFF (public client)
```

**Capability config**:
```
Standard flow: ON
Direct access grants: OFF
```

**Valid redirect URIs:**
```
https://landing.nebari.example.com/*
http://localhost:8080/*  (for local testing)
```

**Web origins:**
```
https://landing.nebari.example.com
http://localhost:8080
```

**Advanced Settings:**
```
Proof Key for Code Exchange Code Challenge Method: S256
```

### Add Groups to Token

1. Go to **Client scopes** → **landing-page-dedicated**
2. Add mapper:
   - **Name**: groups
   - **Mapper type**: User Client Role
   - **Multivalued**: ON
   - **Token Claim Name**: groups
   - **Add to ID token**: ON
   - **Add to access token**: ON

## Step 3: Deploy to Kubernetes

### Update Configuration

Edit `config/landingpage/deployment.yaml` and set your Keycloak URL:

```yaml
env:
  - name: KEYCLOAK_URL
    value: "https://keycloak.nebari.example.com"  # ← Your Keycloak URL
  - name: KEYCLOAK_REALM
    value: "nebari"                                # ← Your realm name
```

### Deploy

```bash
# Apply all landing page resources
make deploy-landingpage

# Or manually:
kubectl apply -k config/landingpage/

# Verify deployment
kubectl get pods -n nebari-system -l app=landing-page
kubectl get svc -n nebari-system landing-page
```

## Step 4: Create Test Services

Create some NebariApp resources with landing page configuration:

```bash
# Public service (no auth required)
kubectl apply -f - <<EOF
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: docs
  namespace: default
spec:
  hostname: docs.nebari.test
  service:
    name: docs-service
    port: 80
  landingPage:
    enabled: true
    displayName: "Documentation"
    description: "Nebari platform documentation"
    icon: "📚"
    category: "Platform"
    priority: 5
    visibility: "public"
EOF

# Authenticated service (requires sign-in)
kubectl apply -f - <<EOF
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: jupyter
  namespace: default
spec:
  hostname: jupyter.nebari.test
  service:
    name: jupyterhub
    port: 8000
  landingPage:
    enabled: true
    displayName: "JupyterLab"
    description: "Interactive Python notebooks"
    icon: "jupyter"
    category: "Data Science"
    priority: 10
    visibility: "authenticated"
EOF

# Private service (requires specific groups)
kubectl apply -f - <<EOF
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: admin-panel
  namespace: default
spec:
  hostname: admin.nebari.test
  service:
    name: admin-service
    port: 80
  landingPage:
    enabled: true
    displayName: "Admin Panel"
    description: "Platform administration"
    icon: "⚙️"
    category: "Platform"
    priority: 99
    visibility: "private"
    requiredGroups:
      - "admin"
EOF
```

## Step 5: Access the Landing Page

### Option A: Port Forward (for testing)

```bash
# Forward landing page service to localhost
kubectl port-forward -n nebari-system svc/landing-page 8080:80

# Open in browser
open http://localhost:8080
```

### Option B: Via Ingress/HTTPRoute

The landing page is deployed as a NebariApp itself, so it will automatically get an HTTPRoute:

```bash
# Check the NebariApp
kubectl get nebariapp -n nebari-system landing-page

# Get the hostname
kubectl get nebariapp -n nebari-system landing-page -o jsonpath='{.spec.hostname}'

# Access via browser (assuming DNS is configured)
open https://landing.nebari.example.com
```

## Step 6: Test Authentication Flow

1. **View Public Services**
   - Open landing page
   - Should see "Documentation" service without signing in

2. **Sign In**
   - Click "Sign In" button
   - Redirects to Keycloak
   - Enter credentials
   - Redirected back to landing page

3. **View Authenticated Services**
   - Should now see "JupyterLab" service
   - User info displayed in header

4. **Test Group-Based Access**
   - Only users in "admin" group see "Admin Panel"
   - Test with different users/groups in Keycloak

## Troubleshooting

### Pods Not Starting

```bash
# Check pod logs
kubectl logs -n nebari-system -l app=landing-page

# Check pod status
kubectl describe pod -n nebari-system -l app=landing-page
```

### Services Not Appearing

```bash
# Check if NebariApps are being watched
kubectl logs -n nebari-system -l app=landing-page | grep -i watch

# Verify NebariApps have landingPage enabled
kubectl get nebariapp -A -o jsonpath='{range .items[?(@.spec.landingPage.enabled)]}{.metadata.name}{"\t"}{.spec.landingPage.displayName}{"\n"}{end}'
```

### Authentication Failing

```bash
# Check Keycloak configuration in deployment
kubectl get deploy -n nebari-system landing-page -o yaml | grep -A5 env:

# Check browser console for errors
# Check Keycloak logs
```

### API Errors

```bash
# Test API directly
kubectl port-forward -n nebari-system svc/landing-page 8080:80

# In another terminal:
curl http://localhost:8080/api/v1/services
curl http://localhost:8080/api/v1/health
```

## Next Steps

- **Customize Frontend**: Edit files in `web/static/` and rebuild
- **Add Health Checks**: Enable health checks in NebariApp configs
- **Configure Categories**: Organize services with consistent category names
- **Set Up TLS**: Configure cert-manager for HTTPS
- **Configure DNS**: Point `landing.nebari.example.com` to your cluster

## Clean Up

```bash
# Remove landing page
make undeploy-landingpage

# Or manually:
kubectl delete -k config/landingpage/

# Remove test services
kubectl delete nebariapp docs jupyter admin-panel -n default
```

## Additional Resources

- [Full Documentation](README.md)
- [API Reference](README.md#api-reference)
- [Configuration Guide](README.md#configuring-services)
- [Design Document](../design/landing-page.md)
