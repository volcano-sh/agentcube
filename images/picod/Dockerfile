# Build stage
FROM golang:1.24.4 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o picod ./cmd/picod

# Run stage
FROM ubuntu:24.04

WORKDIR /root/

COPY --from=builder /app/picod .

ENTRYPOINT ["./picod"]