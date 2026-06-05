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
	"encoding/json"
	"errors"
	"fmt"

	redisv9 "github.com/redis/go-redis/v9"
)

// redisArtifactStore implements ArtifactStore using Redis.
// The compare-and-set is implemented with WATCH + MULTI/EXEC. The version token
// is a JSON encoding of the raw string value, so the caller can pass it back
// unchanged. An empty string means "no previous value".
type redisArtifactStore struct {
	cli *redisv9.Client
}

// NewRedisArtifactStore creates an ArtifactStore backed by the given Redis client.
func NewRedisArtifactStore(cli *redisv9.Client) ArtifactStore {
	return &redisArtifactStore{cli: cli}
}

func (s *redisArtifactStore) GetManifest(ctx context.Context, ownerKey string) (*SnapshotArtifactManifest, error) {
	raw, err := s.cli.Get(ctx, ownerKey).Result()
	if errors.Is(err, redisv9.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("artifact store get %q: %w", ownerKey, err)
	}
	var m SnapshotArtifactManifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("artifact store unmarshal %q: %w", ownerKey, err)
	}
	return &m, nil
}

func (s *redisArtifactStore) PutManifest(ctx context.Context, ownerKey string, manifest *SnapshotArtifactManifest, version string) error {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("artifact store marshal: %w", err)
	}

	// Use WATCH + MULTI/EXEC for optimistic compare-and-set.
	txErr := s.cli.Watch(ctx, func(tx *redisv9.Tx) error {
		current, err := tx.Get(ctx, ownerKey).Result()
		if errors.Is(err, redisv9.Nil) {
			current = ""
		} else if err != nil {
			return fmt.Errorf("artifact store watch-get %q: %w", ownerKey, err)
		}
		if current != version {
			return ErrArtifactStoreConflict
		}
		_, err = tx.TxPipelined(ctx, func(pipe redisv9.Pipeliner) error {
			pipe.Set(ctx, ownerKey, string(encoded), 0)
			return nil
		})
		return err
	}, ownerKey)

	if txErr != nil {
		if errors.Is(txErr, ErrArtifactStoreConflict) {
			return ErrArtifactStoreConflict
		}
		return fmt.Errorf("artifact store put %q: %w", ownerKey, txErr)
	}
	return nil
}

func (s *redisArtifactStore) DeleteManifest(ctx context.Context, ownerKey string) error {
	if err := s.cli.Del(ctx, ownerKey).Err(); err != nil && !errors.Is(err, redisv9.Nil) {
		return fmt.Errorf("artifact store delete %q: %w", ownerKey, err)
	}
	return nil
}

func (s *redisArtifactStore) Close() error {
	return s.cli.Close()
}
