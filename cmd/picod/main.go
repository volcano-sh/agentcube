package main

import (
	"flag"
	"os"

	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 8080, "Port for the PicoD server to listen on")
	bootstrapKeyFile := flag.String("bootstrap-key", "/etc/picod/public-key.pem", "Path to the bootstrap public key file")
	workspace := flag.String("workspace", "", "Root directory for file operations (default: current working directory)")
	authMode := flag.String("auth-mode", "dynamic", "Authentication mode: dynamic (default) or static")
	staticPublicKeyFile := flag.String("static-public-key-file", "", "Path to the static public key file (required if auth-mode is static)")

	// Initialize klog flags
	klog.InitFlags(nil)
	flag.Parse()

	// Read bootstrap key from file
	var bootstrapKey []byte
	if data, err := os.ReadFile(*bootstrapKeyFile); err == nil {
		bootstrapKey = data
	} else {
		klog.Fatalf("Failed to read bootstrap key from %s: %v", *bootstrapKeyFile, err)
	}

	config := picod.Config{
		Port:                *port,
		BootstrapKey:        bootstrapKey,
		Workspace:           *workspace,
		AuthMode:            *authMode,
		StaticPublicKeyFile: *staticPublicKeyFile,
	}

	// Create and start server
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		klog.Fatalf("Failed to start server: %v", err)
	}
}
