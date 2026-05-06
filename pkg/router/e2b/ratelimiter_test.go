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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)
	assert.NotNil(t, rl)
	assert.Equal(t, 1.0, rl.rate)
	assert.Equal(t, 1, rl.burst)
	assert.Equal(t, 1.0, rl.tokens) // starts with full bucket
}

func TestRateLimiter_Allow_NormalTraffic(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)

	// First request should be allowed (burst = 1)
	err := rl.Allow()
	assert.NoError(t, err)

	// Wait for token to replenish
	time.Sleep(1100 * time.Millisecond)

	// Second request should be allowed after 1 second
	err = rl.Allow()
	assert.NoError(t, err)
}

func TestRateLimiter_Allow_ExceedsLimit(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)

	// First request should be allowed (burst = 1)
	err := rl.Allow()
	assert.NoError(t, err)

	// Immediate second request should be rate limited
	err = rl.Allow()
	assert.Error(t, err)
	assert.Equal(t, ErrRateLimitExceeded, err)
}

func TestRateLimiter_Allow_TimeWindowReset(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)

	// First request should be allowed
	err := rl.Allow()
	assert.NoError(t, err)

	// Immediate second request should be rate limited
	err = rl.Allow()
	assert.Error(t, err)

	// Wait for token to replenish (1 second)
	time.Sleep(1100 * time.Millisecond)

	// Third request should be allowed after waiting
	err = rl.Allow()
	assert.NoError(t, err)
}

func TestRateLimiter_AllowN(t *testing.T) {
	rl := NewRateLimiter(10.0, 10) // 10 per second, burst of 10

	// Allow 5 requests at once
	err := rl.AllowN(5)
	assert.NoError(t, err)

	// Should have 5 tokens left, so 6 more should fail
	err = rl.AllowN(6)
	assert.Error(t, err)
	assert.Equal(t, ErrRateLimitExceeded, err)

	// Allow 5 more should succeed
	err = rl.AllowN(5)
	assert.NoError(t, err)
}

func TestRateLimiter_AllowN_ZeroOrNegative(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)

	// Zero should always be allowed
	err := rl.AllowN(0)
	assert.NoError(t, err)

	// Negative should always be allowed
	err = rl.AllowN(-1)
	assert.NoError(t, err)
}

func TestRateLimiter_Tokens(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)

	// Initially should have 1 token
	tokens := rl.Tokens()
	assert.InDelta(t, 1.0, tokens, 0.01)

	// After allowing one request, should have 0 tokens
	_ = rl.Allow()
	tokens = rl.Tokens()
	assert.InDelta(t, 0.0, tokens, 0.01)

	// Wait for half a second, should have ~0.5 tokens
	time.Sleep(500 * time.Millisecond)
	tokens = rl.Tokens()
	assert.InDelta(t, 0.5, tokens, 0.1)
}

func TestRateLimiter_Reset(t *testing.T) {
	rl := NewRateLimiter(1.0, 1)

	// Use up the token
	_ = rl.Allow()
	tokens := rl.Tokens()
	assert.InDelta(t, 0.0, tokens, 0.01)

	// Reset should restore full bucket
	rl.Reset()
	tokens = rl.Tokens()
	assert.InDelta(t, 1.0, tokens, 0.01)
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewRateLimiter(100.0, 100) // High rate for concurrent test

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 10; j++ {
				_ = rl.Allow() //nolint:errcheck // concurrent test, we don't care about individual errors
				time.Sleep(10 * time.Millisecond)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}

	// The rate limiter should still be in a valid state
	tokens := rl.Tokens()
	assert.GreaterOrEqual(t, tokens, 0.0)
	assert.LessOrEqual(t, tokens, 100.0)
}

func TestRateLimiter_StrictOnePerSecond(t *testing.T) {
	// Test strict 1/sec limit as per design requirement
	rl := NewRateLimiter(1.0, 1)

	// First request allowed
	assert.NoError(t, rl.Allow())

	// Next 10 requests should all be rejected
	for i := 0; i < 10; i++ {
		err := rl.Allow()
		assert.Error(t, err, "Request %d should be rate limited", i+1)
	}

	// Wait 1 second
	time.Sleep(1100 * time.Millisecond)

	// Now one more should be allowed
	assert.NoError(t, rl.Allow())
}

func TestErrRateLimitExceeded(t *testing.T) {
	// Test that ErrRateLimitExceeded is properly defined
	assert.NotNil(t, ErrRateLimitExceeded)
	assert.Equal(t, "rate limit exceeded", ErrRateLimitExceeded.Error())
}
