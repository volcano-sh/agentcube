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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ArtifactStore persists SnapshotArtifactManifest records for SandboxSnapshotController.
// SandboxSnapshotController is the sole writer; Workload Manager reads active artifact sets
// to determine restore intent for session Sandbox creation.
type ArtifactStore interface {
	// GetManifest retrieves the manifest for the given snapshot owner key.
	// Returns nil, nil when no manifest exists.
	GetManifest(ctx context.Context, ownerKey string) (*SnapshotArtifactManifest, error)

	// PutManifest atomically writes the manifest using a compare-and-set operation.
	// version is the value returned by the previous GetManifest call (empty string for new records).
	// Returns ErrArtifactStoreConflict when the compare-and-set fails.
	PutManifest(ctx context.Context, ownerKey string, manifest *SnapshotArtifactManifest, version string) error

	// DeleteManifest removes the manifest for the given snapshot owner key.
	DeleteManifest(ctx context.Context, ownerKey string) error

	// Close releases resources held by the store.
	Close() error
}

// ErrArtifactStoreConflict is returned by PutManifest when the compare-and-set fails.
var ErrArtifactStoreConflict = artifactStoreConflictError{}

type artifactStoreConflictError struct{}

func (artifactStoreConflictError) Error() string { return "artifact store conflict: version mismatch" }

// SnapshotArtifactManifest is the owner-scoped artifact-store record for one SandboxSnapshot.
// ArtifactSets maps SnapshotKey to the corresponding artifact set; the map key must match
// SnapshotArtifactSet.SnapshotKey.
type SnapshotArtifactManifest struct {
	// OwnerRef identifies the owning SandboxSnapshot.
	OwnerRef metav1.OwnerReference `json:"ownerRef"`

	// ArtifactSets maps SnapshotKey to the corresponding artifact set.
	ArtifactSets map[string]SnapshotArtifactSet `json:"artifactSets,omitempty"`

	// ActiveSetRef points to the active artifact set in ArtifactSets.
	ActiveSetRef SnapshotArtifactSetRef `json:"activeSetRef,omitempty"`

	// PendingSetRef points to the pending replacement artifact set during background rebuild.
	PendingSetRef SnapshotArtifactSetRef `json:"pendingSetRef,omitempty"`

	// RebuildSeq is a monotonically increasing rebuild sequence number, incremented by the
	// controller each time a new artifact set is started. It is used to form the SnapshotKey.
	RebuildSeq int32 `json:"rebuildSeq,omitempty"`
}

// SnapshotArtifactSetRef points to an entry in SnapshotArtifactManifest.ArtifactSets by key.
type SnapshotArtifactSetRef struct {
	// SnapshotKey is the map key in ArtifactSets.
	SnapshotKey string `json:"snapshotKey,omitempty"`
}

// SnapshotArtifactSet describes one logical snapshot version, shared across target nodes.
type SnapshotArtifactSet struct {
	// SnapshotKey is the logical restore reference passed to the runtime.
	SnapshotKey string `json:"snapshotKey"`

	// SnapshotHash is the hash of the snapshot inputs for this set.
	SnapshotHash string `json:"snapshotHash"`

	// Artifacts contains the per-node artifacts in this set.
	Artifacts []SnapshotArtifact `json:"artifacts,omitempty"`
}

// SnapshotArtifact describes a single node-local snapshot artifact.
type SnapshotArtifact struct {
	// ProviderName identifies the snapshot provider that created this artifact.
	ProviderName string `json:"providerName"`

	// NodeName is the node where this artifact resides.
	NodeName string `json:"nodeName,omitempty"`

	// Phase is the current lifecycle phase of this artifact.
	Phase SnapshotArtifactPhase `json:"phase"`

	// SnapshotKey is the logical restore reference for this artifact (matches the parent set).
	SnapshotKey string `json:"snapshotKey"`

	// SnapshotHash is the hash of the snapshot inputs for this artifact.
	SnapshotHash string `json:"snapshotHash"`

	// CreatedAt is set when the artifact was successfully created.
	CreatedAt *time.Time `json:"createdAt,omitempty"`

	// Retry records the build retry state for this artifact.
	Retry *SnapshotBuildRetry `json:"retry,omitempty"`

	// Message is a human-readable description of the current phase.
	Message string `json:"message,omitempty"`
}

// SnapshotBuildRetry records retry state for a failed artifact build.
type SnapshotBuildRetry struct {
	FailureCount int32      `json:"failureCount,omitempty"`
	LastFailedAt *time.Time `json:"lastFailedAt,omitempty"`
	NextRetryAt  *time.Time `json:"nextRetryAt,omitempty"`
}

// SnapshotArtifactPhase mirrors the CRD type for artifact-store records.
// Defined here to avoid an import cycle with the API package.
type SnapshotArtifactPhase string

const (
	SnapshotArtifactPhaseCreating    SnapshotArtifactPhase = "Creating"
	SnapshotArtifactPhaseReady       SnapshotArtifactPhase = "Ready"
	SnapshotArtifactPhaseFailed      SnapshotArtifactPhase = "Failed"
	SnapshotArtifactPhaseUnavailable SnapshotArtifactPhase = "Unavailable"
)

// ArtifactOwnerKey constructs the artifact store key for a SandboxSnapshot.
// Format: snapshot:{kind}:{namespace}:{name}:{uid}
func ArtifactOwnerKey(kind, namespace, name, uid string) string {
	return "snapshot:" + kind + ":" + namespace + ":" + name + ":" + uid
}
