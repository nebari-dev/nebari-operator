# Keycloak Services

Keycloak realm configuration scripts for nebari-operator development.

Keycloak itself is installed as part of `dev/scripts/services/install.sh` (the full
foundational-services installer). This directory contains only the realm setup script.

## Scripts

### setup.sh

Configures a Keycloak realm for OIDC authentication testing.

**Usage:**
```bash
./setup.sh
```

**What it does:**
- Creates a `nebari` realm
- Configures realm roles (admin, user, developers, data-scientists)
- Creates an admin user for the realm
- Creates operator credentials secret for automatic OIDC client provisioning

**Environment Variables:**
- `CLUSTER_NAME` - Kind cluster name (default: `nebari-operator-dev`)
- `KEYCLOAK_NAMESPACE` - Keycloak namespace (default: `keycloak`)
- `KEYCLOAK_POD` - Keycloak pod name (default: `keycloak-keycloakx-0`)
- `KEYCLOAK_URL` - Keycloak URL (default: `http://localhost:8080/auth`)
- `KEYCLOAK_ADMIN` - Master realm admin username (default: `admin`)
- `KEYCLOAK_ADMIN_PASSWORD` - Master realm admin password (default: `admin`)
- `REALM_NAME` - Realm to create (default: `nebari`)
- `REALM_ADMIN_USER` - Realm admin username (default: `admin`)
- `REALM_ADMIN_PASSWORD` - Realm admin password (default: `nebari-admin`)

## Quick Start

1. **Install foundational services (including Keycloak):**
   ```bash
   cd dev && make services-install
   ```

2. **Configure the Keycloak realm:**
   ```bash
   ./setup.sh
   ```

3. **Access Keycloak admin console:**
   ```bash
   kubectl port-forward -n keycloak svc/keycloak-keycloakx-http 8080:80
   ```
   Then open: http://localhost:8080/auth

4. **Login credentials:**
   - Master realm: admin/admin
   - Nebari realm: admin/nebari-admin

## Integration with NebariApp

Once Keycloak is installed and configured, you can enable OIDC authentication in your NebariApp:

```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: my-app
  namespace: default
spec:
  hostname: myapp.nebari.local
  service:
    name: my-service
    port: 8080
  authentication:
    enabled: true
    oidc:
      realm: nebari
      keycloakUrl: http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth
      credentialsSecretRef:
        name: nebari-realm-admin-credentials
        namespace: keycloak
```

The operator will automatically create OIDC clients in Keycloak for each NebariApp with authentication enabled.

## Troubleshooting

**Keycloak pod not starting:**
```bash
kubectl get pods -n keycloak
kubectl describe pod -n keycloak -l app.kubernetes.io/name=keycloakx
kubectl logs -n keycloak -l app.kubernetes.io/name=keycloakx
```

**Realm setup fails:**
```bash
# Check if pod is ready
kubectl get pod keycloak-keycloakx-0 -n keycloak

# Check kcadm.sh is available
kubectl exec -n keycloak keycloak-keycloakx-0 -- ls -la /opt/keycloak/bin/kcadm.sh
```

**Clean up Keycloak:**
```bash
helm uninstall keycloak -n keycloak
kubectl delete namespace keycloak --ignore-not-found
```
