package picoapiserver

import (
	"sync"
	"time"
)

// Session represents a sandbox session
type Session struct {
	SessionID      string                 `json:"sessionId"`
	Status         string                 `json:"status"` // "running" or "paused"
	CreatedAt      time.Time              `json:"createdAt"`
	ExpiresAt      time.Time              `json:"expiresAt"`
	LastActivityAt time.Time              `json:"lastActivityAt,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	SandboxName    string                 `json:"-"` // Kubernetes Sandbox CRD name
}

// SessionStore manages in-memory session storage
type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*Session
}

// NewSessionStore creates a new session store
func NewSessionStore() *SessionStore {
	return &SessionStore{
		sessions: make(map[string]*Session),
	}
}

// Set sets or updates a session
func (s *SessionStore) Set(sessionID string, session *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[sessionID] = session
}

// Get gets a session
func (s *SessionStore) Get(sessionID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[sessionID]
	if !exists {
		return nil
	}

	// Check if expired
	if time.Now().After(session.ExpiresAt) {
		return nil
	}

	return session
}

// Delete deletes a session
func (s *SessionStore) Delete(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

// List lists all non-expired sessions
func (s *SessionStore) List() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var sessions []*Session
	for _, session := range s.sessions {
		if now.Before(session.ExpiresAt) {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

// CleanExpired cleans up expired sessions
func (s *SessionStore) CleanExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	count := 0
	for sessionID, session := range s.sessions {
		if now.After(session.ExpiresAt) {
			delete(s.sessions, sessionID)
			count++
		}
	}
	return count
}

// CreateSessionRequest represents the request structure for creating a session
type CreateSessionRequest struct {
	TTL          int                    `json:"ttl,omitempty"`
	Image        string                 `json:"image,omitempty"`
	SSHPublicKey string                 `json:"sshPublicKey,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
