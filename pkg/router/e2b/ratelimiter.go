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
	"errors"
	"sync"
	"time"
)

// ErrRateLimitExceeded is returned when the rate limit is exceeded
var ErrRateLimitExceeded = errors.New("rate limit exceeded")

// RateLimiter implements a token bucket rate limiter
// It is used to prevent brute-force amplification when cache misses occur
type RateLimiter struct {
	rate     float64   // tokens per second
	burst    int       // maximum burst size
	tokens   float64   // current tokens in bucket
	lastTime time.Time // last time tokens were updated
	mu       sync.Mutex
}

// NewRateLimiter creates a new RateLimiter with the specified rate and burst
// rate: tokens per second (e.g., 1.0 means 1 token per second)
// burst: maximum number of tokens that can be accumulated (bucket capacity)
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return &RateLimiter{
		rate:     rate,
		burst:    burst,
		tokens:   float64(burst), // start with full bucket
		lastTime: time.Now(),
	}
}

// Allow checks if a request is allowed under the rate limit
// Returns nil if allowed, ErrRateLimitExceeded if rate limited
func (rl *RateLimiter) Allow() error {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastTime).Seconds()
	rl.lastTime = now

	// Add tokens based on elapsed time
	rl.tokens += elapsed * rl.rate
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	// Check if we have enough tokens
	if rl.tokens < 1.0 {
		return ErrRateLimitExceeded
	}

	// Consume one token
	rl.tokens--
	return nil
}

// AllowN checks if n requests are allowed under the rate limit
// Returns nil if allowed, ErrRateLimitExceeded if rate limited
func (rl *RateLimiter) AllowN(n int) error {
	if n <= 0 {
		return nil
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastTime).Seconds()
	rl.lastTime = now

	// Add tokens based on elapsed time
	rl.tokens += elapsed * rl.rate
	if rl.tokens > float64(rl.burst) {
		rl.tokens = float64(rl.burst)
	}

	// Check if we have enough tokens
	if rl.tokens < float64(n) {
		return ErrRateLimitExceeded
	}

	// Consume n tokens
	rl.tokens -= float64(n)
	return nil
}

// Tokens returns the current number of tokens in the bucket
// This is primarily used for testing
func (rl *RateLimiter) Tokens() float64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Calculate current tokens without consuming
	now := time.Now()
	elapsed := now.Sub(rl.lastTime).Seconds()
	tokens := rl.tokens + elapsed*rl.rate
	if tokens > float64(rl.burst) {
		tokens = float64(rl.burst)
	}
	return tokens
}

// Reset resets the rate limiter to full capacity
// This is primarily used for testing
func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.tokens = float64(rl.burst)
	rl.lastTime = time.Now()
}
