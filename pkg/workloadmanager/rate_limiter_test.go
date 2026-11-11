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
	"testing"
	"time"

	"golang.org/x/time/rate"
)

func TestOwnerRateLimiter_AllowWithinBurst(t *testing.T) {
	rl := NewOwnerRateLimiter(rate.Limit(10), 3)
	// First 3 calls (burst=3) must succeed immediately
	for i := 0; i < 3; i++ {
		if !rl.Allow("user-a") {
			t.Fatalf("call %d should have been allowed (within burst)", i+1)
		}
	}
}

func TestOwnerRateLimiter_BlocksAfterBurst(t *testing.T) {
	// rate=0 means no token replenishment; burst=2 means exactly 2 tokens start available.
	rl := NewOwnerRateLimiter(0, 2)
	if !rl.Allow("user-b") {
		t.Fatal("first call should be allowed")
	}
	if !rl.Allow("user-b") {
		t.Fatal("second call should be allowed")
	}
	if rl.Allow("user-b") {
		t.Fatal("third call should be blocked (burst exhausted, rate=0)")
	}
}

func TestOwnerRateLimiter_IndependentPerOwner(t *testing.T) {
	rl := NewOwnerRateLimiter(0, 1)
	if !rl.Allow("alice") {
		t.Fatal("alice first call should be allowed")
	}
	// alice's bucket is empty; bob's bucket is full
	if !rl.Allow("bob") {
		t.Fatal("bob first call should be allowed independently of alice")
	}
	if rl.Allow("alice") {
		t.Fatal("alice second call should be blocked")
	}
}

func TestOwnerRateLimiter_RetryAfterPositiveWhenBlocked(t *testing.T) {
	rl := NewOwnerRateLimiter(rate.Limit(0.1), 1)
	rl.Allow("user-c") // drain the single token
	delay := rl.RetryAfter("user-c")
	if delay <= 0 {
		t.Fatalf("expected positive RetryAfter when bucket is empty, got %v", delay)
	}
}

func TestOwnerRateLimiter_RetryAfterZeroWhenAllowed(t *testing.T) {
	rl := NewOwnerRateLimiter(rate.Limit(10), 10)
	delay := rl.RetryAfter("user-d")
	if delay != 0 {
		t.Fatalf("expected zero RetryAfter for fresh bucket, got %v", delay)
	}
}

func TestOwnerRateLimiter_RetryAfterDoesNotConsumeToken(t *testing.T) {
	rl := NewOwnerRateLimiter(0, 1)
	// RetryAfter on a full bucket should not consume the token
	rl.RetryAfter("user-e")
	if !rl.Allow("user-e") {
		t.Fatal("Allow should succeed: RetryAfter must not consume a token")
	}
}

func TestOwnerRateLimiter_StaleEviction(t *testing.T) {
	rl := NewOwnerRateLimiter(rate.Limit(10), 10)
	rl.Allow("evict-me")

	// Manually back-date the entry so it appears stale
	rl.mu.Lock()
	rl.limiters["evict-me"].lastSeen = time.Now().Add(-staleLimiterTTL - time.Second)
	rl.mu.Unlock()

	// A new Allow call triggers eviction
	rl.Allow("trigger-eviction")

	rl.mu.Lock()
	_, exists := rl.limiters["evict-me"]
	rl.mu.Unlock()
	if exists {
		t.Fatal("stale limiter should have been evicted")
	}
}
