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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// TestConfig holds configuration for E2B API tests
type TestConfig struct {
	EnableAuth     bool
	MockK8sClient  bool
	BasePath       string
	MaxConcurrency int
}

// DefaultTestConfig returns default test configuration
func DefaultTestConfig() *TestConfig {
	return &TestConfig{
		EnableAuth:     false,
		MockK8sClient:  true,
		BasePath:       "/v1",
		MaxConcurrency: 100,
	}
}

// MockStore wraps a store.Store for testing
type MockStore struct {
	store.Store
	mu             sync.RWMutex
	sandboxes      map[string]*types.SandboxInfo
	sessionIndex   map[string]string // sessionID -> sandboxID
	expiresAtIndex map[string]time.Time
	activityIndex  map[string]time.Time
	storeErr       error
	getErr         error
	deleteErr      error
	updateErr      error
	listErr        error
}

// NewMockStore creates a new mock store
func NewMockStore() *MockStore {
	return &MockStore{
		sandboxes:      make(map[string]*types.SandboxInfo),
		sessionIndex:   make(map[string]string),
		expiresAtIndex: make(map[string]time.Time),
		activityIndex:  make(map[string]time.Time),
	}
}

// Ping implements store.Store
func (m *MockStore) Ping(_ context.Context) error {
	return nil
}

// GetSandboxBySessionID implements store.Store
func (m *MockStore) GetSandboxBySessionID(_ context.Context, sessionID string) (*types.SandboxInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.getErr != nil {
		return nil, m.getErr
	}

	sandboxID, exists := m.sessionIndex[sessionID]
	if !exists {
		return nil, store.ErrNotFound
	}

	sb, exists := m.sandboxes[sandboxID]
	if !exists {
		return nil, store.ErrNotFound
	}

	return sb, nil
}

// StoreSandbox implements store.Store
func (m *MockStore) StoreSandbox(_ context.Context, sb *types.SandboxInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.storeErr != nil {
		return m.storeErr
	}

	m.sandboxes[sb.SandboxID] = sb
	m.sessionIndex[sb.SessionID] = sb.SandboxID
	if !sb.ExpiresAt.IsZero() {
		m.expiresAtIndex[sb.SessionID] = sb.ExpiresAt
	}
	return nil
}

// UpdateSandbox implements store.Store
func (m *MockStore) UpdateSandbox(_ context.Context, sb *types.SandboxInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.updateErr != nil {
		return m.updateErr
	}

	if _, exists := m.sandboxes[sb.SandboxID]; !exists {
		return fmt.Errorf("sandbox not found: %s", sb.SandboxID)
	}

	m.sandboxes[sb.SandboxID] = sb
	if !sb.ExpiresAt.IsZero() {
		m.expiresAtIndex[sb.SessionID] = sb.ExpiresAt
	}
	return nil
}

// DeleteSandboxBySessionID implements store.Store
func (m *MockStore) DeleteSandboxBySessionID(_ context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.deleteErr != nil {
		return m.deleteErr
	}

	sandboxID, exists := m.sessionIndex[sessionID]
	if !exists {
		return store.ErrNotFound
	}

	delete(m.sandboxes, sandboxID)
	delete(m.sessionIndex, sessionID)
	delete(m.expiresAtIndex, sessionID)
	delete(m.activityIndex, sessionID)
	return nil
}

// ListExpiredSandboxes implements store.Store
func (m *MockStore) ListExpiredSandboxes(_ context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.listErr != nil {
		return nil, m.listErr
	}

	var result []*types.SandboxInfo
	for sessionID, expiresAt := range m.expiresAtIndex {
		if expiresAt.Before(before) {
			sandboxID := m.sessionIndex[sessionID]
			if sb, exists := m.sandboxes[sandboxID]; exists {
				result = append(result, sb)
			}
			if int64(len(result)) >= limit {
				break
			}
		}
	}
	return result, nil
}

// ListInactiveSandboxes implements store.Store
func (m *MockStore) ListInactiveSandboxes(_ context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.listErr != nil {
		return nil, m.listErr
	}

	var result []*types.SandboxInfo
	for sessionID, lastActivity := range m.activityIndex {
		if lastActivity.Before(before) {
			sandboxID := m.sessionIndex[sessionID]
			if sb, exists := m.sandboxes[sandboxID]; exists {
				result = append(result, sb)
			}
			if int64(len(result)) >= limit {
				break
			}
		}
	}
	return result, nil
}

// UpdateSessionLastActivity implements store.Store
func (m *MockStore) UpdateSessionLastActivity(_ context.Context, sessionID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessionIndex[sessionID]; !exists {
		return store.ErrNotFound
	}

	m.activityIndex[sessionID] = at
	return nil
}

// Close implements store.Store
func (m *MockStore) Close() error {
	return nil
}

// SetStoreError sets error for StoreSandbox
func (m *MockStore) SetStoreError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storeErr = err
}

// SetGetError sets error for GetSandboxBySessionID
func (m *MockStore) SetGetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getErr = err
}

// SetDeleteError sets error for DeleteSandboxBySessionID
func (m *MockStore) SetDeleteError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteErr = err
}

// SetUpdateError sets error for UpdateSandbox
func (m *MockStore) SetUpdateError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.updateErr = err
}

// SetListError sets error for list operations
func (m *MockStore) SetListError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.listErr = err
}

// RedisTestStore wraps miniredis for integration-style tests
type RedisTestStore struct {
	store.Store
	mr *miniredis.Miniredis
}

// NewRedisTestStore creates a new test store using miniredis
func NewRedisTestStore(t *testing.T) *RedisTestStore {
	mr := miniredis.RunT(t)
	// The actual store initialization would be done by the caller
	// using the miniredis address
	return &RedisTestStore{mr: mr}
}

// Addr returns the miniredis address
func (r *RedisTestStore) Addr() string {
	return r.mr.Addr()
}

// Close closes the miniredis instance
func (r *RedisTestStore) Close() {
	r.mr.Close()
}

// TestServer represents a test E2B API server
type TestServer struct {
	Router    *gin.Engine
	Store     *MockStore
	Server    *httptest.Server
	Config    *TestConfig
	mu        sync.RWMutex
	sandboxes map[string]*SandboxRecord
}

// SandboxRecord represents a sandbox in the test server
type SandboxRecord struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
	LastActivity time.Time         `json:"last_activity"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Config       *SandboxConfig    `json:"config,omitempty"`
}

// SandboxConfig represents sandbox configuration
type SandboxConfig struct {
	Template    string            `json:"template,omitempty"`
	Resources   ResourceConfig    `json:"resources,omitempty"`
	EnvVars     map[string]string `json:"env_vars,omitempty"`
	TimeoutSecs int               `json:"timeout_secs,omitempty"`
}

// ResourceConfig represents resource configuration
type ResourceConfig struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Disk   string `json:"disk,omitempty"`
}

// CreateSandboxRequest represents a create sandbox request
type CreateSandboxRequest struct {
	Name   string        `json:"name,omitempty"`
	Config SandboxConfig `json:"config,omitempty"`
}

// CreateSandboxResponse represents a create sandbox response
type CreateSandboxResponse struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// SandboxResponse represents a single sandbox response
type SandboxResponse struct {
	ID           string            `json:"id"`
	SessionID    string            `json:"session_id"`
	Name         string            `json:"name"`
	Status       string            `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	ExpiresAt    *time.Time        `json:"expires_at,omitempty"`
	LastActivity time.Time         `json:"last_activity"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

// ListSandboxesResponse represents list sandboxes response
type ListSandboxesResponse struct {
	Sandboxes []SandboxResponse `json:"sandboxes"`
	Total     int               `json:"total"`
}

// SetTimeoutRequest represents set timeout request
type SetTimeoutRequest struct {
	TimeoutSecs int `json:"timeout_secs"`
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// SuccessResponse represents a success response
type SuccessResponse struct {
	Message string `json:"message"`
}

// RefreshResponse represents a refresh response
type RefreshResponse struct {
	Message      string    `json:"message"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	LastActivity time.Time `json:"last_activity,omitempty"`
}

// NewTestServer creates a new test E2B API server
func NewTestServer(_ *testing.T, config *TestConfig) *TestServer {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	store := NewMockStore()
	ts := &TestServer{
		Router:    router,
		Store:     store,
		Config:    config,
		sandboxes: make(map[string]*SandboxRecord),
	}

	ts.setupRoutes()

	server := httptest.NewServer(router)
	ts.Server = server

	return ts
}

// setupRoutes configures the test E2B API routes
func (ts *TestServer) setupRoutes() {
	base := ts.Router.Group(ts.Config.BasePath)

	// Health check
	ts.Router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy"})
	})

	// Sandbox management endpoints
	base.POST("/sandboxes", ts.handleCreateSandbox)
	base.GET("/sandboxes", ts.handleListSandboxes)
	base.GET("/sandboxes/:id", ts.handleGetSandbox)
	base.DELETE("/sandboxes/:id", ts.handleDeleteSandbox)
	base.POST("/sandboxes/:id/timeout", ts.handleSetTimeout)
	base.POST("/sandboxes/:id/refreshes", ts.handleRefresh)
}

// handleCreateSandbox handles POST /sandboxes
func (ts *TestServer) handleCreateSandbox(c *gin.Context) {
	var req CreateSandboxRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request body: " + err.Error(),
		})
		return
	}

	// Generate IDs
	id := generateTestID("sb_")
	sessionID := generateTestID("sess_")
	now := time.Now().UTC()

	// Set default timeout
	timeoutSecs := req.Config.TimeoutSecs
	if timeoutSecs <= 0 {
		timeoutSecs = 3600 // Default 1 hour
	}
	expiresAt := now.Add(time.Duration(timeoutSecs) * time.Second)

	sb := &SandboxRecord{
		ID:           id,
		SessionID:    sessionID,
		Name:         req.Name,
		Status:       "running",
		CreatedAt:    now,
		ExpiresAt:    &expiresAt,
		LastActivity: now,
		Metadata:     make(map[string]string),
		Config:       &req.Config,
	}

	// Store in mock store
	storeInfo := &types.SandboxInfo{
		SandboxID:        id,
		SessionID:        sessionID,
		Name:             req.Name,
		Status:           "running",
		CreatedAt:        now,
		ExpiresAt:        expiresAt,
		SandboxNamespace: "default",
	}

	if err := ts.Store.StoreSandbox(c.Request.Context(), storeInfo); err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			Error:   "internal_error",
			Message: "Failed to store sandbox: " + err.Error(),
		})
		return
	}

	ts.mu.Lock()
	ts.sandboxes[id] = sb
	ts.mu.Unlock()

	c.JSON(http.StatusCreated, CreateSandboxResponse{
		ID:        id,
		SessionID: sessionID,
		Status:    "running",
		CreatedAt: now,
		ExpiresAt: expiresAt,
	})
}

// handleListSandboxes handles GET /sandboxes
func (ts *TestServer) handleListSandboxes(c *gin.Context) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	var sandboxes []SandboxResponse
	for _, sb := range ts.sandboxes {
		sandboxes = append(sandboxes, SandboxResponse{
			ID:           sb.ID,
			SessionID:    sb.SessionID,
			Name:         sb.Name,
			Status:       sb.Status,
			CreatedAt:    sb.CreatedAt,
			ExpiresAt:    sb.ExpiresAt,
			LastActivity: sb.LastActivity,
			Metadata:     sb.Metadata,
		})
	}

	c.JSON(http.StatusOK, ListSandboxesResponse{
		Sandboxes: sandboxes,
		Total:     len(sandboxes),
	})
}

// handleGetSandbox handles GET /sandboxes/{id}
func (ts *TestServer) handleGetSandbox(c *gin.Context) {
	id := c.Param("id")

	ts.mu.RLock()
	sb, exists := ts.sandboxes[id]
	if !exists {
		ts.mu.RUnlock()
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "not_found",
			Code:    "SANDBOX_NOT_FOUND",
			Message: "Sandbox not found: " + id,
		})
		return
	}

	// Copy sandbox data while holding lock to avoid race conditions
	response := SandboxResponse{
		ID:           sb.ID,
		SessionID:    sb.SessionID,
		Name:         sb.Name,
		Status:       sb.Status,
		CreatedAt:    sb.CreatedAt,
		ExpiresAt:    sb.ExpiresAt,
		LastActivity: sb.LastActivity,
		Metadata:     sb.Metadata,
	}
	ts.mu.RUnlock()

	c.JSON(http.StatusOK, response)
}

// handleDeleteSandbox handles DELETE /sandboxes/{id}
func (ts *TestServer) handleDeleteSandbox(c *gin.Context) {
	id := c.Param("id")

	ts.mu.Lock()
	sb, exists := ts.sandboxes[id]
	if !exists {
		ts.mu.Unlock()
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "not_found",
			Code:    "SANDBOX_NOT_FOUND",
			Message: "Sandbox not found: " + id,
		})
		return
	}

	delete(ts.sandboxes, id)
	ts.mu.Unlock()

	// Also delete from store
	if err := ts.Store.DeleteSandboxBySessionID(c.Request.Context(), sb.SessionID); err != nil {
		// Log error but don't fail the request
		// In real implementation, use proper logging
		_ = err
	}

	c.JSON(http.StatusOK, SuccessResponse{
		Message: "Sandbox deleted successfully",
	})
}

// handleSetTimeout handles POST /sandboxes/{id}/timeout
func (ts *TestServer) handleSetTimeout(c *gin.Context) {
	id := c.Param("id")

	var req SetTimeoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "Invalid request body: " + err.Error(),
		})
		return
	}

	if req.TimeoutSecs <= 0 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_request",
			Message: "timeout_secs must be greater than 0",
		})
		return
	}

	ts.mu.Lock()
	sb, exists := ts.sandboxes[id]
	if !exists {
		ts.mu.Unlock()
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "not_found",
			Code:    "SANDBOX_NOT_FOUND",
			Message: "Sandbox not found: " + id,
		})
		return
	}

	newExpiresAt := time.Now().UTC().Add(time.Duration(req.TimeoutSecs) * time.Second)
	sb.ExpiresAt = &newExpiresAt

	// Capture values while holding lock to avoid race conditions
	sessionID := sb.SessionID
	response := SandboxResponse{
		ID:           sb.ID,
		SessionID:    sb.SessionID,
		Name:         sb.Name,
		Status:       sb.Status,
		CreatedAt:    sb.CreatedAt,
		ExpiresAt:    sb.ExpiresAt,
		LastActivity: sb.LastActivity,
	}
	ts.mu.Unlock()

	// Update in store
	storeInfo, err := ts.Store.GetSandboxBySessionID(c.Request.Context(), sessionID)
	if err == nil {
		// Create a copy to avoid race conditions with other goroutines
		updatedInfo := *storeInfo
		updatedInfo.ExpiresAt = newExpiresAt
		_ = ts.Store.UpdateSandbox(c.Request.Context(), &updatedInfo)
	}

	c.JSON(http.StatusOK, response)
}

// handleRefresh handles POST /sandboxes/{id}/refreshes
func (ts *TestServer) handleRefresh(c *gin.Context) {
	id := c.Param("id")

	ts.mu.Lock()
	sb, exists := ts.sandboxes[id]
	if !exists {
		ts.mu.Unlock()
		c.JSON(http.StatusNotFound, ErrorResponse{
			Error:   "not_found",
			Code:    "SANDBOX_NOT_FOUND",
			Message: "Sandbox not found: " + id,
		})
		return
	}

	now := time.Now().UTC()
	sb.LastActivity = now

	// Extend expiration if configured
	if sb.Config != nil && sb.Config.TimeoutSecs > 0 {
		newExpiresAt := now.Add(time.Duration(sb.Config.TimeoutSecs) * time.Second)
		sb.ExpiresAt = &newExpiresAt
	}

	// Capture values while holding lock to avoid race conditions
	sessionID := sb.SessionID
	expiresAt := sb.ExpiresAt
	ts.mu.Unlock()

	// Update in store
	if err := ts.Store.UpdateSessionLastActivity(c.Request.Context(), sessionID, now); err != nil {
		// Don't fail the request, just log
		_ = err
	}

	c.JSON(http.StatusOK, RefreshResponse{
		Message:      "Sandbox refreshed successfully",
		LastActivity: now,
		ExpiresAt:    *expiresAt,
	})
}

// Close closes the test server
func (ts *TestServer) Close() {
	ts.Server.Close()
}

// generateTestID generates a test ID with the given prefix
func generateTestID(prefix string) string {
	return prefix + strings.ToLower(generateRandomString(16))
}

// generateRandomString generates a random string of the given length
func generateRandomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(b)
}

// Helper functions for tests

// MakeRequest makes an HTTP request to the test server
func (ts *TestServer) MakeRequest(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader *strings.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = strings.NewReader(string(jsonBody))
	} else {
		bodyReader = strings.NewReader("")
	}

	url := ts.Server.URL + path
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return http.DefaultClient.Do(req)
}

// ParseResponse parses an HTTP response into the given target
func ParseResponse(t *testing.T, resp *http.Response, target interface{}) {
	defer resp.Body.Close()
	decoder := json.NewDecoder(resp.Body)
	err := decoder.Decode(target)
	require.NoError(t, err)
}

// AssertStatus asserts the HTTP status code
func AssertStatus(t *testing.T, resp *http.Response, expected int) {
	assert.Equal(t, expected, resp.StatusCode, "Expected status %d, got %d", expected, resp.StatusCode)
}

// CreateTestSandbox creates a test sandbox and returns its ID
func (ts *TestServer) CreateTestSandbox(t *testing.T, name string) *CreateSandboxResponse {
	req := CreateSandboxRequest{
		Name: name,
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 3600,
			Resources: ResourceConfig{
				CPU:    "1",
				Memory: "1Gi",
			},
		},
	}

	resp, err := ts.MakeRequest(http.MethodPost, ts.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)
	AssertStatus(t, resp, http.StatusCreated)

	var result CreateSandboxResponse
	ParseResponse(t, resp, &result)
	return &result
}

// SetupTest is a helper to setup a test with a test server
func SetupTest(t *testing.T) (*TestServer, *TestConfig) {
	config := DefaultTestConfig()
	server := NewTestServer(t, config)
	return server, config
}

// TeardownTest cleans up after a test
func TeardownTest(ts *TestServer) {
	ts.Close()
}
