#!/usr/bin/env bash

# Uninstall foundational services from nic-operator development cluster

set -euo pipefail

export CLUSTER_NAME="${CLUSTER_NAME:-nic-operator-dev}"

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
    log_warning "Cluster '${CLUSTER_NAME}' not found, nothing to uninstall"
    exit 0
fi

echo ""
echo "🧹 Uninstalling foundational services from cluster: ${CLUSTER_NAME}"
echo "=========================================="
echo ""

# Delete Gateway first to cleanup envoy proxy
log_info "Deleting Gateway resources..."
kubectl delete gateway nebari-gateway -n envoy-gateway-system --ignore-not-found --timeout=60s
kubectl delete certificate nebari-gateway-cert -n envoy-gateway-system --ignore-not-found

log_success "Gateway resources deleted"

# Uninstall Envoy Gateway
log_info "Uninstalling Envoy Gateway..."
helm uninstall eg -n envoy-gateway-system 2>/dev/null || true
kubectl delete namespace envoy-gateway-system --ignore-not-found --timeout=60s

log_success "Envoy Gateway uninstalled"

# Uninstall cert-manager
log_info "Uninstalling cert-manager..."
helm uninstall cert-manager -n cert-manager 2>/dev/null || true
kubectl delete namespace cert-manager --ignore-not-found --timeout=60s

log_success "cert-manager uninstalled"

# Uninstall MetalLB
log_info "Uninstalling MetalLB..."
kubectl delete -f https://raw.githubusercontent.com/metallb/metallb/v0.14.8/config/manifests/metallb-native.yaml --ignore-not-found 2>/dev/null || true

log_success "MetalLB uninstalled"

echo ""
log_success "All services uninstalled"
echo ""
