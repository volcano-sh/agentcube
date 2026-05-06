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

package e2b

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"

	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	e2bIDAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	e2bIDLength   = 12
	maxRetries    = 5
)

// ErrE2BSandboxIDExhausted is returned when the generator fails to produce a unique ID after max retries.
var ErrE2BSandboxIDExhausted = errors.New("failed to generate unique e2b sandbox id after maximum retries")

// IDGenerator generates collision-free E2BSandboxIDs.
type IDGenerator struct {
	store store.Store
}

// NewIDGenerator creates a new IDGenerator.
func NewIDGenerator(s store.Store) *IDGenerator {
	return &IDGenerator{store: s}
}

// Generate creates a new E2BSandboxID using CSPRNG base62, with Store probe for collision detection.
func (g *IDGenerator) Generate(ctx context.Context) (string, error) {
	for i := 0; i < maxRetries; i++ {
		id, err := randomBase62(e2bIDLength)
		if err != nil {
			return "", fmt.Errorf("failed to generate random id: %w", err)
		}
		// Probe store for collision
		_, err = g.store.GetSandboxByE2BSandboxID(ctx, id)
		if err != nil {
			// ErrNotFound means the ID is free
			if errors.Is(err, store.ErrNotFound) {
				return id, nil
			}
			return "", fmt.Errorf("store probe failed: %w", err)
		}
		// Collision detected, retry
	}
	return "", ErrE2BSandboxIDExhausted
}

func randomBase62(length int) (string, error) {
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(e2bIDAlphabet))))
		if err != nil {
			return "", err
		}
		result[i] = e2bIDAlphabet[n.Int64()]
	}
	return string(result), nil
}
