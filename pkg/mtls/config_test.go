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

package mtls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig_Validate(t *testing.T) {
	// Create temp files for valid-path tests
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "cert.pem")
	keyFile := filepath.Join(tmpDir, "key.pem")
	caFile := filepath.Join(tmpDir, "ca.pem")
	for _, f := range []string{certFile, keyFile, caFile} {
		if err := os.WriteFile(f, []byte("test"), 0600); err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
	}

	tests := []struct {
		name      string
		config    Config
		wantErr   bool
		errSubstr string
	}{
		{
			name:    "no paths is valid (mTLS disabled)",
			config:  Config{},
			wantErr: false,
		},
		{
			name: "all three paths with existing files is valid",
			config: Config{
				CertFile: certFile,
				KeyFile:  keyFile,
				CAFile:   caFile,
			},
			wantErr: false,
		},
		{
			name: "only one path returns error",
			config: Config{
				CertFile: certFile,
			},
			wantErr:   true,
			errSubstr: "must all be specified together",
		},
		{
			name: "two paths returns error",
			config: Config{
				CertFile: certFile,
				KeyFile:  keyFile,
			},
			wantErr:   true,
			errSubstr: "must all be specified together",
		},
		{
			name: "non-existent files returns error",
			config: Config{
				CertFile: "/nonexistent/cert.pem",
				KeyFile:  "/nonexistent/key.pem",
				CAFile:   "/nonexistent/ca.pem",
			},
			wantErr:   true,
			errSubstr: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
					return
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("Validate() error = %q, want substring %q", err.Error(), tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("Validate() unexpected error: %v", err)
				}
			}
		})
	}
}
