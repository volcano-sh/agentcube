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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	controller "github.com/agent-box/pico-apiserver/pkg/controller"
	picoapiserver "github.com/agent-box/pico-apiserver/pkg/pico-apiserver"
)

var (
	schemeBuilder = runtime.NewScheme()
)

func init() {
	utilruntime.Must(scheme.AddToScheme(schemeBuilder))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(schemeBuilder))
}

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

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

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
	// Setup controller manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: schemeBuilder,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "unable to start manager: %v\n", err)
		os.Exit(1)
	}

	sandboxReconciler := &controller.SandboxReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}

	if err := setupControllers(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "unable to setup controllers: %v\n", err)
		os.Exit(1)
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "problem running manager: %v\n", err)
		os.Exit(1)
	}

	// Create and initialize API server
	server, err := picoapiserver.NewServer(config, sandboxReconciler)
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

func setupControllers(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sandboxv1alpha1.Sandbox{}).
		Complete(&controller.SandboxReconciler{
			Client: mgr.GetClient(),
			Scheme: mgr.GetScheme(),
		})
}
