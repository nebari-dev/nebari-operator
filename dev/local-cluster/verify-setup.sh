#!/usr/bin/env bash
# Verification script for local NIC Operator setup

set -e

echo "🔍 Verifying NIC Operator Local Setup"
echo "======================================"
echo

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

check_command() {
    if command -v "$1" &> /dev/null; then
        echo -e "${GREEN}✓${NC} $1 is installed"
        return 0
    else
        echo -e "${RED}✗${NC} $1 is NOT installed"
        return 1
    fi
}

check_resource() {
    local resource=$1
    local namespace=$2
    local name=$3

    if [ -z "$namespace" ]; then
        if kubectl get "$resource" "$name" &> /dev/null; then
            echo -e "${GREEN}✓${NC} $resource/$name exists"
            return 0
        else
            echo -e "${RED}✗${NC} $resource/$name NOT found"
            return 1
        fi
    else
        if kubectl get "$resource" -n "$namespace" "$name" &> /dev/null; then
            echo -e "${GREEN}✓${NC} $resource/$name exists in namespace $namespace"
            return 0
        else
            echo -e "${RED}✗${NC} $resource/$name NOT found in namespace $namespace"
            return 1
        fi
    fi
}

check_statefulset_ready() {
    local namespace=$1
    local name=$2

    if kubectl get statefulset -n "$namespace" "$name" &> /dev/null; then
        local ready=$(kubectl get statefulset -n "$namespace" "$name" -o jsonpath='{.status.readyReplicas}')
        local desired=$(kubectl get statefulset -n "$namespace" "$name" -o jsonpath='{.spec.replicas}')

        if [ "$ready" == "$desired" ] && [ "$ready" != "" ]; then
            echo -e "${GREEN}✓${NC} StatefulSet $name is ready ($ready/$desired)"
            return 0
        else
            echo -e "${YELLOW}⚠${NC} StatefulSet $name is NOT ready ($ready/$desired)"
            return 1
        fi
    else
        echo -e "${RED}✗${NC} StatefulSet $name NOT found in namespace $namespace"
        return 1
    fi
}

check_deployment_ready() {
    local namespace=$1
    local name=$2

    if kubectl get deployment -n "$namespace" "$name" &> /dev/null; then
        local ready=$(kubectl get deployment -n "$namespace" "$name" -o jsonpath='{.status.readyReplicas}')
        local desired=$(kubectl get deployment -n "$namespace" "$name" -o jsonpath='{.spec.replicas}')

        if [ "$ready" == "$desired" ] && [ "$ready" != "" ]; then
            echo -e "${GREEN}✓${NC} Deployment $name is ready ($ready/$desired)"
            return 0
        else
            echo -e "${YELLOW}⚠${NC} Deployment $name is NOT ready ($ready/$desired)"
            return 1
        fi
    else
        echo -e "${RED}✗${NC} Deployment $name NOT found in namespace $namespace"
        return 1
    fi
}

check_port_forward() {
    local port=$1
    if ps aux | grep "port-forward" | grep -v grep | grep -q "$port"; then
        echo -e "${GREEN}✓${NC} Port-forward on port $port is running"
        return 0
    else
        echo -e "${YELLOW}⚠${NC} Port-forward on port $port is NOT running"
        return 1
    fi
}

# Check prerequisites
echo "Prerequisites:"
check_command kubectl
check_command kind
check_command helm
check_command docker
echo

# Check cluster
echo "Cluster:"
if kubectl cluster-info &> /dev/null; then
    echo -e "${GREEN}✓${NC} Kubernetes cluster is accessible"

    # Check if using a KIND cluster (any name)
    if kubectl get nodes -o jsonpath='{.items[0].metadata.name}' | grep -q "^kind-"; then
        cluster_name=$(kubectl config current-context | sed 's/^kind-//')
        echo -e "${GREEN}✓${NC} Using KIND cluster '$cluster_name'"
    else
        echo -e "${YELLOW}⚠${NC} Not using a KIND cluster"
    fi
else
    echo -e "${RED}✗${NC} Cannot access Kubernetes cluster"
    exit 1
fi
echo

# Check foundational services
echo "Foundational Services:"
check_resource namespace "" envoy-gateway-system
check_resource namespace "" cert-manager
check_resource namespace "" keycloak
check_deployment_ready envoy-gateway-system envoy-gateway
check_deployment_ready cert-manager cert-manager
check_deployment_ready keycloak keycloak
check_statefulset_ready keycloak postgresql
echo

# Check gateways
echo "Gateways:"
check_resource gateway envoy-gateway-system nic-public-gateway
check_resource gateway envoy-gateway-system nic-internal-gateway
echo

# Check certificates
echo "Certificates:"
check_resource certificate cert-manager nic-ca-certificate
check_resource secret envoy-gateway-system nic-wildcard-tls
echo

# Check Keycloak realm
echo "Keycloak Configuration:"
if kubectl exec -n keycloak deployment/keycloak -- \
    /opt/keycloak/bin/kcadm.sh get realms/keycloak \
    --server http://localhost:8080 --realm master \
    --user admin --password admin &> /dev/null; then
    echo -e "${GREEN}✓${NC} Keycloak realm 'keycloak' exists"
else
    echo -e "${RED}✗${NC} Keycloak realm 'keycloak' NOT found"
fi
echo

# Check port forwards
echo "Port Forwards:"
check_port_forward "8080:80"
check_port_forward "443:443"
check_port_forward "8081:8080"
echo

# Check operator
echo "NIC Operator:"
if kubectl get namespace nic-operator-system &> /dev/null; then
    check_deployment_ready nic-operator-system nic-operator-controller-manager

    # Check operator logs for any errors
    if kubectl logs -n nic-operator-system deployment/nic-operator-controller-manager --tail=10 | grep -q "ERROR"; then
        echo -e "${YELLOW}⚠${NC} Operator logs contain errors (check: kubectl logs -n nic-operator-system deployment/nic-operator-controller-manager)"
    else
        echo -e "${GREEN}✓${NC} Operator logs look clean"
    fi
else
    echo -e "${YELLOW}⚠${NC} Operator not deployed yet (namespace nic-operator-system not found)"
fi
echo

# Check test application
echo "Test Application (if deployed):"
if kubectl get namespace demo-app &> /dev/null; then
    check_resource nicapp demo-app nginx-demo
    check_deployment_ready demo-app nginx
    check_resource httproute demo-app nginx-demo-route
    check_resource securitypolicy demo-app nginx-demo-security
    check_resource secret demo-app nginx-demo-oidc-client

    # Check NicApp status
    if kubectl get nicapp nginx-demo -n demo-app -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null | grep -q "True"; then
        echo -e "${GREEN}✓${NC} NicApp nginx-demo is Ready"
    else
        echo -e "${YELLOW}⚠${NC} NicApp nginx-demo is NOT Ready"
    fi
else
    echo -e "${YELLOW}⚠${NC} Test application not deployed (namespace demo-app not found)"
fi
echo

# Check /etc/hosts
echo "DNS Configuration:"
if grep -q "nginx-demo.nic.local" /etc/hosts 2>/dev/null; then
    echo -e "${GREEN}✓${NC} /etc/hosts contains nginx-demo.nic.local"
else
    echo -e "${YELLOW}⚠${NC} /etc/hosts does NOT contain nginx-demo.nic.local"
    echo "  Add: 127.0.0.1 nginx-demo.nic.local"
fi
echo

# Test connectivity
echo "Connectivity Tests:"
if command -v curl &> /dev/null; then
    # Test HTTP
    if curl -s -o /dev/null -w "%{http_code}" http://localhost:8080 | grep -q "404"; then
        echo -e "${GREEN}✓${NC} Gateway HTTP endpoint is accessible (404 expected without app)"
    else
        echo -e "${YELLOW}⚠${NC} Gateway HTTP endpoint may not be accessible"
    fi

    # Test HTTPS
    if curl -k -s -o /dev/null -w "%{http_code}" https://localhost:443 2>/dev/null | grep -qE "404|302"; then
        echo -e "${GREEN}✓${NC} Gateway HTTPS endpoint is accessible"
    else
        echo -e "${YELLOW}⚠${NC} Gateway HTTPS endpoint may not be accessible"
    fi

    # Test Keycloak
    if curl -s -o /dev/null -w "%{http_code}" http://localhost:30080 | grep -q "200"; then
        echo -e "${GREEN}✓${NC} Keycloak is accessible at localhost:30080"
    else
        echo -e "${YELLOW}⚠${NC} Keycloak may not be accessible at localhost:30080"
    fi
else
    echo -e "${YELLOW}⚠${NC} curl not available, skipping connectivity tests"
fi
echo

echo "======================================"
echo "Verification complete!"
echo
echo "Next steps:"
echo "  - If operator not deployed: make kind-load && kubectl apply -f config/..."
echo "  - If test app not deployed: See docs/local-development.md section 6-7"
echo "  - If port forwards missing: See docs/local-development.md section 5"
echo "  - Full guide: docs/local-development.md"
