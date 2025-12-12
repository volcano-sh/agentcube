package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/redis"
)

// SessionManager defines the session management behavior on top of Redis and the workload manager.
type SessionManager interface {
	// GetSandboxBySession returns the sandbox associated with the given sessionID.
	// When sessionID is empty, it creates a new sandbox by calling the external API.
	// When sessionID is not empty, it queries Redis for the sandbox.
	GetSandboxBySession(ctx context.Context, sessionID string, namespace string, name string, kind string) (*types.SandboxRedis, error)
}

// manager is the default implementation of the SessionManager interface.
type manager struct {
	redisClient    redis.Client
	workloadMgrURL string
	httpClient     *http.Client
}

// NewSessionManager returns a SessionManager implementation.
// redisClient is used to query sandbox information from Redis.
// workloadMgrURL is read from the environment variable WORKLOAD_MGR_URL.
func NewSessionManager(redisClient redis.Client) (SessionManager, error) {
	workloadMgrURL := os.Getenv("WORKLOAD_MGR_URL")
	if workloadMgrURL == "" {
		return nil, fmt.Errorf("WORKLOAD_MGR_URL environment variable is not set")
	}

	return &manager{
		redisClient:    redisClient,
		workloadMgrURL: workloadMgrURL,
		httpClient: &http.Client{
			Timeout: time.Minute, // No timeout for createSandbox requests
		},
	}, nil
}

// GetSandboxBySession returns the sandbox associated with the given sessionID.
// When sessionID is empty, it creates a new sandbox by calling the external API.
// When sessionID is not empty, it queries Redis for the sandbox.
func (m *manager) GetSandboxBySession(ctx context.Context, sessionID string, namespace string, name string, kind string) (*types.SandboxRedis, error) {
	// When sessionID is empty, create a new sandbox
	if sessionID == "" {
		return m.createSandbox(ctx, namespace, name, kind)
	}

	// When sessionID is not empty, query Redis
	sandbox, err := m.redisClient.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, redis.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("failed to get sandbox from redis: %w", err)
	}

	return sandbox, nil
}

// createSandbox creates a new sandbox by calling the external workload manager API.
func (m *manager) createSandbox(ctx context.Context, namespace string, name string, kind string) (*types.SandboxRedis, error) {
	// Determine the API endpoint based on kind
	var endpoint string
	switch kind {
	case types.AgentRuntimeKind:
		endpoint = m.workloadMgrURL + "/v1/agent-runtime"
	case types.CodeInterpreterKind:
		endpoint = m.workloadMgrURL + "/v1/code-interpreter"
	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}

	// Prepare the request body
	reqBody := &types.CreateSandboxRequest{
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: status code %d, body: %s", ErrCreateSandboxFailed, resp.StatusCode, string(respBody))
	}

	// Parse response
	var createResp types.CreateSandboxResponse
	if err := json.Unmarshal(respBody, &createResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	// Validate response
	if createResp.SessionID == "" {
		return nil, fmt.Errorf("%w: response with empty session id from workload manager", ErrCreateSandboxFailed)
	}

	// Construct SandboxRedis from response
	sandbox := &types.SandboxRedis{
		SandboxID:   createResp.SandboxID,
		SandboxName: createResp.SandboxName,
		SessionID:   createResp.SessionID,
		EntryPoints: createResp.EntryPoints,
	}

	return sandbox, nil
}

var (
	// ErrSessionNotFound indicates that the session does not exist in redis.
	ErrSessionNotFound = errors.New("sessionmgr: session not found")

	// ErrUpstreamUnavailable indicates that the workload manager is unavailable.
	ErrUpstreamUnavailable = errors.New("sessionmgr: workload manager unavailable")

	// ErrCreateSandboxFailed indicates that the workload manager returned an error.
	ErrCreateSandboxFailed = errors.New("sessionmgr: create sandbox failed")
)
