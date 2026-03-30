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
	"strings"
	"testing"
)

func TestCertSourceConfig_Validate(t *testing.T) {
	tests := []struct {
		name      string
		config    CertSourceConfig
		wantErr   bool
		errSubstr string // if wantErr, error message must contain this
	}{
		{
			name: "empty source is valid (mTLS disabled)",
			config: CertSourceConfig{
				Source: CertSourceNone,
			},
			wantErr: false,
		},
		{
			name: "spire without paths is valid (defaults will be used)",
			config: CertSourceConfig{
				Source: CertSourceSPIRE,
			},
			wantErr: false,
		},
		{
			name: "spire with custom paths is valid",
			config: CertSourceConfig{
				Source:   CertSourceSPIRE,
				CertFile: "/custom/cert.pem",
				KeyFile:  "/custom/key.pem",
				CAFile:   "/custom/ca.pem",
			},
			wantErr: false,
		},
		{
			name: "file with all paths is valid",
			config: CertSourceConfig{
				Source:   CertSourceFile,
				CertFile: "/path/cert.pem",
				KeyFile:  "/path/key.pem",
				CAFile:   "/path/ca.pem",
			},
			wantErr: false,
		},
		{
			name: "file missing cert returns error",
			config: CertSourceConfig{
				Source:  CertSourceFile,
				KeyFile: "/path/key.pem",
				CAFile:  "/path/ca.pem",
			},
			wantErr:   true,
			errSubstr: "--mtls-cert-file",
		},
		{
			name: "file missing key returns error",
			config: CertSourceConfig{
				Source:   CertSourceFile,
				CertFile: "/path/cert.pem",
				CAFile:   "/path/ca.pem",
			},
			wantErr:   true,
			errSubstr: "--mtls-key-file",
		},
		{
			name: "file missing ca returns error",
			config: CertSourceConfig{
				Source:   CertSourceFile,
				CertFile: "/path/cert.pem",
				KeyFile:  "/path/key.pem",
			},
			wantErr:   true,
			errSubstr: "--mtls-ca-file",
		},
		{
			name: "invalid source returns error",
			config: CertSourceConfig{
				Source: CertSource("invalid"),
			},
			wantErr:   true,
			errSubstr: "invalid --mtls-cert-source",
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

func TestCertSourceConfig_Enabled(t *testing.T) {
	tests := []struct {
		name   string
		source CertSource
		want   bool
	}{
		{"empty source is disabled", CertSourceNone, false},
		{"spire source is enabled", CertSourceSPIRE, true},
		{"file source is enabled", CertSourceFile, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &CertSourceConfig{Source: tt.source}
			if got := cfg.Enabled(); got != tt.want {
				t.Errorf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultSPIREPaths(t *testing.T) {
	cfg := DefaultSPIREPaths()

	if cfg.Source != CertSourceSPIRE {
		t.Errorf("Source = %q, want %q", cfg.Source, CertSourceSPIRE)
	}
	if cfg.CertFile != "/run/spire/certs/svid.pem" {
		t.Errorf("CertFile = %q, want /run/spire/certs/svid.pem", cfg.CertFile)
	}
	if cfg.KeyFile != "/run/spire/certs/svid_key.pem" {
		t.Errorf("KeyFile = %q, want /run/spire/certs/svid_key.pem", cfg.KeyFile)
	}
	if cfg.CAFile != "/run/spire/certs/svid_bundle.pem" {
		t.Errorf("CAFile = %q, want /run/spire/certs/svid_bundle.pem", cfg.CAFile)
	}
}
