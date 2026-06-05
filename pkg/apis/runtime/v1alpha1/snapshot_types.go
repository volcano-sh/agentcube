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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// SnapshotClass describes an infrastructure snapshot capability.
// It is cluster-scoped and managed by cluster administrators.
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
type SnapshotClass struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SnapshotClassSpec `json:"spec"`
}

// SnapshotClassSpec defines the snapshot provider and artifact placement strategy.
type SnapshotClassSpec struct {
	// ProviderName is the stable identifier for the snapshot provider.
	// It maps to a SnapshotDriver registered in the node agent.
	// +kubebuilder:validation:Required
	ProviderName string `json:"providerName"`

	// SupportedSnapshotModes lists the snapshot modes this class supports.
	// +kubebuilder:validation:MinItems=1
	SupportedSnapshotModes []SandboxSnapshotMode `json:"supportedSnapshotModes"`

	// NodeSelector selects which nodes are eligible for snapshot builds.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// SnapshotClassList contains a list of SnapshotClass.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type SnapshotClassList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SnapshotClass `json:"items"`
}

// SandboxSnapshot declares a snapshot of an agent-sandbox execution environment.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Mode",type="string",JSONPath=".spec.snapshotMode"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Ready",type="integer",JSONPath=".status.readyNodeCount"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type SandboxSnapshot struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SandboxSnapshotSpec   `json:"spec"`
	Status            SandboxSnapshotStatus `json:"status,omitempty"`
}

// SandboxSnapshotSpec defines the desired state of a SandboxSnapshot.
type SandboxSnapshotSpec struct {
	// SnapshotMode selects the usage mode.
	// +kubebuilder:validation:Required
	SnapshotMode SandboxSnapshotMode `json:"snapshotMode"`

	// SourceRef references the source SandboxTemplate for the snapshot.
	// +kubebuilder:validation:Required
	SourceRef corev1.TypedLocalObjectReference `json:"sourceRef"`

	// SnapshotClassName references the SnapshotClass that defines the provider and placement.
	// +kubebuilder:validation:Required
	SnapshotClassName string `json:"snapshotClassName"`

	// ForkPolicy controls when a Fork snapshot is rebuilt.
	// +optional
	ForkPolicy *SandboxSnapshotForkPolicy `json:"forkPolicy,omitempty"`
}

// SandboxSnapshotMode selects the snapshot usage mode.
// +kubebuilder:validation:Enum=Fork
type SandboxSnapshotMode string

const (
	// SandboxSnapshotModeFork creates a reusable baseline for 1:N forking.
	SandboxSnapshotModeFork SandboxSnapshotMode = "Fork"
)

// SandboxSnapshotForkPolicy describes when a Fork snapshot is rebuilt.
type SandboxSnapshotForkPolicy struct {
	// RebuildOnSourceChange triggers a rebuild when the source SandboxTemplate changes.
	// Defaults to true when nil.
	// +optional
	RebuildOnSourceChange *bool `json:"rebuildOnSourceChange,omitempty"`

	// RebuildAfter triggers a background replacement after the active artifact set
	// has been Ready for this duration. The active set continues serving until the
	// replacement is promoted.
	// +optional
	RebuildAfter *metav1.Duration `json:"rebuildAfter,omitempty"`
}

// SandboxSnapshotStatus describes the observed state of a SandboxSnapshot.
type SandboxSnapshotStatus struct {
	// Phase is the aggregate state of the snapshot's active artifact set.
	Phase SandboxSnapshotPhase `json:"phase,omitempty"`

	// TargetNodeCount is the number of nodes the controller intends to cover.
	TargetNodeCount int32 `json:"targetNodeCount,omitempty"`

	// CreatingNodeCount is the number of target nodes with an artifact build in progress.
	CreatingNodeCount int32 `json:"creatingNodeCount,omitempty"`

	// ReadyNodeCount is the number of target nodes with a Ready artifact.
	ReadyNodeCount int32 `json:"readyNodeCount,omitempty"`

	// FailedNodeCount is the number of target nodes where the build has reached a terminal failure.
	FailedNodeCount int32 `json:"failedNodeCount,omitempty"`

	// UnavailableNodeCount is the number of target nodes whose artifact is not consumable.
	UnavailableNodeCount int32 `json:"unavailableNodeCount,omitempty"`

	// ReadyAt is the time the snapshot first reached Phase=Ready.
	// +optional
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`

	// Conditions holds machine-readable status signals. Reserved for future use.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Message is a human-readable summary of the current status.
	// +optional
	Message string `json:"message,omitempty"`
}

// SandboxSnapshotPhase describes the aggregate lifecycle phase of a SandboxSnapshot.
type SandboxSnapshotPhase string

const (
	SandboxSnapshotPhasePending  SandboxSnapshotPhase = "Pending"
	SandboxSnapshotPhaseCreating SandboxSnapshotPhase = "Creating"
	SandboxSnapshotPhaseReady    SandboxSnapshotPhase = "Ready"
	SandboxSnapshotPhaseFailed   SandboxSnapshotPhase = "Failed"
)

// SandboxSnapshotList contains a list of SandboxSnapshot.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type SandboxSnapshotList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxSnapshot `json:"items"`
}

// SandboxSnapshotTask is the internal node-facing task object for snapshot builds.
// It is created by SandboxSnapshotController and watched by the node agent.
// Users do not create this resource.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Node",type="string",JSONPath=".spec.targetNodeName"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type SandboxSnapshotTask struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SandboxSnapshotTaskSpec   `json:"spec"`
	Status            SandboxSnapshotTaskStatus `json:"status,omitempty"`
}

// SandboxSnapshotTaskSpec describes the node-local snapshot build to perform.
type SandboxSnapshotTaskSpec struct {
	// SnapshotRef is the owning SandboxSnapshot for this task.
	SnapshotRef corev1.TypedLocalObjectReference `json:"snapshotRef"`

	// SnapshotUID is the UID of the owning SandboxSnapshot.
	// Used to reject stale tasks after the snapshot is deleted and recreated.
	SnapshotUID types.UID `json:"snapshotUID"`

	// SnapshotMode is the mode of this snapshot task.
	SnapshotMode SandboxSnapshotMode `json:"snapshotMode"`

	// TargetSandboxRef is the temporary build Sandbox created by SandboxSnapshotController.
	TargetSandboxRef corev1.TypedLocalObjectReference `json:"targetSandboxRef"`

	// TargetNodeName is the node where the snapshot build must run.
	TargetNodeName string `json:"targetNodeName"`

	// ProviderName identifies the SnapshotDriver to use on the target node.
	ProviderName string `json:"providerName"`

	// SnapshotKey is the logical restore reference generated by SandboxSnapshotController.
	SnapshotKey string `json:"snapshotKey"`

	// SnapshotHash is the hash of the snapshot inputs that affect artifact contents.
	SnapshotHash string `json:"snapshotHash"`
}

// SandboxSnapshotTaskStatus is written by the node agent after the snapshot driver reports.
type SandboxSnapshotTaskStatus struct {
	// Phase is the current artifact phase as reported by the node agent.
	Phase SnapshotArtifactPhase `json:"phase,omitempty"`

	// Message is a human-readable description of the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// ObservedAt is the time the node agent last wrote this status.
	// +optional
	ObservedAt *metav1.Time `json:"observedAt,omitempty"`
}

// SnapshotArtifactPhase describes the lifecycle phase of a single node-local artifact.
type SnapshotArtifactPhase string

const (
	SnapshotArtifactPhaseCreating    SnapshotArtifactPhase = "Creating"
	SnapshotArtifactPhaseReady       SnapshotArtifactPhase = "Ready"
	SnapshotArtifactPhaseFailed      SnapshotArtifactPhase = "Failed"
	SnapshotArtifactPhaseUnavailable SnapshotArtifactPhase = "Unavailable"
)

// SandboxSnapshotTaskList contains a list of SandboxSnapshotTask.
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type SandboxSnapshotTaskList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SandboxSnapshotTask `json:"items"`
}

// Annotation keys used for snapshot restore intent on session Sandboxes.
const (
	// SnapshotKeyAnnotation is set on a session Sandbox to request restore from the
	// given snapshot key during Pod sandbox creation.
	SnapshotKeyAnnotation = "agentcube.volcano.sh/snapshot-key"
)

// Label keys used for snapshot build tracking.
const (
	// SnapshotNameLabelKey identifies the owning SandboxSnapshot for build Sandboxes and tasks.
	SnapshotNameLabelKey = "agentcube.volcano.sh/snapshot-name"
	// SnapshotKeyLabelKey identifies the snapshot key version for idempotent task lookup.
	SnapshotKeyLabelKey = "agentcube.volcano.sh/snapshot-key"
	// SnapshotNodeLabelKey identifies the target node for build Sandboxes and tasks.
	SnapshotNodeLabelKey = "agentcube.volcano.sh/snapshot-node"
	// SnapshotBuildLabelKey marks a Sandbox as a temporary fork-mode build sandbox.
	SnapshotBuildLabelKey = "agentcube.volcano.sh/snapshot-build"
)

// Node label key used to advertise snapshot provider capability.
const (
	// SnapshotProviderLabelPrefix is prepended by the provider name to form a node label.
	// Example: agentcube.volcano.sh/snapshot-provider.snapstart.kuasar.io=true
	SnapshotProviderLabelPrefix = "agentcube.volcano.sh/snapshot-provider."
)

func init() {
	SchemeBuilder.Register(&SandboxSnapshot{}, &SandboxSnapshotList{})
	SchemeBuilder.Register(&SandboxSnapshotTask{}, &SandboxSnapshotTaskList{})
	SchemeBuilder.Register(&SnapshotClass{}, &SnapshotClassList{})
}
