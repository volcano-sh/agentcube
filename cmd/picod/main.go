package main

import (
	"flag"

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

	// Create and start server
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		klog.Fatalf("Failed to start server: %v", err)
	}
}
