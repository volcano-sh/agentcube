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

package workloadmanager

import (
	"context"
	"fmt"
	"os"
	"strings"

	redisv9 "github.com/redis/go-redis/v9"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/store"
)

// NewArtifactStoreFromEnv creates an ArtifactStore from the same environment variables
// used by the session store (REDIS_ADDR / VALKEY_ADDR, REDIS_PASSWORD).
// Falls back to a no-op store when configuration is absent so the binary can start
// in environments that have not yet configured snapshot infrastructure.
func NewArtifactStoreFromEnv() store.ArtifactStore {
	// Prefer Valkey when VALKEY_ADDR is set.
	if addr := os.Getenv("VALKEY_ADDR"); addr != "" {
		password := os.Getenv("REDIS_PASSWORD")
		klog.V(2).InfoS("artifact store: using Valkey", "addr", addr)
		return store.NewRedisArtifactStore(newValkeyRedisCompat(addr, password))
	}

	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		password := os.Getenv("REDIS_PASSWORD")
		required := strings.ToLower(os.Getenv("REDIS_PASSWORD_REQUIRED")) != "false"
		if required && password == "" {
			klog.Warningf("REDIS_PASSWORD is required but not set; snapshot restore will use cold start")
			return &noopArtifactStore{}
		}
		client := redisv9.NewClient(&redisv9.Options{
			Addr:     addr,
			Password: password,
		})
		klog.V(2).InfoS("artifact store: using Redis", "addr", addr)
		return store.NewRedisArtifactStore(client)
	}

	klog.V(2).Info("artifact store: no address configured; snapshot restore will use cold start")
	return &noopArtifactStore{}
}

// newValkeyRedisCompat creates a go-redis client that connects to a Valkey instance.
// Valkey and Redis share the same RESP wire protocol, so go-redis works as the transport.
func newValkeyRedisCompat(addr, password string) *redisv9.Client {
	return redisv9.NewClient(&redisv9.Options{
		Addr:     addr,
		Password: password,
	})
}

// noopArtifactStore is returned when no store backend is configured.
// Reads and writes fail so snapshot controllers back off instead of creating
// untracked build resources. Delete remains a no-op to keep finalizer cleanup safe.
type noopArtifactStore struct{}

func (n *noopArtifactStore) GetManifest(_ context.Context, _ string) (*store.SnapshotArtifactManifest, error) {
	return nil, fmt.Errorf("artifact store is not configured")
}
func (n *noopArtifactStore) PutManifest(_ context.Context, _ string, _ *store.SnapshotArtifactManifest, _ string) error {
	return fmt.Errorf("artifact store is not configured")
}
func (n *noopArtifactStore) DeleteManifest(_ context.Context, _ string) error { return nil }
func (n *noopArtifactStore) Close() error                                     { return nil }
