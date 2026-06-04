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

package workloadmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	mathrand "math/rand"
	"time"

	"github.com/google/uuid"

	"github.com/golang-jwt/jwt/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	bootstrapKeySize    = 2048
	bootstrapSecretName = "agentcube-bootstrap-identity"
	bootstrapPrivKeyKey = "bootstrap-private.pem"
	bootstrapPubKeyKey  = "bootstrap-public.pem"
	// bootstrapJWTExpiration is the base expiration. PicoD's VerifyBootstrapJWT adds a
	// 1-minute leeway (jwt.WithLeeway(time.Minute)), making the effective replay window 3 minutes.
	bootstrapJWTExpiration = 2 * time.Minute
	bootstrapJWTIssuer     = "agentcube-workload-manager"
)

// BootstrapAuthManager owns the bootstrap keypair used to sign /init JWTs.
// It must be instantiated via NewBootstrapAuthManager and held on Server —
// never shared as a package-level global, which would break parallel tests
// and multi-instance deployments.
type BootstrapAuthManager struct {
	privateKey   *rsa.PrivateKey
	publicKeyPEM string
	namespace    string
}

// NewBootstrapAuthManager loads the bootstrap keypair from a Kubernetes Secret,
// or generates and persists a new one if the Secret does not exist yet.
// Persisting the keypair means it survives Workload Manager restarts, preventing
// stranded PicoD pods whose containers already received the old public key via
// PICOD_BOOTSTRAP_PUBLIC_KEY.
func NewBootstrapAuthManager(ctx context.Context, clientset kubernetes.Interface, namespace string) (*BootstrapAuthManager, error) {
	m := &BootstrapAuthManager{namespace: namespace}

	const maxRetries = 10
	for attempt := 0; attempt < maxRetries; attempt++ {
		secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, bootstrapSecretName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("failed to get bootstrap secret: %w", err)
		}

		if err == nil {
			// Secret already exists — load the persisted keypair.
			privPEM, ok := secret.Data[bootstrapPrivKeyKey]
			if !ok {
				return nil, fmt.Errorf("bootstrap secret %s/%s is missing key %q",
					namespace, bootstrapSecretName, bootstrapPrivKeyKey)
			}
			if err := m.loadPrivKeyPEM(privPEM); err != nil {
				return nil, fmt.Errorf("failed to parse bootstrap private key from secret: %w", err)
			}
			// Derive the public key PEM from the parsed private key instead of
			// trusting the stored bootstrap-public.pem. This prevents a corrupted
			// or manually edited secret from causing a priv/pub key mismatch that
			// would silently break all /init handshakes.
			pubKeyBytes, err := x509.MarshalPKIXPublicKey(&m.privateKey.PublicKey)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal bootstrap public key: %w", err)
			}
			m.publicKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes}))
			klog.Infof("Loaded bootstrap keypair from existing secret %s/%s", namespace, bootstrapSecretName)
			return m, nil
		}

		// Secret does not exist — generate a new keypair and persist it.
		privKey, err := rsa.GenerateKey(rand.Reader, bootstrapKeySize)
		if err != nil {
			return nil, fmt.Errorf("failed to generate bootstrap RSA key: %w", err)
		}
		m.privateKey = privKey

		pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal bootstrap public key: %w", err)
		}
		pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})
		m.publicKeyPEM = string(pubPEM)

		privPEM := pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privKey),
		})

		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      bootstrapSecretName,
				Namespace: namespace,
				Labels:    map[string]string{"app": "agentcube", "component": "workload-manager"},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				bootstrapPrivKeyKey: privPEM,
				bootstrapPubKeyKey:  pubPEM,
			},
		}
		if _, createErr := clientset.CoreV1().Secrets(namespace).Create(ctx, newSecret, metav1.CreateOptions{}); createErr != nil {
			// Another replica won the race and created the secret first — try loading it again.
			if apierrors.IsAlreadyExists(createErr) {
				select {
				//nolint:gosec // weak random is acceptable for backoff jitter
				case <-time.After(time.Duration(100+mathrand.Intn(100)) * time.Millisecond):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return nil, fmt.Errorf("failed to create bootstrap secret: %w", createErr)
		}

		klog.Infof("Generated new bootstrap keypair and persisted to secret %s/%s", namespace, bootstrapSecretName)
		return m, nil
	}

	return nil, fmt.Errorf("failed to initialize bootstrap auth manager after %d attempts due to concurrent creation races", maxRetries)
}

// PublicKeyPEM returns the bootstrap public key in PEM format.
// Inject this value as PICOD_BOOTSTRAP_PUBLIC_KEY into PicoD container
// environments so PicoD can verify /init JWTs at startup.
func (m *BootstrapAuthManager) PublicKeyPEM() string {
	return m.publicKeyPEM
}

// GetEncryptionKey returns a SHA-256 hash of the raw DER-encoded private key,
// suitable for use as an AES-256 encryption key. Using raw DER bytes (not PEM)
// ensures the hash is stable even if the PEM encoding is normalized by GitOps
// tooling (e.g. trailing newlines, whitespace differences).
func (m *BootstrapAuthManager) GetEncryptionKey() []byte {
	der := x509.MarshalPKCS1PrivateKey(m.privateKey)
	hash := sha256.Sum256(der)
	return hash[:]
}

// GenerateInitJWT creates a short-lived JWT signed by the bootstrap private key.
// The JWT carries the session_public_key claim so PicoD can store it and use it
// to verify all subsequent user-request JWTs for this sandbox session.
// The "sub" claim is set to sessionID so PicoD can bind the key to the correct session.
func (m *BootstrapAuthManager) GenerateInitJWT(sessionID, sessionPubPEM string) (string, error) {
	claims := jwt.MapClaims{
		"iss":                bootstrapJWTIssuer,
		"sub":                sessionID,
		"exp":                jwt.NewNumericDate(time.Now().Add(bootstrapJWTExpiration)),
		"iat":                jwt.NewNumericDate(time.Now()),
		"session_public_key": sessionPubPEM,
		"jti":                uuid.New().String(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign init JWT: %w", err)
	}
	return signed, nil
}

// loadPrivKeyPEM parses a PKCS#1 PEM-encoded RSA private key into m.privateKey.
func (m *BootstrapAuthManager) loadPrivKeyPEM(data []byte) error {
	block, _ := pem.Decode(data)
	if block == nil {
		return fmt.Errorf("failed to decode PEM block from bootstrap private key data")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse PKCS1 private key: %w", err)
	}
	m.privateKey = key
	return nil
}

// GenerateSessionKeyPair generates a unique ECDSA P-256 keypair for one sandbox session.
// The private key is persisted to the KV store (via StoreSessionPrivateKey) so the Router
// can retrieve it later to sign JWTs for this session.
// The public key is delivered to PicoD via the /init JWT so PicoD can verify
// all subsequent user-request JWTs signed by the Router for this session.
func GenerateSessionKeyPair() (privPEM string, pubPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate session ECDSA key: %w", err)
	}

	privBytes, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal session private key: %w", err)
	}

	privPEMBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privBytes,
	})

	pubBytes, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal session public key: %w", err)
	}
	pubPEMBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes})

	return string(privPEMBytes), string(pubPEMBytes), nil
}
