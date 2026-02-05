.PHONY: help cluster-create cluster-delete services-install services-uninstall setup teardown clean

# Configuration
CLUSTER_NAME ?= nic-operator-dev
KUBECONFIG ?= $(HOME)/.kube/config

help: ## Show this help message
	@echo "NIC Operator Development Environment"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*##"; printf ""} /^[a-zA-Z_-]+:.*?##/ { printf "  %-20s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

cluster-create: ## Create Kind cluster for development
	@echo "Creating Kind cluster: $(CLUSTER_NAME)..."
	@./setup-cluster.sh

cluster-delete: ## Delete Kind cluster
	@echo "Deleting Kind cluster: $(CLUSTER_NAME)..."
	@kind delete cluster --name $(CLUSTER_NAME)

services-install: ## Install foundational services (Envoy Gateway, cert-manager, etc.)
	@echo "Installing foundational services..."
	@./install-services.sh

services-uninstall: ## Uninstall foundational services
	@echo "Uninstalling foundational services..."
	@./uninstall-services.sh

setup: cluster-create services-install ## Full setup: create cluster and install services
	@echo ""
	@echo "✅ Development environment is ready!"
	@echo ""
	@echo "Next steps:"
	@echo "  1. Deploy operator: cd .. && make deploy"
	@echo "  2. Run e2e tests: cd .. && make test-e2e"

teardown: services-uninstall cluster-delete ## Full teardown: uninstall services and delete cluster
	@echo "✅ Development environment cleaned up"

clean: teardown ## Alias for teardown

status: ## Show status of development environment
	@echo "Checking development environment status..."
	@echo ""
	@if kind get clusters 2>/dev/null | grep -q "^$(CLUSTER_NAME)$$"; then \
		echo "✅ Cluster '$(CLUSTER_NAME)' exists"; \
		echo ""; \
		echo "Checking services..."; \
		kubectl cluster-info --context kind-$(CLUSTER_NAME) 2>/dev/null || true; \
		echo ""; \
		echo "Namespaces:"; \
		kubectl get ns 2>/dev/null | grep -E "envoy-gateway-system|cert-manager|keycloak" || echo "  No foundational service namespaces found"; \
		echo ""; \
		echo "Gateway API resources:"; \
		kubectl get gatewayclass,gateway -A 2>/dev/null || echo "  No Gateway API resources found"; \
	else \
		echo "❌ Cluster '$(CLUSTER_NAME)' not found"; \
		echo ""; \
		echo "Run 'make cluster-create' to create it"; \
	fi
