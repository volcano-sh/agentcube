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

package router

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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog/v2"
)

const (
	// RSA key size for JWT signing
	rsaKeySize = 2048
	// JWT token expiration time
	jwtExpiration = 5 * time.Minute
	// IdentitySecretName is the name of the secret storing Router's keys
	IdentitySecretName = "picod-router-identity" //nolint:gosec // This is a name reference, not a credential
	// PrivateKeyDataKey is the key in the secret data map for private key
	PrivateKeyDataKey = "private.pem"
	// PublicKeyDataKey is the key in the secret data map for public key
	PublicKeyDataKey = "public.pem"
)

// IdentityNamespace is the namespace for the identity secret
var IdentityNamespace = "default"

func init() {
	if ns := os.Getenv("PICOD_ROUTER_NAMESPACE"); ns != "" {
		IdentityNamespace = ns
	}
}

// JWTManager handles JWT token generation and key management for Router
type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	clientset  kubernetes.Interface
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
		"iss": "agentcube-router",
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

// TryStoreOrLoadJWTKeySecret tries to create identity resources or loads existing ones.
// It stores private key in Secret and public key in ConfigMap.
// If not running in K8s cluster, it will just use the generated keys without persistence.
func (jm *JWTManager) TryStoreOrLoadJWTKeySecret(ctx context.Context) error {
	// Initialize K8s client if not set
	if jm.clientset == nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			// Not running in K8s cluster, use generated keys without persistence
			klog.Warningf("Not running in Kubernetes cluster, JWT keys will not be persisted: %v", err)
			return nil
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		jm.clientset = clientset
	}

	// Get public and private key PEM
	publicKeyPEM, err := jm.GetPublicKeyPEM()
	if err != nil {
		return fmt.Errorf("failed to get public key: %w", err)
	}
	privateKeyPEM := jm.GetPrivateKeyPEM()

	// Try to create or load the identity Secret (both keys)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IdentitySecretName,
			Namespace: IdentityNamespace,
			Labels: map[string]string{
				"app":       "agentcube",
				"component": "router",
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			PrivateKeyDataKey: privateKeyPEM,
			PublicKeyDataKey:  publicKeyPEM,
		},
	}

	_, err = jm.clientset.CoreV1().Secrets(IdentityNamespace).Create(ctx, secret, metav1.CreateOptions{})
	if err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create identity secret: %w", err)
		}

		// Secret already exists, load private key from it
		existingSecret, err := jm.clientset.CoreV1().Secrets(IdentityNamespace).Get(ctx, IdentitySecretName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get identity secret: %w", err)
		}

		privateKeyPEMInSecret, ok := existingSecret.Data[PrivateKeyDataKey]
		if !ok {
			return fmt.Errorf("private key data not found in identity secret")
		}

		// Parse and load the existing keys
		if err := jm.loadPrivateKeyPEM(privateKeyPEMInSecret); err != nil {
			return fmt.Errorf("failed to load private key from secret: %w", err)
		}

		klog.Infof("Loaded identity from existing secret %s/%s", IdentityNamespace, IdentitySecretName)
	} else {
		klog.Infof("Created identity secret %s/%s", IdentityNamespace, IdentitySecretName)
	}

	return nil
}

// loadPrivateKeyPEM loads a private key from PEM bytes
func (jm *JWTManager) loadPrivateKeyPEM(privateKeyPEM []byte) error {
	block, _ := pem.Decode(privateKeyPEM)
	if block == nil {
		return fmt.Errorf("failed to decode private key PEM block")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	jm.privateKey = privateKey
	jm.publicKey = &privateKey.PublicKey
	return nil
}
