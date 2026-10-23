# AgentCube SnapStart Design

> Status: Draft
> Version: v0.2

---

## 1. Background and Goals

The default AgentCube session sandbox lifecycle is:

```text
Create Sandbox -> wait until Ready -> serve requests -> retire idle slots
```

Each new session normally cold-starts from an image. For Code Interpreter and browser
workloads, VM boot is not the dominant cost. Runtime initialization is usually more
expensive: loading Python packages, starting a Jupyter kernel, launching Chromium, and
waiting for CDP readiness.

This design uses snapshot support provided by the underlying runtime or VMM. It creates a
reusable baseline after runtime initialization has completed but before any user-specific
state has been loaded. New sessions can restore from that baseline instead of repeating
the expensive initialization sequence.

At the capability level, snapshots serve two use cases:

```text
Startup baseline snapshot:
Captured after runtime initialization and before user state is loaded. It can be reused by
many new sessions.

Single-session state snapshot:
Captured from an existing session sandbox and used only to restore that session.
```

Phase 1 focuses on startup baseline snapshots. Single-session state snapshots are a later
evolution target under the same abstraction, so the API and artifact model must leave room
for 1:1 restore semantics without requiring full suspend/resume support in Phase 1.

### 1.1 Non-Goals

The first phase does not include:

- suspending and resuming an existing session while preserving user state;
- preserving active TCP connections;
- migrating an existing session across nodes;
- exposing Kuasar, Kata, or other VMM-specific settings to application users;
- requiring users to create `SandboxTemplate`, `SandboxClaim`, `SandboxSnapshotTask`, or
  Fork build `Sandbox` resources manually.

### 1.2 Snapshot Usage Modes

The two snapshot use cases have different usage modes:

| Dimension | `Fork` | `Resume` |
|---|---|---|
| Source | User-state-free runtime template | Existing session sandbox |
| Purpose | Accelerate new sessions | Suspend and resume one existing session |
| User state | Must not exist | Must be preserved |
| Usage | 1:N fork | 1:1 resume |
| Replacement triggers | Template drift, scheduled rebuild | New snapshot object for each saved state |
| Scheduling | Built independently on multiple nodes | Usually tied to the source session |

The target design should let both modes share the snapshot build flow, artifact management,
and restore injection path. Fork manages reusable baseline refresh through rebuild policy.
Resume creates a new snapshot object for each saved session state.

---

## 2. Design Principles

### 2.1 Business Runtime Types Stay Outside the Snapshot Control Plane

AgentCube will support additional workload types over time: Code Interpreter, browser
agents, generic `AgentRuntime`, and third-party runtime CRDs.

Business runtime controllers normalize runtime-specific user intent before it reaches the
snapshot control plane. With that boundary, new runtime types reuse the same
`SandboxSnapshot` schema, webhook, controller logic, and artifact flow.

Fork-mode snapshot build input is normalized to the standard agent-sandbox
`SandboxTemplate`:

```text
Business Runtime CR
-> business Runtime Controller
-> generated SandboxTemplate
-> SandboxSnapshot(snapshotMode=Fork) references SandboxTemplate
```

Resume-mode snapshots reference an existing `Sandbox`. `SandboxSnapshotController`
understands only agent-sandbox resources, not the business CRD that produced them.

### 2.2 The Control Plane Uses Kubernetes Task APIs

The AgentCube control plane dispatches snapshot operations through internal
`SandboxSnapshotTask` resources. Runtime-specific and VMM-specific calls stay on the target
node:

```text
SandboxSnapshotController
-> create SandboxSnapshotTask with standard spec
-> node agent watches tasks assigned to its node
-> node-local SnapshotDriver calls the local runtime / VMM
```

### 2.3 SandboxSnapshotTask Is the Node Task Carrier

`SandboxSnapshotTask` is an internal CRD created by `SandboxSnapshotController`. It carries:

- the snapshot task declaration;
- target-node binding;
- the target sandbox reference;
- provider, snapshot key, and snapshot hash;
- node-agent-reported artifact phase.

In `Fork` mode, the task targets a build `Sandbox` created from the source
`SandboxTemplate`. In `Resume` mode, the task targets the existing source `Sandbox`. The
node agent watches `SandboxSnapshotTask` objects and filters tasks assigned to its node by
`spec.targetNodeName`.

### 2.4 Artifact Records Define Restore Availability

Phase 1 does not implement business-lifecycle cleanup for physical artifacts. When a
`SandboxSnapshot` is deleted or its artifact records are replaced, AgentCube only updates
control-plane metadata:

```text
control plane removes artifact records first
-> new sessions stop using retired snapshot keys immediately
```

Physical artifact cleanup is handled outside the Phase 1 AgentCube business lifecycle, for
example by node-side or provider-side storage protection.

---

## 3. Abstraction Layers

### 3.1 High-Level Architecture

```text
+------------------------------------------------------------------+
| User APIs                                                        |
| CodeInterpreter / AgentRuntime / third-party Runtime              |
| SandboxSnapshot / SnapshotClass                                  |
+------------------------------------------------------------------+
                              |
                              v
+------------------------------------------------------------------+
| Business Runtime Controller                                      |
| Maintains standard SandboxTemplate resources for session creation |
| Owns runtime-specific defaults and session payload construction   |
+------------------------------------------------------------------+
                              |
                              v
+------------------------------------+  artifact store  +-----------------------------+
| SandboxSnapshotController          |<--------------->| Workload Manager            |
| Watches Snapshot / Template /      |   controller     | Reads active artifact set   |
| Sandbox / Node / SnapshotTask      |   is sole writer | Injects restore intent      |
| Builds artifacts and status        |                  | Creates session Sandbox     |
+------------------------------------+                  +-----------------------------+
              |                                                   |
        SandboxSnapshotTask                               session Sandbox
              v                                                   v
+------------------------------------+                  +-----------------------------+
| Node Agent                         |                  | Kubernetes Runtime / VMM    |
| Watches SandboxSnapshotTask        |                  | Consumes restore intent     |
| Runs SnapshotDriver                |                  | Kuasar / Kata / others      |
+------------------------------------+                  +-----------------------------+
              |
              v
+------------------------------------+
| Kubernetes Runtime / VMM           |
| Runs Fork build sandbox and VMM APIs|
| Kuasar / Kata / others             |
+------------------------------------+
```

### 3.2 Business Runtime Controller

Each business runtime controller converts user intent into a standard `SandboxTemplate`:

```text
CodeInterpreterController -> SandboxTemplate
BrowserRuntimeController  -> SandboxTemplate
third-party Controller    -> SandboxTemplate
```

`SandboxTemplate` is a derived resource. Users do not create it manually. Generated
templates should carry an owner reference and a management label:

```yaml
metadata:
  ownerReferences:
  - kind: CodeInterpreter
    name: python
  labels:
    agentcube.volcano.sh/managed-by: code-interpreter-controller
```

When the business runtime is deleted, Kubernetes garbage collection removes the generated
template. The controller reconciles manual drift. A later admission webhook may reject
direct updates to managed templates.

The business runtime integration layer also owns runtime-specific inputs. These inputs stay
in the business integration layer while `SandboxSnapshotController` consumes only the
standard `SandboxTemplate` and `SandboxSnapshot` contracts:

- build-mode environment variables;
- session restore payload construction;
- authentication material injection.

`SandboxSnapshotController` does not depend on an adapter registry and never dispatches on
business runtime kind. Any business-specific conversion stays in the business runtime
controller or Workload Manager integration layer.

### 3.3 SandboxSnapshotController

`SandboxSnapshotController` is the sole owner of the global snapshot state machine. It:

1. watches `SandboxSnapshot`, `SandboxTemplate`, `Sandbox`, Node, and `SandboxSnapshotTask`;
2. reads standard `SandboxTemplate` resources for `Fork` mode and standard `Sandbox`
   resources for `Resume` mode;
3. calculates immutable snapshot hashes;
4. selects target nodes using `SnapshotClass.nodeSelector` and source scheduling
   constraints;
5. creates `SandboxSnapshotTask` resources for nodes without valid artifacts;
6. watches `SandboxSnapshotTask.status` and writes the artifact store;
7. aggregates `SandboxSnapshot.status`;
8. removes artifacts after snapshot hash drift, forced rebuild, or controller-detected
   artifact invalidation;
9. removes old artifact records before any physical deletion so new restores stop
   immediately;
10. manages retries and Kubernetes Events.

Runtime-specific and business-specific responsibilities stay in their owning layers:

- runtime API calls are handled by node-local `SnapshotDriver` implementations;
- business CRD parsing is handled by business runtime controllers.

### 3.4 Node Agent

The node agent runs on nodes that support snapshot drivers. It:

1. advertises provider capabilities through Node labels;
2. watches `SandboxSnapshotTask` resources targeted at its node;
3. selects an in-process driver using `SandboxSnapshotTask.spec.providerName`;
4. creates local artifacts through the runtime / VMM;
5. writes local artifact phase back to `SandboxSnapshotTask.status`;
6. stays on the snapshot build path.

`SandboxSnapshotController` is the only writer of `SandboxSnapshot.status`. This keeps
field ownership clear, avoids resource-version conflicts from concurrent node writers, and
keeps node-agent RBAC scoped to `SandboxSnapshotTask.status`.

### 3.5 SnapshotDriver

`SnapshotDriver` is an in-process node-agent interface, not an RPC protocol:

```go
type SnapshotDriver interface {
    Name() string
    Capabilities(ctx context.Context) SnapshotDriverCapabilities

    Create(ctx context.Context, req SnapshotDriverCreateRequest) (*SnapshotDriverArtifact, error)
    Delete(ctx context.Context, artifact SnapshotDriverArtifact) error
    List(ctx context.Context) ([]SnapshotDriverArtifact, error)
    Inspect(ctx context.Context, artifact SnapshotDriverArtifact) (*SnapshotDriverArtifactStatus, error)
}

type SnapshotDriverCapabilities struct {
    SnapshotModes           []SandboxSnapshotMode
}

type SnapshotDriverCreateRequest struct {
    TaskRef                 corev1.ObjectReference
    TargetSandboxRef        corev1.TypedLocalObjectReference
    TargetNodeName          string
    SnapshotMode            SandboxSnapshotMode
    ProviderName            string
    SnapshotKey             string
    SnapshotHash            string
}
```

`SandboxSnapshotTask` remains the Kubernetes task API. The node-agent reconciler watches
that CRD, validates provider and target state, and converts it into
`SnapshotDriverCreateRequest` before calling the in-process driver. Drivers consume the
typed request and stay independent from Kubernetes object metadata and status fields.

Example registry:

```go
drivers := map[string]SnapshotDriver{
    "snapstart.kuasar.io": newKuasarDriver(...),
}
```

The Kuasar driver calls its local Unix socket. Other drivers may call Kata, Firecracker, or
other runtime APIs. These private protocols stay inside node-local driver implementations.

`ProviderName` comes from `SandboxSnapshotTask.spec.providerName`. The node agent uses it to
select the local `SnapshotDriver`, and it is copied into `SnapshotArtifact.ProviderName`
for artifact metadata.

`SnapshotDriver.Create()` owns the low-level readiness handshake required before artifact
creation. When it returns a Ready artifact, that artifact must be usable for later restore
without depending on temporary files or metadata that will be removed with the target
Sandbox.

`SnapshotDriverArtifact` and `SnapshotDriverArtifactStatus` are driver-private artifact
representations. They may describe node-local or remote artifacts. The node agent converts
their results into the AgentCube standard `SnapshotArtifact` records defined in the
artifact store.

### 3.6 Kubernetes Runtime / VMM Compatibility Layer

Snapshot creation and session restoration are separate paths:

- create: node agent watches a `SandboxSnapshotTask`, then invokes a driver;
- restore: Kubernetes runtime consumes standard restore intent before Pod sandbox
  creation.

AgentCube standard build metadata is mapped to VMM-specific snapshot creation by the
node-agent `SnapshotDriver`. AgentCube standard restore intent is mapped to VMM-specific
restore by the Kubernetes runtime compatibility layer. The node agent stays on the snapshot
build path.

When a session should restore from a snapshot, restore intent must exist before Pod sandbox
creation. Patching restore annotations after the node agent discovers a Pod would race with
CRI sandbox creation.

Each Kubernetes runtime compatibility layer translates AgentCube standard restore intent
into its private restore protocol. This is a cross-system compatibility contract, not an
AgentCube in-process interface. Its implementation may live inside a CRI runtime, shim, or
VMM integration rather than the AgentCube node-agent process.

---

## 4. API Design

### 4.1 SandboxSnapshot CRD

`SandboxSnapshot` declares a snapshot of an agent-sandbox execution environment. The
usage modes introduced in Section 1 are represented by `snapshotMode`: `Fork` is reusable
for 1:N forking, and `Resume` is used for 1:1 resuming.

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: SandboxSnapshot
metadata:
  name: python-ready
spec:
  snapshotMode: Fork
  sourceRef:
    apiGroup: extensions.agents.x-k8s.io
    kind: SandboxTemplate
    name: python
  snapshotClassName: kuasar
  forkPolicy:
    rebuildOnSourceChange: true
    rebuildAfter: 24h
```

Resume example:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: SandboxSnapshot
metadata:
  name: session-abc-resume
spec:
  snapshotMode: Resume
  sourceRef:
    apiGroup: agents.x-k8s.io
    kind: Sandbox
    name: session-abc
  snapshotClassName: kuasar
```

Suggested types:

```go
type SandboxSnapshotSpec struct {
    // +kubebuilder:validation:Required
    SnapshotMode      SandboxSnapshotMode              `json:"snapshotMode"`
    // +kubebuilder:validation:Required
    SourceRef         corev1.TypedLocalObjectReference `json:"sourceRef"`
    // +kubebuilder:validation:Required
    SnapshotClassName string                           `json:"snapshotClassName"`
    ForkPolicy        *SandboxSnapshotForkPolicy       `json:"forkPolicy,omitempty"`
}

// +kubebuilder:validation:Enum=Fork;Resume
type SandboxSnapshotMode string

const (
    SandboxSnapshotModeFork   SandboxSnapshotMode = "Fork"
    SandboxSnapshotModeResume SandboxSnapshotMode = "Resume"
)

type SandboxSnapshotForkPolicy struct {
    // +kubebuilder:default=true
    RebuildOnSourceChange *bool            `json:"rebuildOnSourceChange,omitempty"`
    RebuildAfter          *metav1.Duration `json:"rebuildAfter,omitempty"`
}

```

`snapshotMode=Fork` supports the standard `SandboxTemplate` kind for `sourceRef`.
`snapshotMode=Resume` supports the standard `Sandbox` kind. `sourceRef` uses Kubernetes
`corev1.TypedLocalObjectReference`; `apiGroup` and `kind` make the reference explicit and
leave room for controlled evolution. They are not an invitation to dynamically parse
arbitrary business runtime CRDs.

`forkPolicy` directly describes the conditions that trigger a new reusable baseline build:

- `rebuildOnSourceChange`: rebuild when the generated `SandboxTemplate` changes, including
  image reference, command, args, environment, resources, runtime class, volumes, or
  security context. If unset, it defaults to `true`;
- `rebuildAfter`: rebuild after the baseline has remained Ready for this duration. This is
  a background replacement: the active artifact set continues serving restore intent while
  a pending artifact set is built. The controller promotes the pending set to active only
  after it becomes available; if replacement fails, the previous active set remains in
  service.

`RebuildOnSourceChange` uses a pointer so the API can distinguish an omitted value from an
explicit `false`. CRD defaulting or the controller's defaulting path must normalize `nil`
to `true` before reconciliation logic evaluates the policy.

`Resume` mode has fixed build behavior: creating the object triggers one snapshot build.
Restore is a data-plane action; `SandboxSnapshot.status.phase` describes snapshot artifact
creation and availability. If the same session needs another saved state later, the caller
creates another `SandboxSnapshot(snapshotMode=Resume)` object. If `forkPolicy` is set when
`snapshotMode=Resume`, the admission webhook rejects the object.

An image upgrade must explicitly update the image reference in `SandboxTemplate`.
Implicit registry changes behind an unchanged tag, such as `picod:latest` pointing to a
new digest, are not supported. Prefer digest-pinned or versioned image references.

### 4.2 SandboxSnapshot Status

Status contains aggregate state, not per-node artifact details:

```yaml
status:
  phase: Ready
  targetNodeCount: 3
  creatingNodeCount: 0
  readyNodeCount: 2
  failedNodeCount: 1
  unavailableNodeCount: 0
  readyAt: "2026-06-02T08:00:00Z"
```

Suggested types:

```go
type SandboxSnapshotPhase string

const (
    SandboxSnapshotPhasePending     SandboxSnapshotPhase = "Pending"
    SandboxSnapshotPhaseCreating    SandboxSnapshotPhase = "Creating"
    SandboxSnapshotPhaseReady       SandboxSnapshotPhase = "Ready"
    SandboxSnapshotPhaseFailed      SandboxSnapshotPhase = "Failed"
)

type SandboxSnapshotStatus struct {
    Phase                       SandboxSnapshotPhase `json:"phase"`
    TargetNodeCount             int32                `json:"targetNodeCount"`
    CreatingNodeCount           int32                `json:"creatingNodeCount"`
    ReadyNodeCount              int32                `json:"readyNodeCount"`
    FailedNodeCount             int32                `json:"failedNodeCount"`
    UnavailableNodeCount        int32                `json:"unavailableNodeCount"`
    ReadyAt                     *metav1.Time         `json:"readyAt,omitempty"`
    Conditions                  []metav1.Condition   `json:"conditions,omitempty"`
    Message                     string               `json:"message,omitempty"`
}
```

These counts describe node coverage for the active artifact version, not the total number
of artifact objects. `TargetNodeCount` is the number of nodes the controller expects to
cover. `CreatingNodeCount`, `ReadyNodeCount`, `FailedNodeCount`, and
`UnavailableNodeCount` count each target node at most once, and their sum equals
`TargetNodeCount`. Pending replacement artifacts are tracked in the artifact store and do
not change active-version availability until promotion.

`FailedNodeCount` counts target nodes where the current build attempt has reached a
terminal build failure, such as driver create failure, readiness failure, or retry
exhaustion for the active build. `UnavailableNodeCount` counts target nodes whose active
artifact is not currently consumable because of external availability signals, such as
Node `NotReady`, lost provider capability, validation pending after node recovery, or a
missing physical artifact detected by inspection. A failed build describes the build
result; an unavailable artifact describes current consumability of an expected artifact.

The snapshot is available for restore when `ReadyNodeCount > 0`. `Phase=Ready` means the
active artifact set has at least one Ready artifact.

For `Resume` mode, the target set is normally the source Sandbox's current node, so these
counts usually describe one node.

`Conditions` is reserved for machine-readable status using Kubernetes
`metav1.Condition`. In Phase 1, `phase`, node count fields, and the active artifact set in
the artifact store remain the primary status contract. Restore intent injection does not
depend on conditions. Implementations may add condition types later when they need a stable
machine-readable signal that cannot be derived from phase, counts, or artifact-store state.

Degraded coverage is derived from the node count fields instead of duplicated as a default
condition. The controller emits Kubernetes Events for human-facing transitions such as
partial node failure.

### 4.3 SnapshotClass CRD

`SnapshotClass` describes an infrastructure capability and is managed by cluster
administrators:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: SnapshotClass
metadata:
  name: kuasar
spec:
  providerName: snapstart.kuasar.io
  supportedSnapshotModes:
  - Fork
  - Resume
  nodeSelector:
    agentcube.volcano.sh/snapshot-provider.snapstart.kuasar.io: "true"
```

Suggested type:

```go
type SnapshotClassSpec struct {
    // +kubebuilder:validation:Required
    ProviderName            string                `json:"providerName"`
    // +kubebuilder:validation:MinItems=1
    SupportedSnapshotModes  []SandboxSnapshotMode `json:"supportedSnapshotModes"`
    NodeSelector            map[string]string     `json:"nodeSelector,omitempty"`
}
```

The example class declares a shared Kuasar capability:

```text
supportedSnapshotModes = [Fork, Resume]
```

The API model includes `snapshotMode=Resume`, while Phase 1 implementation uses
`snapshotMode=Fork`.

`SandboxSnapshot.spec.snapshotMode` must be included in
`SnapshotClass.spec.supportedSnapshotModes`. The admission webhook also rejects unsupported
`sourceRef.kind` values and rejects `forkPolicy` when `snapshotMode=Resume`.
`providerName` is the stable provider identifier used by the control plane and node
agent. It maps to a node-agent-local `SnapshotDriver` implementation; it does not imply a
remote provider service.

`supportedSnapshotModes` is a capability set because `SandboxSnapshot.spec.snapshotMode` selects
the actual usage mode. Artifact placement and local-versus-remote resolution stay inside
the provider/runtime implementation and are not exposed as Phase 1 `SnapshotClass` fields.

### 4.4 Artifact Store

Artifact metadata lives in an artifact store rather than CRD status. Phase 1 can
continue to use Redis or Valkey:

```text
key   = snapshot:{ownerKind}:{namespace}:{ownerName}:{ownerUID}
value = JSON-encoded owner-specific store record
```

Generic structure:

```go
type SnapshotArtifactSet struct {
    SnapshotKey  string             `json:"snapshotKey"`
    SnapshotHash string             `json:"snapshotHash"`
    Artifacts   []SnapshotArtifact `json:"artifacts,omitempty"`
}

type SnapshotArtifact struct {
    ProviderName            string                `json:"providerName"`
    NodeName                string                `json:"nodeName,omitempty"`
    Phase                   SnapshotArtifactPhase `json:"phase"`
    SnapshotKey             string                `json:"snapshotKey"`
    SnapshotHash            string                `json:"snapshotHash"`
    CreatedAt               *time.Time            `json:"createdAt,omitempty"`
    Retry                   *SnapshotBuildRetry   `json:"retry,omitempty"`
    Message                 string                `json:"message,omitempty"`
}

type SnapshotBuildRetry struct {
    FailureCount int32      `json:"failureCount,omitempty"`
    LastFailedAt *time.Time `json:"lastFailedAt,omitempty"`
    NextRetryAt  *time.Time `json:"nextRetryAt,omitempty"`
}

type SnapshotArtifactPhase string

const (
    SnapshotArtifactPhaseCreating    SnapshotArtifactPhase = "Creating"
    SnapshotArtifactPhaseReady       SnapshotArtifactPhase = "Ready"
    SnapshotArtifactPhaseFailed      SnapshotArtifactPhase = "Failed"
    SnapshotArtifactPhaseUnavailable SnapshotArtifactPhase = "Unavailable"
)

type SnapshotArtifactSetRef struct {
    SnapshotKey string `json:"snapshotKey,omitempty"`
}

type SnapshotArtifactManifest struct {
    OwnerRef      metav1.OwnerReference           `json:"ownerRef"`
    // ArtifactSets maps SnapshotKey to the corresponding artifact set.
    // The map key is generated by SandboxSnapshotController and must match
    // SnapshotArtifactSet.SnapshotKey.
    ArtifactSets  map[string]SnapshotArtifactSet `json:"artifactSets,omitempty"`
    ActiveSetRef  SnapshotArtifactSetRef         `json:"activeSetRef,omitempty"`
    PendingSetRef SnapshotArtifactSetRef         `json:"pendingSetRef,omitempty"`
}
```

Where:

- `OwnerRef` uses Kubernetes `metav1.OwnerReference`; namespace is part of the artifact
  store key and is not duplicated in the value;
- `ArtifactSets` maps `SnapshotKey` to the corresponding artifact set, so one node can
  temporarily hold multiple artifacts that belong to different logical versions;
- `ActiveSetRef` and `PendingSetRef` point to entries in `ArtifactSets` by `SnapshotKey`;
- `Artifacts` contains the concrete artifacts in the same artifact set. For the current
  Kuasar Phase 1 provider, artifacts are resolved on the node that created them, so
  `NodeName` is required and each node has at most one artifact in the same
  `SnapshotArtifactSet`;
- `SnapshotKey` is generated by `SandboxSnapshotController` and identifies a logical
  snapshot version shared across nodes. It is also the only AgentCube-level restore
  reference passed to the runtime. It should be human-readable when possible, for example
  `<normalized-snapshot-name>-<mode>-g<generation>-r<rebuildSeq>`. AgentCube does not model
  artifact location, reference format, or local-versus-remote placement. The node agent,
  snapshot provider, and runtime compatibility layer resolve `SnapshotKey` to the actual
  local or remote artifact. Snapshot UID, `SnapshotHash`, and `ProviderName` remain the
  authoritative validation inputs;
- `CreatedAt` is set when the node artifact has been created. It remains unset while the
  artifact is still being created or has not produced a usable artifact;
- `Retry` records build retry state for this artifact. Keeping it nested prevents retry
  policy fields from mixing with artifact identity fields;
- `ProviderName` identifies the snapshot provider used to create the artifact;
- `SnapshotHash` prevents restore from stale snapshot inputs.

Example:

```text
SnapshotHash = sha256:def456
SnapshotKey  = python-ready-fork-g12-r1
```

`SnapshotArtifactManifest` is the owner-scoped artifact-store manifest for one
`SandboxSnapshot`. It records artifact sets, concrete artifact entries, and the
active/pending artifact set references used by restore and rebuild flows. Implementations
may store it as one Redis / Valkey value or split it into artifact-set records and artifact
entries. It is an internal control-plane data record.

`SnapshotArtifactPhase` is shared by snapshot CRDs because it describes a concrete
artifact lifecycle phase. `Creating` means the artifact build has been declared or is
running, but the artifact is not usable for restore yet. `SandboxSnapshotPhase` is shared by
both `Fork` and `Resume` modes; it describes snapshot artifact creation and availability,
not data-plane restore completion.

`SandboxSnapshotController` is the sole writer of its `SnapshotArtifactManifest`. Node agents
report local artifact phases through `SandboxSnapshotTask.status` and never write the
artifact store directly. Valid readers are `SandboxSnapshotController`, which reads its own
records for rebuild decisions, and Workload Manager, which reads Ready artifacts under
`ActiveSetRef.SnapshotKey` before injecting restore intent.

The controller updates a store record with a Redis / Valkey transaction or compare-and-set
operation. In `Fork` mode, a scheduled rebuild retains
`ActiveSetRef.SnapshotKey` while populating `PendingSetRef.SnapshotKey`; a source
change clears `ActiveSetRef` before starting the incompatible replacement. In
`Resume` mode, the active artifact represents the saved session state. The controller also
writes and reads artifact retry state in `SnapshotArtifact.Retry`.

`Resume` mode normally uses only `ActiveSetRef`; `PendingSetRef` is primarily
used by compatible background replacement in `Fork` mode.

The following SandboxSnapshot flows use `activeSetRef.snapshotKey` and
`pendingSetRef.snapshotKey` as short names for the active and pending artifact set
references.

### 4.5 SandboxSnapshotTask CRD

`SandboxSnapshotTask` is an internal CRD. It is the node-facing task object for both `Fork`
and `Resume` builds. Users do not create it.

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: SandboxSnapshotTask
metadata:
  name: python-ready-node-a-r1
  ownerReferences:
  - apiVersion: runtime.agentcube.volcano.sh/v1alpha1
    kind: SandboxSnapshot
    name: python-ready
    uid: ...
spec:
  snapshotRef:
    apiGroup: runtime.agentcube.volcano.sh
    kind: SandboxSnapshot
    name: python-ready
  snapshotUID: ...
  snapshotMode: Fork
  targetSandboxRef:
    apiGroup: agents.x-k8s.io
    kind: Sandbox
    name: python-ready-build-node-a
  targetNodeName: node-a
  providerName: snapstart.kuasar.io
  snapshotKey: python-ready-fork-g12-r1
  snapshotHash: sha256:def456
status:
  phase: Ready
  observedAt: "2026-06-02T08:00:00Z"
```

Suggested types:

```go
type SandboxSnapshotTaskSpec struct {
    SnapshotRef             corev1.TypedLocalObjectReference `json:"snapshotRef"`
    SnapshotUID             types.UID                        `json:"snapshotUID"`
    SnapshotMode            SandboxSnapshotMode              `json:"snapshotMode"`
    TargetSandboxRef        corev1.TypedLocalObjectReference `json:"targetSandboxRef"`
    TargetNodeName          string                           `json:"targetNodeName"`
    ProviderName            string                           `json:"providerName"`
    SnapshotKey             string                           `json:"snapshotKey"`
    SnapshotHash            string                           `json:"snapshotHash"`
}

type SandboxSnapshotTaskStatus struct {
    Phase       SnapshotArtifactPhase `json:"phase,omitempty"`
    Message     string                `json:"message,omitempty"`
    ObservedAt  *metav1.Time         `json:"observedAt,omitempty"`
}
```

`snapshotRef` is the owner snapshot object for this task. Phase 1 requires it to reference
`runtime.agentcube.volcano.sh/v1alpha1, Kind=SandboxSnapshot`. `snapshotUID` remains a
separate field so the node agent and controller can reject stale tasks after a same-name
snapshot is deleted and recreated.

`targetSandboxRef` is the sandbox to snapshot:

- `Fork`: a build `Sandbox` created by `SandboxSnapshotController` from the source
  `SandboxTemplate`;
- `Resume`: the existing source session `Sandbox`.

Both references are namespace-local typed references.

`SandboxSnapshotTask.status.phase` is the node agent's report for the local snapshot
artifact. The controller creates artifact entries as `Creating` when it creates tasks. Node
agents report `Ready` or `Failed` after `SnapshotDriver.Create()` returns. The controller
may derive `Unavailable` from control-plane signals such as node loss, task timeout, or
capability mismatch.

Build-stage protocol fields live on `SandboxSnapshotTask`, not on the target `Sandbox`:

- build intent is stored in `SandboxSnapshotTask.spec`;
- build result phase is stored in `SandboxSnapshotTask.status`;
- the Fork build `Sandbox` is only the runtime target referenced by `targetSandboxRef`;
- the session `Sandbox` keeps only restore intent annotations consumed by the runtime
  compatibility layer.

The node agent processes tasks whose `spec.targetNodeName` matches its node name and
writes only `SandboxSnapshotTask.status`. It does not write the artifact store or
`SandboxSnapshot.status`.

### 4.6 Session Restore Intent

Before creating a session Sandbox, Workload Manager injects:

```text
agentcube.volcano.sh/snapshot-key = ...
```

Kubernetes runtime consumes this AgentCube standard annotation before Pod sandbox
creation. The runtime compatibility layer is selected by the session Sandbox
`runtimeClassName` or the underlying runtime implementation. `snapshot-key` is the
runtime-consumable AgentCube restore reference. Kuasar-specific `kuasar.io/*` annotations
belong only inside a Kuasar compatibility layer.

On a session `Sandbox`, the presence of `snapshot-key` is the restore intent.
Restore compatibility is defined by the runtime compatibility layer and the snapshot
provider that resolves the snapshot key.

---

## 5. End-to-End Flows

### 5.1 User Defines a Runtime

The user creates a business runtime:

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: CodeInterpreter
metadata:
  name: python
spec:
  template:
    image: picod:v1
    args: [--preload=numpy,pandas]
```

`CodeInterpreterController` generates:

```yaml
apiVersion: extensions.agents.x-k8s.io/v1alpha1
kind: SandboxTemplate
metadata:
  name: python
  ownerReferences:
  - apiVersion: runtime.agentcube.volcano.sh/v1alpha1
    kind: CodeInterpreter
    name: python
  labels:
    agentcube.volcano.sh/managed-by: code-interpreter-controller
spec:
  podTemplate:
    spec:
      runtimeClassName: kuasar
      containers:
      - name: code-interpreter
        image: picod:v1
        args: [--preload=numpy,pandas]
```

The user does not create `SandboxTemplate` manually.

The generated template name follows a deterministic convention. Phase 1 uses the business
runtime name as the default `SandboxTemplate` name. Administrators can also list generated
templates by owner reference or management label:

```text
kubectl get sandboxtemplates -l agentcube.volcano.sh/managed-by=code-interpreter-controller
```

### 5.2 Administrator Enables Fork Snapshot

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: SandboxSnapshot
metadata:
  name: python-ready
spec:
  snapshotMode: Fork
  sourceRef:
    apiGroup: extensions.agents.x-k8s.io
    kind: SandboxTemplate
    name: python
  snapshotClassName: kuasar
  forkPolicy:
    rebuildOnSourceChange: true
    rebuildAfter: 24h
```

### 5.3 User or Workload Manager Creates Resume Snapshot

```yaml
apiVersion: runtime.agentcube.volcano.sh/v1alpha1
kind: SandboxSnapshot
metadata:
  name: session-abc-resume
spec:
  snapshotMode: Resume
  sourceRef:
    apiGroup: agents.x-k8s.io
    kind: Sandbox
    name: session-abc
  snapshotClassName: kuasar
```

`Resume` mode captures a specific session Sandbox. It must not be used to fork unrelated
new sessions.

### 5.4 Controller Creates Build Tasks

For `Fork` mode, `SandboxSnapshotController`:

```text
1. Reads SandboxTemplate.
2. Computes snapshotHash and creates a new pending artifact set reference.
3. Reads SnapshotClass.
4. Selects target nodes using SnapshotClass and source scheduling constraints.
5. Reads the artifact store.
6. Creates one build Sandbox per target node that needs an artifact.
7. Binds each build Sandbox using podTemplate.spec.nodeName.
8. Creates one SandboxSnapshotTask per build Sandbox.
9. Sets SandboxSnapshotTask.targetSandboxRef to the build Sandbox.
```

For `Resume` mode, `SandboxSnapshotController`:

```text
1. Reads the source Sandbox.
2. Computes snapshotHash from the source Sandbox identity, runtime-relevant spec, and provider protocol.
3. Selects the node that currently hosts the Sandbox.
4. Creates one SandboxSnapshotTask for that node.
5. Sets SandboxSnapshotTask.targetSandboxRef to the source Sandbox.
6. Records the resulting artifact under the active artifact set reference after the task succeeds.
```

### 5.5 Node Agent Creates Artifacts

The target node agent:

```text
1. Watches SandboxSnapshotTask where spec.targetNodeName=<this-node>.
2. Reads task.spec.targetSandboxRef.
3. For Fork, waits for the target build Sandbox to become Running.
4. For Resume, verifies that the source Sandbox still runs on this node.
5. Calls SnapshotDriver.Create(); the driver performs its low-level readiness handshake.
6. Reports artifact phase on SandboxSnapshotTask.status.
```

`SandboxSnapshotController` observes `SandboxSnapshotTask.status`:

```text
1. Writes the artifact store.
2. Aggregates SandboxSnapshot.status.
3. Promotes PendingSetRef to ActiveSetRef when the artifact set becomes available.
4. Deletes temporary Fork build Sandboxes after their tasks complete.
5. Deletes completed SandboxSnapshotTasks after the target Sandbox cleanup has started or
   completed.
```

Deleting a temporary Fork build Sandbox after task completion is valid because Ready means
the driver has detached the artifact from the build Sandbox lifecycle.

Keeping the completed `SandboxSnapshotTask` until the build Sandbox cleanup step preserves a
short-lived Kubernetes object for debugging and avoids a window where the task has
disappeared while the temporary build Sandbox is still visible.

Completed `SandboxSnapshotTask` objects are deleted by `SandboxSnapshotController` after:

1. terminal task status has been consumed;
2. the artifact store has been updated;
3. `SandboxSnapshot.status` has been aggregated;
4. for `Fork` mode, temporary build Sandbox deletion has started or completed;
5. optional controller-level retention TTL has elapsed.

At least one Ready artifact makes the snapshot available.

### 5.6 New Session Creation from Fork Snapshot

Users continue to create sessions through the existing Router or SDK and do not interact
with `SandboxSnapshot`.

Workload Manager:

```text
1. Finds a Ready SandboxSnapshot(snapshotMode=Fork) associated with the session SandboxTemplate.
2. Reads SnapshotArtifactManifest from the artifact store.
3. Resolves manifest.ActiveSetRef.SnapshotKey to the active SnapshotArtifactSet.
4. Confirms the active set has at least one Ready artifact.
5. Validates snapshot UID, provider name, snapshot key, and snapshot hash.
6. Injects restore intent using the active set's snapshot key if a valid artifact exists.
7. Otherwise creates a regular cold-start Sandbox.
```

Workload Manager derives restore intent from the active artifact set in the artifact
store. It injects `manifest.ActiveSetRef.SnapshotKey` as
`agentcube.volcano.sh/snapshot-key`. AgentCube does not choose a local or remote
artifact location. The runtime compatibility layer and snapshot provider resolve the
snapshot key on the scheduled node.

Kubernetes runtime consumes restore intent:

```text
snapshot-key exists
-> runtime attempts restore from SnapshotKey

snapshot-key is absent
-> regular cold start

restore fails after intent injection
-> runtime handles the failure according to its implementation
```

Router and SDK do not distinguish restore from cold start.

### 5.7 Session Resume from Resume Snapshot

Workload Manager:

```text
1. Finds the Ready SandboxSnapshot(snapshotMode=Resume) created for the session.
2. Reads SnapshotArtifactManifest from the artifact store.
3. Resolves manifest.ActiveSetRef.SnapshotKey to the active SnapshotArtifactSet.
4. Confirms the active set has a Ready artifact.
5. Validates snapshot UID, provider name, snapshot key, and snapshot hash.
6. Creates a Sandbox with AgentCube standard restore intent.
7. Leaves SandboxSnapshot status unchanged by the data-plane restore result.
```

No component writes a generic restore result back to the agent-sandbox object. If restore
fails, the resume attempt fails from the caller's perspective. If the business layer allows
discarding the old session state, it should create a new session through the normal session
creation path.

### 5.8 Template Change and Automatic Rebuild

```text
business runtime changes
-> business controller updates SandboxTemplate
-> snapshot hash changes
-> SandboxSnapshotController clears ActiveSetRef and removes old artifact records
-> new sessions temporarily cold-start
-> controller creates new build Sandboxes and SandboxSnapshotTasks
-> new artifacts become Ready
-> sessions start restoring from new artifacts
```

On incompatible source changes, `SandboxSnapshotController` clears `ActiveSetRef` before
creating replacement tasks. New sessions then use cold start until a replacement artifact
set becomes Ready.

### 5.9 SandboxSnapshot Deletion

```text
SandboxSnapshotController
-> removes every artifact record for the SandboxSnapshot from the artifact store
-> new sessions stop using old artifacts immediately
-> deletes SandboxSnapshot
```

Phase 1 finalizers protect control-plane metadata cleanup only. They do not synchronously
wait for physical deletion on every node.

### 5.10 Scheduled Fork Rebuild

`rebuildAfter` triggers a background replacement without removing the active version first:

```text
SandboxSnapshot remains Ready
-> controller creates PendingSetRef, build Sandboxes, and SandboxSnapshotTasks
-> artifacts under ActiveSetRef continue serving sessions
-> new artifacts become Ready
-> controller atomically promotes PendingSetRef to ActiveSetRef
```

This differs from a source change. A source change makes the old artifact incompatible,
so old artifact records are removed immediately and sessions cold-start until new artifacts are
Ready.

---

## 6. Snapshot Hash and Version Validation

`snapshotHash` identifies the inputs that affect artifact contents or restore compatibility.
`SandboxSnapshotController` computes it before creating a new `SnapshotArtifactSet`. The
same semantic input must produce the same hash.

Recommended input model:

```text
SnapshotHashInput {
  snapshotMode
  sourceIdentity
  sourcePodTemplateSpec
  snapshotClass
}
```

`sourceIdentity` is mode-specific:

- `Fork`: source `SandboxTemplate` namespace, name, and UID;
- `Resume`: source `Sandbox` namespace, name, and UID.

`sourcePodTemplateSpec` is mode-specific:

- `Fork`: normalized `SandboxTemplate.spec.podTemplate.spec`;
- `Resume`: normalized source `Sandbox.spec.podTemplate.spec`.

`snapshotClass` contains:

- `snapshotClassName`;
- `SnapshotClass.spec.providerName`.

The controller serializes `SnapshotHashInput` with a stable JSON serializer and computes:

```text
sha256(stableJSON(SnapshotHashInput))
```

Normalization rules:

- include the full pod template spec after normalization, rather than maintaining a
  fragile allowlist of individual pod fields;
- do not include object `status`;
- do not include Kubernetes metadata except the explicitly listed source identity fields;
- do not include `resourceVersion`, `generation`, `managedFields`, or
  `creationTimestamp`;
- sort map keys before serialization;
- preserve list order because Kubernetes list order is semantically meaningful for fields
  such as containers, initContainers, volumes, env, volumeMounts, and tolerations;
- include references present in the pod template spec, such as Secret names, ConfigMap
  names, PVC claim names, projected volume definitions, `env`, `envFrom`, `volumes`, and
  `volumeMounts`;
- do not dereference the data behind those references.

Changing a reference changes `snapshotHash`. Changing data behind the same reference is not
visible to the AgentCube control plane and does not change `snapshotHash`. For example,
updating `ConfigMap.data`, `Secret.data`, or files stored in the same PVC without changing
the referenced name does not automatically trigger snapshot rebuild.

`snapshotHash` is a control-plane hash of snapshot inputs that affect compatibility. It is
not a physical artifact content digest and not a hash of the captured runtime memory state.

Implicit registry changes behind an unchanged image tag are not supported. An image
upgrade must update the template image reference explicitly. Prefer digest-pinned or
versioned image references.

---

## 7. Snapshot Readiness and Restore Injection

### 7.1 Layered Responsibilities

Readiness and restore injection are extension points below the generic control plane:

| Layer | Responsibility |
|---|---|
| `SandboxSnapshotController` | Creates snapshot tasks and aggregates artifact phases; understands no private runtime protocol |
| `SnapshotDriver` | Performs runtime / VMM-specific readiness handshake and artifact operations |
| Runtime compatibility layer | Translates AgentCube standard restore intent into a private restore protocol |

`SandboxSnapshot` declares the snapshot source. Runtime-internal snapshot timing belongs to
the selected `SnapshotDriver`, which decides when the runtime is ready for snapshot
creation. New runtimes reuse the same CRD and `SandboxSnapshotController`.

### 7.2 Driver Readiness

The driver's `Create()` implementation decides when the low-level artifact can be created.
For Kuasar Fork snapshot support, the Kuasar integration uses its inject-socket readiness probe. The
runtime answers the probe with capabilities before the driver creates the artifact.

Another driver may use a shim API, a VMM API, a guest-agent handshake, or no additional
handshake if its create API already guarantees readiness. AgentCube does not require a
generic HTTP endpoint.

The minimum Phase 1 driver contract is that `Create()` returns only after the runtime is in
a safe snapshot point for reusable Fork artifacts. If a driver cannot prove that condition,
it must fail the snapshot task instead of producing a Ready artifact. This makes the readiness
mechanism provider-specific while keeping the safety boundary explicit.

### 7.3 Fork-Safe Snapshot Point

For `snapshotMode=Fork`, a safe snapshot point is a business-runtime contract, not just a
process or socket readiness signal. A reusable Fork artifact is Ready only when all of the
following are true:

1. expensive bootstrap initialization has completed;
2. no user request, user file, session token, active task, or session-specific context has
   been loaded;
3. state that must be unique per session is absent from the artifact or can be reset after
   restore;
4. the restored runtime can accept fresh session identity, credentials, workspace, and task
   context through the session path;
5. the driver or runtime integration can verify the condition before reporting Ready.

The business runtime integration defines the concrete readiness probe for its runtime. For
example, a code interpreter may require the language runtime and base package cache to be
ready while user code and uploaded files are absent. A browser runtime may require the
browser process and reusable profile seed to be ready while cookies, user profile data,
CDP session state, network identity, and credentials are absent or resettable.

`SnapshotDriver.Create()` consumes that runtime-specific signal and then performs the
runtime / VMM capture. If the runtime cannot provide a fork-safe readiness signal, the
driver must not report a Ready Fork artifact.

### 7.4 Code Interpreter Fork-Safe Point

Phase 1 implements SnapStart for Code Interpreter, so its Fork-safe point is part of the
Phase 1 contract.

Before reporting Fork Ready, Code Interpreter bootstrap must have completed:

1. the interpreter runtime process has started;
2. the runtime service endpoint used by AgentCube is reachable;
3. base environment initialization has completed, including configured package preload,
   import cache, runtime health checks, and other user-state-free bootstrap work;
4. the runtime is blocked from accepting user execution requests until after restore-time
   session injection.

The reusable Fork artifact must not contain:

1. user code, notebook state, execution history, or active tasks;
2. uploaded files or user workspace contents;
3. session token, auth material, request-specific environment variables, or task metadata;
4. user-specific proxy, network identity, or credential state;
5. temporary files that are created by a user session.

After restore, the session path injects fresh session state:

1. session identity and auth token;
2. workspace mount or bind path;
3. uploaded files and request payload;
4. per-session environment variables;
5. task metadata and runtime context.

The Code Interpreter integration must expose a bootstrap-ready signal that is checked by
the runtime-specific readiness probe before `SnapshotDriver.Create()` captures the artifact.
That signal means bootstrap is complete and no session input has been accepted. If the
runtime cannot prove both conditions, the driver must not report Ready.

Phase 1 tests should cover at least:

1. a restored session does not see files from another restored session;
2. a restored session receives a fresh session token and request context;
3. user code submitted before one session finishes is not visible in another session;
4. the Fork artifact can be restored after the temporary build Sandbox is deleted;
5. versioned ConfigMap, Secret, image, or template references trigger a new
   `snapshotHash`, while data changes behind unchanged references do not.

### 7.5 Standard Restore Intent

AgentCube standard restore intent is intentionally minimal:

```text
agentcube.volcano.sh/snapshot-key = <SnapshotKey>
```

The presence of this annotation means the runtime should attempt restore from the given
snapshot key during Pod sandbox creation. When the annotation is absent, the runtime follows
the regular cold-start path. This annotation is scoped to session `Sandbox` restore intent;
build results use `SandboxSnapshotTask.status.phase`. AgentCube does not model artifact
locations or provider reference formats. `SnapshotKey` is the stable logical restore reference.
Users do not provide `SnapshotKey`; user input affects artifact generation only through
`SandboxSnapshot` spec, `SnapshotClass`, and the source template.

For the current Kuasar Phase 1 provider, the runtime resolves `SnapshotKey` against
provider metadata on the scheduled node. For a future remote artifact mode, the provider resolves the same `SnapshotKey` to an
applicable remote artifact using provider-owned metadata, such as OS, architecture, runtime
profile, or VMM compatibility. AgentCube does not require users to name or select artifact
variants.

Business-session context, if any, is outside the standard restore intent and is handled by
the business runtime integration.

### 7.6 Runtime Compatibility Layer

The runtime compatibility layer maps AgentCube restore intent to its private VMM restore
protocol. The compatibility layer is selected by the session Sandbox
`runtimeClassName` or the underlying runtime implementation. It uses
`snapshot-key` as the runtime-consumable restore reference. The compatibility
layer resolves the key on the node where the Sandbox is scheduled, then restores,
cold-starts, or fails according to the runtime's restore policy. For the current Kuasar
Phase 1 provider, this resolution uses provider metadata on that node. For future remote
artifact modes, the provider may resolve the same key to an applicable remote artifact. During Pod sandbox creation, the
compatibility layer uses the Sandbox annotations and runtime-local state. AgentCube does
not call a restore API and does not define an in-process restore interface.

For Kuasar, the compatibility layer uses its existing inject socket sequence:

```text
CAPABILITIES -> PREPARE -> READY -> COMMIT -> STARTED
```

Other runtimes implement an equivalent compatibility layer in their CRI runtime, shim, or
VMM integration. The generic Workload Manager and `SandboxSnapshotController` remain
unchanged.

### 7.7 Adding a Non-Kuasar Runtime

A non-Kuasar runtime integrates through the same boundaries:

| Extension | Required | Location |
|---|---|---|
| Business Runtime Controller that generates `SandboxTemplate` and session payload | Yes | Business control plane |
| `SnapshotDriver` registered under a stable provider name | Yes | Node Agent |
| Runtime compatibility layer that consumes AgentCube standard restore intent | Yes | CRI runtime, shim, or VMM integration |
| `SnapshotClass` selecting provider capability and target nodes | Yes | Cluster configuration |

Adding a runtime keeps `SandboxSnapshot`, the `SandboxSnapshotTask` API, artifact records,
and `SandboxSnapshotController` stable. With the phase-1 in-process registry, shipping a
new driver may still require rebuilding or extending the node-agent distribution. A future
plugin mechanism can change packaging while preserving these contracts.

---

## 8. SandboxTemplate, SandboxClaim, and WarmPool

### 8.1 SandboxTemplate Is the Standard Runtime Template

`SandboxTemplate` is the standard runtime execution template generated and maintained by a
business runtime controller. Users create the business runtime object, and the controller
derives the corresponding `SandboxTemplate`.

Any runtime that supports session creation reconciles a stable `SandboxTemplate`
regardless of WarmPool or SnapStart configuration. `SandboxSnapshot` Fork mode consumes
that template; it does not trigger template generation.

### 8.2 SandboxClaim Belongs to the WarmPool Path

`SandboxClaim` is an internal resource used by the WarmPool session path. Users do not
create it manually.

When a user configures:

```yaml
kind: CodeInterpreter
spec:
  warmPoolSize: 3
```

the business runtime controller creates or updates WarmPool-specific resources:

```text
SandboxWarmPool
```

`SandboxTemplate` already exists as the runtime's standard execution template and is
referenced by `SandboxWarmPool`.

When a new session uses the WarmPool path, Workload Manager creates:

```text
SandboxClaim
```

`SandboxClaim` then acquires a pre-created Sandbox from `SandboxWarmPool`.

### 8.3 Phase 1 Session Path Selection

Phase 1 keeps the Snapshot restore path and the WarmPool path independent. One session
creation request uses one path.

```text
Snapshot restore path:
Workload Manager creates a new Sandbox with restore intent

WarmPool path:
SandboxClaim acquires a pre-created Sandbox
```

Both paths share the `SandboxTemplate` generated by the business runtime controller.

If a business runtime enables both WarmPool and Snapshot configuration in Phase 1, the
business runtime integration or Workload Manager selects one session path according to its
runtime policy. The default policy is WarmPool first for requests served by an available
warm slot; otherwise the Snapshot restore path can create a new Sandbox with restore
intent.

WarmPool slot refill does not use Snapshot restore in Phase 1. A WarmPool + Snapshot
combination can be designed in a later phase, for example:

```text
SandboxWarmPool refills a slot
-> creates Sandbox
-> restores from SandboxSnapshot
-> SandboxClaim acquires the restored slot
```

The later combination still uses the standard restore intent annotation. The runtime
compatibility layer resolves `SnapshotKey` on the node where the refill Sandbox is
scheduled.

---

## 9. Node Discovery and Scheduling

### 9.1 Capability Labels

Node Agent advertises registered providers through Node labels:

```text
agentcube.volcano.sh/snapshot-provider.snapstart.kuasar.io=true
```

`SnapshotClass.nodeSelector` uses these labels. Node Agent must reject a snapshot task when
the selected provider implementation does not match `SnapshotClass.spec.providerName`.

Provider labels are discovery hints, not compatibility proofs. The controller and node
agent still validate provider name and snapshot mode against `SnapshotClass` and
`SnapshotDriverCapabilities`.

### 9.2 Target Build Nodes

`SandboxSnapshotController` selects target build nodes.

For `Fork` mode, a target build node must:

1. have `Ready=True`;
2. match `SnapshotClass.nodeSelector`;
3. support the selected provider name and snapshot mode;
4. satisfy RuntimeClass scheduling constraints from the source pod template;
5. satisfy hard scheduling constraints from the source pod template, such as node selector,
   required node affinity, tolerations, and explicit `nodeName`.

For `Resume` mode, the target build node is the node currently hosting the source Sandbox.
The controller fails the snapshot if the source Sandbox has no assigned node or is no
longer running.

### 9.3 Session Restore Scheduling

Workload Manager keeps session scheduling independent from per-node artifact availability.
When a Ready `SandboxSnapshot` has an active artifact set, Workload Manager injects restore
intent before creating the session Sandbox and lets the normal Kubernetes scheduler place
it.

Restore intent carries `SnapshotKey`. The runtime compatibility layer resolves that key on
the scheduled node. If the provider can resolve a usable artifact, the runtime can restore
from it; otherwise the runtime follows its restore policy, such as cold start or fail.

`SnapshotArtifact.NodeName` remains part of the artifact store for per-node status
aggregation. Session placement stays with the Kubernetes scheduler.

---

## 10. Artifact Record Lifecycle

AgentCube Phase 1 manages artifact records, not the business lifecycle of physical
artifacts. A record is added when a `SandboxSnapshotTask` reports a Ready artifact, and is
removed when the snapshot is deleted, replaced, or no longer valid for restore.

Physical artifact cleanup is outside the Phase 1 AgentCube control-plane contract.

### 10.1 Failure Cases

| Case | Handling |
|---|---|
| Controller crashes after artifact creation but before artifact-store write | No artifact record is created; the artifact is not used for restore |
| Snapshot owner deleted | Artifact records are removed first; new sessions stop referencing the snapshot key |
| Node becomes NotReady | Controller marks artifacts on that node as `Unavailable`; other Ready artifacts remain usable |
| Node recovers | Controller validates the node artifact, for example by creating a validation task or using driver inspection, then marks it `Ready` again when validation succeeds |
| Node permanently removed | Control plane removes artifact records for that node |
| Build hash changes | Old artifact records are removed immediately |

---

## 11. State Machines

### 11.1 SandboxSnapshot

Fork mode:

```text
Pending
  |
  | create build Sandboxes and SandboxSnapshotTasks
  v
Creating
  +-----------------+
  | any Ready       | all nodes failed
  v                 v
Ready             Failed
  |
  | incompatible source change / unusable active artifacts
  +---------------> Creating

Ready
  |
  | rebuildAfter background replacement
  +---------------> Ready
```

When active artifacts become incompatible or unusable, the controller removes their records and
enters `Creating` directly. During a compatible scheduled replacement, the snapshot
remains `Ready` while a replacement version is being created.

`Phase=Ready` means:

```text
at least one artifact.phase == Ready
and artifact.snapshotHash == current snapshot hash
```

Resume mode:

```text
Pending
  |
  | create one SandboxSnapshotTask on source node
  v
Creating
  +-----------------+
  | artifact Ready  | build failed / source gone
  v                 v
Ready             Failed
```

`Phase=Ready` means the resume artifact is Ready. A data-plane restore does not change
the `SandboxSnapshot` phase.

### 11.2 SandboxSnapshotTask

```text
create
-> assign targetNodeName
-> resolve targetSandboxRef
-> Fork: target build Sandbox Running
-> Resume: source Sandbox still on target node
-> Node Agent calls driver.Create()
-> driver readiness handshake succeeds
-> artifact phase Ready | Failed
-> Node Agent writes task status
-> Controller consumes terminal task status
-> Controller updates artifact store
-> Controller aggregates SandboxSnapshot.status
-> Fork: Controller starts or completes temporary build Sandbox deletion
-> optional completed-task retention TTL elapses
-> Controller deletes completed task
```

### 11.3 Session Sandbox

```text
create session
-> active artifact set available?
   + no: regular cold-start Sandbox
   + yes: inject restore intent with snapshot key
          -> runtime restore
          -> restore or runtime-level fallback
```

---

## 12. Failure Handling

| Failure | Behavior |
|---|---|
| No target node in Fork mode | `SandboxSnapshot.status.phase=Failed`; sessions cold-start |
| Resume source Sandbox missing or no longer running | `SandboxSnapshot.status.phase=Failed`; resume fails |
| Fork target build Sandbox never reaches Running | Artifact build fails and retries with backoff |
| SandboxSnapshotTask times out | Artifact build fails and retries with backoff |
| Driver readiness proof is unavailable or fails | Artifact build fails; report driver-specific reason |
| Driver artifact creation fails | Artifact Failed; other Ready artifacts remain usable |
| Partial node failures | `Phase=Ready` if at least one artifact is Ready; node count fields show incomplete coverage; emit `SandboxSnapshotDegraded` Event for observation |
| Artifact store unavailable | Sessions use the regular cold-start path until the artifact store is available and validation succeeds |
| Restore fails after intent injection | Runtime handles the failure according to its implementation; the generic control plane records no restore result |
| Controller detects an artifact is unusable | Remove artifact records and rebuild |

---

## 13. Observability

### 13.1 Kubernetes Events

| Event | Meaning |
|---|---|
| `SandboxSnapshotCreating` | Snapshot build started |
| `SandboxSnapshotReady` | At least one artifact is available |
| `SandboxSnapshotDegraded` | Some target nodes failed or became unavailable |
| `SandboxSnapshotRebuilding` | Fork source change or explicit rebuild started replacement artifacts |
| `SandboxSnapshotFailed` | Every artifact build failed |

### 13.2 Metrics

```text
agentcube_sandbox_snapshot_build_duration_seconds
agentcube_sandbox_snapshot_artifacts{phase=...}
agentcube_sandbox_snapshot_target_node_count
agentcube_sandbox_snapshot_ready_node_count
agentcube_snapshot_build_task_total{phase=...}
agentcube_snapshot_restore_intent_total{mode=...,result=cold_start|restore_intent}
agentcube_sandbox_snapshot_rebuild_total{reason=...}
agentcube_snapshot_artifact_record_removed_total{reason=...}
```

---

## 14. Security Boundaries

1. Snapshot-build artifacts contain runtime bootstrap state only. User state, active tasks,
   and session credentials stay outside reusable build artifacts. Phase 1 requires each
   `SnapshotDriver.Create()` implementation to prove, through a readiness handshake or an
   equivalent runtime guarantee, that the runtime is at a safe snapshot point before it
   returns a Ready artifact.
2. Session credentials are injected through the session path instead of reusable bootstrap
   artifacts.
3. Runtime-specific session context is handled by the business runtime integration layer
   and stays outside generic snapshot artifacts.
4. Node Agent processes only `SandboxSnapshotTask` objects with
   `spec.targetNodeName=<this-node>`.
5. Before reporting `SandboxSnapshotTask.status`, Node Agent validates:
   - SandboxSnapshot UID;
   - snapshot key;
   - snapshot hash;
   - driver;
   - target node.
6. Node Agent RBAC is scoped to `SandboxSnapshotTask.status`; `SandboxSnapshot.status` is
   owned by `SandboxSnapshotController`.

---

## 15. Delivery Plan

### Phase 1: Code Interpreter Fork SnapStart

1. Add generic `SandboxSnapshot` and `SnapshotClass` CRDs; implement `Fork` mode first.
2. Make CodeInterpreterController always reconcile standard `SandboxTemplate`.
3. Implement `SandboxSnapshotController`.
4. Add internal `SandboxSnapshotTask` CRD and controller reconciliation.
5. Change `agentd` to watch node-targeted `SandboxSnapshotTask` resources.
6. Implement the Kuasar `SnapshotDriver` inside Node Agent.
7. Generalize the artifact store to `SnapshotArtifactManifest`, `SnapshotArtifactSet`, and `SnapshotArtifact`.
8. Make Workload Manager write AgentCube standard restore intent.
9. Implement Code Interpreter SnapStart end to end, including its Fork-safe readiness signal
   and session restore injection path.

### Phase 2: WarmPool Combination

```text
SandboxWarmPool refills slot
-> new Sandbox restores from SandboxSnapshot(snapshotMode=Fork)
-> SandboxClaim allocates restored slot
```
