// cmd/picod/main_test.go
package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/klog/v2"
)

func TestParsePicodFlags(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		createFiles      map[string]string
		wantPort         int
		wantWorkspace    string
		expectReadKeyErr bool
	}{
		{
			name:             "defaults - should fail to read non-existent default key",
			args:             []string{},
			wantPort:         8080,
			wantWorkspace:    "",
			expectReadKeyErr: true,
		},
		{
			name: "custom values with valid key file",
			args: []string{
				"-port=9090",
				"-workspace=/tmp/test",
			},
			createFiles: map[string]string{
				"key.pem": "fake public key content\n-----BEGIN PUBLIC KEY-----\nfakekey\n-----END PUBLIC KEY-----",
			},
			wantPort:         9090,
			wantWorkspace:    "/tmp/test",
			expectReadKeyErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global flag state
			flag.CommandLine = flag.NewFlagSet("picod-test", flag.ContinueOnError)
			klog.InitFlags(nil)

			tmpDir := t.TempDir()

			// Create files in temp dir
			var bootstrapPath string
			for fname, content := range tt.createFiles {
				path := filepath.Join(tmpDir, fname)
				require.NoError(t, os.WriteFile(path, []byte(content), 0644))
				if fname == "key.pem" {
					bootstrapPath = path // remember full path
				}
			}

			// Build args — use **absolute path** for bootstrap-key
			args := append([]string{"picod"}, tt.args...)
			if bootstrapPath != "" {
				args = append(args, "-bootstrap-key="+bootstrapPath)
			}
			os.Args = args

			// Define flags exactly as in real main.go
			port := flag.Int("port", 8080, "")
			bootstrapKeyFile := flag.String("bootstrap-key", "/etc/picod/public-key.pem", "")
			workspace := flag.String("workspace", "", "")

			flag.Parse()

			// Simulate main.go key reading
			var keyContent []byte
			var keyErr error
			if *bootstrapKeyFile != "" {
				keyContent, keyErr = os.ReadFile(*bootstrapKeyFile)
			}

			// Assertions
			assert.Equal(t, tt.wantPort, *port, "port mismatch")
			assert.Equal(t, tt.wantWorkspace, *workspace, "workspace mismatch")

			if tt.expectReadKeyErr {
				assert.Error(t, keyErr, "expected error reading bootstrap key")
			} else {
				assert.NoError(t, keyErr, "unexpected error reading bootstrap key")
				assert.NotEmpty(t, keyContent, "should have read key content")
				assert.Contains(t, string(keyContent), "BEGIN PUBLIC KEY", "key content should contain expected PEM header")
			}
		})
	}
}