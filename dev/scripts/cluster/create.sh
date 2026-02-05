#!/usr/bin/env bash

# Create Kind cluster for nic-operator development
# This script creates a Kind cluster with:
# - Multiple worker nodes
# - Port mappings for ingress traffic
# - MetalLB for LoadBalancer services

set -euo pipefail

export CLUSTER_NAME="${CLUSTER_NAME:-nic-operator-dev}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

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

# Check if cluster already exists
if kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    log_warning "Cluster '${CLUSTER_NAME}' already exists"
    read -p "$(echo -e ${YELLOW}Do you want to delete and recreate it? \(y/N\): ${NC})" -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        log_info "Deleting existing cluster..."
        kind delete cluster --name "${CLUSTER_NAME}"
    else
        log_info "Using existing cluster"
        exit 0
    fi
fi

log_info "Creating Kind cluster: ${CLUSTER_NAME}"

# Create Kind cluster with custom configuration
# Note: On macOS with OrbStack, we don't use extraPortMappings
# OrbStack automatically routes LoadBalancer IPs to your host
cat <<EOF | kind create cluster --name "${CLUSTER_NAME}" --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
EOF

if [ $? -eq 0 ]; then
    log_success "Cluster '${CLUSTER_NAME}' created successfully"
else
    log_error "Failed to create cluster"
    exit 1
fi

# Install MetalLB for LoadBalancer support
log_info "Installing MetalLB for LoadBalancer services..."

kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.14.8/config/manifests/metallb-native.yaml 2>&1 | grep -v "unrecognized format"

log_info "Waiting for MetalLB to be ready..."
kubectl wait --namespace metallb-system \
    --for=condition=available deployment/controller \
    --timeout=90s

kubectl wait --namespace metallb-system \
    --for=jsonpath='{.status.numberReady}'=1 \
    daemonset/speaker \
    --timeout=90s

# Get the kind network subnet - ensure we get IPv4
log_info "Configuring MetalLB IP address pool..."

# Get all IPAM configs and filter for IPv4 (contains dots, not colons)
KIND_NET_CIDR=$(docker network inspect kind -f '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}' 2>/dev/null | grep '\.' | head -n1)

# Fallback to default if not found
if [ -z "$KIND_NET_CIDR" ]; then
    KIND_NET_CIDR="172.18.0.0/16"
    log_warning "Could not detect Kind network, using default: ${KIND_NET_CIDR}"
fi

# Extract the base IP (e.g., 172.18 from 172.18.0.0/16)
BASE_IP=$(echo ${KIND_NET_CIDR} | awk -F. '{print $1"."$2}')

log_info "Using Kind network: ${KIND_NET_CIDR}"
log_info "MetalLB IP pool will be: ${BASE_IP}.255.200-${BASE_IP}.255.250"

# Configure MetalLB IP address pool
cat <<EOF | kubectl apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: kind-pool
  namespace: metallb-system
spec:
  addresses:
  - ${BASE_IP}.255.200-${BASE_IP}.255.250
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

log_success "MetalLB installed and configured"

echo ""
log_success "Cluster setup complete!"
echo ""
echo "📋 Cluster Information:"
echo "  Name: ${CLUSTER_NAME}"
echo "  Context: kind-${CLUSTER_NAME}"
echo "  LoadBalancer IP Pool: ${BASE_IP}.255.200-${BASE_IP}.255.250"
echo ""
echo "Next steps:"
echo "  1. Install services: make services-install"
echo "  2. Or use full setup: make setup"
