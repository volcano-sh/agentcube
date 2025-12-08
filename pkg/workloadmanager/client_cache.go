package workloadmanager

import (
	"container/list"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// clientCacheEntry represents a cached client entry
type clientCacheEntry struct {
	key         string
	client      *UserK8sClient
	tokenExpiry time.Time // Token expiration time parsed from JWT
	element     *list.Element
}

// ClientCache is a thread-safe LRU cache for Kubernetes clients
type ClientCache struct {
	mu      sync.RWMutex
	cache   map[string]*clientCacheEntry
	lruList *list.List
	maxSize int
}

// parseJWTExpiry parses JWT token and extracts expiration time
// Returns zero time if parsing fails or token has no expiration
func parseJWTExpiry(token string) time.Time {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}
	}

	// Decode payload (second part)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}
	}

	// Parse JSON payload
	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}
	}

	// Extract exp claim
	exp, ok := claims["exp"]
	if !ok {
		return time.Time{}
	}

	// Convert to time.Time
	var expiry time.Time
	switch v := exp.(type) {
	case float64:
		expiry = time.Unix(int64(v), 0)
	case int64:
		expiry = time.Unix(v, 0)
	default:
		return time.Time{}
	}

	return expiry
}

// NewClientCache creates a new client cache with specified max size
func NewClientCache(maxSize int) *ClientCache {
	if maxSize <= 0 {
		maxSize = 100 // Default max size
	}
	return &ClientCache{
		cache:   make(map[string]*clientCacheEntry),
		lruList: list.New(),
		maxSize: maxSize,
	}
}

// Get retrieves a client from cache based on key (service account)
// Returns the client if found and cached token is not expired, nil otherwise
// Different tokens for the same service account can share the same client
func (c *ClientCache) Get(key string) *UserK8sClient {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.cache[key]
	if !exists {
		return nil
	}

	// Check if cached entry's token is expired
	if !entry.tokenExpiry.IsZero() && time.Now().After(entry.tokenExpiry) {
		// Cached token expired, remove entry
		c.lruList.Remove(entry.element)
		delete(c.cache, key)
		return nil
	}

	// Move to front (LRU update)
	c.lruList.MoveToFront(entry.element)

	return entry.client
}

// Set stores a client in cache with the given key and token
// Parses JWT token to extract expiration time
func (c *ClientCache) Set(key, token string, client *UserK8sClient) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Parse token expiry from JWT
	tokenExpiry := parseJWTExpiry(token)

	// Check if entry already exists
	if entry, exists := c.cache[key]; exists {
		// Update existing entry
		entry.client = client
		entry.tokenExpiry = tokenExpiry
		c.lruList.MoveToFront(entry.element)
		return
	}

	// Remove oldest entry if cache is full
	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	// Create new entry
	entry := &clientCacheEntry{
		key:         key,
		client:      client,
		tokenExpiry: tokenExpiry,
	}
	entry.element = c.lruList.PushFront(entry)
	c.cache[key] = entry
}

// Remove removes an entry from cache
func (c *ClientCache) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.cache[key]
	if !exists {
		return
	}

	c.lruList.Remove(entry.element)
	delete(c.cache, key)
}

// evictOldest removes the oldest entry from cache (must be called with lock held)
func (c *ClientCache) evictOldest() {
	back := c.lruList.Back()
	if back == nil {
		return
	}

	// nolint:errcheck
	entry := back.Value.(*clientCacheEntry)
	c.lruList.Remove(back)
	delete(c.cache, entry.key)
}

// Size returns the current number of entries in cache
func (c *ClientCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// makeCacheKey creates a cache key from namespace and service account name
func makeCacheKey(namespace, serviceAccountName string) string {
	return fmt.Sprintf("%s:%s", namespace, serviceAccountName)
}

// tokenCacheEntry represents a cached token validation entry
type tokenCacheEntry struct {
	token         string
	authenticated bool
	username      string
	lastAccess    time.Time
	element       *list.Element
}

// TokenCache is a thread-safe LRU cache for token validation results
type TokenCache struct {
	mu      sync.RWMutex
	cache   map[string]*tokenCacheEntry
	lruList *list.List
	maxSize int
	ttl     time.Duration // Time-to-live for cache entries
}

// NewTokenCache creates a new token cache with specified max size and TTL
func NewTokenCache(maxSize int, ttl time.Duration) *TokenCache {
	if maxSize <= 0 {
		maxSize = 1000 // Default max size for tokens
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute // Default TTL: 5 minutes (tokens may expire)
	}
	return &TokenCache{
		cache:   make(map[string]*tokenCacheEntry),
		lruList: list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves a token validation result from cache
// Returns found status, authenticated status, and username
// If found is false, the token was not in cache or expired
func (c *TokenCache) Get(token string) (found bool, authenticated bool, username string) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[token]
	if !exists {
		return false, false, ""
	}

	// Check if entry is expired
	if time.Since(entry.lastAccess) > c.ttl {
		return false, false, ""
	}

	return true, entry.authenticated, entry.username
}

// Set stores a token validation result in cache
func (c *TokenCache) Set(token string, authenticated bool, username string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if entry already exists
	if entry, exists := c.cache[token]; exists {
		// Update existing entry
		entry.authenticated = authenticated
		entry.username = username
		entry.lastAccess = time.Now()
		c.lruList.MoveToFront(entry.element)
		return
	}

	// Remove oldest entry if cache is full
	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	// Create new entry
	entry := &tokenCacheEntry{
		token:         token,
		authenticated: authenticated,
		username:      username,
		lastAccess:    time.Now(),
	}
	entry.element = c.lruList.PushFront(entry)
	c.cache[token] = entry
}

// Remove removes an entry from cache
func (c *TokenCache) Remove(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, exists := c.cache[token]
	if !exists {
		return
	}

	c.lruList.Remove(entry.element)
	delete(c.cache, token)
}

// evictOldest removes the oldest entry from cache (must be called with lock held)
func (c *TokenCache) evictOldest() {
	back := c.lruList.Back()
	if back == nil {
		return
	}
	// nolint:errcheck
	entry := back.Value.(*tokenCacheEntry)
	c.lruList.Remove(back)
	delete(c.cache, entry.token)
}

// Size returns the current number of entries in cache
func (c *TokenCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}
