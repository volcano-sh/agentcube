package apiserver

import (
	"sync"
	"time"
)

// Sandbox represents a sandbox instance
type Sandbox struct {
	SandboxID      string                 `json:"sandboxId"`
	Status         string                 `json:"status"` // "running" or "paused"
	CreatedAt      time.Time              `json:"createdAt"`
	ExpiresAt      time.Time              `json:"expiresAt"`
	LastActivityAt time.Time              `json:"lastActivityAt,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
	SandboxName    string                 `json:"-"` // Kubernetes Sandbox CRD name
}

// SandboxStore manages in-memory sandbox storage
type SandboxStore struct {
	mu        sync.RWMutex
	sandboxes map[string]*Sandbox
}

// NewSandboxStore creates a new sandbox store
func NewSandboxStore() *SandboxStore {
	return &SandboxStore{
		sandboxes: make(map[string]*Sandbox),
	}
}

// Set sets or updates a sandbox
func (s *SandboxStore) Set(sandboxID string, sandbox *Sandbox) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sandboxes[sandboxID] = sandbox
}

// Get gets a sandbox
func (s *SandboxStore) Get(sandboxID string) *Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sandbox, exists := s.sandboxes[sandboxID]
	if !exists {
		return nil
	}

	// Check if expired
	if time.Now().After(sandbox.ExpiresAt) {
		return nil
	}

	return sandbox
}

// Delete deletes a sandbox
func (s *SandboxStore) Delete(sandboxID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sandboxes, sandboxID)
}

// List lists all non-expired sandboxes
func (s *SandboxStore) List() []*Sandbox {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	var sandboxes []*Sandbox
	for _, sandbox := range s.sandboxes {
		if now.Before(sandbox.ExpiresAt) {
			sandboxes = append(sandboxes, sandbox)
		}
	}
	return sandboxes
}

// CleanExpired cleans up expired sandboxes
func (s *SandboxStore) CleanExpired() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	count := 0
	for sandboxID, sandbox := range s.sandboxes {
		if now.After(sandbox.ExpiresAt) {
			delete(s.sandboxes, sandboxID)
			count++
		}
	}
	return count
}

// CreateSandboxRequest represents the request structure for creating a sandbox
type CreateSandboxRequest struct {
	TTL          int                    `json:"ttl,omitempty"`
	Image        string                 `json:"image,omitempty"`
	SSHPublicKey string                 `json:"sshPublicKey,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}
