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
	"strings"
	"time"
)

// DefaultCertificateFileWaitTimeout bounds the startup race while spiffe-helper writes the initial SVID files.
// spiffe-helper remains a sidecar so it can rotate certificates after startup.
const DefaultCertificateFileWaitTimeout = 30 * time.Second

// WaitForCertificateFiles waits until the configured cert, key, and CA files exist.
func WaitForCertificateFiles(cfg Config, timeout time.Duration) error {
	if cfg.CertFile == "" || cfg.KeyFile == "" || cfg.CAFile == "" {
		return fmt.Errorf("mTLS requires cert, key, and CA file paths to be provided (got cert=%q, key=%q, ca=%q)", cfg.CertFile, cfg.KeyFile, cfg.CAFile)
	}

	deadline := time.Now().Add(timeout)
	var missing []string
	for {
		exist, currentMissing, err := certificateFilesStatus(cfg)
		if err != nil {
			return fmt.Errorf("failed to access mTLS cert/key/CA files: %w", err)
		}
		if exist {
			return nil
		}
		missing = currentMissing
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for mTLS cert/key/CA files: missing %s", strings.Join(missing, ", "))
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func certificateFilesStatus(cfg Config) (bool, []string, error) {
	var missing []string
	for _, path := range []string{cfg.CertFile, cfg.KeyFile, cfg.CAFile} {
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				missing = append(missing, path)
				continue
			}
			return false, nil, fmt.Errorf("stat %q: %w", path, err)
		}
	}
	return len(missing) == 0, missing, nil
}
