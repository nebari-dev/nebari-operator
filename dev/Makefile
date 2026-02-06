.PHONY: help cluster-create cluster-delete services-install services-uninstall setup teardown clean

# Configuration
CLUSTER_NAME ?= nebari-operator-dev
KUBECONFIG ?= $(HOME)/.kube/config

help: ## Show this help message
	@echo "Nebari Operator Development Environment"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@awk 'BEGIN {FS = ":.*##"; printf ""} /^[a-zA-Z_-]+:.*?##/ { printf "  %-20s %s\n", $$1, $$2 }' $(MAKEFILE_LIST)

cluster-create: ## Create Kind cluster for development
	@echo "Creating Kind cluster: $(CLUSTER_NAME)..."
	@./scripts/cluster/create.sh

cluster-delete: ## Delete Kind cluster
	@echo "Deleting Kind cluster: $(CLUSTER_NAME)..."
	@./scripts/cluster/delete.sh

services-install: ## Install foundational services (Envoy Gateway, cert-manager, etc.)
	@echo "Installing foundational services..."
	@./scripts/services/install.sh

services-uninstall: ## Uninstall foundational services
	@echo "Uninstalling foundational services..."
	@./scripts/services/uninstall.sh

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

update-hosts: ## Update /etc/hosts with NebariApp hostnames
	@./scripts/networking/update-hosts.sh

test-connectivity: ## Test connectivity to a NebariApp (usage: make test-connectivity APP=<name> NS=<namespace>)
	@if [ -z "$(APP)" ]; then \
		echo "Usage: make test-connectivity APP=<app-name> NS=<namespace>"; \
		echo "Example: make test-connectivity APP=sample-app-routing NS=default"; \
		exit 1; \
	fi
	@./scripts/testing/test-connectivity.sh $(APP) $(NS)

port-forward: ## Setup port forwarding for local access
	@./scripts/networking/port-forward.sh
