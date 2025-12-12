# Build stage
FROM golang:1.24.4 AS builder

# Build arguments for multi-architecture support
ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -a -installsuffix cgo -o picod ./cmd/picod

# Run stage
FROM ubuntu:24.04

# Install Python3 to support code execution tasks (Code Interpreter)
RUN apt-get update && apt-get install -y python3

# Use /root/ as the working directory
# We run as root to allow 'chattr +i' on the public key file (see pkg/picod/auth.go)
# and to ensure sufficient permissions for arbitrary code execution within the sandbox.
WORKDIR /root/

COPY --from=builder /app/picod .

ENTRYPOINT ["./picod"]