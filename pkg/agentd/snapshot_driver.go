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

package agentd

import (
	"context"

	corev1 "k8s.io/api/core/v1"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

// SnapshotDriver is an in-process interface implemented by each snapshot provider.
// Node-agent-local implementations call the provider's runtime or VMM directly.
// Each implementation is registered under a stable provider name and selected by
// SandboxSnapshotTask.spec.providerName.
type SnapshotDriver interface {
	// Name returns the stable provider name (e.g. "snapstart.kuasar.io").
	Name() string

	// Capabilities returns the capabilities advertised by this driver.
	Capabilities(ctx context.Context) SnapshotDriverCapabilities

	// Create performs the snapshot and returns a Ready artifact.
	// It must only return successfully after the runtime has reached a fork-safe
	// snapshot point (e.g. bootstrap complete, no user state present).
	// The artifact must be usable for restore after the build Sandbox is deleted.
	Create(ctx context.Context, req SnapshotDriverCreateRequest) (*SnapshotDriverArtifact, error)

	// Delete removes the physical artifact.
	Delete(ctx context.Context, artifact SnapshotDriverArtifact) error

	// List enumerates all artifacts managed by this driver on this node.
	List(ctx context.Context) ([]SnapshotDriverArtifact, error)

	// Inspect returns the current status of an artifact.
	Inspect(ctx context.Context, artifact SnapshotDriverArtifact) (*SnapshotDriverArtifactStatus, error)
}

// SnapshotDriverCapabilities describes what a driver can do.
type SnapshotDriverCapabilities struct {
	// SnapshotModes lists the modes this driver supports.
	SnapshotModes []runtimev1alpha1.SandboxSnapshotMode
}

// SnapshotDriverCreateRequest carries the inputs for a snapshot creation call.
type SnapshotDriverCreateRequest struct {
	// TaskRef is the Kubernetes object reference to the SandboxSnapshotTask.
	TaskRef corev1.ObjectReference

	// TargetSandboxRef identifies the Sandbox to snapshot.
	TargetSandboxRef corev1.TypedLocalObjectReference

	// TargetNodeName is the node where the build Sandbox is running.
	TargetNodeName string

	// SnapshotMode is the mode of the snapshot (Fork or Resume).
	SnapshotMode runtimev1alpha1.SandboxSnapshotMode

	// ProviderName is the stable provider identifier.
	ProviderName string

	// SnapshotKey is the logical restore reference.
	SnapshotKey string

	// SnapshotHash is the hash of the snapshot inputs.
	SnapshotHash string
}

// SnapshotDriverArtifact is the driver's representation of a created artifact.
type SnapshotDriverArtifact struct {
	// ProviderName identifies the driver that created the artifact.
	ProviderName string

	// SnapshotKey is the logical restore reference for this artifact.
	SnapshotKey string

	// SnapshotHash is the hash of the snapshot inputs.
	SnapshotHash string

	// ProviderRef is a driver-private reference to the physical artifact
	// (e.g. a path, an ID, a URL). Opaque to the AgentCube control plane.
	ProviderRef string
}

// SnapshotDriverArtifactStatus describes the current condition of an artifact.
type SnapshotDriverArtifactStatus struct {
	// Phase is the current artifact phase as seen by the driver.
	Phase runtimev1alpha1.SnapshotArtifactPhase

	// Message is a human-readable description.
	Message string
}
