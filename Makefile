# Image URL to use all building/pushing image targets
HUB ?= ghcr.io/volcano-sh
TAG ?= latest
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))

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

.PHONY: gen-crd
gen-crd: controller-gen ## Generate CRD manifests
	$(CONTROLLER_GEN) crd paths="./pkg/apis/runtime/v1alpha1/..." output:crd:artifacts:config=crds

.PHONY: generate
generate: controller-gen gen-crd ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./pkg/apis/..."
	go mod tidy

.PHONY: gen-client
gen-client: ## Generate client-go code for CRDs
	@echo "Generating client-go code..."
	@bash hack/update-codegen.sh
	@go mod tidy

.PHONY: gen-all
gen-all: generate gen-client ## Generate all code (CRDs, DeepCopy methods, and client-go)

.PHONY: gen-check
gen-check: gen-all ## Check if generated code is up to date
	git diff --exit-code

.PHONY: build run clean test deps

# Build targets
build: generate ## Build workloadmanager binary
	@echo "Building workloadmanager..."
	go build -o bin/workloadmanager ./cmd/workload-manager

build-agentd: generate ## Build agentd binary
	@echo "Building agentd..."
	go build -o bin/agentd ./cmd/agentd

build-test-tunnel: ## Build test-tunnel tool
	@echo "Building test-tunnel..."
	go build -o bin/test-tunnel ./cmd/test-tunnel

build-all: build build-agentd build-test-tunnel ## Build all binaries

# Run server (development mode)
run:
	@echo "Running workloadmanager..."
	go run ./cmd/workload-manager/main.go \
		--port=8080 \
		--ssh-username=sandbox \
		--ssh-port=22

# Run server (with kubeconfig)
run-local:
	@echo "Running workloadmanager with local kubeconfig..."
	go run ./cmd/workload-manager/main.go \
		--port=8080 \
		--kubeconfig=${HOME}/.kube/config \
		--ssh-username=sandbox \
		--ssh-port=22

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f workloadmanager agentd

# Install dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Update dependencies
update-deps:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Format code
fmt: ## Format code
	@echo "Formatting code..."
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

# Run linter
.PHONY: lint
lint: golangci-lint ## Run golangci-lint
	$(GOLANGCI_LINT) run ./...

# Install to system
install: build
	@echo "Installing workloadmanager..."
	sudo cp bin/workloadmanager /usr/local/bin/

# Docker image variables
APISERVER_IMAGE ?= workloadmanager:latest
IMAGE_REGISTRY ?= ""

# Docker and Kubernetes targets
docker-build:
	@echo "Building Docker image..."
	docker build -t $(APISERVER_IMAGE) .

# Multi-architecture build (supports amd64, arm64)
docker-buildx:
	@echo "Building multi-architecture Docker image..."
	docker buildx build --platform linux/amd64,linux/arm64 -t $(APISERVER_IMAGE) .

# Multi-architecture build and push
docker-buildx-push:
	@if [ -z "$(IMAGE_REGISTRY)" ]; then \
		echo "Error: IMAGE_REGISTRY not set. Usage: make docker-buildx-push IMAGE_REGISTRY=your-registry.com"; \
		exit 1; \
	fi
	@echo "Building and pushing multi-architecture Docker image to $(IMAGE_REGISTRY)/$(APISERVER_IMAGE)..."
	docker buildx build --platform linux/amd64,linux/arm64 \
		-t $(IMAGE_REGISTRY)/$(APISERVER_IMAGE) \
		--push .

docker-push: docker-build
	@if [ -z "$(IMAGE_REGISTRY)" ]; then \
		echo "Error: IMAGE_REGISTRY not set. Usage: make docker-push IMAGE_REGISTRY=your-registry.com"; \
		exit 1; \
	fi
	@echo "Tagging and pushing Docker image to $(IMAGE_REGISTRY)/$(APISERVER_IMAGE)..."
	docker tag $(APISERVER_IMAGE) $(IMAGE_REGISTRY)/$(APISERVER_IMAGE)
	docker push $(IMAGE_REGISTRY)/$(APISERVER_IMAGE)

k8s-deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f k8s/workloadmanager.yaml

k8s-delete:
	@echo "Deleting from Kubernetes..."
	kubectl delete -f k8s/workloadmanager.yaml

k8s-logs:
	@echo "Showing logs..."
	kubectl logs -n agentcube -l app=workloadmanager -f

# Load image to kind cluster
kind-load:
	@echo "Loading image to kind..."
	kind load docker-image $(APISERVER_IMAGE)

# Sandbox image targets
SANDBOX_IMAGE ?= sandbox:latest

sandbox-build:
	@echo "Building sandbox image..."
	docker build -t $(SANDBOX_IMAGE) images/sandbox/

sandbox-push: sandbox-build
	@if [ -z "$(IMAGE_REGISTRY)" ]; then \
		echo "Error: IMAGE_REGISTRY not set. Usage: make sandbox-push IMAGE_REGISTRY=your-registry.com"; \
		exit 1; \
	fi
	@echo "Tagging and pushing sandbox image to $(IMAGE_REGISTRY)/$(SANDBOX_IMAGE)..."
	docker tag $(SANDBOX_IMAGE) $(IMAGE_REGISTRY)/$(SANDBOX_IMAGE)
	docker push $(IMAGE_REGISTRY)/$(SANDBOX_IMAGE)

# Multi-architecture build for sandbox (supports amd64, arm64)
sandbox-buildx:
	@echo "Building multi-architecture sandbox image..."
	docker buildx build --platform linux/amd64,linux/arm64 -t $(SANDBOX_IMAGE) images/sandbox/

# Multi-architecture build and push for sandbox
sandbox-buildx-push:
	@if [ -z "$(IMAGE_REGISTRY)" ]; then \
		echo "Error: IMAGE_REGISTRY not set. Usage: make sandbox-buildx-push IMAGE_REGISTRY=your-registry.com"; \
		exit 1; \
	fi
	@echo "Building and pushing multi-architecture sandbox image to $(IMAGE_REGISTRY)/$(SANDBOX_IMAGE)..."
	docker buildx build --platform linux/amd64,linux/arm64 \
		-t $(IMAGE_REGISTRY)/$(SANDBOX_IMAGE) \
		--push images/sandbox/

sandbox-test:
	@echo "Testing sandbox image locally..."
	docker run -d -p 2222:22 --name sandbox-test $(SANDBOX_IMAGE)
	@echo "Sandbox running on port 2222. Test with: ssh -p 2222 sandbox@localhost"
	@echo "Password: sandbox"
	@echo "Stop with: make sandbox-test-stop"

sandbox-test-stop:
	@echo "Stopping and removing sandbox test container..."
	docker stop sandbox-test || true
	docker rm sandbox-test || true

sandbox-kind-load:
	@echo "Loading sandbox image to kind..."
	kind load docker-image $(SANDBOX_IMAGE)

# Test targets
test-tunnel:
	@if [ -z "$(SESSION_ID)" ]; then \
		echo "Error: SESSION_ID not set. Usage: make test-tunnel SESSION_ID=<session-id>"; \
		exit 1; \
	fi
	@echo "Testing tunnel for session $(SESSION_ID)..."
	@go run ./cmd/test-tunnel/main.go -session $(SESSION_ID) -api $(API_URL) -token $(TOKEN)

test-tunnel-build:
	@echo "Building and running tunnel test..."
	@make build-test-tunnel
	@if [ -z "$(SESSION_ID)" ]; then \
		echo "Error: SESSION_ID not set. Usage: make test-tunnel-build SESSION_ID=<session-id>"; \
		exit 1; \
	fi
	./bin/test-tunnel -session $(SESSION_ID) -api $(API_URL) -token $(TOKEN)

# Variables for test-tunnel
API_URL ?= http://localhost:8080
TOKEN ?= ""
SESSION_ID ?= ""


##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.17.2
GOLANGCI_LINT_VERSION ?= v1.64.1

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1)
endef

# E2E Test targets
E2E_CLUSTER_NAME ?= agentcube-e2e
AGENT_SANDBOX_REPO ?= https://github.com/kubernetes-sigs/agent-sandbox.git
AGENT_SANDBOX_VERSION ?= main

e2e:
	./test/e2e/run_e2e.sh

e2e-clean:
	@echo "Cleaning up E2E environment..."
	kind delete cluster --name $(E2E_CLUSTER_NAME)
	rm -rf /tmp/agent-sandbox
