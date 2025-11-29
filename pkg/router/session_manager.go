package router

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// Kind constants for sandbox types
const (
	KindAgent           = "agent"
	KindCodeInterpreter = "codeinterpreter"
)

// SessionManager interface for managing sandbox sessions
type SessionManager interface {
	// GetSandboxInfoBySessionId returns sandbox endpoint and session ID
	// When sessionId is empty, creates a new session
	// kind can be "agent" or "codeinterpreter"
	GetSandboxInfoBySessionId(sessionId, namespace, name, kind string) (endpoint string, newSessionId string, err error)
}

// MockSessionManager is a simple implementation for testing
type MockSessionManager struct {
	mu               sync.RWMutex
	sandboxEndpoints []string
	currentIndex     int
	sessions         map[string]string // sessionId -> endpoint
}

// NewMockSessionManager creates a new mock session manager
func NewMockSessionManager(sandboxEndpoints []string) *MockSessionManager {
	if len(sandboxEndpoints) == 0 {
		// Default sandbox endpoints for testing
		sandboxEndpoints = []string{
			"http://sandbox-1:8080",
			"http://sandbox-2:8080",
			"http://sandbox-3:8080",
		}
	}

	return &MockSessionManager{
		sandboxEndpoints: sandboxEndpoints,
		sessions:         make(map[string]string),
	}
}

// GetSandboxInfoBySessionId implements SessionManager interface
func (m *MockSessionManager) GetSandboxInfoBySessionId(sessionId, namespace, name, kind string) (string, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate kind parameter
	if kind != "" && kind != KindAgent && kind != KindCodeInterpreter {
		return "", "", fmt.Errorf("invalid kind: %s, must be '%s' or '%s'", kind, KindAgent, KindCodeInterpreter)
	}

	// If sessionId is empty, create a new session
	if sessionId == "" {
		sessionId = m.generateNewSessionId()
	}

	// Check if session already exists
	if endpoint, exists := m.sessions[sessionId]; exists {
		return endpoint, sessionId, nil
	}

	// Create new session with round-robin endpoint selection
	if len(m.sandboxEndpoints) == 0 {
		return "", "", fmt.Errorf("no sandbox endpoints available")
	}

	// Select endpoint based on kind if specified
	var endpoint string
	if kind == KindAgent {
		// For agent kind, prefer agent-specific endpoints or use round-robin
		endpoint = m.sandboxEndpoints[m.currentIndex%len(m.sandboxEndpoints)]
	} else if kind == KindCodeInterpreter {
		// For codeinterpreter kind, prefer code interpreter endpoints or use round-robin
		endpoint = m.sandboxEndpoints[m.currentIndex%len(m.sandboxEndpoints)]
	} else {
		// Default behavior for backward compatibility
		endpoint = m.sandboxEndpoints[m.currentIndex%len(m.sandboxEndpoints)]
	}

	m.currentIndex++

	// Store the session with additional metadata
	sessionKey := sessionId
	if namespace != "" && name != "" {
		sessionKey = fmt.Sprintf("%s/%s/%s", namespace, name, sessionId)
	}
	m.sessions[sessionKey] = endpoint

	return endpoint, sessionId, nil
}

// generateNewSessionId generates a new UUID-based session ID
func (m *MockSessionManager) generateNewSessionId() string {
	return uuid.New().String()
}

// RemoveSession removes a session (for cleanup)
func (m *MockSessionManager) RemoveSession(sessionId string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, sessionId)
}

// GetSessionCount returns the number of active sessions
func (m *MockSessionManager) GetSessionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.sessions)
}
