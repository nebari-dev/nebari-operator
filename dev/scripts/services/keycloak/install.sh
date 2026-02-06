#!/usr/bin/env bash

# Install Keycloak for nebari-operator development
# This script installs Keycloak for OIDC authentication testing

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
export CLUSTER_NAME="${CLUSTER_NAME:-nebari-operator-dev}"

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

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    log_error "helm is not installed. Please install helm first:"
    echo "  curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash"
    exit 1
fi

echo ""
echo "🔐 Installing Keycloak to cluster: ${CLUSTER_NAME}"
echo "=========================================="
echo ""

# Create keycloak namespace
log_info "Creating keycloak namespace..."
kubectl create namespace keycloak --dry-run=client -o yaml | kubectl apply -f -
log_success "Namespace created"

# Clean up any failed installations
log_info "Cleaning up any previous Keycloak installations..."
kubectl delete statefulset keycloak-keycloakx -n keycloak --ignore-not-found --timeout=60s || true
kubectl delete pod -n keycloak -l app.kubernetes.io/name=keycloakx --ignore-not-found || true
log_success "Cleanup complete"

# Install Keycloak using helm
if ! helm repo list | grep -q codecentric; then
    log_info "Adding codecentric helm repo..."
    helm repo add codecentric https://codecentric.github.io/helm-charts
    helm repo update
fi

log_info "Installing Keycloak via Helm..."

# Create a temporary values file
cat > /tmp/keycloak-values.yaml <<EOF
replicas: 1

resources:
  requests:
    memory: "1Gi"
    cpu: "500m"
  limits:
    memory: "2Gi"
    cpu: "1000m"

command:
  - "/opt/keycloak/bin/kc.sh"
  - "start-dev"

extraEnv: |
  - name: KC_BOOTSTRAP_ADMIN_USERNAME
    value: "admin"
  - name: KC_BOOTSTRAP_ADMIN_PASSWORD
    value: "admin"
  - name: KC_HTTP_RELATIVE_PATH
    value: "/auth"
  - name: JAVA_OPTS_APPEND
    value: "-Xms512m -Xmx1536m"

http:
  relativePath: "/auth"
EOF

helm upgrade --install keycloak codecentric/keycloakx \
    --namespace keycloak \
    --values /tmp/keycloak-values.yaml \
    --wait \
    --timeout=5m || {
        log_error "Keycloak installation failed"
        echo ""
        echo "Check Keycloak logs:"
        echo "  kubectl logs -n keycloak -l app.kubernetes.io/name=keycloakx"
        rm -f /tmp/keycloak-values.yaml
        exit 1
    }

rm -f /tmp/keycloak-values.yaml

# Create Keycloak admin credentials secret
log_info "Creating Keycloak admin credentials secret..."
kubectl create secret generic keycloak-admin-credentials \
    --namespace keycloak \
    --from-literal=admin-username=admin \
    --from-literal=admin-password=admin \
    --dry-run=client -o yaml | kubectl apply -f -

log_success "Secret created"

log_info "Waiting for Keycloak to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=keycloakx -n keycloak --timeout=300s || {
    log_error "Keycloak pod not ready after 5 minutes"
    echo ""
    echo "Check pod status:"
    echo "  kubectl get pods -n keycloak"
    echo "  kubectl describe pod -n keycloak -l app.kubernetes.io/name=keycloakx"
    exit 1
}

log_success "Keycloak is ready"

echo ""
echo "=========================================="
echo "✨ Keycloak installation complete!"
echo "=========================================="
echo ""
echo "📋 Keycloak Information:"
echo "  Admin credentials: admin/admin"
echo "  Internal URL: http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth"
echo "  Secret: keycloak-admin-credentials (namespace: keycloak)"
echo ""
echo "🌐 Access Keycloak:"
echo "  Port-forward: kubectl port-forward -n keycloak svc/keycloak-keycloakx-http 8080:80"
echo "  Then open: http://localhost:8080/auth"
echo ""
echo "📝 Next steps:"
echo "  1. Setup realm: ${SCRIPT_DIR}/setup.sh"
echo "  2. Configure NebariApp with OIDC authentication"
echo ""
