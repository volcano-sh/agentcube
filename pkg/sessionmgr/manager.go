package sessionmgr

import (
	"context"
	"errors"
	"fmt"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/redis"
)

// Manager defines the session management behavior on top of Redis and the sandbox manager.
type Manager interface {
	// GetSandboxBySession returns the sandbox associated with the given sessionID.
	GetSandboxBySession(ctx context.Context, sessionID string) (*types.SandboxRedis, error)
	// CreateSandbox creates a new sandbox via the sandbox manager and returns a SandboxRedis view.
	CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.SandboxRedis, error)
}

// RedisClient is the subset of the redis.Client interface used by the session manager.
// A redis.Client returned by redis.NewClient satisfies this interface.
type RedisClient interface {
	GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxRedis, error)
}

// SandboxManagerClient defines the sandbox manager operations used by the session manager.
type SandboxManagerClient interface {
	CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.CreateSandboxResponse, error)
}

// manager is the default implementation of the Manager interface.
type manager struct {
	redis   RedisClient
	sandbox SandboxManagerClient
}

// New returns a default Manager implementation.
// Redis and sandbox manager clients are injected from the outside to make testing
// and implementation swapping easier.
func New(redisClient RedisClient, sandboxClient SandboxManagerClient) Manager {
	return &manager{
		redis:   redisClient,
		sandbox: sandboxClient,
	}
}

// GetSandboxBySession looks up the sandbox by sessionID using Redis.
func (m *manager) GetSandboxBySession(ctx context.Context, sessionID string) (*types.SandboxRedis, error) {
	if sessionID == "" {
		return nil, ErrInvalidArgument
	}

	// For now we do not validate the SessionID format; any non-empty string is treated as valid.

	sb, err := m.redis.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		// redis.ErrNotFound is mapped to the unified ErrSessionNotFound in session manager.
		if errors.Is(err, redis.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		// Other errors are wrapped and propagated for upper layers to log and map to 5xx.
		return nil, fmt.Errorf("sessionmgr: get sandbox by sessionID %q from redis failed: %w", sessionID, err)
	}
	if sb == nil {
		return nil, fmt.Errorf("sessionmgr: get sandbox by sessionID %q returned nil sandbox", sessionID)
	}

	return sb, nil
}

// CreateSandbox creates a new sandbox via the sandbox manager using the shared CreateSandboxRequest type.
func (m *manager) CreateSandbox(ctx context.Context, req *types.CreateSandboxRequest) (*types.SandboxRedis, error) {
	if req == nil {
		return nil, ErrInvalidArgument
	}
	// Basic argument validation: creating a sandbox requires at least Kind and Namespace.
	if req.Kind == "" || req.Namespace == "" {
		return nil, ErrInvalidArgument
	}

	cResp, err := m.sandbox.CreateSandbox(ctx, req)
	if err != nil {
		// Upstream network/timeouts etc. are treated as ErrUpstreamUnavailable.
		// Here we roughly distinguish by whether the error is our ErrCreateSandboxFailed.
		if errors.Is(err, ErrCreateSandboxFailed) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}

	if cResp == nil || cResp.SessionID == "" || cResp.SandboxID == "" {
		return nil, fmt.Errorf("%w: invalid response from sandbox manager", ErrCreateSandboxFailed)
	}

	// Construct a SandboxRedis view from the response so that callers
	// see a consistent sandbox object.
	sb := &types.SandboxRedis{
		SandboxID:   cResp.SandboxID,
		SandboxName: cResp.SandboxName,
		EntryPoints: cResp.Accesses,
		SessionID:   cResp.SessionID,
		// CreatedAt / ExpiresAt / Status can be filled later when they are available.
	}

	return sb, nil
}

var (
	// ErrInvalidArgument indicates that the request arguments are invalid
	// (for example, missing kind/namespace when creating a sandbox).
	ErrInvalidArgument = errors.New("sessionmgr: invalid argument")

	// ErrSessionNotFound indicates that the session does not exist in redis,
	// and is typically mapped to HTTP 404/410.
	ErrSessionNotFound = errors.New("sessionmgr: session not found")

	// ErrUpstreamUnavailable indicates that the sandbox manager is unavailable
	// (e.g. due to network errors), and is typically mapped to HTTP 503.
	ErrUpstreamUnavailable = errors.New("sessionmgr: sandbox manager unavailable")

	// ErrCreateSandboxFailed indicates that the sandbox manager returned a business-level error.
	ErrCreateSandboxFailed = errors.New("sessionmgr: create sandbox failed")
)
