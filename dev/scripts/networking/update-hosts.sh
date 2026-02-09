#!/usr/bin/env bash

# Update /etc/hosts with nebari.local hostnames
# Usage: ./update-hosts.sh [app-name]
#   - With app-name: adds specific app hostname
#   - Without app-name: scans all NebariApp resources and adds them

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

# Get Gateway IP
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")

if [ -z "${GATEWAY_IP}" ] || [ "${GATEWAY_IP}" == "pending" ]; then
    log_error "Gateway IP not available. Make sure services are installed: make services-install"
    exit 1
fi

# Check if we need sudo
if [ -w "/etc/hosts" ]; then
    SUDO_CMD=""
else
    SUDO_CMD="sudo"
fi

# Function to add hostname to /etc/hosts
add_hostname() {
    local hostname=$1

    # Check if hostname already exists in /etc/hosts
    if grep -q "^[0-9.]\+[[:space:]]\+${hostname}[[:space:]]*# nebari-gateway" /etc/hosts 2>/dev/null; then
        log_info "Hostname ${hostname} already in /etc/hosts"
        return 0
    fi

    # Add hostname
    echo "${GATEWAY_IP} ${hostname} # nebari-gateway" | ${SUDO_CMD} tee -a /etc/hosts > /dev/null
    log_success "Added ${hostname} -> ${GATEWAY_IP}"
}

# If app name provided, add just that one
if [ $# -eq 1 ]; then
    APP_NAME=$1
    HOSTNAME="${APP_NAME}.nebari.local"

    log_info "Adding hostname for app: ${APP_NAME}"
    add_hostname "${HOSTNAME}"

    log_success "Done! You can now access: https://${HOSTNAME}"
    exit 0
fi

# Otherwise, scan all NebariApp resources
log_info "Scanning for NebariApp resources..."

# Get all NebariApps and extract their hostnames
HOSTNAMES=$(kubectl get nebariapp --all-namespaces -o jsonpath='{range .items[*]}{.spec.hostname}{"\n"}{end}' 2>/dev/null | grep -v '^$' || echo "")

if [ -z "${HOSTNAMES}" ]; then
    log_warning "No NebariApp resources found with routing configured"
    log_info "Usage: $0 [app-name]"
    log_info "  Example: $0 sample-app-routing"
    exit 0
fi

# Add each hostname
echo "${HOSTNAMES}" | while read -r hostname; do
    if [ -n "${hostname}" ]; then
        add_hostname "${hostname}"
    fi
done

log_success "All NebariApp hostnames added to /etc/hosts"
echo ""
log_info "Added hostnames:"
echo "${HOSTNAMES}" | while read -r hostname; do
    if [ -n "${hostname}" ]; then
        echo "  ➜ https://${hostname}"
    fi
done
