package apiserver

import (
	"container/list"
	"fmt"
	"sync"
	"time"
)

// clientCacheEntry represents a cached client entry
type clientCacheEntry struct {
	key        string
	client     *UserK8sClient
	token      string // Store token for validation
	lastAccess time.Time
	element    *list.Element
}

// ClientCache is a thread-safe LRU cache for Kubernetes clients
type ClientCache struct {
	mu      sync.RWMutex
	cache   map[string]*clientCacheEntry
	lruList *list.List
	maxSize int
	ttl     time.Duration // Time-to-live for cache entries
}

// NewClientCache creates a new client cache with specified max size and TTL
func NewClientCache(maxSize int, ttl time.Duration) *ClientCache {
	if maxSize <= 0 {
		maxSize = 100 // Default max size
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute // Default TTL: 30 minutes
	}
	return &ClientCache{
		cache:   make(map[string]*clientCacheEntry),
		lruList: list.New(),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get retrieves a client from cache by key
// Returns the client if found and not expired, nil otherwise
func (c *ClientCache) Get(key string) *UserK8sClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[key]
	if !exists {
		return nil
	}

	// Check if entry is expired
	if time.Since(entry.lastAccess) > c.ttl {
		return nil
	}

	return entry.client
}

// GetWithToken retrieves a client from cache and validates the token matches
// Returns the client if found, token matches, and not expired, nil otherwise
func (c *ClientCache) GetWithToken(key, token string) *UserK8sClient {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.cache[key]
	if !exists {
		return nil
	}

	// Check if token matches
	if entry.token != token {
		return nil
	}

	// Check if entry is expired
	if time.Since(entry.lastAccess) > c.ttl {
		return nil
	}

	return entry.client
}

// Set stores a client in cache with the given key and token
func (c *ClientCache) Set(key, token string, client *UserK8sClient) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if entry already exists
	if entry, exists := c.cache[key]; exists {
		// Update existing entry
		entry.client = client
		entry.token = token
		entry.lastAccess = time.Now()
		c.lruList.MoveToFront(entry.element)
		return
	}

	// Remove oldest entry if cache is full
	if len(c.cache) >= c.maxSize {
		c.evictOldest()
	}

	// Create new entry
	entry := &clientCacheEntry{
		key:        key,
		client:     client,
		token:      token,
		lastAccess: time.Now(),
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

	entry := back.Value.(*clientCacheEntry)
	c.lruList.Remove(back)
	delete(c.cache, entry.key)
}

// CleanExpired removes all expired entries from cache
func (c *ClientCache) CleanExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	count := 0
	now := time.Now()
	for key, entry := range c.cache {
		if now.Sub(entry.lastAccess) > c.ttl {
			c.lruList.Remove(entry.element)
			delete(c.cache, key)
			count++
		}
	}
	return count
}

// Size returns the current number of entries in cache
func (c *ClientCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache)
}

// Clear removes all entries from cache
func (c *ClientCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*clientCacheEntry)
	c.lruList = list.New()
}

// makeCacheKey creates a cache key from namespace and service account name
func makeCacheKey(namespace, serviceAccountName string) string {
	return fmt.Sprintf("%s:%s", namespace, serviceAccountName)
}
