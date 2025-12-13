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

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
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
	)

	// Parse command line flags
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	// Setup controller manager
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
	}

	// Create and initialize API server
	server, err := workloadmanager.NewServer(config, sandboxReconciler)
	if err != nil {
		log.Fatalf("Failed to create API server: %v", err)
	}

	// Setup signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Start controller manager in goroutine
	go func() {
		log.Println("Starting controller manager...")
		if err := mgr.Start(ctx); err != nil {
			log.Fatalf("Controller manager error: %v", err)
		}
	}()

	// Start API server in goroutine
	errCh := make(chan error, 1)
	go func() {
		log.Printf("Starting workloadmanager on port %s", *port)
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

	log.Println("Server stopped")
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
