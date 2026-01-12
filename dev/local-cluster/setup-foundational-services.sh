#!/usr/bin/env bash

# Install foundational services for nic-operator development
# This script installs:
# - Envoy Gateway (Gateway API provider)
# - cert-manager (TLS certificate management)
# - Keycloak (SSO/OIDC provider)
# - Istio (optional, for service mesh capabilities)

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

wait_for_pod() {
    local namespace=$1
    local label=$2
    local timeout=${3:-300}

    log_info "Waiting for pod with label ${label} in namespace ${namespace}..."
    if kubectl wait --for=condition=Ready pod -l ${label} -n ${namespace} --timeout=${timeout}s; then
        log_success "Pod with label ${label} is ready"
        return 0
    else
        log_error "Pod with label ${label} failed to become ready"
        return 1
    fi
}

# Check if cluster exists
if ! kubectl cluster-info --context "kind-${CLUSTER_NAME}" &>/dev/null; then
    log_error "Cluster '${CLUSTER_NAME}' not found. Run 'make kind-create' first."
    exit 1
fi

echo ""
echo "🚀 Installing foundational services to cluster: ${CLUSTER_NAME}"
echo "=========================================="
echo ""

# ============================================
# 1. Install Gateway API CRDs
# ============================================
log_info "Installing Gateway API CRDs..."
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.1/standard-install.yaml

log_success "Gateway API CRDs installed"
echo ""

# ============================================
# 2. Install Envoy Gateway
# ============================================
log_info "Installing Envoy Gateway..."

# Install Envoy Gateway using server-side apply to handle large CRDs
kubectl apply --server-side -f https://github.com/envoyproxy/gateway/releases/download/v1.2.4/install.yaml 2>&1 | grep -v "Too long: must have at most" || true

# Wait for the deployment to be ready
log_info "Waiting for Envoy Gateway to be ready..."
sleep 10
wait_for_deployment "envoy-gateway-system" "envoy-gateway"

log_success "Envoy Gateway installed"
echo ""

# ============================================
# 3. Create shared Gateway resources
# ============================================
log_info "Creating shared Gateway resources..."

cat <<EOF | kubectl apply -f -
---
apiVersion: gateway.networking.k8s.io/v1
kind: GatewayClass
metadata:
  name: nic-gateway-class
spec:
  controllerName: gateway.envoyproxy.io/gatewayclass-controller
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: nic-public-gateway
  namespace: envoy-gateway-system
spec:
  gatewayClassName: nic-gateway-class
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
      - name: nic-wildcard-tls
        kind: Secret
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: nic-internal-gateway
  namespace: envoy-gateway-system
spec:
  gatewayClassName: nic-gateway-class
  listeners:
  - name: http
    protocol: HTTP
    port: 8080
    allowedRoutes:
      namespaces:
        from: All
EOF

log_success "Gateway resources created"
echo ""

# ============================================
# 3b. Wait for Gateway Envoy proxies
# ============================================
log_info "Waiting for Gateway Envoy proxy pods to be created..."
sleep 10

# Wait for the public gateway proxy deployment
kubectl wait --for=condition=Available deployment \
  -l gateway.envoyproxy.io/owning-gateway-name=nic-public-gateway \
  -n envoy-gateway-system --timeout=120s 2>/dev/null || log_warning "Gateway proxy not ready yet, continuing..."

log_success "Gateway LoadBalancer services will receive external IPs from MetalLB"
log_info "Use 'kubectl get svc -n envoy-gateway-system' to see assigned IPs"

echo ""

# ============================================
# 4. Install cert-manager
# ============================================
log_info "Installing cert-manager..."

helm repo add jetstack https://charts.jetstack.io || true
helm repo update

helm upgrade --install cert-manager jetstack/cert-manager \
    --namespace cert-manager \
    --create-namespace \
    --version v1.16.2 \
    --set crds.enabled=true \
    --wait \
    --timeout 5m

wait_for_deployment "cert-manager" "cert-manager"
wait_for_deployment "cert-manager" "cert-manager-webhook"
wait_for_deployment "cert-manager" "cert-manager-cainjector"

log_success "cert-manager installed"
echo ""

# ============================================
# 5. Create self-signed ClusterIssuer for development
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
  name: nic-ca-certificate
  namespace: cert-manager
spec:
  isCA: true
  commonName: nic-dev-ca
  secretName: nic-ca-secret
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
  name: nic-ca-issuer
spec:
  ca:
    secretName: nic-ca-secret
EOF

# Wait for CA certificate to be ready
sleep 5
kubectl wait --for=condition=Ready certificate/nic-ca-certificate -n cert-manager --timeout=60s

log_success "ClusterIssuer created"
echo ""

# ============================================
# 6. Create wildcard certificate
# ============================================
log_info "Creating wildcard certificate..."

cat <<EOF | kubectl apply -f -
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: nic-wildcard-certificate
  namespace: envoy-gateway-system
spec:
  secretName: nic-wildcard-tls
  issuerRef:
    name: nic-ca-issuer
    kind: ClusterIssuer
  commonName: "*.nic.local"
  dnsNames:
  - "*.nic.local"
  - "nic.local"
EOF

kubectl wait --for=condition=Ready certificate/nic-wildcard-certificate -n envoy-gateway-system --timeout=60s

log_success "Wildcard certificate created"
echo ""

# ============================================
# 7. Install PostgreSQL for Keycloak
# ============================================
log_info "Installing PostgreSQL for Keycloak..."

helm repo add bitnami https://charts.bitnami.com/bitnami || true
helm repo update

kubectl create namespace keycloak || true

helm upgrade --install postgresql bitnami/postgresql \
    --namespace keycloak \
    --set auth.username=keycloak \
    --set auth.password=keycloak \
    --set auth.database=keycloak \
    --set primary.persistence.enabled=false \
    --wait \
    --timeout 5m

wait_for_pod "keycloak" "app.kubernetes.io/name=postgresql"

log_success "PostgreSQL installed"
echo ""

# ============================================
# 8. Install Keycloak (simplified for development)
# ============================================
log_info "Installing Keycloak..."

# Deploy Keycloak using a simple deployment for development
# For production, use the official Keycloak operator or Bitnami chart with proper images
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: keycloak
  namespace: keycloak
  labels:
    app: keycloak
spec:
  type: NodePort
  ports:
  - name: http
    port: 8080
    targetPort: 8080
    nodePort: 30080
  selector:
    app: keycloak
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: keycloak
  namespace: keycloak
  labels:
    app: keycloak
spec:
  replicas: 1
  selector:
    matchLabels:
      app: keycloak
  template:
    metadata:
      labels:
        app: keycloak
    spec:
      containers:
      - name: keycloak
        image: quay.io/keycloak/keycloak:23.0.7
        args:
        - start-dev
        env:
        - name: KEYCLOAK_ADMIN
          value: admin
        - name: KEYCLOAK_ADMIN_PASSWORD
          value: admin
        - name: KC_DB
          value: postgres
        - name: KC_DB_URL
          value: jdbc:postgresql://postgresql:5432/keycloak
        - name: KC_DB_USERNAME
          value: keycloak
        - name: KC_DB_PASSWORD
          value: keycloak
        - name: KC_PROXY
          value: edge
        - name: KC_HOSTNAME
          value: "localhost:30080"
        - name: KC_HOSTNAME_STRICT
          value: "false"
        - name: KC_HOSTNAME_STRICT_HTTPS
          value: "false"
        - name: KC_HTTP_ENABLED
          value: "true"
        ports:
        - name: http
          containerPort: 8080
        readinessProbe:
          httpGet:
            path: /realms/master
            port: 8080
          initialDelaySeconds: 60
          periodSeconds: 10
          timeoutSeconds: 5
          failureThreshold: 30
        resources:
          requests:
            memory: "512Mi"
            cpu: "250m"
          limits:
            memory: "1Gi"
            cpu: "500m"
EOF

log_info "Waiting for Keycloak to be ready (this may take a few minutes)..."
kubectl wait --for=condition=Available deployment/keycloak -n keycloak --timeout=600s

# Wait for Keycloak to be fully ready
sleep 15

# Create the 'keycloak' realm for NIC operator
log_info "Creating 'keycloak' realm in Keycloak..."
kubectl exec -n keycloak deployment/keycloak -- \
    /opt/keycloak/bin/kcadm.sh create realms \
    --server http://localhost:8080 \
    --realm master \
    --user admin \
    --password admin \
    -s realm=keycloak \
    -s enabled=true 2>/dev/null || log_warning "Realm may already exist"

log_success "Keycloak installed and configured"
echo ""

# ============================================
# 9. Install Istio (optional)
# ============================================
read -p "$(echo -e ${YELLOW}Do you want to install Istio? \(y/N\): ${NC})" -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    log_info "Installing Istio..."

    # Check if istioctl is installed
    if ! command -v istioctl &> /dev/null; then
        log_warning "istioctl not found. Skipping Istio installation."
        log_info "To install Istio later, visit: https://istio.io/latest/docs/setup/getting-started/"
    else
        istioctl install --set profile=demo -y
        kubectl label namespace default istio-injection=enabled --overwrite

        wait_for_deployment "istio-system" "istiod"

        log_success "Istio installed"
    fi
    echo ""
else
    log_info "Skipping Istio installation"
    echo ""
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
echo "  ✅ Gateway API CRDs"
echo "  ✅ Envoy Gateway"
echo "  ✅ cert-manager with self-signed CA"
echo "  ✅ Keycloak with PostgreSQL"
if [[ $REPLY =~ ^[Yy]$ ]] && command -v istioctl &> /dev/null; then
    echo "  ✅ Istio service mesh"
fi
echo ""
echo "🔑 Access Information:"
echo "  Keycloak Admin Console: http://localhost:30080"
echo "    Username: admin"
echo "    Password: admin"
echo "    Realm: keycloak (for OIDC clients)"
echo ""
echo "  ⚠️  Note: Keycloak is configured for local development with:"
echo "    - KC_HOSTNAME=localhost:30080 (for OIDC redirects)"
echo "    - KC_HOSTNAME_STRICT=false (allows internal cluster access)"
echo "    - For production, configure proper external hostname"
echo ""
echo "🌐 Gateway Information:"
echo "  Public Gateway: nic-public-gateway (namespace: envoy-gateway-system)"
echo "  Internal Gateway: nic-internal-gateway (namespace: envoy-gateway-system)"
echo ""
echo "📜 TLS Certificate:"
echo "  Wildcard cert: *.nic.local (secret: nic-wildcard-tls)"
echo ""
echo "Next steps:"
echo "  1. Setup gateway access (macOS only): ./dev/nic-dev gateway:setup"
echo "  2. Deploy the operator: make deploy"
echo "  3. Create a test namespace: kubectl create namespace test-app"
echo "  4. Label it for NIC management: kubectl label namespace test-app nic.nebari.dev/managed=true"
echo "  5. Deploy a sample app: kubectl apply -f dev/local-cluster/sample-apps/"
echo ""
