package mtls

import "fmt"

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

// Enabled returns true if mTLS certificate paths are configured.
func (c *Config) Enabled() bool {
	return c.CertFile != ""
}

// Validate checks that the configuration is internally consistent.
// If any path is provided, all three must be specified together.
func (c *Config) Validate() error {
	paths := []string{c.CertFile, c.KeyFile, c.CAFile}
	set := 0
	for _, p := range paths {
		if p != "" {
			set++
		}
	}
	if set > 0 && set < 3 {
		return fmt.Errorf("--mtls-cert-file, --mtls-key-file, and --mtls-ca-file must all be specified together")
	}
	return nil
}
