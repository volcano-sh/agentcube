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

	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/mtls"
	"github.com/volcano-sh/agentcube/pkg/picod"
)

func main() {
	port := flag.Int("port", 8080, "Port for the PicoD server to listen on")
	workspace := flag.String("workspace", "", "Root directory for file operations (default: current working directory)")
	enableMTLS := flag.Bool("enable-mtls", false, "Enable mutual TLS on the PicoD listener")

	// mTLS flags (certificate source abstraction)
	var mtlsCertFile, mtlsKeyFile, mtlsCAFile string
	flag.StringVar(&mtlsCertFile, "mtls-cert-file", "", "Path to mTLS certificate file")
	flag.StringVar(&mtlsKeyFile, "mtls-key-file", "", "Path to mTLS private key file")
	flag.StringVar(&mtlsCAFile, "mtls-ca-file", "", "Path to mTLS CA bundle file")

	// Initialize klog flags
	klog.InitFlags(nil)
	flag.Parse()

	// Validate mTLS configuration early (fail fast on bad flags)
	mTLSCfg := mtls.Config{
		CertFile: mtlsCertFile,
		KeyFile:  mtlsKeyFile,
		CAFile:   mtlsCAFile,
	}
	if err := mTLSCfg.Validate(); err != nil {
		klog.Fatalf("Invalid mTLS configuration: %v", err)
	}

	config := picod.Config{
		Port:           *port,
		Workspace:      *workspace,
		EnableMTLS:     *enableMTLS,
		MTLSCertFile:   mtlsCertFile,
		MTLSKeyFile:    mtlsKeyFile,
		MTLSCAFile:     mtlsCAFile,
	}

	// Create and start server
	server := picod.NewServer(config)

	if err := server.Run(); err != nil {
		klog.Fatalf("Failed to start server: %v", err)
	}
}
