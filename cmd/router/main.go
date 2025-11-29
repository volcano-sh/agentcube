package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/volcano-sh/agentcube/pkg/router"
)

func main() {
	var (
		port                  = flag.String("port", "8080", "Router API server port")
		enableTLS             = flag.Bool("enable-tls", false, "Enable TLS (HTTPS)")
		tlsCert               = flag.String("tls-cert", "", "Path to TLS certificate file")
		tlsKey                = flag.String("tls-key", "", "Path to TLS key file")
		debug                 = flag.Bool("debug", true, "Enable debug mode")
		maxConcurrentRequests = flag.Int("max-concurrent-requests", 1000, "Maximum number of concurrent requests")
		requestTimeout        = flag.Int("request-timeout", 30, "Request timeout in seconds")
		maxIdleConns          = flag.Int("max-idle-conns", 100, "Maximum number of idle connections")
		maxConnsPerHost       = flag.Int("max-conns-per-host", 10, "Maximum number of connections per host")
	)

	// Parse command line flags
	flag.Parse()

	// Create Router API server configuration
	config := &router.Config{
		Port: *port,
		SandboxEndpoints: []string{
			"http://sandbox-1:8080",
			"http://sandbox-2:8080",
			"http://sandbox-3:8080",
		}, // Default sandbox endpoints, can be configured via env vars
		Debug:                 *debug,
		EnableTLS:             *enableTLS,
		TLSCert:               *tlsCert,
		TLSKey:                *tlsKey,
		MaxConcurrentRequests: *maxConcurrentRequests,
		RequestTimeout:        *requestTimeout,
		MaxIdleConns:          *maxIdleConns,
		MaxConnsPerHost:       *maxConnsPerHost,
	}

	// Create Router API server
	server, err := router.NewServer(config)
	if err != nil {
		log.Fatalf("Failed to create Router API server: %v", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start Router API server in goroutine
	errCh := make(chan error, 1)
	go func() {
		log.Printf("Starting agentcube Router server on port %s", *port)
		if err := server.Start(ctx); err != nil {
			errCh <- err
		}
	}()

	// Wait for signal or error
	select {
	case <-sigCh:
		log.Println("Received shutdown signal, shutting down gracefully...")
		cancel()
		time.Sleep(2 * time.Second) // Give server time to shutdown gracefully
	case err := <-errCh:
		log.Fatalf("Server error: %v", err)
	}

	log.Println("Router server stopped")
}
