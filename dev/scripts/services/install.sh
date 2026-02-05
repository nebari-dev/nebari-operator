#!/usr/bin/env bash

# Install foundational services for nic-operator development
# This script installs:
# - Envoy Gateway (Gateway API provider)
# - cert-manager (TLS certificate management)
# - Gateway and TLS resources

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

wait_for_deployment() {
    local namespace=$1
    local deployment=$2
    local timeout=${3:-300}

    log_info "Waiting for deployment ${deployment} in namespace ${namespace}..."
    if kubectl wait --for=condition=Available deployment/${deployment} -n ${namespace} --timeout=${timeout}s; then
        log_success "Deployment ${deployment} is ready"
        return 0
    else
        log_error "Deployment ${deployment} failed to become ready"
        return 1
    fi
}

# Check if cluster exists
if ! kubectl cluster-info --context "kind-${CLUSTER_NAME}" &>/dev/null; then
    log_error "Cluster '${CLUSTER_NAME}' not found. Run 'make cluster-create' first."
    exit 1
fi

echo ""
echo "🚀 Installing foundational services to cluster: ${CLUSTER_NAME}"
echo "=========================================="
echo ""

# ============================================
# 1. Install Envoy Gateway with Helm
# ============================================
log_info "Installing Envoy Gateway..."

# Create namespace
kubectl create namespace envoy-gateway-system --dry-run=client -o yaml | kubectl apply -f -

# Install Envoy Gateway with Helm
log_info "Installing Envoy Gateway via Helm (this may take a few minutes)..."
helm upgrade --install eg oci://docker.io/envoyproxy/gateway-helm \
    --version v1.2.4 \
    --namespace envoy-gateway-system \
    --wait \
    --timeout 5m 2>&1 | grep -v "unrecognized format"

# Wait for Gateway API CRDs to be established
log_info "Waiting for Gateway API CRDs to be established..."
kubectl wait --for=condition=established --timeout=2m \
    crd/gateways.gateway.networking.k8s.io \
    crd/httproutes.gateway.networking.k8s.io \
    crd/gatewayclasses.gateway.networking.k8s.io

wait_for_deployment "envoy-gateway-system" "envoy-gateway"

log_success "Envoy Gateway installed"
echo ""

# ============================================
# 2. Create GatewayClass
# ============================================
log_info "Creating GatewayClass..."

cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: envoy-gateway
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
EOF

# Wait for GatewayClass to be accepted
log_info "Waiting for GatewayClass to be accepted..."
kubectl wait --for=condition=Accepted gatewayclass/envoy-gateway --timeout=1m

log_success "GatewayClass created and accepted"
echo ""

# ============================================
# 3. Install cert-manager with Helm
# ============================================
log_info "Installing cert-manager..."

helm repo add jetstack https://charts.jetstack.io 2>/dev/null || true
helm repo update

helm upgrade --install cert-manager oci://quay.io/jetstack/charts/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --version v1.16.2 \
    --set crds.enabled=true \
    --set config.apiVersion=controller.config.cert-manager.io/v1alpha1 \
    --set config.kind=ControllerConfiguration \
    --set config.enableGatewayAPI=true \
    --wait \
    --timeout 5m

wait_for_deployment "cert-manager" "cert-manager"
wait_for_deployment "cert-manager" "cert-manager-webhook"
wait_for_deployment "cert-manager" "cert-manager-cainjector"

log_success "cert-manager installed with Gateway API support"
echo ""

# ============================================
# 4. Create self-signed ClusterIssuer
# ============================================
log_info "Creating self-signed ClusterIssuer..."

cat <<EOF | kubectl apply -f -
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nebari-ca-certificate
  namespace: cert-manager
spec:
  isCA: true
  commonName: nebari-dev-ca
  secretName: nebari-ca-secret
  privateKey:
    algorithm: ECDSA
    size: 256
  issuerRef:
    name: selfsigned-issuer
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: nebari-ca-issuer
spec:
  ca:
    secretName: nebari-ca-secret
EOF

# Wait for CA certificate
sleep 5
kubectl wait --for=condition=Ready certificate/nebari-ca-certificate -n cert-manager --timeout=60s

log_success "ClusterIssuer created"
echo ""

# ============================================
# 5. Create wildcard certificate
# ============================================
log_info "Creating wildcard certificate..."

cat <<EOF | kubectl apply -f -
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nebari-gateway-cert
  namespace: envoy-gateway-system
spec:
  secretName: nebari-gateway-tls
  issuerRef:
    name: nebari-ca-issuer
    kind: ClusterIssuer
  commonName: "*.nebari.local"
  dnsNames:
  - "*.nebari.local"
  - "nebari.local"
EOF

kubectl wait --for=condition=Ready certificate/nebari-gateway-cert -n envoy-gateway-system --timeout=60s

log_success "Wildcard certificate created"
echo ""

# ============================================
# 6. Create shared Gateway
# ============================================
log_info "Creating shared Gateway..."

cat <<EOF | kubectl apply -f -
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: nebari-gateway
  namespace: envoy-gateway-system
spec:
  gatewayClassName: envoy-gateway
  listeners:
  - name: http
    protocol: HTTP
    port: 80
    allowedRoutes:
      namespaces:
        from: All
  - name: https
    protocol: HTTPS
    port: 443
    allowedRoutes:
      namespaces:
        from: All
    tls:
      mode: Terminate
      certificateRefs:
      - name: nebari-gateway-tls
        kind: Secret
EOF

log_success "Gateway created"
echo ""

# Wait for Gateway to get an address
log_info "Waiting for Gateway to receive LoadBalancer address..."
sleep 10
kubectl wait --for=condition=Programmed gateway/nebari-gateway -n envoy-gateway-system --timeout=120s || \
    log_warning "Gateway not yet programmed, continuing..."

# ============================================
# 7. Install Keycloak
# ============================================
log_info "Installing Keycloak for authentication..."

# Create keycloak namespace
kubectl create namespace keycloak --dry-run=client -o yaml | kubectl apply -f -

# Add Keycloak Helm repository
helm repo add codecentric https://codecentric.github.io/helm-charts 2>/dev/null || true
helm repo update

# Install Keycloak with keycloakx chart
log_info "Installing Keycloak via Helm (this may take a few minutes)..."
helm upgrade --install keycloak codecentric/keycloakx \
    --namespace keycloak \
    --set replicas=1 \
    --set image.tag="26.0.7" \
    --set command[0]="/opt/keycloak/bin/kc.sh" \
    --set command[1]="start-dev" \
    --set extraEnv[0].name="KEYCLOAK_ADMIN" \
    --set extraEnv[0].value="admin" \
    --set extraEnv[1].name="KEYCLOAK_ADMIN_PASSWORD" \
    --set extraEnv[1].value="admin" \
    --set extraEnv[2].name="KC_PROXY_HEADERS" \
    --set extraEnv[2].value="xforwarded" \
    --set service.type="ClusterIP" \
    --set service.httpPort=80 \
    --set ingress.enabled=false \
    --wait \
    --timeout 5m

wait_for_deployment "keycloak" "keycloak-keycloakx"

log_success "Keycloak installed"
echo ""

# ============================================
# 8. Create Keycloak admin credentials secret
# ============================================
log_info "Creating Keycloak admin credentials secret..."

kubectl create secret generic nebari-realm-admin-credentials \
    --namespace keycloak \
    --from-literal=username=admin \
    --from-literal=password=admin \
    --dry-run=client -o yaml | kubectl apply -f -

log_success "Keycloak admin credentials secret created"
echo ""

# ============================================
# 9. Configure /etc/hosts for nebari.local
# ============================================
GATEWAY_IP=$(kubectl get svc -n envoy-gateway-system -l gateway.envoyproxy.io/owning-gateway-name=nebari-gateway -o jsonpath='{.items[0].status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "")

if [ -n "${GATEWAY_IP}" ] && [ "${GATEWAY_IP}" != "pending" ]; then
    log_info "Configuring /etc/hosts for nebari.local domains..."

    # Check if we need sudo
    if [ -w "/etc/hosts" ]; then
        SUDO_CMD=""
    else
        SUDO_CMD="sudo"
    fi

    # Remove old nebari.local entries
    ${SUDO_CMD} sed -i.bak '/# nebari-gateway$/d' /etc/hosts 2>/dev/null || true

    # Add base nebari.local entry
    echo "${GATEWAY_IP} nebari.local # nebari-gateway" | ${SUDO_CMD} tee -a /etc/hosts > /dev/null

    log_success "/etc/hosts configured with Gateway IP: ${GATEWAY_IP}"
    log_info "To add app-specific hostnames, use: ${SCRIPT_DIR}/../networking/update-hosts.sh <app-name>"
else
    log_warning "Gateway IP not available yet. Run '${SCRIPT_DIR}/../networking/update-hosts.sh' later to configure /etc/hosts"
fi

# ============================================
# Summary
# ============================================
echo ""
echo "=========================================="
echo "✨ Foundational services installation complete!"
echo "=========================================="
echo ""
echo "📋 Installed components:"
echo "  ✅ Envoy Gateway (v1.2.4)"
echo "  ✅ cert-manager (v1.16.2) with Gateway API support"
echo "  ✅ Self-signed CA ClusterIssuer"
echo "  ✅ Wildcard certificate (*.nebari.local)"
echo "  ✅ Shared Gateway (nebari-gateway)"
echo "  ✅ Keycloak (v26.0.7)"
echo "  ✅ Keycloak admin credentials (nebari-realm-admin-credentials)"
if [ -n "${GATEWAY_IP}" ] && [ "${GATEWAY_IP}" != "pending" ]; then
    echo "  ✅ /etc/hosts configured for nebari.local"
fi
echo ""
echo "🌐 Gateway Information:"
echo "  Name: nebari-gateway"
echo "  Namespace: envoy-gateway-system"
echo "  GatewayClass: envoy-gateway"
if [ -n "${GATEWAY_IP}" ] && [ "${GATEWAY_IP}" != "pending" ]; then
    echo "  LoadBalancer IP: ${GATEWAY_IP}"
else
    echo "  LoadBalancer IP: pending"
fi
echo ""
echo "🔐 Keycloak Information:"
echo "  Service: keycloak-keycloakx-http.keycloak.svc.cluster.local"
echo "  Admin credentials: admin / admin (secret: nebari-realm-admin-credentials)"
echo "  URL: http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth"
echo ""
echo "📜 TLS Certificate:"
echo "  Secret: nebari-gateway-tls (namespace: envoy-gateway-system)"
echo "  DNS Names: *.nebari.local, nebari.local"
echo ""
echo "Next steps:"
echo "  1. Deploy the operator: cd .. && make deploy"
echo "  2. Run e2e tests: cd .. && make test-e2e"
echo "  3. Create test apps with NebariApp CRD"
echo ""
