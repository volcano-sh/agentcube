package sessionmgr

import (
	"errors"
	"fmt"

	"github.com/volcano-sh/agentcube/pkg/redis"
)

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

// GetSandboxBySession is the single entry point called by the Router.
func (m *manager) GetSandboxBySession(ctx Context, req *GetSandboxBySessionRequest) (*GetSandboxBySessionResponse, error) {
	if req == nil {
		return nil, ErrInvalidArgument
	}

	if req.SessionID != "" {
		return m.getExistingSession(ctx, req)
	}

	return m.createNewSession(ctx, req)
}

// --------- Private helpers: existing session branch ---------

func (m *manager) getExistingSession(ctx Context, req *GetSandboxBySessionRequest) (*GetSandboxBySessionResponse, error) {
	// For now we do not validate the SessionID format; any non-empty string is treated as valid.
	// If you introduce a fixed format (UUID, etc.) in the future, you can validate it here.

	sb, err := m.redis.GetSandboxBySessionID(ctx, req.SessionID)
	if err != nil {
		// redis.ErrNotFound is mapped to the unified ErrSessionNotFound in session manager.
		if errors.Is(err, redis.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		// Other errors are wrapped and propagated for upper layers to log and map to 5xx.
		return nil, fmt.Errorf("sessionmgr: get sandbox by sessionID %q from redis failed: %w", req.SessionID, err)
	}

	// Normal successful path.
	resp := &GetSandboxBySessionResponse{
		SessionID: req.SessionID,
		Endpoint:  sb.Endpoint,
		Sandbox:   sb,
	}
	return resp, nil
}

// --------- Private helpers: empty sessionID, create new sandbox ---------

func (m *manager) createNewSession(ctx Context, req *GetSandboxBySessionRequest) (*GetSandboxBySessionResponse, error) {
	// Basic argument validation: creating a sandbox requires at least Kind and Namespace.
	if req.Kind == "" || req.Namespace == "" {
		return nil, ErrInvalidArgument
	}

	cReq := &CreateSandboxRequest{
		Kind:      req.Kind,
		Name:      req.Name,
		Namespace: req.Namespace,
	}

	cResp, err := m.sandbox.CreateSandbox(ctx, cReq)
	if err != nil {
		// Upstream network/timeouts etc. are treated as ErrUpstreamUnavailable.
		// Here we roughly distinguish by whether the error is our ErrCreateSandboxFailed.
		if errors.Is(err, ErrCreateSandboxFailed) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrUpstreamUnavailable, err)
	}

	if cResp == nil || cResp.Sandbox == nil || cResp.SessionID == "" {
		return nil, fmt.Errorf("%w: invalid response from sandbox manager", ErrCreateSandboxFailed)
	}

	resp := &GetSandboxBySessionResponse{
		SessionID: cResp.SessionID,
		Endpoint:  cResp.Sandbox.Endpoint,
		Sandbox:   cResp.Sandbox,
	}
	return resp, nil
}
