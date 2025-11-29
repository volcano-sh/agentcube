package sessionmgr

import (
	"errors"
	"time"

	"github.com/volcano-sh/agentcube/pkg/redis"
)

// GetSandboxBySessionRequest is the input payload when Router calls the session manager.
type GetSandboxBySessionRequest struct {
	// SessionID is the session identifier propagated from upstream; it can be empty.
	SessionID string

	// The following fields are only used when SessionID is empty and a new sandbox needs to be created.
	Kind      redis.SandboxKind
	Name      string
	Namespace string
}

// GetSandboxBySessionResponse is the response returned to Router.
type GetSandboxBySessionResponse struct {
	// SessionID is always non-empty. If the request did not provide one, a new session ID is generated.
	SessionID string

	// Endpoint is the address to access the sandbox, e.g. "10.0.0.1:9000".
	Endpoint string

	// Sandbox contains the full sandbox information, useful for debugging and logging.
	// Router typically only cares about Endpoint.
	Sandbox *redis.Sandbox
}

// Manager is the public interface used by Router to manage sandboxes.
type Manager interface {
	GetSandboxBySession(ctx Context, req *GetSandboxBySessionRequest) (*GetSandboxBySessionResponse, error)
}

// Context is a small interface compatible with context.Context, making it easier to mock in tests.
type Context interface {
	Done() <-chan struct{}
	Err() error
	Deadline() (deadline time.Time, ok bool)
}

// --------- Dependency interfaces ---------

// RedisClient is a subset of redis.Client that exposes only the read methods needed by the session manager.
type RedisClient interface {
	GetSandboxBySessionID(ctx Context, sessionID string) (*redis.Sandbox, error)
}

// SandboxManagerClient abstracts the HTTP client used to talk to the sandbox manager service.
type SandboxManagerClient interface {
	CreateSandbox(ctx Context, req *CreateSandboxRequest) (*CreateSandboxResponse, error)
}

// CreateSandboxRequest is the request body used by the session manager to create a sandbox via the sandbox manager.
type CreateSandboxRequest struct {
	Kind      redis.SandboxKind `json:"kind"`
	Name      string            `json:"name,omitempty"`
	Namespace string            `json:"namespace"`
}

// CreateSandboxResponse is the response body returned by the sandbox manager.
// By convention, the sandbox manager is responsible for binding sessionID and sandbox in Redis.
type CreateSandboxResponse struct {
	SessionID string         `json:"session_id"`
	Sandbox   *redis.Sandbox `json:"sandbox"`
}

// --------- Business errors ---------

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
