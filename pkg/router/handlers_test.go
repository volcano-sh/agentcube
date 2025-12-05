package router

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/volcano-sh/agentcube/pkg/common/types"
)

func init() {
	// Set Gin to test mode
	gin.SetMode(gin.TestMode)
}

// Mock SessionManager for testing
type mockSessionManager struct {
	sandbox *types.SandboxRedis
	err     error
}

func (m *mockSessionManager) GetSandboxBySession(ctx context.Context, sessionID string, namespace string, name string, kind string) (*types.SandboxRedis, error) {
	return m.sandbox, m.err
}

func TestHandleHealth(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	config := &Config{
		Port: "8080",
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	server.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	expectedBody := `{"status":"healthy"}`
	if w.Body.String() != expectedBody {
		t.Errorf("Expected body %s, got %s", expectedBody, w.Body.String())
	}
}

func TestHandleHealthLive(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	config := &Config{
		Port: "8080",
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health/live", nil)
	server.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	expectedBody := `{"status":"alive"}`
	if w.Body.String() != expectedBody {
		t.Errorf("Expected body %s, got %s", expectedBody, w.Body.String())
	}
}

func TestHandleHealthReady(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	tests := []struct {
		name               string
		sessionManager     SessionManager
		expectedStatusCode int
		expectedBody       string
	}{
		{
			name:               "ready with session manager",
			sessionManager:     &mockSessionManager{},
			expectedStatusCode: http.StatusOK,
			expectedBody:       `{"status":"ready"}`,
		},
		{
			name:               "not ready without session manager",
			sessionManager:     nil,
			expectedStatusCode: http.StatusServiceUnavailable,
			expectedBody:       `{"error":"session manager not available","status":"not ready"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				Port: "8080",
			}

			server, err := NewServer(config)
			if err != nil {
				t.Fatalf("Failed to create server: %v", err)
			}

			// Override session manager for testing
			server.sessionManager = tt.sessionManager

			w := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/health/ready", nil)
			server.engine.ServeHTTP(w, req)

			if w.Code != tt.expectedStatusCode {
				t.Errorf("Expected status code %d, got %d", tt.expectedStatusCode, w.Code)
			}

			if w.Body.String() != tt.expectedBody {
				t.Errorf("Expected body %s, got %s", tt.expectedBody, w.Body.String())
			}
		})
	}
}

func TestHandleInvoke_SessionManagerError(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	config := &Config{
		Port: "8080",
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Mock session manager that returns error
	server.sessionManager = &mockSessionManager{
		err: errors.New("session manager error"),
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", nil)
	server.engine.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}
}

func TestHandleInvoke_NoEntryPoints(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	config := &Config{
		Port: "8080",
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Mock session manager that returns sandbox with no entry points
	server.sessionManager = &mockSessionManager{
		sandbox: &types.SandboxRedis{
			SandboxID:   "test-sandbox",
			SessionID:   "test-session",
			EntryPoints: []types.SandboxEntryPoints{},
		},
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", nil)
	server.engine.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, w.Code)
	}
}

func TestHandleAgentInvoke(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	// Create a test HTTP server to act as the sandbox
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"success"}`))
	}))
	defer testServer.Close()

	config := &Config{
		Port:           "8080",
		RequestTimeout: 30,
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Mock session manager that returns sandbox with test server endpoint
	server.sessionManager = &mockSessionManager{
		sandbox: &types.SandboxRedis{
			SandboxID:   "test-sandbox",
			SessionID:   "test-session",
			SandboxName: "test-sandbox",
			EntryPoints: []types.SandboxEntryPoints{
				{
					Endpoint: testServer.URL,
					Path:     "/test",
				},
			},
		},
	}

	// Use real HTTP client instead of httptest.ResponseRecorder to avoid CloseNotifier panic
	req, _ := http.NewRequest("POST", "/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", nil)
	req.Header.Set("x-agentcube-session-id", "test-session")

	// Start a real test server
	testRouterServer := httptest.NewServer(server.engine)
	defer testRouterServer.Close()

	// Make real HTTP request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(testRouterServer.URL+"/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", "application/json", nil)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Check if session ID is set in response header
	sessionID := resp.Header.Get("x-agentcube-session-id")
	if sessionID != "test-session" {
		t.Errorf("Expected session ID 'test-session', got '%s'", sessionID)
	}
}

func TestHandleCodeInterpreterInvoke(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	// Create a test HTTP server to act as the sandbox
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"success"}`))
	}))
	defer testServer.Close()

	config := &Config{
		Port:           "8080",
		RequestTimeout: 30,
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Mock session manager that returns sandbox with test server endpoint
	server.sessionManager = &mockSessionManager{
		sandbox: &types.SandboxRedis{
			SandboxID:   "test-sandbox",
			SessionID:   "test-session",
			SandboxName: "test-sandbox",
			EntryPoints: []types.SandboxEntryPoints{
				{
					Endpoint: testServer.URL,
					Path:     "/execute",
				},
			},
		},
	}

	// Use real HTTP client instead of httptest.ResponseRecorder to avoid CloseNotifier panic
	testRouterServer := httptest.NewServer(server.engine)
	defer testRouterServer.Close()

	// Make real HTTP request
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(testRouterServer.URL+"/v1/namespaces/default/code-interpreters/test-ci/invocations/execute", "application/json", nil)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Check if session ID is set in response header
	sessionID := resp.Header.Get("x-agentcube-session-id")
	if sessionID != "test-session" {
		t.Errorf("Expected session ID 'test-session', got '%s'", sessionID)
	}
}

func TestForwardToSandbox_InvalidEndpoint(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	config := &Config{
		Port:           "8080",
		RequestTimeout: 30,
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Mock session manager that returns sandbox with invalid endpoint
	server.sessionManager = &mockSessionManager{
		sandbox: &types.SandboxRedis{
			SandboxID:   "test-sandbox",
			SessionID:   "test-session",
			SandboxName: "test-sandbox",
			EntryPoints: []types.SandboxEntryPoints{
				{
					Endpoint: "://invalid-url",
					Path:     "/test",
				},
			},
		},
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", nil)
	server.engine.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, w.Code)
	}
}

func TestConcurrencyLimitMiddleware_Overload(t *testing.T) {
	// Set required environment variables
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "test-password")
	os.Setenv("WORKLOAD_MGR_URL", "http://localhost:8080")
	defer func() {
		os.Unsetenv("REDIS_ADDR")
		os.Unsetenv("REDIS_PASSWORD")
		os.Unsetenv("WORKLOAD_MGR_URL")
	}()

	config := &Config{
		Port:                  "8080",
		MaxConcurrentRequests: 1, // Set to 1 to easily trigger overload
		RequestTimeout:        30,
	}

	server, err := NewServer(config)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	// Create a slow test server
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowServer.Close()

	// Mock session manager with slow response
	server.sessionManager = &mockSessionManager{
		sandbox: &types.SandboxRedis{
			SandboxID:   "test-sandbox",
			SessionID:   "test-session",
			SandboxName: "test-sandbox",
			EntryPoints: []types.SandboxEntryPoints{
				{
					Endpoint: slowServer.URL,
					Path:     "/test",
				},
			},
		},
	}

	// Start a real test server
	testRouterServer := httptest.NewServer(server.engine)
	defer testRouterServer.Close()

	// Start first request (will occupy the semaphore)
	done := make(chan bool)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		_, _ = client.Post(testRouterServer.URL+"/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", "application/json", nil)
		done <- true
	}()

	// Give first request time to acquire semaphore
	time.Sleep(50 * time.Millisecond)

	// Try second request (should be rejected due to overload)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(testRouterServer.URL+"/v1/namespaces/default/agent-runtimes/test-agent/invocations/test", "application/json", nil)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("Expected status code %d, got %d", http.StatusServiceUnavailable, resp.StatusCode)
	}

	// Wait for first request to complete
	<-done
}
