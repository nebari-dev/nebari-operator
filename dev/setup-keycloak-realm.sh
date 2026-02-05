#!/bin/bash
set -e

KEYCLOAK_NAMESPACE=${KEYCLOAK_NAMESPACE:-keycloak}
KEYCLOAK_POD=${KEYCLOAK_POD:-keycloak-keycloakx-0}
KEYCLOAK_URL=${KEYCLOAK_URL:-http://localhost:8080/auth}
KEYCLOAK_ADMIN=${KEYCLOAK_ADMIN:-admin}
KEYCLOAK_ADMIN_PASSWORD=${KEYCLOAK_ADMIN_PASSWORD:-admin}
REALM_NAME=${REALM_NAME:-nebari}
REALM_ADMIN_USER=${REALM_ADMIN_USER:-admin}
REALM_ADMIN_PASSWORD=${REALM_ADMIN_PASSWORD:-nebari-admin}

echo "Setting up Keycloak realm: $REALM_NAME"

# Check if Keycloak pod is running
if ! kubectl get pod $KEYCLOAK_POD -n $KEYCLOAK_NAMESPACE &> /dev/null; then
    echo "ERROR: Keycloak pod $KEYCLOAK_POD not found in namespace $KEYCLOAK_NAMESPACE"
    exit 1
fi

echo "Using Keycloak pod: $KEYCLOAK_POD"

# Create realm admin credentials secret
echo "Creating realm admin credentials secret..."
kubectl create secret generic nebari-realm-admin-credentials \
    --namespace "$KEYCLOAK_NAMESPACE" \
    --from-literal=username="$REALM_ADMIN_USER" \
    --from-literal=password="$REALM_ADMIN_PASSWORD" \
    --dry-run=client -o yaml | kubectl apply -f -

# Create realm setup job
echo "Configuring Keycloak realm directly..."

# Helper function to run kcadm commands
run_kcadm() {
    kubectl exec -n $KEYCLOAK_NAMESPACE $KEYCLOAK_POD -- /opt/keycloak/bin/kcadm.sh "$@"
}

echo "Authenticating with Keycloak..."
run_kcadm config credentials \
    --server "$KEYCLOAK_URL" \
    --realm master \
    --user "$KEYCLOAK_ADMIN" \
    --password "$KEYCLOAK_ADMIN_PASSWORD"

echo "Creating $REALM_NAME realm..."
run_kcadm create realms \
    -s realm="$REALM_NAME" \
    -s enabled=true \
    -s displayName="Nebari" \
    -s sslRequired=external \
    -s registrationAllowed=false \
    -s loginWithEmailAllowed=true \
    -s resetPasswordAllowed=true \
    -s bruteForceProtected=true || echo "Realm may already exist"

echo "Creating realm roles..."
run_kcadm create roles -r "$REALM_NAME" -s name=admin -s description="Administrator role" || true
run_kcadm create roles -r "$REALM_NAME" -s name=user -s description="Regular user role" || true
run_kcadm create roles -r "$REALM_NAME" -s name=developers -s description="Developers group" || true
run_kcadm create roles -r "$REALM_NAME" -s name=data-scientists -s description="Data Scientists group" || true

echo "Creating admin user in $REALM_NAME realm..."
run_kcadm create users -r "$REALM_NAME" \
    -s username="$REALM_ADMIN_USER" \
    -s enabled=true \
    -s emailVerified=true \
    -s firstName=Admin \
    -s lastName=User \
    -s email=admin@nebari.local || echo "User may already exist"

echo "Setting admin user password..."
run_kcadm set-password -r "$REALM_NAME" \
    --username "$REALM_ADMIN_USER" \
    --new-password "$REALM_ADMIN_PASSWORD"

echo "Assigning roles to admin user..."
run_kcadm add-roles -r "$REALM_NAME" --uusername "$REALM_ADMIN_USER" --rolename admin || true
run_kcadm add-roles -r "$REALM_NAME" --uusername "$REALM_ADMIN_USER" --rolename user || true

echo "✓ Keycloak realm '$REALM_NAME' setup completed successfully!"
echo "  Realm admin credentials: $REALM_ADMIN_USER/$REALM_ADMIN_PASSWORD"
echo "  Secret: nebari-realm-admin-credentials in namespace $KEYCLOAK_NAMESPACE"
