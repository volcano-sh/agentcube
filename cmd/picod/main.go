package main

import (
	"flag"
	"log"
	"os"

	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 8080, "Port for the PicoD server to listen on")
	bootstrapKeyFile := flag.String("bootstrap-key", "/etc/picod/public-key.pem", "Path to the bootstrap public key file")
	workspace := flag.String("workspace", "", "Root directory for file operations (default: current working directory)")
	flag.Parse()

	// Read bootstrap key from file
	var bootstrapKey string
	if data, err := os.ReadFile(*bootstrapKeyFile); err == nil {
		bootstrapKey = string(data)
	} else {
		// Only log if the user explicitly provided a flag that failed,
		// or if we want to warn about missing default.
		// Since we want strict security, logging a warning is good.
		log.Printf("Warning: Could not read bootstrap key from %s: %v", *bootstrapKeyFile, err)
	}

	config := picod.Config{
		Port:         *port,
		BootstrapKey: bootstrapKey,
		Workspace:    *workspace,
	}

	// Create and start server
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
