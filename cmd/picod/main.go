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

	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 8080, "Port for the PicoD server to listen on")
	workspace := flag.String("workspace", "", "Root directory for file operations (default: current working directory)")

	// Initialize klog flags
	klog.InitFlags(nil)
	flag.Parse()

	config := picod.Config{
		Port:      *port,
		Workspace: *workspace,
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
