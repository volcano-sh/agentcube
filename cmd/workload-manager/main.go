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
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/workloadmanager"
)

var (
	schemeBuilder = runtime.NewScheme()
)

func init() {
	utilruntime.Must(scheme.AddToScheme(schemeBuilder))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(schemeBuilder))
	utilruntime.Must(extensionsv1alpha1.AddToScheme(schemeBuilder))
	utilruntime.Must(runtimev1alpha1.AddToScheme(schemeBuilder))
}

func main() {
	var (
		port             = flag.String("port", "8080", "API server port")
		runtimeClassName = flag.String("runtime-class-name", "kuasar-vmm", "RuntimeClassName for sandbox pods")
		enableTLS        = flag.Bool("enable-tls", false, "Enable TLS (HTTPS)")
		tlsCert          = flag.String("tls-cert", "", "Path to TLS certificate file")
		tlsKey           = flag.String("tls-key", "", "Path to TLS key file")
		enableAuth       = flag.Bool("enable-auth", false, "Enable Authentication")
	)

	// Initialize klog flags
	klog.InitFlags(nil)

	// Parse command line flags
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: schemeBuilder,
		Metrics: metricsserver.Options{
			BindAddress: "0", // Disable metrics server
		},
		HealthProbeBindAddress: "0", // Disable health probe server
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to start manager: %v\n", err)
		os.Exit(1)
	}

	sandboxReconciler := &workloadmanager.SandboxReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	codeInterpreterReconciler := &workloadmanager.CodeInterpreterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	if err := setupControllers(mgr, sandboxReconciler, codeInterpreterReconciler); err != nil {
		fmt.Fprintf(os.Stderr, "unable to setup controllers: %v\n", err)
		os.Exit(1)
	}

	// Create API server configuration
	config := &workloadmanager.Config{
		Port:             *port,
		RuntimeClassName: *runtimeClassName,
		EnableTLS:        *enableTLS,
		TLSCert:          *tlsCert,
		TLSKey:           *tlsKey,
		EnableAuth:       *enableAuth,
	}

	// Create and initialize API server
	server, err := workloadmanager.NewServer(config, sandboxReconciler)
	if err != nil {
		klog.Fatalf("Failed to create API server: %v", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start controller manager in goroutine
	go func() {
		klog.Info("Starting controller manager...")
		if err := mgr.Start(ctx); err != nil {
			klog.Fatalf("Controller manager error: %v", err)
		}
	}()

	// Start API server in goroutine
	errCh := make(chan error, 1)
	go func() {
		klog.Infof("Starting workloadmanager on port %s", *port)
		if err := server.Start(ctx); err != nil {
			errCh <- err
		}
	}()

	// Wait for signal or error
	select {
	case <-sigCh:
		klog.Info("Received shutdown signal, shutting down gracefully...")
		cancel()
		time.Sleep(2 * time.Second) // Give server time to shutdown gracefully
	case err := <-errCh:
		klog.Fatalf("Server error: %v", err)
	}

	klog.Info("Server stopped")
}

func setupControllers(mgr ctrl.Manager, sandboxReconciler *workloadmanager.SandboxReconciler, codeInterpreterReconciler *workloadmanager.CodeInterpreterReconciler) error {
	// Setup Sandbox controller
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.Sandbox{}).
		Complete(sandboxReconciler); err != nil {
		return fmt.Errorf("unable to create sandbox controller: %w", err)
	}

	// Setup CodeInterpreter controller
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.CodeInterpreter{}).
		Complete(codeInterpreterReconciler); err != nil {
		return fmt.Errorf("unable to create codeinterpreter controller: %w", err)
	}

	return nil
}
