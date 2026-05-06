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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// MockStore implements store.Store interface for testing
type MockStore struct {
	mock.Mock
}

func (m *MockStore) Ping(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockStore) GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxInfo, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	sandbox, _ := args.Get(0).(*types.SandboxInfo)
	return sandbox, args.Error(1)
}

func (m *MockStore) StoreSandbox(ctx context.Context, sandbox *types.SandboxInfo) error {
	args := m.Called(ctx, sandbox)
	return args.Error(0)
}

func (m *MockStore) UpdateSandbox(ctx context.Context, sandbox *types.SandboxInfo) error {
	args := m.Called(ctx, sandbox)
	return args.Error(0)
}

func (m *MockStore) DeleteSandboxBySessionID(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

//nolint:errcheck // Mock type assertion
func (m *MockStore) ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	args := m.Called(ctx, before, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.SandboxInfo), args.Error(1)
}

//nolint:errcheck // Mock type assertion
func (m *MockStore) ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error) {
	args := m.Called(ctx, before, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.SandboxInfo), args.Error(1)
}

func (m *MockStore) UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error {
	args := m.Called(ctx, sessionID, at)
	return args.Error(0)
}

func (m *MockStore) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockStore) GetSandboxByE2BSandboxID(ctx context.Context, e2bSandboxID string) (*types.SandboxInfo, error) {
	args := m.Called(ctx, e2bSandboxID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	sandbox, _ := args.Get(0).(*types.SandboxInfo)
	return sandbox, args.Error(1)
}

//nolint:errcheck // Mock type assertion
func (m *MockStore) ListSandboxesByAPIKeyHash(ctx context.Context, apiKeyHash string) ([]*types.SandboxInfo, error) {
	args := m.Called(ctx, apiKeyHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*types.SandboxInfo), args.Error(1)
}

func (m *MockStore) UpdateSandboxTTL(ctx context.Context, sessionID string, expiresAt time.Time) error {
	args := m.Called(ctx, sessionID, expiresAt)
	return args.Error(0)
}

// MockSessionManager implements SessionManager interface for testing
type MockSessionManager struct {
	mock.Mock
}

//nolint:errcheck // Mock type assertion
func (m *MockSessionManager) GetSandboxBySession(ctx context.Context, sessionID, namespace, name, kind string, envVars map[string]string) (*types.SandboxInfo, error) {
	args := m.Called(ctx, sessionID, namespace, name, kind, envVars)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*types.SandboxInfo), args.Error(1)
}

func setupTestServer() (*gin.Engine, *MockStore, *MockSessionManager) {
	gin.SetMode(gin.TestMode)
	mockStore := new(MockStore)
	mockSessionMgr := new(MockSessionManager)

	router := gin.New()
	v1 := router.Group("/v1")

	_, _ = NewServerWithAuthenticator(v1, mockStore, mockSessionMgr, NewAuthenticatorWithMap(map[string]string{
		"test-api-key": "test-client",
	}))

	return router, mockStore, mockSessionMgr
}

func TestHandleCreateSandbox(t *testing.T) {
	router, mockStore, mockSessionMgr := setupTestServer()

	tests := []struct {
		name             string
		requestBody      interface{}
		mockSetup        func()
		expectedStatus   int
		expectedClientID string
	}{
		{
			name: "success create sandbox",
			requestBody: NewSandbox{
				TemplateID: "test-template",
				Timeout:    60,
			},
			mockSetup: func() {
				// IDGenerator probes store for collision detection
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, mock.AnythingOfType("string")).
					Return(nil, store.ErrNotFound).Maybe()
				mockSessionMgr.On("GetSandboxBySession", mock.Anything, "", "test-client", "test-template", types.CodeInterpreterKind, mock.Anything).
					Return(&types.SandboxInfo{
						SandboxID:        "sb-123",
						SandboxNamespace: "test-client",
						Name:             "test-template",
						SessionID:        "session-123",
						CreatedAt:        time.Now(),
						ExpiresAt:        time.Now().Add(60 * time.Second),
						Status:           "running",
						EntryPoints: []types.SandboxEntryPoint{
							{Path: "/", Protocol: "http", Endpoint: "10.0.0.1:8080"},
						},
					}, nil).Once()
				mockStore.On("UpdateSandbox", mock.Anything, mock.AnythingOfType("*types.SandboxInfo")).
					Return(nil).Once()
				mockStore.On("UpdateSandboxTTL", mock.Anything, "session-123", mock.AnythingOfType("time.Time")).
					Return(nil).Once()
			},
			expectedStatus:   http.StatusCreated,
			expectedClientID: hashKey("test-api-key"),
		},
		{
			name: "success create sandbox with env vars",
			requestBody: NewSandbox{
				TemplateID: "test-template",
				Timeout:    60,
				EnvVars: map[string]string{
					"FOO": "bar",
					"BAZ": "qux",
				},
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, mock.AnythingOfType("string")).
					Return(nil, store.ErrNotFound).Maybe()
				mockSessionMgr.On("GetSandboxBySession", mock.Anything, "", "test-client", "test-template", types.CodeInterpreterKind, map[string]string{"FOO": "bar", "BAZ": "qux"}).
					Return(&types.SandboxInfo{
						SandboxID:        "sb-456",
						SandboxNamespace: "test-client",
						Name:             "test-template",
						SessionID:        "session-456",
						CreatedAt:        time.Now(),
						ExpiresAt:        time.Now().Add(60 * time.Second),
						Status:           "running",
						EntryPoints: []types.SandboxEntryPoint{
							{Path: "/", Protocol: "http", Endpoint: "10.0.0.1:8080"},
						},
					}, nil).Once()
				mockStore.On("UpdateSandbox", mock.Anything, mock.AnythingOfType("*types.SandboxInfo")).
					Return(nil).Once()
				mockStore.On("UpdateSandboxTTL", mock.Anything, "session-456", mock.AnythingOfType("time.Time")).
					Return(nil).Once()
			},
			expectedStatus:   http.StatusCreated,
			expectedClientID: hashKey("test-api-key"),
		},
		{
			name:           "invalid request body",
			requestBody:    "invalid json",
			mockSetup:      func() {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "auto_pause not supported",
			requestBody: NewSandbox{
				TemplateID: "test-template",
				AutoPause:  true,
			},
			mockSetup:      func() {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "session manager error",
			requestBody: NewSandbox{
				TemplateID: "test-template",
				Timeout:    60,
			},
			mockSetup: func() {
				mockSessionMgr.On("GetSandboxBySession", mock.Anything, "", "test-client", "test-template", types.CodeInterpreterKind, mock.Anything).
					Return(nil, errors.New("failed to create sandbox")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockSetup()

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedClientID != "" {
				var resp Sandbox
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedClientID, resp.ClientID)
			}
			mockSessionMgr.AssertExpectations(t)
		})
	}
}

func TestHandleListSandboxes(t *testing.T) {
	router, mockStore, _ := setupTestServer()

	tests := []struct {
		name           string
		mockSetup      func()
		expectedStatus int
		expectedCount  int
	}{
		{
			name: "success list sandboxes",
			mockSetup: func() {
				mockStore.On("ListSandboxesByAPIKeyHash", mock.Anything, mock.AnythingOfType("string")).
					Return([]*types.SandboxInfo{
						{
							SandboxID:        "sb-1",
							SandboxNamespace: "default",
							Name:             "template-1",
							SessionID:        "session-1",
							CreatedAt:        time.Now(),
							ExpiresAt:        time.Now().Add(60 * time.Second),
							Status:           "running",
						},
						{
							SandboxID:        "sb-2",
							SandboxNamespace: "default",
							Name:             "template-2",
							SessionID:        "session-2",
							CreatedAt:        time.Now(),
							ExpiresAt:        time.Now().Add(120 * time.Second),
							Status:           "running",
						},
					}, nil).Once()
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name: "empty list",
			mockSetup: func() {
				mockStore.On("ListSandboxesByAPIKeyHash", mock.Anything, mock.AnythingOfType("string")).
					Return([]*types.SandboxInfo{}, nil).Once()
			},
			expectedStatus: http.StatusOK,
			expectedCount:  0,
		},
		{
			name: "store error",
			mockSetup: func() {
				mockStore.On("ListSandboxesByAPIKeyHash", mock.Anything, mock.AnythingOfType("string")).
					Return(nil, errors.New("store error")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
			expectedCount:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockSetup()

			req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes", nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if tt.expectedStatus == http.StatusOK {
				var response []ListedSandbox
				err := json.Unmarshal(w.Body.Bytes(), &response)
				assert.NoError(t, err)
				assert.Len(t, response, tt.expectedCount)
				for _, sb := range response {
					assert.Equal(t, hashKey("test-api-key"), sb.ClientID)
				}
			}

			mockStore.AssertExpectations(t)
		})
	}
}

func TestHandleGetSandbox(t *testing.T) {
	router, mockStore, _ := setupTestServer()

	tests := []struct {
		name           string
		sandboxID      string
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:      "success get sandbox",
			sandboxID: "e2b-sb-123",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-sb-123").
					Return(&types.SandboxInfo{
						SandboxID:        "sb-123",
						E2BSandboxID:     "e2b-sb-123",
						APIKeyHash:       hashKey("test-api-key"),
						SandboxNamespace: "default",
						Name:             "template-1",
						SessionID:        "session-123",
						CreatedAt:        time.Now(),
						ExpiresAt:        time.Now().Add(60 * time.Second),
						Status:           "running",
						EntryPoints: []types.SandboxEntryPoint{
							{Path: "/", Protocol: "http", Endpoint: "10.0.0.1:8080"},
						},
					}, nil).Once()
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:      "sandbox not found",
			sandboxID: "e2b-notfound",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-notfound").
					Return(nil, store.ErrNotFound).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:      "store error",
			sandboxID: "e2b-error",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-error").
					Return(nil, errors.New("store error")).Once()
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:      "sandbox owned by different api key",
			sandboxID: "e2b-other",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-other").
					Return(&types.SandboxInfo{
						E2BSandboxID: "e2b-other",
						APIKeyHash:   "different-hash",
						SessionID:    "session-other",
					}, nil).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockSetup()

			req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/"+tt.sandboxID, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedStatus == http.StatusOK {
				var resp SandboxDetail
				err := json.Unmarshal(w.Body.Bytes(), &resp)
				assert.NoError(t, err)
				assert.Equal(t, hashKey("test-api-key"), resp.ClientID)
			}
			mockStore.AssertExpectations(t)
		})
	}
}

func TestHandleDeleteSandbox(t *testing.T) {
	router, mockStore, _ := setupTestServer()

	tests := []struct {
		name           string
		sandboxID      string
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:      "success delete sandbox",
			sandboxID: "e2b-sb-123",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-sb-123").
					Return(&types.SandboxInfo{
						SandboxID:    "sb-123",
						E2BSandboxID: "e2b-sb-123",
						APIKeyHash:   hashKey("test-api-key"),
						SessionID:    "session-123",
					}, nil).Once()
				mockStore.On("DeleteSandboxBySessionID", mock.Anything, "session-123").
					Return(nil).Once()
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:      "sandbox not found",
			sandboxID: "e2b-notfound",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-notfound").
					Return(nil, store.ErrNotFound).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:      "sandbox owned by different api key",
			sandboxID: "e2b-other",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-other").
					Return(&types.SandboxInfo{
						E2BSandboxID: "e2b-other",
						APIKeyHash:   "different-hash",
						SessionID:    "session-other",
					}, nil).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockSetup()

			req := httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/"+tt.sandboxID, nil)
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			mockStore.AssertExpectations(t)
		})
	}
}

func TestHandleSetTimeout(t *testing.T) {
	router, mockStore, _ := setupTestServer()

	tests := []struct {
		name           string
		sandboxID      string
		requestBody    TimeoutRequest
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:      "success set timeout",
			sandboxID: "e2b-sb-123",
			requestBody: TimeoutRequest{
				Timeout: 300,
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-sb-123").
					Return(&types.SandboxInfo{
						SandboxID:    "sb-123",
						E2BSandboxID: "e2b-sb-123",
						APIKeyHash:   hashKey("test-api-key"),
						SessionID:    "session-123",
						ExpiresAt:    time.Now(),
					}, nil).Once()
				mockStore.On("UpdateSandboxTTL", mock.Anything, "session-123", mock.AnythingOfType("time.Time")).
					Return(nil).Once()
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:      "sandbox not found",
			sandboxID: "e2b-notfound",
			requestBody: TimeoutRequest{
				Timeout: 300,
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-notfound").
					Return(nil, store.ErrNotFound).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:      "sandbox owned by different api key",
			sandboxID: "e2b-other",
			requestBody: TimeoutRequest{
				Timeout: 300,
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-other").
					Return(&types.SandboxInfo{
						E2BSandboxID: "e2b-other",
						APIKeyHash:   "different-hash",
						SessionID:    "session-other",
					}, nil).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "invalid request body",
			sandboxID:      "e2b-sb-123",
			requestBody:    TimeoutRequest{},
			mockSetup:      func() {},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockSetup()

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+tt.sandboxID+"/timeout", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			mockStore.AssertExpectations(t)
		})
	}
}

func TestHandleRefreshSandbox(t *testing.T) {
	router, mockStore, _ := setupTestServer()

	tests := []struct {
		name           string
		sandboxID      string
		requestBody    RefreshRequest
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:      "success refresh with timeout",
			sandboxID: "e2b-sb-123",
			requestBody: RefreshRequest{
				Timeout: 300,
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-sb-123").
					Return(&types.SandboxInfo{
						SandboxID:    "sb-123",
						E2BSandboxID: "e2b-sb-123",
						APIKeyHash:   hashKey("test-api-key"),
						SessionID:    "session-123",
						ExpiresAt:    time.Now(),
					}, nil).Once()
				mockStore.On("UpdateSandboxTTL", mock.Anything, "session-123", mock.AnythingOfType("time.Time")).
					Return(nil).Once()
				mockStore.On("UpdateSessionLastActivity", mock.Anything, "session-123", mock.AnythingOfType("time.Time")).
					Return(nil).Once()
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:        "success refresh without timeout",
			sandboxID:   "e2b-sb-123",
			requestBody: RefreshRequest{},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-sb-123").
					Return(&types.SandboxInfo{
						SandboxID:    "sb-123",
						E2BSandboxID: "e2b-sb-123",
						APIKeyHash:   hashKey("test-api-key"),
						SessionID:    "session-123",
						ExpiresAt:    time.Now().Add(60 * time.Second),
					}, nil).Once()
				mockStore.On("UpdateSessionLastActivity", mock.Anything, "session-123", mock.AnythingOfType("time.Time")).
					Return(nil).Once()
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:      "sandbox not found",
			sandboxID: "e2b-notfound",
			requestBody: RefreshRequest{
				Timeout: 300,
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-notfound").
					Return(nil, store.ErrNotFound).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:      "sandbox owned by different api key",
			sandboxID: "e2b-other",
			requestBody: RefreshRequest{
				Timeout: 300,
			},
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, "e2b-other").
					Return(&types.SandboxInfo{
						E2BSandboxID: "e2b-other",
						APIKeyHash:   "different-hash",
						SessionID:    "session-other",
					}, nil).Once()
			},
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.mockSetup()

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/v1/sandboxes/"+tt.sandboxID+"/refreshes", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "test-api-key")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			mockStore.AssertExpectations(t)
		})
	}
}

func TestAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mockStore := new(MockStore)
	mockSessionMgr := new(MockSessionManager)

	router := gin.New()
	v1 := router.Group("/v1")

	// Create server with authentication enabled
	auth := NewAuthenticatorWithMap(map[string]string{
		"valid-api-key": "test-client",
	})
	_, _ = NewServerWithAuthenticator(v1, mockStore, mockSessionMgr, auth)

	tests := []struct {
		name           string
		apiKey         string
		mockSetup      func()
		expectedStatus int
	}{
		{
			name:           "missing api key",
			apiKey:         "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "invalid api key",
			apiKey:         "invalid-key",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:   "valid api key",
			apiKey: "valid-api-key",
			mockSetup: func() {
				mockStore.On("GetSandboxByE2BSandboxID", mock.Anything, mock.AnythingOfType("string")).
					Return(&types.SandboxInfo{
						SandboxID:    "sb-123",
						E2BSandboxID: "e2b-sb-123",
						APIKeyHash:   hashKey("valid-api-key"),
						SessionID:    "session-123",
						CreatedAt:    time.Now(),
						ExpiresAt:    time.Now().Add(60 * time.Second),
						Status:       "running",
					}, nil).Once()
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.mockSetup != nil {
				tt.mockSetup()
			}

			req := httptest.NewRequest(http.MethodGet, "/v1/sandboxes/e2b-sb-123", nil)
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			mockStore.AssertExpectations(t)
		})
	}
}
