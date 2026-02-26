# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	@set +e; \
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases 2>&1 | grep -v 'Warning: unrecognized format' | grep -v 'gateway-api@v1.4.1'; \
	if [ -f config/crd/bases/reconcilers.nebari.dev_nebariapps.yaml ]; then \
		echo "CRDs generated successfully"; exit 0; \
	else \
		echo "Error: CRD generation failed"; exit 1; \
	fi

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	@"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..." 2>&1 | grep -v 'Warning: unrecognized format' || true

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e | grep -v 'internal/controller$$') -coverprofile cover.out 2>&1 | grep -v "compile: version.*does not match" | grep -v "^# " || true

.PHONY: test-unit
test-unit: ## Run controller unit tests with coverage.
	@echo "Running unit tests..."
	@go test ./internal/controller/... -v -coverprofile=unit-coverage.out
	@echo "\nCoverage Summary:"
	@go tool cover -func=unit-coverage.out | tail -1

.PHONY: test-svc-api
test-svc-api: ## Run service-discovery-api unit tests (cache, api handlers, auth).
	@echo "Running service-discovery-api unit tests..."
	@go test ./internal/servicediscovery/api/... ./internal/servicediscovery/auth/... ./internal/servicediscovery/cache/... -v -coverprofile=svc-api-coverage.out
	@echo "\nCoverage Summary:"
	@go tool cover -func=svc-api-coverage.out | tail -1

.PHONY: test-svc-api-html
test-svc-api-html: test-svc-api ## Generate HTML coverage report for service-discovery-api tests.
	@go tool cover -html=svc-api-coverage.out -o svc-api-coverage.html
	@echo "Coverage report generated: svc-api-coverage.html"
	@xdg-open svc-api-coverage.html 2>/dev/null || echo "Open svc-api-coverage.html in your browser"

.PHONY: test-unit-html
test-unit-html: test-unit ## Generate HTML coverage report for unit tests.
	@go tool cover -html=unit-coverage.out -o unit-coverage.html
	@echo "Coverage report generated: unit-coverage.html"
	@xdg-open unit-coverage.html 2>/dev/null || open unit-coverage.html 2>/dev/null || echo "Open unit-coverage.html in your browser"

.PHONY: test-e2e
test-e2e: manifests generate fmt vet ## Run all e2e tests.
	USE_EXISTING_CLUSTER=$(USE_EXISTING_CLUSTER) IMG=$(IMG) WEBAPI_IMG=$(WEBAPI_DEV_IMG) go test ./test/e2e -v -ginkgo.v -tags=e2e -timeout=30m

.PHONY: test-e2e-operator
test-e2e-operator: manifests generate fmt vet ## Run operator e2e tests (excludes webapi tests).
	USE_EXISTING_CLUSTER=$(USE_EXISTING_CLUSTER) go test ./test/e2e -v -ginkgo.v -tags=e2e -timeout=30m -ginkgo.skip="Service Discovery API"

.PHONY: render-webapi
render-webapi: ## Render webapi Helm templates to deploy/webapi/manifest.yaml for local dev and E2E testing.
	@command -v helm >/dev/null 2>&1 || { echo "helm is required. See https://helm.sh/docs/intro/install/"; exit 1; }
	@if [ ! -d "dist/chart" ]; then \
		echo "dist/chart/ not found — running make helm-chart first..."; \
		$(MAKE) helm-chart; \
	fi
	@mkdir -p deploy/webapi
	@if [ -n "$(WEBAPI_IMG)" ]; then \
		_nav_repo=$$(echo "$(WEBAPI_IMG)" | cut -d: -f1); \
		_nav_tag=$$(echo "$(WEBAPI_IMG)" | cut -d: -f2-); \
		echo "Rendering with custom webapi image: $(WEBAPI_IMG) (auth enabled — Keycloak nebari realm)"; \
		helm template nebari-operator dist/chart \
			--set webapi.enable=true \
			--set webapi.nameOverride=webapi \
			--namespace nebari-system \
			--set webapi.image.repository=$$_nav_repo \
			--set webapi.image.tag=$$_nav_tag \
			--set webapi.image.pullPolicy=IfNotPresent \
			--set-json 'webapi.env=[{"name":"ENABLE_AUTH","value":"true"},{"name":"KEYCLOAK_URL","value":"http://keycloak-keycloakx-http.keycloak.svc.cluster.local/auth"},{"name":"KEYCLOAK_REALM","value":"nebari"}]' \
			--show-only templates/webapi/deployment.yaml \
			--show-only templates/webapi/service.yaml \
			--show-only templates/webapi/serviceaccount.yaml \
			--show-only templates/webapi/rbac.yaml \
			> deploy/webapi/manifest.yaml; \
	else \
		helm template nebari-operator dist/chart \
			--set webapi.enable=true \
			--set webapi.nameOverride=webapi \
			--namespace nebari-system \
			--show-only templates/webapi/deployment.yaml \
			--show-only templates/webapi/service.yaml \
			--show-only templates/webapi/serviceaccount.yaml \
			--show-only templates/webapi/rbac.yaml \
			> deploy/webapi/manifest.yaml; \
	fi
	@echo "✅ WebAPI manifest rendered to deploy/webapi/manifest.yaml"

.PHONY: test-e2e-webapi
test-e2e-webapi: render-webapi ## Run webapi e2e tests only (renders manifests first).
	USE_EXISTING_CLUSTER=$(USE_EXISTING_CLUSTER) go test ./test/e2e -v -ginkgo.v -tags=e2e -timeout=15m -ginkgo.focus="Service Discovery API"

.PHONY: test-e2e-parallel
test-e2e-parallel: manifests generate fmt vet ## Run e2e tests in parallel (faster).
	USE_EXISTING_CLUSTER=$(USE_EXISTING_CLUSTER) go test ./test/e2e -v -ginkgo.v -ginkgo.procs=4 -tags=e2e -timeout=30m

.PHONY: test-e2e-smoke
test-e2e-smoke: manifests generate fmt vet ## Run quick smoke tests only.
	USE_EXISTING_CLUSTER=$(USE_EXISTING_CLUSTER) go test ./test/e2e -v -ginkgo.v -ginkgo.focus="should reconcile a NebariApp" -tags=e2e -timeout=10m

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager ./cmd/operator

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/operator

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} $(DOCKER_BUILD_ARGS) .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name nebari-operator-builder
	$(CONTAINER_TOOL) buildx use nebari-operator-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm nebari-operator-builder
	rm Dockerfile.cross

##@ WebAPI

WEBAPI_IMG ?= quay.io/nebari/nebari-webapi:latest

.PHONY: build-webapi
build-webapi: ## Build webapi binary
	go build -o bin/webapi ./cmd/webapi

.PHONY: docker-build-webapi
docker-build-webapi: ## Build docker image for webapi
	$(CONTAINER_TOOL) build -f Dockerfile.webapi -t ${WEBAPI_IMG} .

.PHONY: docker-push-webapi
docker-push-webapi: ## Push docker image for webapi
	$(CONTAINER_TOOL) push ${WEBAPI_IMG}

.PHONY: deploy-webapi
deploy-webapi: render-webapi ## Render and deploy webapi to cluster.
	kubectl apply -f deploy/webapi/manifest.yaml

.PHONY: undeploy-webapi
undeploy-webapi: ## Remove webapi from cluster.
	@if [ -f "deploy/webapi/manifest.yaml" ]; then \
		kubectl delete -f deploy/webapi/manifest.yaml --ignore-not-found; \
	else \
		echo "deploy/webapi/manifest.yaml not found — run make render-webapi first"; \
	fi

# WEBAPI_DEV_IMG is the local image tag used for dev/testing
WEBAPI_DEV_IMG ?= webapi:dev
# Kind cluster to load the dev image into
KIND_CLUSTER ?= nebari-operator-dev

.PHONY: dev-webapi
dev-webapi: ## Build webapi, load into Kind, and deploy for local testing.
	@echo "Building webapi image $(WEBAPI_DEV_IMG)..."
	$(CONTAINER_TOOL) build -f Dockerfile.webapi -t $(WEBAPI_DEV_IMG) .
	@echo "Loading image into Kind cluster '$(KIND_CLUSTER)'..."
	kind load docker-image $(WEBAPI_DEV_IMG) --name $(KIND_CLUSTER)
	@echo "Deploying webapi..."
	kubectl create namespace nebari-system --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -k deploy/webapi/
	kubectl set image deployment/webapi api=$(WEBAPI_DEV_IMG) -n nebari-system
	kubectl patch deployment webapi -n nebari-system --type=json \
		-p='[{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"Never"},{"op":"replace","path":"/spec/template/spec/containers/0/env/3/value","value":"false"}]'
	kubectl rollout status deployment/webapi -n nebari-system --timeout=60s
	@echo ""
	@echo "✅ WebAPI deployed. Port-forward with:"
	@echo "  kubectl port-forward -n nebari-system svc/webapi 8080:8080"
	@echo ""
	@echo "Then test the API:"
	@echo "  curl http://localhost:8080/api/v1/health"
	@echo "  curl http://localhost:8080/api/v1/services"
	@echo "  curl http://localhost:8080/api/v1/categories"

.PHONY: webapi-pf
webapi-pf: ## Port-forward the webapi to localhost:8080
	kubectl port-forward -n nebari-system svc/webapi 8080:8080

##@ Installer

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

.PHONY: helm-chart
helm-chart: build-installer ## Generate Helm chart from manifests using kubebuilder.
	@command -v kubebuilder >/dev/null 2>&1 || { echo >&2 "kubebuilder is required but not installed. See https://book.kubebuilder.io/quick-start.html#installation"; exit 1; }
	kubebuilder edit --plugins=helm/v2-alpha --force
	@echo "Merging chart extensions from config/chart-extensions/..."
	@for dir in config/chart-extensions/*/; do \
		name=$$(basename $$dir); \
		mkdir -p dist/chart/templates/$$name; \
		cp -r $$dir/. dist/chart/templates/$$name/; \
	done
	@echo "✅ Helm chart generated in dist/chart/"
	@echo ""
	@echo "To package the chart:"
	@echo "  make helm-package"
	@echo ""
	@echo "To update chart version:"
	@echo "  make helm-chart-version VERSION=1.0.0 APP_VERSION=v1.0.0"

.PHONY: helm-package
helm-package: ## Package the Helm chart (run helm-chart first).
	@command -v helm >/dev/null 2>&1 || { echo >&2 "helm is required but not installed. See https://helm.sh/docs/intro/install/"; exit 1; }
	@if [ ! -d "dist/chart" ]; then echo "Error: dist/chart/ not found. Run 'make helm-chart' first."; exit 1; fi
	helm package dist/chart --destination dist/
	@echo "✅ Helm chart packaged in dist/"

.PHONY: helm-chart-version
helm-chart-version: ## Update Helm chart version and appVersion (requires VERSION and APP_VERSION vars).
	@if [ -z "$(VERSION)" ] || [ -z "$(APP_VERSION)" ]; then \
		echo "Error: VERSION and APP_VERSION must be set"; \
		echo "Usage: make helm-chart-version VERSION=1.0.0 APP_VERSION=v1.0.0"; \
		exit 1; \
	fi
	@if [ ! -f "dist/chart/Chart.yaml" ]; then echo "Error: dist/chart/Chart.yaml not found. Run 'make helm-chart' first."; exit 1; fi
	sed -i.bak "s/^version:.*/version: $(VERSION)/" dist/chart/Chart.yaml
	sed -i.bak "s/^appVersion:.*/appVersion: \"$(APP_VERSION)\"/" dist/chart/Chart.yaml
	rm -f dist/chart/Chart.yaml.bak
	@echo "✅ Updated chart version to $(VERSION) and appVersion to $(APP_VERSION)"

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.7.1
CONTROLLER_TOOLS_VERSION ?= v0.19.0

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.5.0
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
