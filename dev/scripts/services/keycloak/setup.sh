#!/usr/bin/env bash

# Setup Keycloak realm for nebari-operator development
# This script configures a Keycloak realm with OIDC clients for testing

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export CLUSTER_NAME="${CLUSTER_NAME:-nebari-operator-dev}"
KEYCLOAK_NAMESPACE=${KEYCLOAK_NAMESPACE:-keycloak}
KEYCLOAK_POD=${KEYCLOAK_POD:-keycloak-keycloakx-0}
KEYCLOAK_URL=${KEYCLOAK_URL:-http://localhost:8080/auth}
KEYCLOAK_ADMIN=${KEYCLOAK_ADMIN:-admin}
KEYCLOAK_ADMIN_PASSWORD=${KEYCLOAK_ADMIN_PASSWORD:-admin}
REALM_NAME=${REALM_NAME:-nebari}
REALM_ADMIN_USER=${REALM_ADMIN_USER:-admin}
REALM_ADMIN_PASSWORD=${REALM_ADMIN_PASSWORD:-nebari-admin}

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() { echo -e "${BLUE}ℹ️  $1${NC}"; }
log_success() { echo -e "${GREEN}✅ $1${NC}"; }
log_warning() { echo -e "${YELLOW}⚠️  $1${NC}"; }
log_error() { echo -e "${RED}❌ $1${NC}"; }

# Check if cluster exists
if ! kubectl cluster-info --context "kind-${CLUSTER_NAME}" &>/dev/null; then
    log_error "Cluster '${CLUSTER_NAME}' not found. Run 'make cluster-create' first."
    exit 1
fi

echo ""
echo "🔐 Setting up Keycloak realm: ${REALM_NAME}"
echo "=========================================="
echo ""

# Check if Keycloak pod is running
if ! kubectl get pod $KEYCLOAK_POD -n $KEYCLOAK_NAMESPACE &> /dev/null; then
    log_error "Keycloak pod $KEYCLOAK_POD not found in namespace $KEYCLOAK_NAMESPACE"
    echo ""
    echo "Install Keycloak first:"
    echo "  ${SCRIPT_DIR}/install.sh"
    exit 1
fi

log_info "Using Keycloak pod: $KEYCLOAK_POD"

# Create master realm admin credentials secret for the operator
# The operator needs master realm credentials to provision OIDC clients
log_info "Creating master realm admin credentials secret for operator..."
kubectl create secret generic nebari-realm-admin-credentials \
    --namespace "$KEYCLOAK_NAMESPACE" \
    --from-literal=username="$KEYCLOAK_ADMIN" \
    --from-literal=password="$KEYCLOAK_ADMIN_PASSWORD" \
    --dry-run=client -o yaml | kubectl apply -f -

log_success "Secret created"

# Helper function to run kcadm commands
run_kcadm() {
    kubectl exec -n $KEYCLOAK_NAMESPACE $KEYCLOAK_POD -- /opt/keycloak/bin/kcadm.sh "$@"
}

log_info "Authenticating with Keycloak..."
run_kcadm config credentials \
    --server "$KEYCLOAK_URL" \
    --realm master \
    --user "$KEYCLOAK_ADMIN" \
    --password "$KEYCLOAK_ADMIN_PASSWORD"

log_success "Authenticated"

log_info "Creating $REALM_NAME realm..."
run_kcadm create realms \
    -s realm="$REALM_NAME" \
    -s enabled=true \
    -s displayName="Nebari" \
    -s sslRequired=external \
    -s registrationAllowed=false \
    -s loginWithEmailAllowed=true \
    -s resetPasswordAllowed=true \
    -s bruteForceProtected=true || log_warning "Realm may already exist"

log_success "Realm created"

log_info "Creating realm roles..."
run_kcadm create roles -r "$REALM_NAME" -s name=admin -s description="Administrator role" || true
run_kcadm create roles -r "$REALM_NAME" -s name=user -s description="Regular user role" || true
run_kcadm create roles -r "$REALM_NAME" -s name=developers -s description="Developers group" || true
run_kcadm create roles -r "$REALM_NAME" -s name=data-scientists -s description="Data Scientists group" || true

log_success "Roles created"

log_info "Creating admin user in $REALM_NAME realm..."
run_kcadm create users -r "$REALM_NAME" \
    -s username="$REALM_ADMIN_USER" \
    -s enabled=true \
    -s emailVerified=true \
    -s firstName=Admin \
    -s lastName=User \
    -s email=admin@nebari.local || log_warning "User may already exist"

log_success "User created"

log_info "Setting admin user password..."
run_kcadm set-password -r "$REALM_NAME" \
    --username "$REALM_ADMIN_USER" \
    --new-password "$REALM_ADMIN_PASSWORD"

log_success "Password set"

log_info "Assigning roles to admin user..."
run_kcadm add-roles -r "$REALM_NAME" --uusername "$REALM_ADMIN_USER" --rolename admin || true
run_kcadm add-roles -r "$REALM_NAME" --uusername "$REALM_ADMIN_USER" --rolename user || true

log_success "Roles assigned"

# Keycloak 26+ uses "lightweight access tokens" by default: access tokens carry only
# minimal session claims (jti, iss, exp, azp, sid, scope).  User-profile claims such
# as preferred_username and sub only appear in the id_token and /userinfo endpoint.
# To include those claims in the bearer access token (required by the webapi's JWT
# validator today), we must explicitly create per-client protocol mappers on admin-cli
# in the nebari realm.  The profile scope already has the mappers for the id_token
# path; we add matching ones that explicitly target the access token.
log_info "Configuring admin-cli protocol mappers in $REALM_NAME realm (Keycloak 26 lightweight token workaround)..."

# Find admin-cli client UUID in the nebari realm
ADMIN_CLI_ID=$(kubectl exec -n "$KEYCLOAK_NAMESPACE" "$KEYCLOAK_POD" -- \
    /opt/keycloak/bin/kcadm.sh get clients -r "$REALM_NAME" --fields id,clientId 2>/dev/null \
    | python3 -c "
import sys, json
clients = json.load(sys.stdin)
print(next((c['id'] for c in clients if c.get('clientId') == 'admin-cli'), ''))
" 2>/dev/null) || true

if [ -n "$ADMIN_CLI_ID" ]; then
    # preferred_username — maps Keycloak username into the access token
    run_kcadm create "clients/$ADMIN_CLI_ID/protocol-mappers/models" -r "$REALM_NAME" \
        -s name=at-preferred-username \
        -s protocol=openid-connect \
        -s protocolMapper=oidc-usermodel-attribute-mapper \
        -s 'config.user.attribute=username' \
        -s 'config.claim.name=preferred_username' \
        -s 'config.jsonType.label=String' \
        -s 'config.id.token.claim=false' \
        -s 'config.access.token.claim=true' \
        -s 'config.userinfo.token.claim=false' \
        -s 'config.lightweight.claim=false' 2>/dev/null || log_warning "at-preferred-username mapper may already exist"

    # sub — includes the user UUID as the JWT Subject in the access token
    run_kcadm create "clients/$ADMIN_CLI_ID/protocol-mappers/models" -r "$REALM_NAME" \
        -s name=at-sub \
        -s protocol=openid-connect \
        -s protocolMapper=oidc-sub-mapper \
        -s 'config.access.token.claim=true' \
        -s 'config.lightweight.claim=false' 2>/dev/null || log_warning "at-sub mapper may already exist"

    log_success "Protocol mappers configured on admin-cli (admin-cli UUID: $ADMIN_CLI_ID)"
else
    log_warning "Could not find admin-cli client ID in realm $REALM_NAME — skipping protocol mapper setup"
fi

echo ""
echo "=========================================="
echo "✨ Keycloak realm setup complete!"
echo "=========================================="
echo ""
echo "📋 Realm Information:"
echo "  Realm name: $REALM_NAME"
echo "  Realm admin user: $REALM_ADMIN_USER/$REALM_ADMIN_PASSWORD"
echo ""
echo "🔑 Operator Credentials:"
echo "  Secret: nebari-realm-admin-credentials"
echo "  Namespace: $KEYCLOAK_NAMESPACE"
echo "  Master realm admin: $KEYCLOAK_ADMIN/$KEYCLOAK_ADMIN_PASSWORD"
echo ""
echo "📝 Next steps:"
echo "  1. Deploy operator with Keycloak integration enabled"
echo "  2. Create NebariApp with authentication.enabled=true"
echo "  3. Operator will auto-create OIDC clients for each app"
echo ""
