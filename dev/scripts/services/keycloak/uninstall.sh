#!/usr/bin/env bash

# Uninstall Keycloak from nebari-operator development cluster

set -euo pipefail

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

# Check if cluster exists
if ! kubectl cluster-info --context "kind-${CLUSTER_NAME}" &>/dev/null; then
    log_warning "Cluster '${CLUSTER_NAME}' not found, nothing to uninstall"
    exit 0
fi

echo ""
echo "🧹 Uninstalling Keycloak from cluster: ${CLUSTER_NAME}"
echo "=========================================="
echo ""

log_info "Uninstalling Keycloak helm release..."
helm uninstall keycloak -n keycloak 2>/dev/null || log_warning "Keycloak helm release not found"

log_info "Deleting keycloak namespace..."
kubectl delete namespace keycloak --ignore-not-found --timeout=60s

log_success "Keycloak uninstalled"

echo ""
echo "✨ Keycloak uninstallation complete!"
echo ""
