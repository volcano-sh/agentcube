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

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// SnapshotModeHandler encapsulates all mode-specific logic for reconciling a SandboxSnapshot.
// To add a new snapshot mode, implement this interface and register it in SetupWithManager.
type SnapshotModeHandler interface {
	// ComputeHash returns the content hash for the snapshot's current source inputs.
	// The hash is used to detect when the snapshot needs to be rebuilt.
	ComputeHash(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass) (string, error)

	// PrepareArtifactSet ensures the manifest has an active or pending artifact set,
	// applying mode-specific rebuild policy. May save the manifest multiple times.
	// Returns the updated version token.
	PrepareArtifactSet(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass,
		manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, currentHash string) (string, error)

	// EnsureTasks creates snapshot tasks for uncovered target nodes in the working artifact set.
	// Appends new artifacts and saves the manifest when tasks are added.
	EnsureTasks(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass,
		manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, workingKey string,
		artifactSet store.SnapshotArtifactSet) (string, error)

	// ReadyToPromote returns true when the pending artifact set should be promoted to active.
	ReadyToPromote(pending store.SnapshotArtifactSet) bool

	// CleanupTask performs mode-specific cleanup when a task reaches a terminal phase.
	CleanupTask(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, task *runtimev1alpha1.SandboxSnapshotTask) error

	// CleanupAll performs mode-specific cleanup when the SandboxSnapshot is deleted.
	CleanupAll(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot) error
}
