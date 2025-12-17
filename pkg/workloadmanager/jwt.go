package workloadmanager

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
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
	// JWTKeySecretName is the name of the secret storing JWT key
	JWTKeySecretName = "agentcube-jwt-key" //nolint:gosec // This is a name reference, not a credential
	// JWTPublicKeyDataKey is the key in the secret data map
	JWTPublicKeyDataKey = "public-key.pem"
	// JWTPrivateKeyDataKey is the key in the secret data map for private key
	JWTPrivateKeyDataKey = "private-key.pem"
	// JWTKeyVolumeName the name of JWT key volume
	JWTKeyVolumeName = "jwt-key"
)

// JWTKeySecretNamespace is the namespace for the JWT key secret
var JWTKeySecretNamespace = "default"

func init() {
	if ns := os.Getenv("JWT_KEY_SECRET_NAMESPACE"); ns != "" {
		JWTKeySecretNamespace = ns
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

// NewJWTManagerWithPEM creates a new JWT manager with specify RSA key pair
func NewJWTManagerWithPEM(publicKeyPEM, privateKeyPEM []byte) (*JWTManager, error) {
	blockPublicKey, _ := pem.Decode(publicKeyPEM)
	if blockPublicKey == nil {
		return nil, fmt.Errorf("failed to decode public key PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(blockPublicKey.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key is not an valid RSA public key")
	}

	blockPrivateKey, _ := pem.Decode(privateKeyPEM)
	if blockPrivateKey == nil {
		return nil, fmt.Errorf("failed to decode private key PEM block")
	}

	pri, err := x509.ParsePKCS1PrivateKey(blockPrivateKey.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return &JWTManager{
		privateKey: pri,
		publicKey:  rsaPub,
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
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	return pubKeyPEM, nil
}

// GetPrivateKeyPEM returns the private key in PEM format
func (jm *JWTManager) GetPrivateKeyPEM() []byte {
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(jm.privateKey)

	privateKeyPem := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	})

	return privateKeyPem
}

// TryStoreOrLoadJWTKeySecret try stores the JWT public key and private key in a Kubernetes secret
// reset JWTManager if secret already exists
//
// Currently, the JWT key secret is stored in a single namespace (default
// or from JWT_KEY_SECRET_NAMESPACE). This causes a multi-tenancy issue
// because CodeInterpreter sandboxes can be created in any namespace by users,
// but they expect the secret to be mounted from their own namespace. This will
// be addressed in a future update to ensure the key is available in all
// necessary namespaces.
func (s *Server) TryStoreOrLoadJWTKeySecret(ctx context.Context) error {
	// Store JWT key in Kubernetes secret
	publicKeyPEM, err := s.jwtManager.GetPublicKeyPEM()
	if err != nil {
		return fmt.Errorf("failed to get JWT public key: %w", err)
	}
	privateKeyPEM := s.jwtManager.GetPrivateKeyPEM()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      JWTKeySecretName,
			Namespace: JWTKeySecretNamespace,
			Labels: map[string]string{
				"app":       "agentcube",
				"component": "workload-manager",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			JWTPublicKeyDataKey:  publicKeyPEM,
			JWTPrivateKeyDataKey: privateKeyPEM,
		},
	}

	// Try to create secret
	_, err = s.k8sClient.clientset.CoreV1().Secrets(JWTKeySecretNamespace).Create(
		ctx,
		secret,
		metav1.CreateOptions{},
	)
	if err == nil {
		// create successfully and return
		log.Printf("Successfully create JWT key secret %s/%s", secret.Namespace, secret.Name)
		return nil
	}

	// create failed with not an already existing error, return err directly
	if !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create JWT key secret: %w", err)
	}

	// secret already exists, get and reset JWTManager by keys in secret
	jwtKeySecret, err := s.k8sClient.clientset.CoreV1().Secrets(JWTKeySecretNamespace).Get(
		ctx,
		JWTKeySecretName,
		metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("failed to get JWT key secret: %w", err)
	}

	publicKeyPEMInSecret, ok := jwtKeySecret.Data[JWTPublicKeyDataKey]
	if !ok {
		return fmt.Errorf("failed to get public key data from JWT key secret")
	}

	privateKeyPEMInSecret, ok := jwtKeySecret.Data[JWTPrivateKeyDataKey]
	if !ok {
		return fmt.Errorf("failed to get private key data from JWT key secret")
	}

	jwtManager, err := NewJWTManagerWithPEM(publicKeyPEMInSecret, privateKeyPEMInSecret)
	if err != nil {
		return fmt.Errorf("new JWT manager with PEM failed: %w", err)
	}

	s.jwtManager = jwtManager
	log.Printf("Reset JWT manager by secret %s/%s successfully", secret.Namespace, secret.Name)
	return nil
}
