package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/agent-box/pico-apiserver/pkg/picolet"
)

func main() {
	var (
		port       = flag.String("port", "9090", "Picolet server port")
		kubeconfig = flag.String("kubeconfig", "", "Path to kubeconfig file")
	)
	flag.Parse()

	picoletInstance, err := picolet.NewPicolet(":"+*port, *kubeconfig)
	if err != nil {
		log.Fatalf("Failed to create picolet: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	errCh := make(chan error, 1)

	go func() {
		if err := picoletInstance.Start(ctx); err != nil {
			errCh <- err
		}
	}()

	log.Printf("Picolet started on port %s", *port)

	select {
	case <-sigCh:
		log.Println("Received shutdown signal")
	case err := <-errCh:
		log.Printf("Picolet error: %v", err)
	}

	cancel()
	log.Println("Picolet stopped")
}
