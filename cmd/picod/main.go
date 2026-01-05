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
	"flag"
	"os"

	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 8080, "Port for the PicoD server to listen on")
	bootstrapKeyFile := flag.String("bootstrap-key", "/etc/picod/public-key.pem", "Path to the bootstrap public key file")
	workspace := flag.String("workspace", "", "Root directory for file operations (default: current working directory)")

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
		Port:         *port,
		BootstrapKey: bootstrapKey,
		Workspace:    *workspace,
	}

	// Create and start server
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		klog.Fatalf("Failed to start server: %v", err)
	}
}
