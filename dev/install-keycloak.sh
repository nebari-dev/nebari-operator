#!/bin/bash
set -e

echo "Installing Keycloak..."

# Check if helm is installed
if ! command -v helm &> /dev/null; then
    echo "ERROR: helm is not installed. Please install helm first:"
    echo "  curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash"
    exit 1
fi

# Create keycloak namespace
kubectl create namespace keycloak --dry-run=client -o yaml | kubectl apply -f -

# Clean up any failed installations
echo "Cleaning up any previous Keycloak installations..."
kubectl delete statefulset keycloak-keycloakx -n keycloak --ignore-not-found --timeout=60s || true
kubectl delete pod -n keycloak -l app.kubernetes.io/name=keycloakx --ignore-not-found || true

# Install Keycloak using helm
if ! helm repo list | grep -q codecentric; then
    echo "Adding codecentric helm repo..."
    helm repo add codecentric https://codecentric.github.io/helm-charts
    helm repo update
fi

echo "Installing Keycloak via Helm..."

# Create a temporary values file
cat > /tmp/keycloak-values.yaml <<EOF
replicas: 1

resources:
  requests:
    memory: "1Gi"
    cpu: "500m"
  limits:
    memory: "2Gi"
    cpu: "1000m"

command:
  - "/opt/keycloak/bin/kc.sh"
  - "start-dev"

extraEnv: |
  - name: KC_BOOTSTRAP_ADMIN_USERNAME
    value: "admin"
  - name: KC_BOOTSTRAP_ADMIN_PASSWORD
    value: "admin"
  - name: KC_HTTP_RELATIVE_PATH
    value: "/auth"
  - name: JAVA_OPTS_APPEND
    value: "-Xms512m -Xmx1536m"

http:
  relativePath: "/auth"
EOF

helm upgrade --install keycloak codecentric/keycloakx \
    --namespace keycloak \
    --values /tmp/keycloak-values.yaml \
    --wait \
    --timeout=5m || {
        echo "ERROR: Keycloak installation failed"
        echo "Check Keycloak logs:"
        echo "  kubectl logs -n keycloak -l app.kubernetes.io/name=keycloakx"
        rm -f /tmp/keycloak-values.yaml
        exit 1
    }

rm -f /tmp/keycloak-values.yaml

# Create Keycloak admin credentials secret
echo "Creating Keycloak admin credentials secret..."
kubectl create secret generic keycloak-admin-credentials \
    --namespace keycloak \
    --from-literal=admin-username=admin \
    --from-literal=admin-password=admin \
    --dry-run=client -o yaml | kubectl apply -f -

echo "Waiting for Keycloak to be ready..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=keycloakx -n keycloak --timeout=300s || {
    echo "WARNING: Keycloak pod not ready after 5 minutes"
    echo "Check pod status:"
    echo "  kubectl get pods -n keycloak"
    echo "  kubectl describe pod -n keycloak -l app.kubernetes.io/name=keycloakx"
    exit 1
}

echo "Keycloak installed successfully!"
echo "Admin credentials: admin/admin"
echo ""
echo "Access Keycloak:"
echo "  Internal URL: http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth"
echo "  Port-forward: kubectl port-forward -n keycloak svc/keycloak-keycloakx-http 8080:80"
echo "  Then open: http://localhost:8080/auth"
