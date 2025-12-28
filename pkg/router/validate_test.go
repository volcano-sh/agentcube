package router

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

/* ---------- 1. CONFIG PARSING ---------- */

func TestConfig_Defaults(t *testing.T) {
	t.Setenv("WORKLOAD_MANAGER_ADDR", "http://localhost:8080")
	t.Setenv("REDIS_ADDR", "dummy:6379")

	c := &Config{Port: "8080"}
	s, err := NewServer(c)
	require.NoError(t, err)
	if s.httpServer != nil {
		defer s.httpServer.Close()
	}

	assert.Equal(t, 1000, c.MaxConcurrentRequests)
	assert.Equal(t, 30, c.RequestTimeout)
	assert.Equal(t, 100, c.MaxIdleConns)
	assert.Equal(t, 10, c.MaxConnsPerHost)
}

// TLS cert/key validation occurs in server.Start(), not NewServer
func TestConfig_TLSValidation(t *testing.T) {
	t.Setenv("WORKLOAD_MANAGER_ADDR", "http://localhost:8080")
	t.Setenv("REDIS_ADDR", "dummy:6379")

	// EnableTLS alone is allowed in config
	_, err := NewServer(&Config{Port: "8443", EnableTLS: true})
	require.NoError(t, err)

	t.Log("Note: TLS file validation occurs in server.Start(), not NewServer")
}

/* ---------- 2. SESSION MANAGER ---------- */

type stubStore struct {
	sandbox *types.SandboxInfo
	err     error
}

func (s stubStore) GetSandboxBySessionID(_ context.Context, _ string) (*types.SandboxInfo, error) {
	return s.sandbox, s.err
}
func (s stubStore) SetSessionLockIfAbsent(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (s stubStore) BindSessionWithSandbox(_ context.Context, _ string, _ *types.SandboxInfo, _ time.Duration) error {
	return nil
}
func (s stubStore) DeleteSessionBySandboxID(_ context.Context, _ string) error { return nil }
func (s stubStore) DeleteSandboxBySessionID(_ context.Context, _ string) error { return nil }
func (s stubStore) UpdateSandbox(_ context.Context, _ *types.SandboxInfo) error { return nil }
func (s stubStore) UpdateSessionLastActivity(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (s stubStore) StoreSandbox(_ context.Context, _ *types.SandboxInfo) error { return nil }
func (s stubStore) Ping(_ context.Context) error                               { return nil }
func (s stubStore) ListExpiredSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (s stubStore) ListInactiveSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (s stubStore) UpdateSandboxLastActivity(_ context.Context, _ string, _ time.Time) error { return nil }

func TestSessionManager_CreateSandbox_AgentRuntime(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent-runtime", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		var req types.CreateSandboxRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, types.AgentRuntimeKind, req.Kind)

		_ = json.NewEncoder(w).Encode(types.CreateSandboxResponse{
			SessionID:   "sess-123",
			SandboxID:   "sb-456",
			SandboxName: "test-sb",
			EntryPoints: []types.SandboxEntryPoints{{Endpoint: "10.0.0.1:9000"}},
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Setenv("WORKLOAD_MANAGER_ADDR", srv.URL)
	mgr, err := NewSessionManager(stubStore{})
	require.NoError(t, err)

	sb, err := mgr.GetSandboxBySession(context.Background(), "", "ns", "agent1", types.AgentRuntimeKind)
	require.NoError(t, err)
	assert.Equal(t, "sess-123", sb.SessionID)
	assert.Equal(t, "sb-456", sb.SandboxID)
}

func TestSessionManager_CreateSandbox_UnsupportedKind(t *testing.T) {
	t.Setenv("WORKLOAD_MANAGER_ADDR", "http://localhost:8080")
	mgr, err := NewSessionManager(stubStore{})
	require.NoError(t, err)

	_, err = mgr.GetSandboxBySession(context.Background(), "", "ns", "x", "BadKind")
	assert.ErrorContains(t, err, "unsupported kind")
}

func TestSessionManager_GetExistingSession(t *testing.T) {
	t.Setenv("WORKLOAD_MANAGER_ADDR", "http://localhost:8080")
	want := &types.SandboxInfo{SessionID: "existing", SandboxID: "sb-1"}
	mgr, err := NewSessionManager(stubStore{sandbox: want})
	require.NoError(t, err)

	got, err := mgr.GetSandboxBySession(context.Background(), "existing", "ns", "name", types.AgentRuntimeKind)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

/* ---------- 3. HTTP HANDLERS ---------- */

func TestHandleHealthLive_Unit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{engine: gin.New()}
	s.engine.GET("/health/live", s.handleHealthLive)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health/live", nil)
	s.engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"status":"alive"}`, w.Body.String())
}

func TestHandleHealthReady_Unit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{engine: gin.New()}
	s.engine.GET("/health/ready", s.handleHealthReady)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health/ready", nil)
	s.engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	s.sessionManager = &mockSM{}
	w = httptest.NewRecorder()
	s.engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"status":"ready"}`, w.Body.String())
}

func TestHandleInvoke_Forward(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/exec", r.URL.Path)
		assert.Equal(t, "sess-xyz", r.Header.Get("x-agentcube-session-id"))
		w.Header().Set("x-agentcube-session-id", "sess-xyz")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer upstream.Close()

	gin.SetMode(gin.TestMode)
	engine := gin.New()

	mockSandbox := &types.SandboxInfo{
		SessionID: "sess-xyz",
		EntryPoints: []types.SandboxEntryPoints{
			{Endpoint: upstream.URL, Path: "/exec", Protocol: "http"},
		},
	}
	mgr := &mockSM{sandbox: mockSandbox}

	// ✅ Critical: server must have non-nil storeClient and httpTransport
	storeClient := &stubStore{} // no-op
	httpTransport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
	}

	s := &Server{
		engine:         engine,
		sessionManager: mgr,
		storeClient:    storeClient,
		httpTransport:  httpTransport,
		semaphore:      make(chan struct{}, 10),
	}

	engine.Use(s.concurrencyLimitMiddleware())
	engine.POST("/v1/namespaces/:namespace/code-interpreters/:name/invocations/*path", s.handleCodeInterpreterInvoke)

	testSrv := httptest.NewServer(engine)
	defer testSrv.Close()

	// Send request with session header (helps debug)
	req, _ := http.NewRequest("POST",
		testSrv.URL+"/v1/namespaces/ns1/code-interpreters/ci1/invocations/exec",
		nil)
	req.Header.Set("x-agentcube-session-id", "sess-xyz")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "sess-xyz", resp.Header.Get("x-agentcube-session-id"))

	body, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(body), `"result":"ok"`)
}

func TestConcurrencyLimitMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{
		engine:    gin.New(),
		semaphore: make(chan struct{}, 1),
	}
	s.engine.Use(s.concurrencyLimitMiddleware())
	s.engine.GET("/", func(c *gin.Context) {
		time.Sleep(100 * time.Millisecond)
		c.String(http.StatusOK, "ok")
	})

	done := make(chan struct{})
	go func() {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/", nil)
		s.engine.ServeHTTP(w, req)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)
	s.engine.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "overloaded")

	<-done
}

/* ---------- Helpers ---------- */

type mockSM struct {
	sandbox *types.SandboxInfo
	err     error
}

func (m *mockSM) GetSandboxBySession(_ context.Context, _, _, _, _ string) (*types.SandboxInfo, error) {
	return m.sandbox, m.err
}