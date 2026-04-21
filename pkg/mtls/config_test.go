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

func TestConfig_Validate(t *testing.T) {
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
			name: "all three paths is valid",
			config: Config{
				CertFile: "/path/cert.pem",
				KeyFile:  "/path/key.pem",
				CAFile:   "/path/ca.pem",
			},
			wantErr: false,
		},
		{
			name: "only one path returns error",
			config: Config{
				CertFile: "/path/cert.pem",
			},
			wantErr:   true,
			errSubstr: "must all be specified together",
		},
		{
			name: "two paths returns error",
			config: Config{
				CertFile: "/path/cert.pem",
				KeyFile:  "/path/key.pem",
			},
			wantErr:   true,
			errSubstr: "must all be specified together",
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

func TestConfig_Enabled(t *testing.T) {
	tests := []struct {
		name     string
		certFile string
		want     bool
	}{
		{"empty cert file is disabled", "", false},
		{"non-empty cert file is enabled", "/path/cert.pem", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{CertFile: tt.certFile}
			if got := cfg.Enabled(); got != tt.want {
				t.Errorf("Enabled() = %v, want %v", got, tt.want)
			}
		})
	}
}
