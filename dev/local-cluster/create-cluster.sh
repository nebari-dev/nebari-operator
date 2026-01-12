#!/usr/bin/env bash

# Create KIND cluster for nic-operator development
# This script creates a multi-node cluster with ingress-ready configuration

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLUSTER_NAME="${CLUSTER_NAME:-nic-operator-dev}"

echo "🚀 Creating KIND cluster: ${CLUSTER_NAME}"

if kind get clusters | grep -q "^${CLUSTER_NAME}$"; then
    echo "⚠️  Cluster '${CLUSTER_NAME}' already exists"
    read -p "Do you want to delete and recreate it? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        echo "🗑️  Deleting existing cluster..."
        kind delete cluster --name "${CLUSTER_NAME}"
    else
        echo "✅ Using existing cluster"
        exit 0
    fi
fi

echo "📝 Creating cluster with configuration from ${SCRIPT_DIR}/kind-config.yaml"
kind create cluster --config="${SCRIPT_DIR}/kind-config.yaml"

echo "⏳ Waiting for cluster to be ready..."
kubectl wait --for=condition=Ready nodes --all --timeout=300s

echo "✅ KIND cluster '${CLUSTER_NAME}' is ready!"
echo ""

# Install MetalLB for LoadBalancer support
echo "🔧 Setting up MetalLB for LoadBalancer support..."
"${SCRIPT_DIR}/setup-metallb.sh"

echo "📋 Cluster info:"
kubectl cluster-info --context "kind-${CLUSTER_NAME}"
echo ""
echo "🔍 Nodes:"
kubectl get nodes
echo ""
echo "Next steps:"
echo "  1. Install services: ./nic-dev services:install"
echo "  2. Setup gateway access (macOS): ./nic-dev gateway:setup"
echo "  3. Or run complete setup: make playground-setup"
