package workloadmanager

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// RSA key size for JWT signing
	rsaKeySize = 2048
	// JWT token expiration time
	jwtExpiration = 5 * time.Minute
	// JWTPublicKeySecretName is the name of the secret storing JWT public key
	JWTPublicKeySecretName = "agentcube-jwt-public-key"
	// JWTPublicKeyDataKey is the key in the secret data map
	JWTPublicKeyDataKey = "public-key.pem"
)

// JWTPublicKeySecretNamespace is the namespace for the JWT public key secret
var JWTPublicKeySecretNamespace = "default"

func init() {
	if ns := os.Getenv("JWT_PUBLIC_KEY_SECRET_NAMESPACE"); ns != "" {
		JWTPublicKeySecretNamespace = ns
	}
}

// JWTManager handles JWT token generation and key management
type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// NewJWTManager creates a new JWT manager with a fresh RSA key pair
func NewJWTManager() (*JWTManager, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, rsaKeySize)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key pair: %w", err)
	}

	return &JWTManager{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
	}, nil
}

// GenerateToken generates a JWT token with the given claims
func (jm *JWTManager) GenerateToken(claims map[string]interface{}) (string, error) {
	// Create JWT claims
	jwtClaims := jwt.MapClaims{
		"exp": time.Now().Add(jwtExpiration).Unix(),
		"iat": time.Now().Unix(),
	}

	// Add custom claims
	for k, v := range claims {
		jwtClaims[k] = v
	}

	// Create token
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwtClaims)

	// Sign token with private key
	tokenString, err := token.SignedString(jm.privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign JWT token: %w", err)
	}

	return tokenString, nil
}

// GetPublicKeyPEM returns the public key in PEM format
func (jm *JWTManager) GetPublicKeyPEM() ([]byte, error) {
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(jm.publicKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal public key: %w", err)
	}

	pubKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	return pubKeyPEM, nil
}

// GetPrivateKeyPEM returns the private key in PEM format (for debugging/backup purposes)
func (jm *JWTManager) GetPrivateKeyPEM() ([]byte, error) {
	privKeyBytes := x509.MarshalPKCS1PrivateKey(jm.privateKey)
	privKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privKeyBytes,
	})

	return privKeyPEM, nil
}

// StoreJWTPublicKeyInSecret stores the JWT public key in a Kubernetes secret
// If the secret already exists, it will be updated
func (c *K8sClient) StoreJWTPublicKeyInSecret(ctx context.Context, publicKeyPEM []byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      JWTPublicKeySecretName,
			Namespace: JWTPublicKeySecretNamespace,
			Labels: map[string]string{
				"app":       "agentcube",
				"component": "workload-manager",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			JWTPublicKeyDataKey: publicKeyPEM,
		},
	}

	// Try to get existing secret
	existingSecret, err := c.clientset.CoreV1().Secrets(JWTPublicKeySecretNamespace).Get(
		ctx,
		JWTPublicKeySecretName,
		metav1.GetOptions{},
	)

	if err != nil {
		if apierrors.IsNotFound(err) {
			// Secret doesn't exist, create it
			_, err = c.clientset.CoreV1().Secrets(JWTPublicKeySecretNamespace).Create(
				ctx,
				secret,
				metav1.CreateOptions{},
			)
			if err != nil {
				return fmt.Errorf("failed to create JWT public key secret: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get JWT public key secret: %w", err)
	}

	// Secret exists, update it
	existingSecret.Data = secret.Data
	_, err = c.clientset.CoreV1().Secrets(JWTPublicKeySecretNamespace).Update(
		ctx,
		existingSecret,
		metav1.UpdateOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to update JWT public key secret: %w", err)
	}

	return nil
}
