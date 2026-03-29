package mtls

import "fmt"

// CertSource defines the certificate provisioning mode.
type CertSource string

const (
    // CertSourceNone means mTLS is disabled (default).
    CertSourceNone CertSource = ""
    // CertSourceSPIRE means certs are written to disk by spiffe-helper.
    CertSourceSPIRE CertSource = "spire"
    // CertSourceFile means certs are loaded from user-specified paths.
    CertSourceFile CertSource = "file"
)

// CertSourceConfig holds the certificate source configuration for a component.
type CertSourceConfig struct {
    // Source is the certificate provisioning mode: "", "spire", or "file".
    Source CertSource
    // CertFile is the path to the mTLS certificate (PEM).
    CertFile string
    // KeyFile is the path to the mTLS private key (PEM).
    KeyFile string
    // CAFile is the path to the mTLS CA bundle for peer verification (PEM).
    CAFile string
}

// Enabled returns true if a certificate source is configured.
func (c *CertSourceConfig) Enabled() bool {
    return c.Source != CertSourceNone
}

// Validate checks that the configuration is internally consistent.
func (c *CertSourceConfig) Validate() error {
    switch c.Source {
    case CertSourceNone:
        return nil
    case CertSourceSPIRE:
        // SPIRE mode: spiffe-helper writes to well-known paths.
        // CertFile/KeyFile/CAFile are optional overrides; spiffe-helper
        // defaults are /run/spire/certs/{svid.pem, svid_key.pem, svid_bundle.pem}.
        return nil
    case CertSourceFile:
        if c.CertFile == "" || c.KeyFile == "" || c.CAFile == "" {
            return fmt.Errorf("--mtls-cert-source=file requires --mtls-cert-file, --mtls-key-file, and --mtls-ca-file")
        }
        return nil
    default:
        return fmt.Errorf("invalid --mtls-cert-source value %q (must be \"spire\" or \"file\")", c.Source)
    }
}

// DefaultSPIREPaths returns CertSourceConfig with spiffe-helper's default output paths.
func DefaultSPIREPaths() CertSourceConfig {
    return CertSourceConfig{
        Source:   CertSourceSPIRE,
        CertFile: "/run/spire/certs/svid.pem",
        KeyFile:  "/run/spire/certs/svid_key.pem",
        CAFile:   "/run/spire/certs/svid_bundle.pem",
    }
}
