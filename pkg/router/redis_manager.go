package router

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// RedisManager interface for managing session activity in Redis
type RedisManager interface {
	// UpdateSessionActivity updates the lastActive time for a session ID
	UpdateSessionActivity(sessionID string) error

	// GetSessionLastActive gets the last active time for a session ID
	GetSessionLastActive(sessionID string) (time.Time, error)

	// CleanupExpiredSessions removes sessions that haven't been active for a specified duration
	CleanupExpiredSessions(expireDuration time.Duration) error
}

// MockRedisManager is a mock implementation for testing
type MockRedisManager struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // sessionID -> lastActive time
	enabled  bool
}

// NewMockRedisManager creates a new mock Redis manager
func NewMockRedisManager(enabled bool) *MockRedisManager {
	return &MockRedisManager{
		sessions: make(map[string]time.Time),
		enabled:  enabled,
	}
}

// UpdateSessionActivity implements RedisManager interface
func (r *MockRedisManager) UpdateSessionActivity(sessionID string) error {
	if !r.enabled {
		return nil // Silently skip if disabled
	}

	if sessionID == "" {
		return fmt.Errorf("session ID cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	r.sessions[sessionID] = now

	log.Printf("Updated session activity for session %s at %s", sessionID, now.Format(time.RFC3339))
	return nil
}

// GetSessionLastActive implements RedisManager interface
func (r *MockRedisManager) GetSessionLastActive(sessionID string) (time.Time, error) {
	if !r.enabled {
		return time.Time{}, fmt.Errorf("Redis manager is disabled")
	}

	if sessionID == "" {
		return time.Time{}, fmt.Errorf("session ID cannot be empty")
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	lastActive, exists := r.sessions[sessionID]
	if !exists {
		return time.Time{}, fmt.Errorf("session %s not found", sessionID)
	}

	return lastActive, nil
}

// CleanupExpiredSessions implements RedisManager interface
func (r *MockRedisManager) CleanupExpiredSessions(expireDuration time.Duration) error {
	if !r.enabled {
		return nil // Silently skip if disabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	expiredSessions := make([]string, 0)

	for sessionID, lastActive := range r.sessions {
		if now.Sub(lastActive) > expireDuration {
			expiredSessions = append(expiredSessions, sessionID)
		}
	}

	// Remove expired sessions
	for _, sessionID := range expiredSessions {
		delete(r.sessions, sessionID)
		log.Printf("Cleaned up expired session: %s", sessionID)
	}

	if len(expiredSessions) > 0 {
		log.Printf("Cleaned up %d expired sessions", len(expiredSessions))
	}

	return nil
}

// GetActiveSessionCount returns the number of active sessions
func (r *MockRedisManager) GetActiveSessionCount() int {
	if !r.enabled {
		return 0
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sessions)
}

// GetAllActiveSessions returns all active session IDs (for debugging)
func (r *MockRedisManager) GetAllActiveSessions() []string {
	if !r.enabled {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	sessions := make([]string, 0, len(r.sessions))
	for sessionID := range r.sessions {
		sessions = append(sessions, sessionID)
	}

	return sessions
}
