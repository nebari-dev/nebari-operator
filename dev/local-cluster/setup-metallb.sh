#!/usr/bin/env bash

# Install and configure MetalLB for KIND cluster
# This provides LoadBalancer support in KIND

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-nic-operator-dev}"

# Color codes for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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

echo ""
log_info "Installing MetalLB for LoadBalancer support..."
echo ""

# Install MetalLB
log_info "Applying MetalLB manifests..."
kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.14.9/config/manifests/metallb-native.yaml

# Wait for MetalLB to be ready
log_info "Waiting for MetalLB controller..."
kubectl wait --namespace metallb-system \
    --for=condition=ready pod \
    --selector=app=metallb \
    --timeout=90s

log_success "MetalLB controller is ready"

# Get the Docker network subnet for KIND (prefer IPv4)
log_info "Detecting KIND network subnet..."
KIND_NET_CIDR=$(docker network inspect kind | jq -r '.[0].IPAM.Config[] | select(.Subnet | contains(":") | not) | .Subnet' | head -1)

if [ -z "$KIND_NET_CIDR" ]; then
    log_error "Could not detect KIND network IPv4 subnet"
    exit 1
fi

log_info "KIND network subnet: ${KIND_NET_CIDR}"

# Extract the base IP and calculate a range for MetalLB
# For example, if subnet is 172.18.0.0/16, we'll use 172.18.255.200-172.18.255.250
BASE_IP=$(echo ${KIND_NET_CIDR} | sed -E 's|^([0-9]+\.[0-9]+)\..*|\1|')
LB_RANGE="${BASE_IP}.255.200-${BASE_IP}.255.250"

log_info "Configuring MetalLB IP address pool: ${LB_RANGE}"

# Create MetalLB configuration
cat <<EOF | kubectl apply -f -
---
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: kind-pool
  namespace: metallb-system
spec:
  addresses:
  - ${LB_RANGE}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: kind-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - kind-pool
EOF

log_success "MetalLB configured with IP pool: ${LB_RANGE}"
echo ""
log_info "LoadBalancer services will now automatically receive external IPs from this range"
echo ""
