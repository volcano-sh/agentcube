.PHONY: build run clean test deps

# 构建目标
build:
	@echo "Building pico-apiserver..."
	go build -o bin/pico-apiserver ./cmd/pico-apiserver

# 运行服务器（开发模式）
run:
	@echo "Running pico-apiserver..."
	go run ./cmd/pico-apiserver/main.go \
		--port=8080 \
		--namespace=default \
		--ssh-username=sandbox \
		--ssh-port=22

# 运行服务器（使用 kubeconfig）
run-local:
	@echo "Running pico-apiserver with local kubeconfig..."
	go run ./cmd/pico-apiserver/main.go \
		--port=8080 \
		--kubeconfig=${HOME}/.kube/config \
		--namespace=default \
		--ssh-username=sandbox \
		--ssh-port=22

# 清理构建文件
clean:
	@echo "Cleaning..."
	rm -rf bin/
	rm -f pico-apiserver

# 安装依赖
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# 更新依赖
update-deps:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy

# 运行测试
test:
	@echo "Running tests..."
	go test -v ./...

# 代码格式化
fmt:
	@echo "Formatting code..."
	go fmt ./...

# 代码检查
lint:
	@echo "Running linter..."
	golangci-lint run ./...

# 构建 Docker 镜像
docker-build:
	@echo "Building Docker image..."
	docker build -t pico-apiserver:latest .

# 安装到系统
install: build
	@echo "Installing pico-apiserver..."
	sudo cp bin/pico-apiserver /usr/local/bin/

# 显示帮助信息
help:
	@echo "Available targets:"
	@echo "  build        - Build the binary"
	@echo "  run          - Run in development mode"
	@echo "  run-local    - Run with local kubeconfig"
	@echo "  clean        - Clean build artifacts"
	@echo "  deps         - Download dependencies"
	@echo "  update-deps  - Update dependencies"
	@echo "  test         - Run tests"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  docker-build - Build Docker image"
	@echo "  install      - Install to /usr/local/bin"
	@echo "  help         - Show this help message"

