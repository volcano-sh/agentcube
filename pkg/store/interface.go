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
	// Close releases all resources held by the store (e.g. connection pools)
	Close() error
}
