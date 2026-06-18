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
	"container/list"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
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
	IdentitySecretName = "agentcube-bootstrap-identity" //nolint:gosec // This is a name reference, not a credential
	// PrivateKeyDataKey is the key in the secret data map for private key
	PrivateKeyDataKey = "bootstrap-private.pem"
	// PublicKeyDataKey is the key in the secret data map for public key
	PublicKeyDataKey = "bootstrap-public.pem"
)

// IdentityNamespace is the namespace for the identity secret
var IdentityNamespace = "default"

func init() {
	if ns := os.Getenv("AGENTCUBE_NAMESPACE"); ns != "" {
		IdentityNamespace = ns
	}
}

type cacheEntry struct {
	sessionID string
	privKey   *ecdsa.PrivateKey
}

// JWTManager handles JWT token generation and key management for Router
type JWTManager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	clientset  kubernetes.Interface
	keyCache   map[string]*list.Element
	evictList  *list.List
	cacheMu    sync.RWMutex
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
		keyCache:   make(map[string]*list.Element),
		evictList:  list.New(),
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

const keyCacheMaxSize = 1000

// GetCachedKey retrieves the parsed ECDSA private key from the LRU cache.
// It returns the key and true if found, moving the entry to the front of the LRU list.
func (jm *JWTManager) GetCachedKey(sessionID string) (*ecdsa.PrivateKey, bool) {
	jm.cacheMu.RLock()
	elem, ok := jm.keyCache[sessionID]
	var privKey *ecdsa.PrivateKey
	if ok {
		if entry, ok := elem.Value.(*cacheEntry); ok {
			privKey = entry.privKey
		}
	}
	isFront := ok && jm.evictList.Front() == elem
	jm.cacheMu.RUnlock()

	if !ok {
		return nil, false
	}

	// If the element is already at the front, we don't need to acquire the write lock to move it
	if isFront {
		return privKey, privKey != nil
	}

	jm.cacheMu.Lock()
	defer jm.cacheMu.Unlock()

	// Double check after acquiring write lock
	elem, ok = jm.keyCache[sessionID]
	if !ok {
		return nil, false
	}

	// Only move to front if it's not already at the front
	if jm.evictList.Front() != elem {
		jm.evictList.MoveToFront(elem)
	}

	if entry, ok := elem.Value.(*cacheEntry); ok {
		privKey = entry.privKey
	}
	return privKey, privKey != nil
}

var ErrKeyNotCached = errors.New("private key PEM is required when key is not cached")

// GenerateTokenWithKey generates a JWT token signed with a specific PEM-encoded
// private key (used for per-session PicoD auth). It caches the parsed *ecdsa.PrivateKey
// to avoid expensive PEM decoding and parsing on every request.
func (jm *JWTManager) GenerateTokenWithKey(sessionID string, claims map[string]interface{}, privateKeyPEM string) (string, error) {
	privKey, ok := jm.GetCachedKey(sessionID)

	if !ok {
		if privateKeyPEM == "" {
			return "", ErrKeyNotCached
		}
		parsedKey, err := jwt.ParseECPrivateKeyFromPEM([]byte(privateKeyPEM))
		if err != nil {
			return "", fmt.Errorf("failed to parse private key: %w", err)
		}
		privKey = parsedKey

		// Store in cache
		jm.cacheMu.Lock()
		// Double-check after acquiring write lock
		if cachedElem, ok := jm.keyCache[sessionID]; ok {
			if entry, ok := cachedElem.Value.(*cacheEntry); ok {
				privKey = entry.privKey
			}
			jm.evictList.MoveToFront(cachedElem)
		} else {
			if len(jm.keyCache) >= keyCacheMaxSize {
				oldest := jm.evictList.Back()
				if oldest != nil {
					jm.evictList.Remove(oldest)
					if entry, ok := oldest.Value.(*cacheEntry); ok {
						delete(jm.keyCache, entry.sessionID)
					}
				}
			}
			newElem := jm.evictList.PushFront(&cacheEntry{sessionID: sessionID, privKey: privKey})
			jm.keyCache[sessionID] = newElem
		}
		jm.cacheMu.Unlock()
	}

	jwtClaims := jwt.MapClaims{
		"exp": jwt.NewNumericDate(time.Now().Add(jwtExpiration)),
		"iat": jwt.NewNumericDate(time.Now()),
		"iss": "agentcube-router",
	}
	for k, v := range claims {
		jwtClaims[k] = v
	}

	// Generate Token with ECDSA private key
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwtClaims)
	tokenString, err := token.SignedString(privKey)
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

// WaitForAndLoadBootstrapSecret tries to load the bootstrap identity resources.
// It waits for Workload Manager to create it if it doesn't exist.
// If not running in K8s cluster, it will just use the generated keys without persistence.
func (jm *JWTManager) WaitForAndLoadBootstrapSecret(ctx context.Context, storeClient interface{ SetEncryptionKey([]byte) error }) error {
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

	// Try to load the identity Secret
	var existingSecret *corev1.Secret
	var err error
	for i := 0; i < 30; i++ {
		existingSecret, err = jm.clientset.CoreV1().Secrets(IdentityNamespace).Get(ctx, IdentitySecretName, metav1.GetOptions{})
		if err == nil {
			break
		}
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get identity secret: %w", err)
		}
		klog.Infof("Waiting for %s/%s secret to be created...", IdentityNamespace, IdentitySecretName)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	if existingSecret == nil {
		return fmt.Errorf("timed out waiting for identity secret")
	}

	privateKeyPEMInSecret, ok := existingSecret.Data[PrivateKeyDataKey]
	if !ok {
		return fmt.Errorf("private key data not found in identity secret")
	}

	// Parse and load the existing keys
	if err := jm.loadPrivateKeyPEM(privateKeyPEMInSecret); err != nil {
		return fmt.Errorf("failed to load private key from secret: %w", err)
	}

	if storeClient != nil {
		// Hash the raw DER bytes (not the PEM string) so the encryption key is
		// stable even if the PEM is re-encoded with minor formatting differences
		// (e.g. by ArgoCD or kubectl). This must match GetEncryptionKey() in the
		// WorkloadManager's BootstrapAuthManager.
		block, _ := pem.Decode(privateKeyPEMInSecret)
		if block == nil {
			return fmt.Errorf("failed to decode private key PEM for encryption key derivation")
		}
		hash := sha256.Sum256(block.Bytes)
		if err := storeClient.SetEncryptionKey(hash[:]); err != nil {
			return fmt.Errorf("failed to set store encryption key: %w", err)
		}
	}

	klog.Infof("Loaded identity from existing secret %s/%s", IdentityNamespace, IdentitySecretName)
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
