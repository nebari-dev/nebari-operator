#!/usr/bin/env bash

# Delete KIND cluster for nic-operator development

set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-nic-operator-dev}"

echo "🗑️  Deleting KIND cluster: ${CLUSTER_NAME}"

if ! kind get clusters | grep -q "^${CLUSTER_NAME}$"; then
    echo "⚠️  Cluster '${CLUSTER_NAME}' does not exist"
    exit 0
fi

kind delete cluster --name "${CLUSTER_NAME}"

echo "✅ Cluster '${CLUSTER_NAME}' deleted successfully"
