#!/usr/bin/env bash

# Setup port forwarding from KIND control plane to Envoy Gateway LoadBalancer
# This allows accessing services via localhost:80/443 on macOS with OrbStack

set -euo pipefail

# Color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info() {
    echo -e "${BLUE}ℹ️  $1${NC}"
}

log_success() {
    echo -e "${GREEN}✅ $1${NC}"
}

log_warning() {
    echo -e "${YELLOW}⚠️  $1${NC}"
}

log_error() {
    echo -e "${RED}❌ $1${NC}"
}

CLUSTER_NAME="${CLUSTER_NAME:-nebari-operator-dev}"

log_info "Setting up traffic forwarding for LoadBalancer access on macOS"
echo ""

# Get the LoadBalancer IP
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system \
    -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway \
    -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}' 2>/dev/null)

if [ -z "$GATEWAY_IP" ]; then
    log_error "Could not get gateway LoadBalancer IP"
    exit 1
fi

log_success "Gateway LoadBalancer IP: ${GATEWAY_IP}"

# Install socat in the control plane node if not present
log_info "Checking for socat in KIND control plane..."
if ! docker exec "${CLUSTER_NAME}-control-plane" which socat >/dev/null 2>&1; then
    log_info "Installing socat..."
    docker exec "${CLUSTER_NAME}-control-plane" sh -c "apt-get update -qq && apt-get install -y -qq socat" >/dev/null 2>&1
    log_success "socat installed"
else
    log_success "socat already installed"
fi

# Kill existing socat processes
log_info "Cleaning up existing port forwards..."
docker exec "${CLUSTER_NAME}-control-plane" pkill socat 2>/dev/null || true

# Setup port forwarding in the control plane node
log_info "Setting up port forwarding: 80 -> ${GATEWAY_IP}:80"
docker exec -d "${CLUSTER_NAME}-control-plane" socat TCP-LISTEN:80,fork,reuseaddr TCP:${GATEWAY_IP}:80

log_info "Setting up port forwarding: 443 -> ${GATEWAY_IP}:443"
docker exec -d "${CLUSTER_NAME}-control-plane" socat TCP-LISTEN:443,fork,reuseaddr TCP:${GATEWAY_IP}:443

echo ""
log_success "Port forwarding configured!"
echo ""
log_info "You can now access services via:"
echo "  - HTTP:  http://localhost"
echo "  - HTTPS: https://localhost"
echo ""
log_info "Add hostnames to /etc/hosts for convenience:"
echo "  echo \"127.0.0.1  sample-app-routing.nebari.local\" | sudo tee -a /etc/hosts"
echo ""
log_info "Then access: https://sample-app-routing.nebari.local"
echo ""
log_warning "Note: Port forwarding is lost when the cluster is deleted/restarted"
log_info "Re-run this script after cluster recreation"
