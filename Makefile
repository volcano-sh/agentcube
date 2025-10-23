.PHONY: build run clean test deps

# Build targets
build:
	@echo "Building pico-apiserver..."
	go build -o bin/pico-apiserver ./cmd/pico-apiserver

# Run server (development mode)
run:
	@echo "Running pico-apiserver..."
	go run ./cmd/pico-apiserver/main.go \
		--port=8080 \
		--namespace=default \
		--ssh-username=sandbox \
		--ssh-port=22

# Run server (with kubeconfig)
run-local:
	@echo "Running pico-apiserver with local kubeconfig..."
	go run ./cmd/pico-apiserver/main.go \
		--port=8080 \
		--kubeconfig=${HOME}/.kube/config \
		--namespace=default \
		--ssh-username=sandbox \
		--ssh-port=22

# Clean build artifacts
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f pico-apiserver

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

# Build Docker image
docker-build:
	@echo "Building Docker image..."
	docker build -t pico-apiserver:latest .

# Install to system
install: build
	@echo "Installing pico-apiserver..."
	sudo cp bin/pico-apiserver /usr/local/bin/

# Docker and Kubernetes targets
docker-build:
	@echo "Building Docker image..."
	docker build -t pico-apiserver:latest .

docker-push:
	@echo "Pushing Docker image..."
	docker push pico-apiserver:latest

k8s-deploy:
	@echo "Deploying to Kubernetes..."
	kubectl apply -f k8s/pico-apiserver.yaml

k8s-delete:
	@echo "Deleting from Kubernetes..."
	kubectl delete -f k8s/pico-apiserver.yaml

k8s-logs:
	@echo "Showing logs..."
	kubectl logs -n pico -l app=pico-apiserver -f

# Load image to kind cluster
kind-load:
	@echo "Loading image to kind..."
	kind load docker-image pico-apiserver:latest

# Show help message
help:
	@echo "Available targets:"
	@echo "  build         - Build the binary"
	@echo "  run           - Run in development mode"
	@echo "  run-local     - Run with local kubeconfig"
	@echo "  clean         - Clean build artifacts"
	@echo "  deps          - Download dependencies"
	@echo "  update-deps   - Update dependencies"
	@echo "  test          - Run tests"
	@echo "  fmt           - Format code"
	@echo "  lint          - Run linter"
	@echo "  docker-build  - Build Docker image"
	@echo "  docker-push   - Push Docker image"
	@echo "  k8s-deploy    - Deploy to Kubernetes"
	@echo "  k8s-delete    - Delete from Kubernetes"
	@echo "  k8s-logs      - Show pod logs"
	@echo "  k8s-restart   - Restart deployment"
	@echo "  kind-load     - Load image to kind cluster"
	@echo "  install       - Install to /usr/local/bin"
	@echo "  help          - Show this help message"

