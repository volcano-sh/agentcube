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
	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 8080, "Port for the PicoD server to listen on")
	workspace := flag.String("workspace", "", "Root directory for file operations (default: current working directory)")
	enableMTLS := flag.Bool("enable-mtls", false, "Enable mutual TLS on the PicoD listener")

	// mTLS flags (certificate source abstraction)
	var mtlsCertFile, mtlsKeyFile, mtlsCAFile string
	flag.StringVar(&mtlsCertFile, "mtls-cert-file", "", "Path to mTLS certificate file")
	flag.StringVar(&mtlsKeyFile, "mtls-key-file", "", "Path to mTLS private key file")
	flag.StringVar(&mtlsCAFile, "mtls-ca-file", "", "Path to mTLS CA bundle file")

	// Initialize klog flags
	klog.InitFlags(nil)
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

	if *enableMTLS && !mTLSCfg.Enabled() {
		klog.Fatalf("Invalid mTLS configuration: --enable-mtls requires --mtls-cert-file, --mtls-key-file, and --mtls-ca-file")
	}

	config := picod.Config{
		Port:         *port,
		Workspace:    *workspace,
		EnableMTLS:   *enableMTLS,
		MTLSCertFile: mtlsCertFile,
		MTLSKeyFile:  mtlsKeyFile,
		MTLSCAFile:   mtlsCAFile,
	}

	// Create server
	server := picod.NewServer(config)

	// Setup signal handling with context cancellation
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Start PicoD server in goroutine
	errCh := make(chan error, 1)
	go func() {
		klog.Infof("Starting PicoD server on port %d", *port)
		if err := server.Start(ctx); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for signal or fatal error
	select {
	case <-ctx.Done():
		klog.Info("Received shutdown signal, shutting down gracefully...")
		cancel()
		<-errCh
	case err := <-errCh:
		klog.Fatalf("Server error: %v", err)
	}

	klog.Info("PicoD server stopped")
}
