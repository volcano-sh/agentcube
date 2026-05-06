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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// APIKeyCacheEntry holds the cached metadata for an API key.
type APIKeyCacheEntry struct {
	Status    string
	Namespace string
	Hash      string
}

// AuthConfig holds authentication configuration
type AuthConfig struct {
	// APIKeySecret is the Kubernetes secret name containing API key statuses
	APIKeySecret string
	// APIKeySecretNamespace is the namespace where the secret is stored
	APIKeySecretNamespace string
	// APIKeyConfigMap is the Kubernetes ConfigMap name for API key namespace mapping
	APIKeyConfigMap string
}

// DefaultAuthConfig returns default auth configuration
func DefaultAuthConfig() *AuthConfig {
	return &AuthConfig{
		APIKeySecret:          "e2b-api-keys",
		APIKeySecretNamespace: "agentcube-system",
		APIKeyConfigMap:       "e2b-api-key-config",
	}
}

// Authenticator handles API key authentication
type Authenticator struct {
	config            *AuthConfig
	apiKeys           map[string]*APIKeyCacheEntry // hash -> entry
	mu                sync.RWMutex
	k8sClient         kubernetes.Interface // Kubernetes client for informer
	informer          cache.SharedInformer // Secret informer for watching API key changes
	configMapInformer cache.SharedInformer // ConfigMap informer for watching namespace mapping changes
	stopCh            chan struct{}        // Channel to stop the informer
	started           bool                 // Whether the informer has started

	// Background refresh fields (fallback for informer)
	refreshTicker *time.Ticker  // Ticker for periodic background refresh
	refreshDone   chan struct{} // Channel to signal background refresh stop
	ctrlClient    client.Client // Controller-runtime client for background refresh

	// Rate limiter for cache miss protection (prevents brute-force amplification)
	rateLimiter *RateLimiter
}

// NewAuthenticator creates a new Authenticator instance
func NewAuthenticator(config *AuthConfig) *Authenticator {
	if config == nil {
		config = DefaultAuthConfig()
	}
	return &Authenticator{
		config:  config,
		apiKeys: make(map[string]*APIKeyCacheEntry),
	}
}

// NewAuthenticatorWithMap creates an Authenticator with a pre-defined API key map (for testing)
// The provided API keys are automatically hashed using SHA-256 before storage
func NewAuthenticatorWithMap(apiKeys map[string]string) *Authenticator {
	hashedKeys := make(map[string]*APIKeyCacheEntry, len(apiKeys))
	for apiKey, namespace := range apiKeys {
		hashedKeys[hashKey(apiKey)] = &APIKeyCacheEntry{
			Status:    "valid",
			Namespace: namespace,
			Hash:      hashKey(apiKey),
		}
	}
	return &Authenticator{
		config:  DefaultAuthConfig(),
		apiKeys: hashedKeys,
		stopCh:  make(chan struct{}),
	}
}

// NewAuthenticatorWithK8s creates a new Authenticator with Kubernetes client
// This enables the informer-based cache for API keys
func NewAuthenticatorWithK8s(config *AuthConfig, k8sClient kubernetes.Interface) *Authenticator {
	if config == nil {
		config = DefaultAuthConfig()
	}
	return &Authenticator{
		config:      config,
		apiKeys:     make(map[string]*APIKeyCacheEntry),
		k8sClient:   k8sClient,
		stopCh:      make(chan struct{}),
		rateLimiter: NewRateLimiter(1.0, 1), // 1 request per second, burst of 1
	}
}

// NewAuthenticatorWithK8sClient creates a new Authenticator with Kubernetes client (alias for testing)
// Deprecated: Use NewAuthenticatorWithK8s instead
func NewAuthenticatorWithK8sClient(config *AuthConfig, k8sClient kubernetes.Interface) *Authenticator {
	return NewAuthenticatorWithK8s(config, k8sClient)
}

// APIKeyMiddleware returns a Gin middleware that validates API keys
func (a *Authenticator) APIKeyMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip authentication for health check endpoints
		if c.Request.URL.Path == "/health/live" || c.Request.URL.Path == "/health/ready" {
			c.Next()
			return
		}

		apiKey := c.GetHeader("X-API-Key")
		if apiKey == "" {
			respondWithError(c, ErrUnauthorized, "API key is required")
			c.Abort()
			return
		}

		entry, err := a.ValidateAPIKey(apiKey)
		if err != nil {
			klog.V(4).Infof("API key validation failed: %v", err)
			// Check if it's a rate limit error
			if errors.Is(err, ErrRateLimitExceeded) {
				respondWithError(c, ErrTooManyRequests, "rate limit exceeded")
			} else {
				respondWithError(c, ErrUnauthorized, "invalid API key")
			}
			c.Abort()
			return
		}

		if entry.Status != "valid" {
			respondWithError(c, ErrUnauthorized, "invalid or revoked api key")
			c.Abort()
			return
		}

		// Store namespace and api_key_hash in context for handlers to use
		c.Set("namespace", entry.Namespace)
		c.Set("api_key_hash", entry.Hash)
		c.Next()
	}
}

// hashKey computes the SHA-256 hash of the API key
// This is used as the cache key because Kubernetes Secret data keys must match [-._a-zA-Z0-9]+
func hashKey(apiKey string) string {
	hash := sha256.Sum256([]byte(apiKey))
	return hex.EncodeToString(hash[:])
}

// ValidateAPIKey validates an API key and returns the associated cache entry
// The provided API key is hashed using SHA-256 before cache lookup
func (a *Authenticator) ValidateAPIKey(apiKey string) (*APIKeyCacheEntry, error) {
	// Hash the API key for cache lookup
	hashedKey := hashKey(apiKey)

	a.mu.RLock()
	entry, ok := a.apiKeys[hashedKey]
	a.mu.RUnlock()

	if ok {
		return entry, nil
	}

	// Cache miss - invalid key, apply rate limiting
	if a.rateLimiter != nil {
		if err := a.rateLimiter.Allow(); err != nil {
			return nil, err
		}
	}
	return nil, fmt.Errorf("invalid API key")
}

// loadFromK8sSecret loads API key statuses from Kubernetes Secret
// Secret format: data[hash] = "status" (valid/revoked/expired)
func (a *Authenticator) loadFromK8sSecret() (*corev1.Secret, error) {
	// Initialize K8s client if not set
	if a.k8sClient == nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("not running in Kubernetes cluster: %w", err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		a.k8sClient = clientset
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Get the secret
	secret, err := a.k8sClient.CoreV1().Secrets(a.config.APIKeySecretNamespace).Get(
		ctx, a.config.APIKeySecret, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("secret %s/%s not found", a.config.APIKeySecretNamespace, a.config.APIKeySecret)
		}
		if apierrors.IsForbidden(err) {
			return nil, fmt.Errorf("forbidden to access secret %s/%s: %w", a.config.APIKeySecretNamespace, a.config.APIKeySecret, err)
		}
		return nil, fmt.Errorf("failed to get secret %s/%s: %w", a.config.APIKeySecretNamespace, a.config.APIKeySecret, err)
	}

	return secret, nil
}

// loadFromK8sConfigMap loads API key namespace mappings from Kubernetes ConfigMap
// ConfigMap format: data[hash] = "namespace", plus "defaultNamespace" key
func (a *Authenticator) loadFromK8sConfigMap() (*corev1.ConfigMap, error) {
	if a.k8sClient == nil {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("not running in Kubernetes cluster: %w", err)
		}

		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
		}
		a.k8sClient = clientset
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cm, err := a.k8sClient.CoreV1().ConfigMaps(a.config.APIKeySecretNamespace).Get(
		ctx, a.config.APIKeyConfigMap, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("configmap %s/%s not found", a.config.APIKeySecretNamespace, a.config.APIKeyConfigMap)
		}
		if apierrors.IsForbidden(err) {
			return nil, fmt.Errorf("forbidden to access configmap %s/%s: %w", a.config.APIKeySecretNamespace, a.config.APIKeyConfigMap, err)
		}
		return nil, fmt.Errorf("failed to get configmap %s/%s: %w", a.config.APIKeySecretNamespace, a.config.APIKeyConfigMap, err)
	}

	return cm, nil
}

// buildCache builds the API key cache from Secret (status) and ConfigMap (namespace mapping).
// Namespace resolution order: ConfigMap[hash] -> ConfigMap["defaultNamespace"] -> E2B_DEFAULT_NAMESPACE env -> "default"
func (a *Authenticator) buildCache(secret *corev1.Secret, configMap *corev1.ConfigMap) {
	a.mu.Lock()
	defer a.mu.Unlock()

	defaultNamespace := resolveDefaultNamespace(configMap)
	newCache := make(map[string]*APIKeyCacheEntry)

	if secret != nil && secret.Data != nil {
		for keyHash, value := range secret.Data {
			entry := buildCacheEntry(keyHash, value, defaultNamespace, configMap)
			if entry != nil {
				newCache[keyHash] = entry
			}
		}
	}

	a.apiKeys = newCache
}

func resolveDefaultNamespace(configMap *corev1.ConfigMap) string {
	if configMap != nil && configMap.Data != nil {
		if dn, ok := configMap.Data["defaultNamespace"]; ok && dn != "" {
			return dn
		}
	}
	if envNS := os.Getenv("E2B_DEFAULT_NAMESPACE"); envNS != "" {
		return envNS
	}
	return "default"
}

func buildCacheEntry(keyHash string, value []byte, defaultNamespace string, configMap *corev1.ConfigMap) *APIKeyCacheEntry {
	if keyHash == "" || keyHash == "defaultNamespace" {
		return nil
	}

	status := strings.TrimSpace(string(value))
	if status == "" {
		return nil
	}

	namespace := defaultNamespace
	if configMap != nil && configMap.Data != nil {
		if ns, ok := configMap.Data[keyHash]; ok && ns != "" {
			namespace = ns
		}
	}

	return &APIKeyCacheEntry{
		Status:    status,
		Namespace: namespace,
		Hash:      keyHash,
	}
}

// LoadAPIKeys loads API keys from Kubernetes secret + configmap or environment variable.
// Priority: 1) K8s Secret+ConfigMap, 2) Environment variable.
// Format for env var: E2B_API_KEYS="key1:namespace1,key2:namespace2"
// Format for K8s Secret: data[sha256(api_key)] = "status"
// Format for K8s ConfigMap: data[sha256(api_key)] = "namespace", data["defaultNamespace"] = "fallback-ns"
func (a *Authenticator) LoadAPIKeys() error {
	// Try to load from Kubernetes first
	secret, secretErr := a.loadFromK8sSecret()
	configMap, configMapErr := a.loadFromK8sConfigMap()

	if secretErr == nil || configMapErr == nil {
		a.buildCache(secret, configMap)
		klog.V(2).InfoS("E2B: Loaded API keys from Kubernetes", "secretErr", secretErr, "configMapErr", configMapErr, "count", a.GetAPIKeyCount())
		return nil
	}

	klog.Warningf("failed to load API keys from Kubernetes: secret=%v, configmap=%v, falling back to environment", secretErr, configMapErr)

	// Fallback to environment variable
	a.mu.Lock()
	defer a.mu.Unlock()

	envKeys := os.Getenv("E2B_API_KEYS")
	if envKeys != "" {
		pairs := strings.Split(envKeys, ",")
		for _, pair := range pairs {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				apiKey := strings.TrimSpace(parts[0])
				namespace := strings.TrimSpace(parts[1])
				h := hashKey(apiKey)
				a.apiKeys[h] = &APIKeyCacheEntry{
					Status:    "valid",
					Namespace: namespace,
					Hash:      h,
				}
			}
		}
		return nil
	}

	return fmt.Errorf("no API keys configured: Kubernetes secret/configmap unavailable and E2B_API_KEYS not set")

}

// AddAPIKey adds a new API key (for testing)
// The API key is hashed using SHA-256 before storage
func (a *Authenticator) AddAPIKey(apiKey, namespace string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := hashKey(apiKey)
	a.apiKeys[h] = &APIKeyCacheEntry{
		Status:    "valid",
		Namespace: namespace,
		Hash:      h,
	}
}

// ResetRateLimiter resets the rate limiter (for testing)
func (a *Authenticator) ResetRateLimiter() {
	if a.rateLimiter != nil {
		a.rateLimiter.Reset()
	}
}

// getEnvOrDefault returns the value of an environment variable or a default value
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// InitializeInformer initializes the Kubernetes informer for watching Secret changes
// This must be called before Start() to set up the informer with the k8s client
func (a *Authenticator) InitializeInformer() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.k8sClient == nil {
		return fmt.Errorf("kubernetes client is nil, cannot initialize informer")
	}

	if a.informer != nil {
		klog.V(2).Info("Informer already initialized, skipping")
		return nil
	}

	// Create informer factory with namespace restriction
	factory := informers.NewSharedInformerFactoryWithOptions(
		a.k8sClient,
		10*time.Minute,
		informers.WithNamespace(a.config.APIKeySecretNamespace),
	)

	// Create secret informer filtered by secret name
	secretInformer := factory.Core().V1().Secrets().Informer()

	// Add event handlers for Secret changes
	_, err := secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    a.onSecretAdd,
		UpdateFunc: a.onSecretUpdate,
		DeleteFunc: a.onSecretDelete,
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler to secret informer: %w", err)
	}

	// Create configmap informer for namespace mapping changes
	configMapInformer := factory.Core().V1().ConfigMaps().Informer()

	// Add event handlers for ConfigMap changes
	_, err = configMapInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    a.onConfigMapAdd,
		UpdateFunc: a.onConfigMapUpdate,
		DeleteFunc: a.onConfigMapDelete,
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler to configmap informer: %w", err)
	}

	a.informer = secretInformer
	a.configMapInformer = configMapInformer
	klog.V(2).InfoS("Informer initialized", "namespace", a.config.APIKeySecretNamespace, "secret", a.config.APIKeySecret, "configmap", a.config.APIKeyConfigMap)
	return nil
}

// onConfigMapAdd handles ConfigMap add events
func (a *Authenticator) onConfigMapAdd(obj interface{}) {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast to ConfigMap")
		return
	}

	// Only process the configured configmap
	if configMap.Name != a.config.APIKeyConfigMap {
		return
	}

	klog.V(2).InfoS("ConfigMap added, updating API key cache", "configmap", configMap.Name, "namespace", configMap.Namespace)
	// Reload both secret and configmap to build consistent cache
	freshSecret, _ := a.loadFromK8sSecret()
	freshConfigMap, _ := a.loadFromK8sConfigMap()
	a.buildCache(freshSecret, freshConfigMap)
}

// onConfigMapUpdate handles ConfigMap update events
func (a *Authenticator) onConfigMapUpdate(oldObj, newObj interface{}) {
	oldConfigMap, ok1 := oldObj.(*corev1.ConfigMap)
	newConfigMap, ok2 := newObj.(*corev1.ConfigMap)
	if !ok1 || !ok2 {
		klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast to ConfigMap")
		return
	}

	// Only process the configured configmap
	if newConfigMap.Name != a.config.APIKeyConfigMap {
		return
	}

	klog.V(2).InfoS("ConfigMap updated, refreshing API key cache",
		"configmap", newConfigMap.Name,
		"namespace", newConfigMap.Namespace,
		"oldResourceVersion", oldConfigMap.ResourceVersion,
		"newResourceVersion", newConfigMap.ResourceVersion,
	)
	freshSecret, _ := a.loadFromK8sSecret()
	freshConfigMap, _ := a.loadFromK8sConfigMap()
	a.buildCache(freshSecret, freshConfigMap)
}

// onConfigMapDelete handles ConfigMap delete events
func (a *Authenticator) onConfigMapDelete(obj interface{}) {
	configMap, ok := obj.(*corev1.ConfigMap)
	if !ok {
		// Handle tombstone object
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast to ConfigMap or DeletedFinalStateUnknown")
			return
		}
		configMap, ok = tombstone.Obj.(*corev1.ConfigMap)
		if !ok {
			klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast tombstone object to ConfigMap")
			return
		}
	}

	// Only process the configured configmap
	if configMap.Name != a.config.APIKeyConfigMap {
		return
	}

	klog.InfoS("API key configmap deleted, reloading cache with defaults",
		"configmap", configMap.Name,
		"namespace", configMap.Namespace,
	)

	// Reload cache - namespace mappings will fall back to defaults
	freshSecret, _ := a.loadFromK8sSecret()
	a.buildCache(freshSecret, nil)
}

// onSecretAdd handles Secret add events
func (a *Authenticator) onSecretAdd(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast to Secret")
		return
	}

	// Only process the configured secret
	if secret.Name != a.config.APIKeySecret {
		return
	}

	klog.V(2).InfoS("Secret added, updating API key cache", "secret", secret.Name, "namespace", secret.Namespace)
	// Reload both secret and configmap to build consistent cache
	freshSecret, _ := a.loadFromK8sSecret()
	freshConfigMap, _ := a.loadFromK8sConfigMap()
	a.buildCache(freshSecret, freshConfigMap)
}

// onSecretUpdate handles Secret update events
func (a *Authenticator) onSecretUpdate(oldObj, newObj interface{}) {
	oldSecret, ok1 := oldObj.(*corev1.Secret)
	newSecret, ok2 := newObj.(*corev1.Secret)
	if !ok1 || !ok2 {
		klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast to Secret")
		return
	}

	// Only process the configured secret
	if newSecret.Name != a.config.APIKeySecret {
		return
	}

	klog.V(2).InfoS("Secret updated, refreshing API key cache",
		"secret", newSecret.Name,
		"namespace", newSecret.Namespace,
		"oldResourceVersion", oldSecret.ResourceVersion,
		"newResourceVersion", newSecret.ResourceVersion,
	)
	freshSecret, _ := a.loadFromK8sSecret()
	freshConfigMap, _ := a.loadFromK8sConfigMap()
	a.buildCache(freshSecret, freshConfigMap)
}

// onSecretDelete handles Secret delete events
func (a *Authenticator) onSecretDelete(obj interface{}) {
	secret, ok := obj.(*corev1.Secret)
	if !ok {
		// Handle tombstone object
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast to Secret or DeletedFinalStateUnknown")
			return
		}
		secret, ok = tombstone.Obj.(*corev1.Secret)
		if !ok {
			klog.V(2).ErrorS(fmt.Errorf("unexpected object type"), "Failed to cast tombstone object to Secret")
			return
		}
	}

	// Only process the configured secret
	if secret.Name != a.config.APIKeySecret {
		return
	}

	klog.InfoS("API key secret deleted, clearing cache",
		"secret", secret.Name,
		"namespace", secret.Namespace,
	)

	a.mu.Lock()
	defer a.mu.Unlock()

	// Clear the cache when the secret is deleted
	a.apiKeys = make(map[string]*APIKeyCacheEntry)
}

// Start starts the informers to watch for Secret and ConfigMap changes
// This method blocks until the informers are stopped or the context is canceled
func (a *Authenticator) Start(ctx context.Context) error {
	a.mu.Lock()
	if a.informer == nil {
		a.mu.Unlock()
		return fmt.Errorf("secret informer not initialized, call InitializeInformer first")
	}
	if a.configMapInformer == nil {
		a.mu.Unlock()
		return fmt.Errorf("configmap informer not initialized, call InitializeInformer first")
	}
	if a.started {
		a.mu.Unlock()
		klog.V(2).Info("Informer already started, skipping")
		return nil
	}
	a.started = true
	a.mu.Unlock()

	klog.InfoS("Starting API key informers", "namespace", a.config.APIKeySecretNamespace, "secret", a.config.APIKeySecret, "configmap", a.config.APIKeyConfigMap)

	// Start both informers
	go a.informer.Run(a.stopCh)
	go a.configMapInformer.Run(a.stopCh)

	// Wait for both caches to sync
	if !cache.WaitForCacheSync(ctx.Done(), a.informer.HasSynced, a.configMapInformer.HasSynced) {
		return fmt.Errorf("failed to sync informer caches")
	}

	klog.Info("API key informer caches synced successfully")

	// Block until context is canceled or stopCh is closed
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-a.stopCh:
		return nil
	}
}

// Stop stops the informer gracefully
func (a *Authenticator) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		klog.V(2).Info("Informer not started, nothing to stop")
		return
	}

	klog.Info("Stopping API key informer")
	close(a.stopCh)
	a.started = false
}

// GetAPIKeyCount returns the current number of cached API keys (for testing)
func (a *Authenticator) GetAPIKeyCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.apiKeys)
}

// StartBackgroundRefresh starts the periodic background refresh (5 min interval)
// This serves as a fallback mechanism to ensure cache consistency even if
// the informer misses some updates.
func (a *Authenticator) StartBackgroundRefresh(ctx context.Context, k8sClient client.Client) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Already started
	if a.refreshTicker != nil {
		return nil
	}

	// Store controller-runtime client for refresh operations
	a.ctrlClient = k8sClient

	// Create ticker with 5 minute interval
	a.refreshTicker = time.NewTicker(5 * time.Minute)
	a.refreshDone = make(chan struct{})

	// Start background goroutine
	go a.backgroundRefreshLoop(ctx)

	klog.InfoS("E2B: Background refresh started", "interval", "5m")
	return nil
}

// backgroundRefreshLoop runs the periodic refresh loop
func (a *Authenticator) backgroundRefreshLoop(ctx context.Context) {
	for {
		select {
		case <-a.refreshTicker.C:
			if err := a.performFullRefresh(ctx); err != nil {
				klog.ErrorS(err, "E2B: Background refresh failed")
			} else {
				klog.V(4).InfoS("E2B: Background refresh completed successfully")
			}
		case <-a.refreshDone:
			klog.V(4).InfoS("E2B: Background refresh loop stopped")
			return
		case <-ctx.Done():
			klog.V(4).InfoS("E2B: Background refresh loop stopped due to context cancellation")
			return
		}
	}
}

// performFullRefresh performs a full refresh of the API key cache from K8s API
// This is the fallback mechanism that runs every 5 minutes
func (a *Authenticator) performFullRefresh(ctx context.Context) error {
	// Create timeout context for the refresh operation
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var secret *corev1.Secret
	var configMap *corev1.ConfigMap

	if a.ctrlClient != nil {
		s := &corev1.Secret{}
		err := a.ctrlClient.Get(refreshCtx, client.ObjectKey{
			Name:      a.config.APIKeySecret,
			Namespace: a.config.APIKeySecretNamespace,
		}, s)
		if err == nil {
			secret = s
		} else {
			klog.V(2).ErrorS(err, "E2B: Background refresh failed to get secret")
		}

		cm := &corev1.ConfigMap{}
		err = a.ctrlClient.Get(refreshCtx, client.ObjectKey{
			Name:      a.config.APIKeyConfigMap,
			Namespace: a.config.APIKeySecretNamespace,
		}, cm)
		if err == nil {
			configMap = cm
		} else {
			klog.V(2).ErrorS(err, "E2B: Background refresh failed to get configmap")
		}
	}

	a.buildCache(secret, configMap)

	klog.V(2).InfoS("E2B: Full cache refresh completed",
		"keyCount", a.GetAPIKeyCount(),
		"namespace", a.config.APIKeySecretNamespace)

	return nil
}

// StopBackgroundRefresh stops the background refresh goroutine
// This should be called during shutdown to ensure clean exit
func (a *Authenticator) StopBackgroundRefresh() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.refreshTicker != nil {
		a.refreshTicker.Stop()
		close(a.refreshDone)
		a.refreshTicker = nil
		a.refreshDone = nil
		klog.InfoS("E2B: Background refresh stopped")
	}
}

// SetupInformer sets up the informers with a pre-configured informer factory (for testing)
func (a *Authenticator) SetupInformer(factory informers.SharedInformerFactory) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Create secret informer filtered by secret name
	secretInformer := factory.Core().V1().Secrets().Informer()

	// Add event handlers for Secret changes
	_, _ = secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    a.onSecretAdd,
		UpdateFunc: a.onSecretUpdate,
		DeleteFunc: a.onSecretDelete,
	})

	// Create configmap informer for namespace mapping changes
	configMapInformer := factory.Core().V1().ConfigMaps().Informer()

	// Add event handlers for ConfigMap changes
	_, _ = configMapInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    a.onConfigMapAdd,
		UpdateFunc: a.onConfigMapUpdate,
		DeleteFunc: a.onConfigMapDelete,
	})

	a.informer = secretInformer
	a.configMapInformer = configMapInformer
}
