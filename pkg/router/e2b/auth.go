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

package e2b

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	contextKeyTenant   = "e2b_tenant"
	apiKeyHeader       = "X-API-Key" //nolint:gosec // Header name, not a secret.
	bearerPrefix       = "Bearer "
	identityHeaderName = "X-AgentCube-User-Identity"
)

// TenantIdentity identifies the authenticated caller behind an E2B API key.
type TenantIdentity struct {
	OwnerID   string
	Namespace string
	ClientID  string
}

// IdentitySigner signs short-lived caller identity JWTs accepted by WorkloadManager.
type IdentitySigner interface {
	GenerateToken(claims map[string]interface{}) (string, error)
}

// Authenticator validates E2B API keys using SHA-256 key hashes.
type Authenticator struct {
	mu               sync.RWMutex
	keys             map[string]TenantIdentity
	defaultNamespace string
}

// NewAuthenticator creates an empty Authenticator with the fallback namespace.
func NewAuthenticator(defaultNamespace string) *Authenticator {
	if defaultNamespace == "" {
		defaultNamespace = defaultNamespaceName
	}
	return &Authenticator{
		keys:             make(map[string]TenantIdentity),
		defaultNamespace: defaultNamespace,
	}
}

// HashAPIKey returns the hex-encoded SHA-256 digest of a raw API key.
func HashAPIKey(rawKey string) string {
	sum := sha256.Sum256([]byte(rawKey))
	return hex.EncodeToString(sum[:])
}

// LoadDevAPIKeys parses dev-only "<raw-key>=<ownerId>[:<namespace>]" entries,
// hashes each raw key immediately, and merges them into the authenticator.
func (a *Authenticator) LoadDevAPIKeys(spec string) error {
	if strings.TrimSpace(spec) == "" {
		return nil
	}
	entries := make(map[string]TenantIdentity)
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		rawKey, target, ok := strings.Cut(item, "=")
		rawKey = strings.TrimSpace(rawKey)
		if !ok || rawKey == "" {
			return fmt.Errorf("invalid dev api key entry %q: expected key=ownerId[:namespace]", item)
		}
		id, err := parseTenantSpec(target, a.defaultNamespace)
		if err != nil {
			return fmt.Errorf("invalid dev api key target %q: %w", target, err)
		}
		entries[HashAPIKey(rawKey)] = id
	}

	a.mu.Lock()
	for k, v := range entries {
		a.keys[k] = v
	}
	a.mu.Unlock()
	klog.Warning("E2B_DEV_API_KEYS is active; do not use plaintext env API keys in production")
	return nil
}

// LoadFromSecret loads SHA-256 hashed API keys ("<sha256hex>: <ownerId>[:<namespace>]")
// from a Kubernetes Secret. Missing Secret is non-fatal (fails closed on all E2B API calls).
func (a *Authenticator) LoadFromSecret(ctx context.Context, clientset kubernetes.Interface, namespace, secretName string) error {
	if clientset == nil || secretName == "" {
		return nil
	}
	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, secretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			klog.V(2).Infof("E2B API key secret %s/%s not found; E2B endpoints will fail closed", namespace, secretName)
			return nil
		}
		return fmt.Errorf("read API key secret %s/%s: %w", namespace, secretName, err)
	}

	entries := make(map[string]TenantIdentity, len(secret.Data))
	for k, v := range secret.Data {
		hashKey := strings.ToLower(strings.TrimSpace(k))
		if !isSHA256Hex(hashKey) {
			klog.Warningf("Skipping non-SHA256 key %q in secret %s/%s", k, namespace, secretName)
			continue
		}
		id, err := parseTenantSpec(string(v), a.defaultNamespace)
		if err != nil {
			return fmt.Errorf("invalid tenant mapping for key %q in secret %s/%s: %w", k, namespace, secretName, err)
		}
		entries[hashKey] = id
	}

	a.mu.Lock()
	a.keys = entries
	a.mu.Unlock()
	klog.Infof("Loaded %d hashed E2B API key entries from secret %s/%s", len(entries), namespace, secretName)
	return nil
}

// Validate checks a raw API key against the SHA-256 hashed key map.
func (a *Authenticator) Validate(rawKey string) (TenantIdentity, bool) {
	if strings.TrimSpace(rawKey) == "" {
		return TenantIdentity{}, false
	}
	digest := HashAPIKey(strings.TrimSpace(rawKey))
	a.mu.RLock()
	id, ok := a.keys[digest]
	a.mu.RUnlock()
	return id, ok
}

// APIKeyMiddleware enforces E2B API key authentication on Platform API routes.
func (a *Authenticator) APIKeyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rawKey := strings.TrimSpace(c.GetHeader(apiKeyHeader))
		if rawKey == "" {
			if authHdr := strings.TrimSpace(c.GetHeader("Authorization")); strings.HasPrefix(authHdr, bearerPrefix) {
				rawKey = strings.TrimSpace(strings.TrimPrefix(authHdr, bearerPrefix))
			}
		}
		if rawKey == "" {
			respondError(c, http.StatusUnauthorized, "missing API key")
			c.Abort()
			return
		}
		tenant, ok := a.Validate(rawKey)
		if !ok {
			respondError(c, http.StatusUnauthorized, "invalid API key")
			c.Abort()
			return
		}
		c.Set(contextKeyTenant, tenant)
		c.Next()
	}
}

func tenantFromContext(c *gin.Context) (TenantIdentity, bool) {
	val, ok := c.Get(contextKeyTenant)
	if !ok {
		return TenantIdentity{}, false
	}
	tenant, ok := val.(TenantIdentity)
	return tenant, ok
}

func parseTenantSpec(raw, defaultNs string) (TenantIdentity, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return TenantIdentity{}, fmt.Errorf("ownerId is required")
	}
	ownerID := strings.TrimSpace(parts[0])
	ns := defaultNs
	if len(parts) >= 2 && strings.TrimSpace(parts[1]) != "" {
		ns = strings.TrimSpace(parts[1])
	}
	return TenantIdentity{
		OwnerID:   ownerID,
		Namespace: ns,
		ClientID:  shortClientID(ownerID),
	}, nil
}

func shortClientID(ownerID string) string {
	sum := sha256.Sum256([]byte(ownerID))
	return hex.EncodeToString(sum[:])[:12]
}

func isSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func generateRandomToken(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
