.PHONY: build run clean test deps

# Build targets
build:
	@echo "Building agentcube-apiserver..."
	go build -o bin/agentcube-apiserver ./cmd/apiserver

build-agentd:
	@echo "Building agentd..."
	go build -o bin/agentd ./cmd/agentd

build-test-tunnel:
	@echo "Building test-tunnel..."
	go build -o bin/test-tunnel ./cmd/test-tunnel

build-all: build build-agentd build-test-tunnel

# Run server (development mode)
run:
	@echo "Running agentcube-apiserver..."
	go run ./cmd/apiserver/main.go \
		--port=8080 \
		--namespace=agentcube \
		--ssh-username=sandbox \
		--ssh-port=22

# Run server (with kubeconfig)
run-local:
	@echo "Running agentcube-apiserver with local kubeconfig..."
	go run ./cmd/apiserver/main.go \
		--port=8080 \
		--kubeconfig=${HOME}/.kube/config \
		--namespace=agentcube \
		--ssh-username=sandbox \
		--ssh-port=22

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f agentcube-apiserver agentd

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
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run ./...

# Install to system
install: build
	@echo "Installing agentcube-apiserver..."
	sudo cp bin/agentcube-apiserver /usr/local/bin/

# Docker image variables
APISERVER_IMAGE ?= agentcube-apiserver:latest
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
	kubectl apply -f k8s/agentcube-apiserver.yaml

k8s-delete:
	@echo "Deleting from Kubernetes..."
	kubectl delete -f k8s/agentcube-apiserver.yaml

k8s-logs:
	@echo "Showing logs..."
	kubectl logs -n agentcube -l app=agentcube-apiserver -f

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

# Show help message
help:
	@echo "Available targets:"
	@echo ""
	@echo "Build targets:"
	@echo "  build              - Build the binary"
	@echo "  build-agentd       - Build agentd binary"
	@echo "  build-all          - Build all binaries"
	@echo "  build-test-tunnel  - Build test-tunnel tool"
	@echo "  install            - Install to /usr/local/bin"
	@echo ""
	@echo "Development targets:"
	@echo "  run                - Run in development mode"
	@echo "  run-local          - Run with local kubeconfig"
	@echo "  test               - Run tests"
	@echo "  fmt                - Format code"
	@echo "  lint               - Run linter"
	@echo "  clean              - Clean build artifacts"
	@echo "  deps               - Download dependencies"
	@echo "  update-deps        - Update dependencies"
	@echo ""
	@echo "Docker targets (agentcube-apiserver):"
	@echo "  docker-build       - Build Docker image (current platform)"
	@echo "  docker-buildx      - Build multi-arch image (amd64, arm64)"
	@echo "  docker-buildx-push - Build and push multi-arch image (requires IMAGE_REGISTRY)"
	@echo "  docker-push        - Push Docker image (requires IMAGE_REGISTRY)"
	@echo ""
	@echo "Sandbox targets:"
	@echo "  sandbox-build      - Build sandbox image (current platform)"
	@echo "  sandbox-buildx     - Build multi-arch sandbox (amd64, arm64)"
	@echo "  sandbox-buildx-push - Build and push multi-arch sandbox (requires IMAGE_REGISTRY)"
	@echo "  sandbox-push       - Push sandbox image to registry (requires IMAGE_REGISTRY)"
	@echo "  sandbox-test       - Test sandbox image locally"
	@echo "  sandbox-test-stop  - Stop sandbox test container"
	@echo "  sandbox-kind-load  - Load sandbox image to kind"
	@echo ""
	@echo "Kubernetes targets:"
	@echo "  k8s-deploy         - Deploy to Kubernetes"
	@echo "  k8s-delete         - Delete from Kubernetes"
	@echo "  k8s-logs           - Show pod logs"
	@echo "  kind-load          - Load agentcube-apiserver image to kind cluster"
	@echo ""
	@echo "Test targets:"
	@echo "  test-tunnel        - Test tunnel connection (requires SESSION_ID)"
	@echo "  test-tunnel-build  - Build and test tunnel connection"
	@echo ""
	@echo "Variables:"
	@echo "  APISERVER_IMAGE    - agentcube-apiserver image name (default: agentcube-apiserver:latest)"
	@echo "  SANDBOX_IMAGE      - Sandbox image name (default: sandbox:latest)"
	@echo "  IMAGE_REGISTRY     - Container registry URL (required for push targets)"
	@echo "  API_URL            - API URL for tests (default: http://localhost:8080)"
	@echo "  SESSION_ID         - Session ID for tunnel tests"
	@echo "  TOKEN              - Auth token for API requests"
	@echo ""
	@echo "Examples:"
	@echo "  make docker-build APISERVER_IMAGE=my-api:v1.0"
	@echo "  make docker-buildx-push IMAGE_REGISTRY=docker.io/myuser"
	@echo "  make sandbox-buildx-push IMAGE_REGISTRY=ghcr.io/myorg SANDBOX_IMAGE=sandbox:v2"

