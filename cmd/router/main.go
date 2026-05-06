/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/mtls"
	"github.com/volcano-sh/agentcube/pkg/router"
)

func main() {
	var (
		port                  = flag.String("port", "8080", "Router API server port")
		enableTLS             = flag.Bool("enable-tls", false, "Enable TLS (HTTPS)")
		tlsCert               = flag.String("tls-cert", "", "Path to TLS certificate file")
		tlsKey                = flag.String("tls-key", "", "Path to TLS key file")
		debug                 = flag.Bool("debug", false, "Enable debug mode")
		maxConcurrentRequests = flag.Int("max-concurrent-requests", 1000, "Maximum number of concurrent requests that a router server can handle (0 = unlimited)")
		enableMTLS            = flag.Bool("enable-mtls", false, "Enable mutual TLS for Router outbound connections")
		picodAuthMode         = flag.String("picod-auth-mode", "mtls", "Authentication mode for Router->PicoD: 'mtls' (default) or 'jwt'")

		// mTLS flags (certificate source abstraction)
		mtlsCertFile, mtlsKeyFile, mtlsCAFile string
	)

	// Initialize klog flags
	klog.InitFlags(nil)

	flag.StringVar(&mtlsCertFile, "mtls-cert-file", "", "Path to mTLS certificate file")
	flag.StringVar(&mtlsKeyFile, "mtls-key-file", "", "Path to mTLS private key file")
	flag.StringVar(&mtlsCAFile, "mtls-ca-file", "", "Path to mTLS CA bundle file")

	// Parse command line flags
	flag.Parse()

	// Validate mTLS configuration early (fail fast on bad flags)
	mTLSCfg := mtls.Config{
		CertFile: mtlsCertFile,
		KeyFile:  mtlsKeyFile,
		CAFile:   mtlsCAFile,
	}
	if err := mTLSCfg.Validate(); err != nil {
		klog.Fatalf("Invalid mTLS configuration: %v", err)
	}

	// Create Router API server configuration
	// Validate picod-auth-mode
	switch *picodAuthMode {
	case router.PicodAuthModeMTLS, router.PicodAuthModeJWT:
		// valid
	default:
		klog.Fatalf("invalid --picod-auth-mode %q: must be %q or %q", *picodAuthMode, router.PicodAuthModeMTLS, router.PicodAuthModeJWT)
	}

	config := &router.Config{
		Port:                  *port,
		Debug:                 *debug,
		EnableTLS:             *enableTLS,
		TLSCert:               *tlsCert,
		TLSKey:                *tlsKey,
		MaxConcurrentRequests: *maxConcurrentRequests,
		EnableMTLS:            *enableMTLS,
		PicodAuthMode:         *picodAuthMode,
		MTLSCertFile:          mtlsCertFile,
		MTLSKeyFile:           mtlsKeyFile,
		MTLSCAFile:            mtlsCAFile,
	}

	// Create Router API server
	server, err := router.NewServer(config)
	if err != nil {
		klog.Fatalf("Failed to create Router API server: %v", err)
	}

	// Setup signal handling with context cancellation
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start Router API server in goroutine
	errCh := make(chan error, 1)
	go func() {
		klog.Infof("Starting agentcube Router server on port %s", *port)
		if err := server.Start(ctx); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or error
	select {
	case <-ctx.Done():
		klog.Info("Received shutdown signal, shutting down gracefully...")
		// Cancel the context to trigger server shutdown
		cancel()
		// Wait for server goroutine to exit after graceful shutdown is complete
		<-errCh
	case err := <-errCh:
		klog.Fatalf("Server error: %v", err)
	}

	klog.Info("Router server stopped")
}
