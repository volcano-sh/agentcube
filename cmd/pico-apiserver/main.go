package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	picoapiserver "github.com/agent-box/pico-apiserver/pkg/pico-apiserver"
)

func main() {
	var (
		port        = flag.String("port", "8080", "API server port")
		kubeconfig  = flag.String("kubeconfig", "", "Path to kubeconfig file")
		namespace   = flag.String("namespace", "default", "Kubernetes namespace for sandboxes")
		sshUsername = flag.String("ssh-username", "sandbox", "Default SSH username for sandbox pods")
		sshPort     = flag.Int("ssh-port", 22, "SSH port on sandbox pods")
		enableTLS   = flag.Bool("enable-tls", false, "Enable TLS (HTTPS)")
		tlsCert     = flag.String("tls-cert", "", "Path to TLS certificate file")
		tlsKey      = flag.String("tls-key", "", "Path to TLS key file")
		jwtSecret   = flag.String("jwt-secret", "", "JWT secret for token validation (optional)")
	)
	flag.Parse()

	// Create API server configuration
	config := &picoapiserver.Config{
		Port:        *port,
		Kubeconfig:  *kubeconfig,
		Namespace:   *namespace,
		SSHUsername: *sshUsername,
		SSHPort:     *sshPort,
		EnableTLS:   *enableTLS,
		TLSCert:     *tlsCert,
		TLSKey:      *tlsKey,
		JWTSecret:   *jwtSecret,
	}

	// Create and initialize API server
	server, err := picoapiserver.NewServer(config)
	if err != nil {
		log.Fatalf("Failed to create API server: %v", err)
	}

	// Start the server
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		log.Printf("Starting pico-apiserver on port %s", *port)
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

	fmt.Println("Server stopped")
}
