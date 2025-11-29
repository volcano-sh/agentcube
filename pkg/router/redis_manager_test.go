package router

import (
	"testing"
	"time"
)

func TestMockRedisManager_UpdateSessionActivity(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		sessionID string
		wantErr   bool
	}{
		{
			name:      "valid session ID with enabled Redis",
			enabled:   true,
			sessionID: "test-session-123",
			wantErr:   false,
		},
		{
			name:      "empty session ID with enabled Redis",
			enabled:   true,
			sessionID: "",
			wantErr:   true,
		},
		{
			name:      "valid session ID with disabled Redis",
			enabled:   false,
			sessionID: "test-session-123",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewMockRedisManager(tt.enabled)
			err := r.UpdateSessionActivity(tt.sessionID)

			if (err != nil) != tt.wantErr {
				t.Errorf("UpdateSessionActivity() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// If enabled and no error, check if session was stored by checking count
			if tt.enabled && !tt.wantErr {
				if count := r.GetActiveSessionCount(); count != 1 {
					t.Errorf("Expected 1 session to be stored, got %d", count)
				}
			}
		})
	}
}

func TestMockRedisManager_GetSessionLastActive(t *testing.T) {
	r := NewMockRedisManager(true)
	sessionID := "test-session-123"

	// Test getting non-existent session
	_, err := r.GetSessionLastActive(sessionID)
	if err == nil {
		t.Error("Expected error for non-existent session")
	}

	// Add session and test retrieval
	beforeTime := time.Now()
	err = r.UpdateSessionActivity(sessionID)
	if err != nil {
		t.Fatalf("Failed to update session activity: %v", err)
	}
	afterTime := time.Now()

	lastActive, err := r.GetSessionLastActive(sessionID)
	if err != nil {
		t.Fatalf("Failed to get session last active: %v", err)
	}

	if lastActive.Before(beforeTime) || lastActive.After(afterTime) {
		t.Errorf("Last active time %v is not within expected range [%v, %v]", lastActive, beforeTime, afterTime)
	}

	// Test with disabled Redis
	rDisabled := NewMockRedisManager(false)
	_, err = rDisabled.GetSessionLastActive(sessionID)
	if err == nil {
		t.Error("Expected error when Redis is disabled")
	}
}

func TestMockRedisManager_CleanupExpiredSessions(t *testing.T) {
	r := NewMockRedisManager(true)

	// Add some sessions
	oldSessionID := "old-session"
	newSessionID := "new-session"

	// Add sessions - the old one will be cleaned up based on time
	err := r.UpdateSessionActivity(oldSessionID)
	if err != nil {
		t.Fatalf("Failed to update old session activity: %v", err)
	}

	// Manually set old session timestamp by accessing the struct field
	r.sessions[oldSessionID] = time.Now().Add(-2 * time.Hour)

	// Add new session
	err = r.UpdateSessionActivity(newSessionID)
	if err != nil {
		t.Fatalf("Failed to update new session activity: %v", err)
	}

	// Get initial count
	initialCount := r.GetActiveSessionCount()
	if initialCount != 2 {
		t.Errorf("Expected 2 initial sessions, got %d", initialCount)
	}

	// Cleanup sessions older than 1 hour
	err = r.CleanupExpiredSessions(1 * time.Hour)
	if err != nil {
		t.Fatalf("Failed to cleanup expired sessions: %v", err)
	}

	// Check that count decreased
	finalCount := r.GetActiveSessionCount()
	if finalCount != 1 {
		t.Errorf("Expected 1 session after cleanup, got %d", finalCount)
	}

	// Test with disabled Redis
	rDisabled := NewMockRedisManager(false)
	err = rDisabled.CleanupExpiredSessions(1 * time.Hour)
	if err != nil {
		t.Errorf("Cleanup should not fail when Redis is disabled: %v", err)
	}
}

func TestMockRedisManager_GetActiveSessionCount(t *testing.T) {
	r := NewMockRedisManager(true)

	// Initially should be 0
	if count := r.GetActiveSessionCount(); count != 0 {
		t.Errorf("Expected 0 active sessions, got %d", count)
	}

	// Add some sessions
	sessions := []string{"session1", "session2", "session3"}
	for _, sessionID := range sessions {
		err := r.UpdateSessionActivity(sessionID)
		if err != nil {
			t.Fatalf("Failed to update session activity: %v", err)
		}
	}

	// Should have 3 sessions
	if count := r.GetActiveSessionCount(); count != 3 {
		t.Errorf("Expected 3 active sessions, got %d", count)
	}

	// Test with disabled Redis
	rDisabled := NewMockRedisManager(false)
	if count := rDisabled.GetActiveSessionCount(); count != 0 {
		t.Errorf("Expected 0 active sessions when disabled, got %d", count)
	}
}

func TestMockRedisManager_GetAllActiveSessions(t *testing.T) {
	r := NewMockRedisManager(true)

	// Initially should be empty
	if sessions := r.GetAllActiveSessions(); len(sessions) != 0 {
		t.Errorf("Expected 0 active sessions, got %d", len(sessions))
	}

	// Add some sessions
	expectedSessions := []string{"session1", "session2", "session3"}
	for _, sessionID := range expectedSessions {
		err := r.UpdateSessionActivity(sessionID)
		if err != nil {
			t.Fatalf("Failed to update session activity: %v", err)
		}
	}

	// Get all sessions
	activeSessions := r.GetAllActiveSessions()
	if len(activeSessions) != len(expectedSessions) {
		t.Errorf("Expected %d active sessions, got %d", len(expectedSessions), len(activeSessions))
	}

	// Check that all expected sessions are present
	sessionMap := make(map[string]bool)
	for _, session := range activeSessions {
		sessionMap[session] = true
	}

	for _, expected := range expectedSessions {
		if !sessionMap[expected] {
			t.Errorf("Expected session %s not found in active sessions", expected)
		}
	}

	// Test with disabled Redis
	rDisabled := NewMockRedisManager(false)
	if sessions := rDisabled.GetAllActiveSessions(); sessions != nil {
		t.Error("Expected nil when Redis is disabled")
	}
}

func TestMockRedisManager_ConcurrentAccess(t *testing.T) {
	r := NewMockRedisManager(true)
	sessionID := "concurrent-session"

	// Test concurrent updates
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			err := r.UpdateSessionActivity(sessionID)
			if err != nil {
				t.Errorf("Concurrent update failed: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Check that session exists by checking count
	if count := r.GetActiveSessionCount(); count != 1 {
		t.Errorf("Expected 1 session after concurrent updates, got %d", count)
	}

	// Test concurrent reads
	for i := 0; i < 10; i++ {
		go func() {
			_, err := r.GetSessionLastActive(sessionID)
			if err != nil {
				t.Errorf("Concurrent read failed: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all read goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}
