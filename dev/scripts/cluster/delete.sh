#!/usr/bin/env bash

# Delete Kind cluster for nebari-operator development

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
log_error() { echo -e "${RED}❌ $1${NC}"; }

# Check if cluster exists
if ! kind get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"; then
    log_info "Cluster '${CLUSTER_NAME}' does not exist"
    exit 0
fi

log_info "Deleting Kind cluster: ${CLUSTER_NAME}"
kind delete cluster --name "${CLUSTER_NAME}"

if [ $? -eq 0 ]; then
    log_success "Cluster '${CLUSTER_NAME}' deleted successfully"
else
    log_error "Failed to delete cluster"
    exit 1
fi
