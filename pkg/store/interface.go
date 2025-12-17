package store

import (
	"context"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

type Store interface {
	// Ping check store provider available or not
	Ping(ctx context.Context) error
	// GetSandboxBySessionID get the sandbox by session ID
	GetSandboxBySessionID(ctx context.Context, sessionID string) (*types.SandboxInfo, error)
	// StoreSandbox store sandbox into storage
	StoreSandbox(ctx context.Context, sandboxStore *types.SandboxInfo) error
	// UpdateSandbox update sandbox of storage
	UpdateSandbox(ctx context.Context, sandboxStore *types.SandboxInfo) error
	// DeleteSandboxBySessionID delete sandbox by session ID
	DeleteSandboxBySessionID(ctx context.Context, sessionID string) error
	// ListExpiredSandboxes returns up to limit sandboxes with ExpiresAt before the given time
	ListExpiredSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error)
	// ListInactiveSandboxes returns up to limit sandboxes with last-activity time before the given time
	ListInactiveSandboxes(ctx context.Context, before time.Time, limit int64) ([]*types.SandboxInfo, error)
	// UpdateSessionLastActivity updates the last-activity index for the given session
	UpdateSessionLastActivity(ctx context.Context, sessionID string, at time.Time) error
}
