package main

import (
	"flag"
	"log"

	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 9527, "Port for the PicoD server to listen on")
	flag.Parse()

	config := picod.Config{
		Port: *port,
	}

	// Create and start server
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
