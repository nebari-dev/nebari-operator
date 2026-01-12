#!/usr/bin/env bash

# Setup routing to access Docker networks from macOS host
# This makes MetalLB LoadBalancer IPs accessible without docker-mac-net-connect

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

# Check if running on macOS
if [[ "$OSTYPE" != "darwin"* ]]; then
    log_error "This script is only for macOS"
    exit 1
fi

# Get the Docker VM IP address (the host.docker.internal address)
log_info "Detecting Docker VM gateway..."
DOCKER_VM_IP=$(docker run --rm --privileged --pid=host alpine nsenter -t 1 -m -u -n -i ip route show | grep default | awk '{print $3}' | head -1)

if [ -z "$DOCKER_VM_IP" ]; then
    log_warning "Could not detect via nsenter, trying alternative method..."
    # Alternative: Get from docker network inspect
    DOCKER_VM_IP=$(docker network inspect bridge -f '{{range .IPAM.Config}}{{.Gateway}}{{end}}' 2>/dev/null)
fi

if [ -z "$DOCKER_VM_IP" ]; then
    log_error "Could not detect Docker VM IP"
    exit 1
fi

log_success "Docker VM gateway: ${DOCKER_VM_IP}"

# Get Docker network subnets
log_info "Detecting Docker network subnets..."
DOCKER_NETWORKS=$(docker network inspect $(docker network ls -q) 2>/dev/null | \
    jq -r '.[] | select(.IPAM.Config != null) | .IPAM.Config[] | select(.Subnet | contains(":") | not) | .Subnet' | \
    sort -u)

if [ -z "$DOCKER_NETWORKS" ]; then
    log_error "Could not detect any Docker networks"
    exit 1
fi

echo ""
log_info "Found Docker networks:"
echo "$DOCKER_NETWORKS" | while read subnet; do
    echo "  - $subnet"
done
echo ""

# Add routes to Docker networks via Docker VM
log_info "Adding routes to Docker networks..."

while read subnet; do
    # Check if route already exists
    if route -n get -net "$subnet" >/dev/null 2>&1; then
        EXISTING_GW=$(route -n get -net "$subnet" 2>/dev/null | grep gateway | awk '{print $2}')
        if [ "$EXISTING_GW" = "$DOCKER_VM_IP" ]; then
            log_info "Route to $subnet already exists (via $EXISTING_GW)"
            continue
        else
            log_warning "Route to $subnet exists via different gateway ($EXISTING_GW), updating..."
            sudo route -q -n delete -net "$subnet" >/dev/null 2>&1 || true
        fi
    fi

    log_info "Adding route: $subnet via $DOCKER_VM_IP"
    if sudo route -q -n add -net "$subnet" "$DOCKER_VM_IP" >/dev/null 2>&1; then
        log_success "Route added: $subnet -> $DOCKER_VM_IP"
    else
        log_error "Failed to add route for $subnet"
    fi
done <<< "$DOCKER_NETWORKS"

echo ""
log_success "Docker network routing configured!"
echo ""
log_info "You can now access LoadBalancer IPs directly:"

# Get MetalLB pool if it exists
METALLB_POOL=$(kubectl get ipaddresspool -n metallb-system -o jsonpath='{.items[0].spec.addresses[0]}' 2>/dev/null || echo "")
if [ -n "$METALLB_POOL" ]; then
    echo "  MetalLB IP pool: $METALLB_POOL"
fi

# Get gateway LoadBalancer IP
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system \
    -l gateway.envoyproxy.io/owning-gateway-name=nic-public-gateway \
    -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")

if [ -n "$GATEWAY_IP" ]; then
    echo "  Gateway IP: $GATEWAY_IP"
    echo ""
    log_info "Test with: ping $GATEWAY_IP"
fi

echo ""
log_warning "Note: These routes are temporary and will be lost on reboot"
log_info "Re-run this script after restarting Docker or your Mac"
