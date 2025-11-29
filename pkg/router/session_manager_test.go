package router

import (
	"fmt"
	"testing"
)

func TestMockSessionManager_GetSandboxInfoBySessionId(t *testing.T) {
	endpoints := []string{
		"http://sandbox-1:8080",
		"http://sandbox-2:8080",
		"http://sandbox-3:8080",
	}
	sm := NewMockSessionManager(endpoints)

	tests := []struct {
		name      string
		sessionID string
		namespace string
		agentName string
		kind      string
		wantErr   bool
	}{
		{
			name:      "new session with agent kind",
			sessionID: "",
			namespace: "default",
			agentName: "test-agent",
			kind:      KindAgent,
			wantErr:   false,
		},
		{
			name:      "new session with code interpreter kind",
			sessionID: "",
			namespace: "default",
			agentName: "test-interpreter",
			kind:      KindCodeInterpreter,
			wantErr:   false,
		},
		{
			name:      "existing session",
			sessionID: "existing-session-123",
			namespace: "default",
			agentName: "test-agent",
			kind:      KindAgent,
			wantErr:   false,
		},
		{
			name:      "invalid kind",
			sessionID: "",
			namespace: "default",
			agentName: "test-agent",
			kind:      "invalid-kind",
			wantErr:   true,
		},
		{
			name:      "empty kind (should work for backward compatibility)",
			sessionID: "",
			namespace: "default",
			agentName: "test-agent",
			kind:      "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			endpoint, sessionID, err := sm.GetSandboxInfoBySessionId(tt.sessionID, tt.namespace, tt.agentName, tt.kind)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetSandboxInfoBySessionId() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				// Check that endpoint is not empty
				if endpoint == "" {
					t.Error("Expected non-empty endpoint")
				}

				// Check that sessionID is not empty
				if sessionID == "" {
					t.Error("Expected non-empty session ID")
				}

				// Check that endpoint is one of the configured endpoints
				found := false
				for _, ep := range endpoints {
					if endpoint == ep {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Endpoint %s not found in configured endpoints", endpoint)
				}

				// If we provided a session ID, it should be returned unchanged
				if tt.sessionID != "" && sessionID != tt.sessionID {
					t.Errorf("Expected session ID %s, got %s", tt.sessionID, sessionID)
				}
			}
		})
	}
}

func TestMockSessionManager_SessionPersistence(t *testing.T) {
	endpoints := []string{"http://sandbox-1:8080"}
	sm := NewMockSessionManager(endpoints)

	// Create a new session
	endpoint1, sessionID, err := sm.GetSandboxInfoBySessionId("", "default", "test-agent", KindAgent)
	if err != nil {
		t.Fatalf("Failed to create new session: %v", err)
	}

	// Use the same session ID again
	endpoint2, sessionID2, err := sm.GetSandboxInfoBySessionId(sessionID, "default", "test-agent", KindAgent)
	if err != nil {
		t.Fatalf("Failed to get existing session: %v", err)
	}

	// Should return the same endpoint and session ID
	if endpoint1 != endpoint2 {
		t.Errorf("Expected same endpoint for existing session, got %s vs %s", endpoint1, endpoint2)
	}

	if sessionID != sessionID2 {
		t.Errorf("Expected same session ID, got %s vs %s", sessionID, sessionID2)
	}
}

func TestMockSessionManager_RoundRobinDistribution(t *testing.T) {
	endpoints := []string{
		"http://sandbox-1:8080",
		"http://sandbox-2:8080",
		"http://sandbox-3:8080",
	}
	sm := NewMockSessionManager(endpoints)

	// Create multiple sessions and track endpoint distribution
	endpointCount := make(map[string]int)
	numSessions := 9 // Multiple of 3 to test round-robin

	for i := 0; i < numSessions; i++ {
		endpoint, _, err := sm.GetSandboxInfoBySessionId("", "default", "test-agent", KindAgent)
		if err != nil {
			t.Fatalf("Failed to create session %d: %v", i, err)
		}
		endpointCount[endpoint]++
	}

	// Each endpoint should be used equally
	expectedCount := numSessions / len(endpoints)
	for _, endpoint := range endpoints {
		if count := endpointCount[endpoint]; count != expectedCount {
			t.Errorf("Expected endpoint %s to be used %d times, got %d", endpoint, expectedCount, count)
		}
	}
}

func TestMockSessionManager_EmptyEndpoints(t *testing.T) {
	// Test with empty endpoints (should use defaults)
	sm := NewMockSessionManager([]string{})

	endpoint, sessionID, err := sm.GetSandboxInfoBySessionId("", "default", "test-agent", KindAgent)
	if err != nil {
		t.Fatalf("Failed to create session with default endpoints: %v", err)
	}

	if endpoint == "" {
		t.Error("Expected non-empty endpoint with default configuration")
	}

	if sessionID == "" {
		t.Error("Expected non-empty session ID")
	}
}

func TestMockSessionManager_SessionKeyGeneration(t *testing.T) {
	endpoints := []string{"http://sandbox-1:8080"}
	sm := NewMockSessionManager(endpoints)

	// Test session with namespace and name
	endpoint1, sessionID1, err := sm.GetSandboxInfoBySessionId("", "namespace1", "agent1", KindAgent)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Test session with different namespace but same name
	_, sessionID2, err := sm.GetSandboxInfoBySessionId("", "namespace2", "agent1", KindAgent)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Should create different sessions
	if sessionID1 == sessionID2 {
		t.Error("Expected different session IDs for different namespaces")
	}

	// Test reusing first session
	endpoint3, sessionID3, err := sm.GetSandboxInfoBySessionId(sessionID1, "namespace1", "agent1", KindAgent)
	if err != nil {
		t.Fatalf("Failed to reuse session: %v", err)
	}

	if endpoint1 != endpoint3 || sessionID1 != sessionID3 {
		t.Error("Expected same endpoint and session ID when reusing session")
	}
}

func TestMockSessionManager_GetSessionCount(t *testing.T) {
	endpoints := []string{"http://sandbox-1:8080"}
	sm := NewMockSessionManager(endpoints)

	// Initially should have 0 sessions
	if count := sm.GetSessionCount(); count != 0 {
		t.Errorf("Expected 0 initial sessions, got %d", count)
	}

	// Create some sessions
	numSessions := 3
	for i := 0; i < numSessions; i++ {
		_, _, err := sm.GetSandboxInfoBySessionId("", "default", "test-agent", KindAgent)
		if err != nil {
			t.Fatalf("Failed to create session %d: %v", i, err)
		}
	}

	// Should have the expected number of sessions
	if count := sm.GetSessionCount(); count != numSessions {
		t.Errorf("Expected %d sessions, got %d", numSessions, count)
	}
}

func TestMockSessionManager_RemoveSession(t *testing.T) {
	endpoints := []string{"http://sandbox-1:8080"}
	sm := NewMockSessionManager(endpoints)

	// Create a session
	_, sessionID, err := sm.GetSandboxInfoBySessionId("", "default", "test-agent", KindAgent)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	// Verify session exists
	if count := sm.GetSessionCount(); count != 1 {
		t.Errorf("Expected 1 session, got %d", count)
	}

	// Remove the session using the composite key (namespace/name/sessionId)
	compositeKey := fmt.Sprintf("default/test-agent/%s", sessionID)
	sm.RemoveSession(compositeKey)

	// Verify session was removed
	if count := sm.GetSessionCount(); count != 0 {
		t.Errorf("Expected 0 sessions after removal, got %d", count)
	}

	// Test removing session without namespace/name (should not affect anything)
	sm.RemoveSession(sessionID)
	if count := sm.GetSessionCount(); count != 0 {
		t.Errorf("Expected 0 sessions after second removal attempt, got %d", count)
	}
}

func TestMockSessionManager_ConcurrentAccess(t *testing.T) {
	endpoints := []string{"http://sandbox-1:8080", "http://sandbox-2:8080"}
	sm := NewMockSessionManager(endpoints)

	// Test concurrent session creation
	done := make(chan bool, 10)
	sessionIDs := make(chan string, 10)

	for i := 0; i < 10; i++ {
		go func(index int) {
			_, sessionID, err := sm.GetSandboxInfoBySessionId("", "default", "test-agent", KindAgent)
			if err != nil {
				t.Errorf("Concurrent session creation failed: %v", err)
			} else {
				sessionIDs <- sessionID
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
	close(sessionIDs)

	// Verify all sessions were created with unique IDs
	uniqueIDs := make(map[string]bool)
	for sessionID := range sessionIDs {
		if uniqueIDs[sessionID] {
			t.Errorf("Duplicate session ID found: %s", sessionID)
		}
		uniqueIDs[sessionID] = true
	}

	if len(uniqueIDs) != 10 {
		t.Errorf("Expected 10 unique session IDs, got %d", len(uniqueIDs))
	}
}
