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
	var bootstrapKey []byte
	if data, err := os.ReadFile(*bootstrapKeyFile); err == nil {
		bootstrapKey = data
	} else {
		log.Fatalf("Failed to read bootstrap key from %s: %v", *bootstrapKeyFile, err)
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
