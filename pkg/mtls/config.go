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
	"fmt"
	"os"
)

// Config holds the mTLS certificate file paths for a component.
// The code is certificate-source agnostic — it simply loads from the provided paths,
// regardless of whether SPIRE, cert-manager, or a self-signed CA provisioned them.
type Config struct {
	// CertFile is the path to the mTLS certificate (PEM).
	CertFile string
	// KeyFile is the path to the mTLS private key (PEM).
	KeyFile string
	// CAFile is the path to the mTLS CA bundle for peer verification (PEM).
	CAFile string
}

// Enabled returns true if all mTLS certificate paths are configured.
func (c *Config) Enabled() bool {
	return c.CertFile != "" && c.KeyFile != "" && c.CAFile != ""
}

// Validate checks that the configuration is internally consistent and that
// all referenced files exist on disk. If any path is provided, all three
// must be specified together.
func (c *Config) Validate() error {
	paths := map[string]string{
		"cert": c.CertFile,
		"key":  c.KeyFile,
		"ca":   c.CAFile,
	}

	// Check all-or-nothing: if any path is set, all must be set.
	set := 0
	for _, p := range paths {
		if p != "" {
			set++
		}
	}
	if set > 0 && set < 3 {
		return fmt.Errorf("cert, key, and ca must all be specified together")
	}

	// If none are set, nothing to validate.
	if set == 0 {
		return nil
	}

	// Verify that each referenced file exists.
	for name, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("mTLS %s file %q not found: %w", name, p, err)
		}
	}

	return nil
}
