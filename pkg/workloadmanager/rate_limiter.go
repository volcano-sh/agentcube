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
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// defaultRatePerSecond is the sustained sandbox-creation rate allowed per owner identity.
	defaultRatePerSecond = rate.Limit(0.5) // 1 sandbox per 2 seconds sustained
	// defaultBurst is the maximum instantaneous burst of sandbox creations per owner identity.
	defaultBurst = 5
	// staleLimiterTTL is how long an idle per-owner limiter is kept before being evicted.
	staleLimiterTTL = 10 * time.Minute
)

// ownerLimiter pairs a token-bucket rate limiter with the last time it was used.
type ownerLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// OwnerRateLimiter is a per-owner token-bucket rate limiter for sandbox creation.
// Each distinct ownerID gets its own bucket; idle buckets are evicted after staleLimiterTTL.
type OwnerRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ownerLimiter
	r        rate.Limit
	burst    int
}

// NewOwnerRateLimiter creates a limiter with the given per-second rate and burst.
func NewOwnerRateLimiter(r rate.Limit, burst int) *OwnerRateLimiter {
	return &OwnerRateLimiter{
		limiters: make(map[string]*ownerLimiter),
		r:        r,
		burst:    burst,
	}
}

// NewDefaultOwnerRateLimiter creates a limiter with the default rate and burst.
func NewDefaultOwnerRateLimiter() *OwnerRateLimiter {
	return NewOwnerRateLimiter(defaultRatePerSecond, defaultBurst)
}

// Allow reports whether ownerID may create a sandbox right now.
// It also evicts stale per-owner buckets on each call to avoid unbounded growth.
func (rl *OwnerRateLimiter) Allow(ownerID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.evictStale()

	ol, ok := rl.limiters[ownerID]
	if !ok {
		ol = &ownerLimiter{limiter: rate.NewLimiter(rl.r, rl.burst)}
		rl.limiters[ownerID] = ol
	}
	ol.lastSeen = time.Now()
	return ol.limiter.Allow()
}

// RetryAfter returns the duration the caller should wait before the next
// sandbox creation is likely to be allowed. It does not consume a token.
func (rl *OwnerRateLimiter) RetryAfter(ownerID string) time.Duration {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	ol, ok := rl.limiters[ownerID]
	if !ok {
		return 0
	}
	r := ol.limiter.Reserve()
	delay := r.Delay()
	r.Cancel() // don't actually consume a token
	return delay
}

// evictStale removes per-owner limiters that have not been used recently.
// Must be called with rl.mu held.
func (rl *OwnerRateLimiter) evictStale() {
	cutoff := time.Now().Add(-staleLimiterTTL)
	for id, ol := range rl.limiters {
		if ol.lastSeen.Before(cutoff) {
			delete(rl.limiters, id)
		}
	}
}
