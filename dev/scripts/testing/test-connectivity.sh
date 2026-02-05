#!/usr/bin/env bash

# Test connectivity to a NebariApp via the Gateway
# Usage: ./test-connectivity.sh <app-name> [namespace]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
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

# Check arguments
if [ $# -lt 1 ]; then
    log_error "Usage: $0 <app-name> [namespace]"
    echo ""
    echo "Example:"
    echo "  $0 sample-app-routing default"
    echo "  $0 my-app my-namespace"
    exit 1
fi

APP_NAME=$1
NAMESPACE=${2:-default}

# Check if cluster exists
if ! kubectl cluster-info --context "kind-${CLUSTER_NAME}" &>/dev/null; then
    log_error "Cluster '${CLUSTER_NAME}' not found. Run 'make cluster-create' first."
    exit 1
fi

# Get Gateway IP
log_info "Getting Gateway IP..."
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")

if [ -z "${GATEWAY_IP}" ] || [ "${GATEWAY_IP}" == "pending" ]; then
    log_error "Gateway IP not available. Make sure services are installed: make services-install"
    exit 1
fi

log_success "Gateway IP: ${GATEWAY_IP}"

# Check if NebariApp exists
log_info "Checking if NebariApp '${APP_NAME}' exists in namespace '${NAMESPACE}'..."
if ! kubectl get nebariapp ${APP_NAME} -n ${NAMESPACE} &>/dev/null; then
    log_error "NebariApp '${APP_NAME}' not found in namespace '${NAMESPACE}'"
    exit 1
fi

# Get hostname
HOSTNAME=$(kubectl get nebariapp ${APP_NAME} -n ${NAMESPACE} -o jsonpath='{.spec.hostname}' 2>/dev/null || echo "")
if [ -z "${HOSTNAME}" ]; then
    log_error "No hostname configured for NebariApp '${APP_NAME}'"
    exit 1
fi

log_success "Hostname: ${HOSTNAME}"

# Check TLS enabled
TLS_ENABLED=$(kubectl get nebariapp ${APP_NAME} -n ${NAMESPACE} -o jsonpath='{.spec.routing.tls.enabled}' 2>/dev/null || echo "true")

# Check if app is ready
log_info "Checking NebariApp status..."
READY_STATUS=$(kubectl get nebariapp ${APP_NAME} -n ${NAMESPACE} -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "Unknown")

if [ "${READY_STATUS}" != "True" ]; then
    log_warning "NebariApp is not ready (status: ${READY_STATUS})"
    log_info "You can check the status with: kubectl describe nebariapp ${APP_NAME} -n ${NAMESPACE}"
else
    log_success "NebariApp is ready"
fi

echo ""
log_info "Testing connectivity..."
echo ""

# Test HTTP (always try this)
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "Testing HTTP connectivity:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 -H "Host: ${HOSTNAME}" "http://${GATEWAY_IP}/" 2>/dev/null || echo "000")

if [ "${HTTP_CODE}" == "301" ] || [ "${HTTP_CODE}" == "302" ]; then
    echo -e "${YELLOW}HTTP ${HTTP_CODE}${NC} - Redirecting to HTTPS (expected for TLS-enabled apps)"
elif [ "${HTTP_CODE}" == "200" ]; then
    echo -e "${GREEN}HTTP ${HTTP_CODE}${NC} - Success!"
elif [ "${HTTP_CODE}" == "000" ]; then
    echo -e "${RED}Connection failed${NC}"
else
    echo -e "${YELLOW}HTTP ${HTTP_CODE}${NC}"
fi

echo ""
echo "Command to test manually:"
echo "  curl -v -H 'Host: ${HOSTNAME}' http://${GATEWAY_IP}/"
echo ""

# Test HTTPS if TLS is enabled
if [ "${TLS_ENABLED}" == "true" ]; then
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Testing HTTPS connectivity:"
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    
    HTTPS_CODE=$(curl -s -o /dev/null -w "%{http_code}" --connect-timeout 5 -k --resolve "${HOSTNAME}:443:${GATEWAY_IP}" "https://${HOSTNAME}/" 2>/dev/null || echo "000")
    
    if [ "${HTTPS_CODE}" == "200" ]; then
        echo -e "${GREEN}HTTPS ${HTTPS_CODE}${NC} - Success!"
    elif [ "${HTTPS_CODE}" == "000" ]; then
        echo -e "${RED}Connection failed${NC}"
    else
        echo -e "${YELLOW}HTTPS ${HTTPS_CODE}${NC}"
    fi
    
    echo ""
    echo "Command to test manually:"
    echo "  curl -k --resolve '${HOSTNAME}:443:${GATEWAY_IP}' https://${HOSTNAME}/"
    echo ""
    echo "Browser URL (after adding to /etc/hosts):"
    echo "  https://${HOSTNAME}"
    echo "  (Accept the self-signed certificate warning)"
fi

# Check /etc/hosts
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "DNS Configuration:"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

if grep -q "^[0-9.]\+[[:space:]]\+${HOSTNAME}[[:space:]]*#.*nebari" /etc/hosts 2>/dev/null; then
    log_success "Hostname ${HOSTNAME} is in /etc/hosts"
    echo ""
    echo "You can access via hostname:"
    if [ "${TLS_ENABLED}" == "true" ]; then
        echo "  curl -k https://${HOSTNAME}"
        echo "  Or in browser: https://${HOSTNAME}"
    else
        echo "  curl http://${HOSTNAME}"
        echo "  Or in browser: http://${HOSTNAME}"
    fi
else
    log_warning "Hostname ${HOSTNAME} is NOT in /etc/hosts"
    echo ""
    echo "To add it, run:"
    echo "  ../networking/update-hosts.sh ${APP_NAME}"
    echo ""
    echo "Or manually:"
    echo "  echo '${GATEWAY_IP} ${HOSTNAME} # nebari-gateway' | sudo tee -a /etc/hosts"
fi

echo ""
