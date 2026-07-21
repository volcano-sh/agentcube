---
title: Sandbox Resource Pool Management
authors:
  - "@lichuqiang"
reviewers:
  - TBD
approvers:
  - TBD
creation-date: 2026-07-06
last-updated: 2026-07-06
status: draft
tracking-issue: "#430"
---

# Sandbox Resource Pool Management

## Summary

### Background

The current AgentCube project implements sandbox lifecycle management based on K8s Pods, but it faces two inherent shortcomings in agent scenarios:

- **High startup latency**: To serve general-purpose workloads, K8s Pod orchestration, scheduling, and network initialization take at least seconds-level time, which is far from the millisecond-level startup expected in agent scenarios. The existing solution accelerates this through pre-warming (pre-creating sandboxes), but it is impractical and wastes resources.
- **Limited scale ceiling**: Constrained by backend storage and the K8s control-plane software architecture, the load scale a K8s cluster can support is limited, making it difficult to carry million-level sandbox capacity.

Based on this, we expect to incubate a next-generation AgentCube system architecture with two resource tracks — fast and slow:

1. **Slow resources**: Implement sandbox resource pool management based on K8s CRD, carving out a portion of node resources for sandbox execution.
2. **Fast resources**: An independent, high-performance, lightweight control plane supporting sandbox ecosystem management.

The end goal is co-location of regular Pods and sandboxes on the same node.

### Scope of This Proposal

This proposal focuses on the **slow resource** part of the next-generation architecture — the design of sandbox resource pool management. The system implements declarative management of sandbox resources through a two-layer architecture:

- **Global policy layer** (SandboxPool Controller): Responsible for Class → Pool mapping, node selection, policy snapshot synchronization, and Phase aggregation.
- **Node execution layer** (placeholder-agent): Acts as the node-local agent, responsible for resource policy enforcement, watermark checks, and status reporting.

Core designs include:

- Using the **Static Pod model** to lock scheduling resources; mirror pods are automatically rebuilt after accidental deletion.
- Declarative state synchronization based on **CRD Watch/List**.
- Implements vertical resource scaling via **[containerd runtime shim (v2 Task API)](https://github.com/containerd/containerd/blob/main/docs/runtime-v2.md)** (VPA analogy), independent of InPlacePodVerticalScaling Feature Gate.
- Coordinating multi-component status writes via the **SSA (server-side apply) field manager mechanism** to avoid field-level conflicts.

## Motivation

In co-location scenarios, regular container workloads and sandbox workloads coexist on the same node. Cluster administrators need to carve out a dedicated portion of node resources for sandbox execution so that sandbox workloads (e.g. AI agent inference sessions, function compute instances) get guaranteed, pre-allocated capacity without competing with regular Pods for scheduling — while the remaining node capacity still serves normal Kubernetes workloads. Without a dedicated resource pool, sandbox workloads either fail to schedule under node pressure or starve regular Pods by consuming unbounded resources. There is currently no Kubernetes-native way to:

- **Declaratively manage sandbox resource pools**: Operators need to define different resource quota policies for different node groups and automatically create and maintain corresponding pool instances.
- **Elastically scale sandbox resources**: When sandbox load changes dynamically, resource quotas need to be adjusted online without interrupting running sandboxes.
- **Guarantee resource locking reliability**: Ensure that scheduling resources occupied by sandboxes are not preempted by other workloads, even in abnormal scenarios such as accidental deletion or node failure.
- **Provide global observability**: Platform operators need a global view of the health status of sandbox resource pools across all nodes.

### Goals

- Provide declarative CRD APIs (SandboxPoolClass + SandboxPool) to manage node-level sandbox resource pools.
- Reliably lock scheduling resources through the Static Pod model; mirror pods are automatically rebuilt after accidental deletion (typically within seconds, depending on kubelet sync interval).
- Support adjusting placeholder Pod resource quotas via containerd runtime shim. This is NOT Kubernetes in-place Pod resize (InPlacePodVerticalScaling) — the placeholder Pod is rebuilt through Static Pod manifest update (delete/recreate), but running sandboxes managed by node-ctl are not interrupted during the rebuild.
- Implement a Phase state machine (Pending → Ready → Degraded → Unready), aggregated and computed by the Controller from Conditions.
- placeholder-agent serves as the sole proxy for node-ctl, implementing resource policy synchronization, watermark checks, and status reporting.
- Pure etcd storage, no external storage dependencies such as Redis.
- Coordinate dual status writes between Controller and placeholder-agent via the SSA (server-side apply) field manager mechanism.

### Non-Goals

- Does not manage sandbox create/suspend/resume/delete (handled by node-ctl).
- Does not design sandbox resource overcommit coordination (handled by node-ctl).
- Does not design sandbox snapshot management (handled by node-ctl).
- Does not implement namespace-level resource isolation (CRD is Cluster-scoped).
- Does not design the internal implementation of node-ctl (treated as a black-box component; only interface contracts are defined).

## Proposal

### Overall Architecture

The system adopts a two-layer architecture model:

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Kubernetes Control Plane                      │
│  Storage: pure etcd (all state via K8s API Server)                   │
│                                                                      │
│  ┌────────────────────────────────────────────────────────────────┐  │
│  │  SandboxPool Controller (Deployment, multi-replica + leader)   │  │
│  └────────────────────────────────────────────────────────────────┘  │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
                               ▲ CRD Watch/List + Status Patch
┌──────────────────────────────┼──────────────────────────────────────┐
│  Node (kubelet)              │                                      │
│                             ┌┘                                      │
│                             │                                       │
│  ┌─ placeholder-agent daemon (systemd) ─────────────────────────┐   │
│  │  CRD Watch + manifest management + status reporting + healthz │   │
│  └──────┬───────────────────────┬────────────────────────────────┘   │
│         │                       │                                     │
│  write manifest          UDS + JSON                           │
│         ▼                       ▼                                     │
│  ┌──────────────────────┐    ┌────────────┐                          │
│  │ Static Pod (mirror   │◄───│ node-ctl   │ (existing/black-box)     │
│  │ pod)                 │    └────────────┘                          │
│  │ runtimeClassName:    │       ▲                                    │
│  │   placeholder        │       │                                    │
│  └──────────┬───────────┘       │                                    │
│             │                   │                                    │
│  ┌──────────▼───────────────────┴─────────────────────────────────┐  │
│  │  containerd (CRI server, kubelet's sole connection)             │  │
│  │  ┌─ runtime handler: runc → runc shim (regular Pod)            │  │
│  │  └─ runtime handler: placeholder → placeholder-agent shim (v2) │  │
│  │     shim.Create→daemon.Create → resource adjustment → write config + restart node-ctl     │  │
│  │     shim.Start → mark container as Running                    │  │
│  │     shim.Delete → mark stop (does not touch node-ctl)          │  │
│  └────────────────────────────────────────────────────────────────┘  │
│              uses carved-out resources to create sandboxes            │
└──────────────────────────────────────────────────────────────────────┘
```

### Core Design Decisions

| Decision | Choice | Reason |
|----------|--------|--------|
| Placeholder Pod model | Static Pod | mirror pod auto-recreated by kubelet from manifest after deletion/eviction (typically within seconds, depending on kubelet sync interval); continuously recreated as long as the manifest exists; no PDB needed. During rebuild, kubelet's local podManager still holds the static pod, so resource accounting (admit checks, node allocatable) is unaffected — resource locking does not depend on the mirror pod's existence in the API |
| Placeholder Pod execution mode | No workload process | placeholder-agent runs as a host-level systemd service; the manifest uses pause:3.9 only to satisfy K8s Pod schema requirements; cgroup creation is skipped via the placeholder shim, not by default K8s behavior |
| Storage backend | Pure etcd (K8s API Server) | Resource pool control plane only needs CRD state; no external storage needed |
| CRD Scope | Cluster-scoped | placeholder-agent is a node-local singleton (one per node) bound to one Pool; no namespace isolation needed |
| Phase calculator | Controller aggregation | When placeholder-agent is unreachable (node deleted or agent crash), it cannot update Phase; Controller aggregation + stale detection ensures Phase can still be updated in extreme scenarios |
| Status write coordination | SSA Apply (fieldManager) | placeholder-agent SSA-Applies status on event (only on status field changes); heartbeat via Lease every 10s; Controller occasionally SSA-Applies; field-level ownership avoids conflicts |
| Resource adjustment method | containerd runtime shim (VPA analogy) | As a containerd shim v2 plugin, intercepts container creation calls to adjust resources online; watermark protection during downscale prevents running sandbox OOM |

> **Manifest Self-Healing**: The Static Pod model protects against accidental **Pod** deletion (kubelet rebuilds from manifest). But if the **manifest file itself** is accidentally deleted, kubelet will stop the static pod. To cover this case, the placeholder-agent daemon's reconcile loop periodically verifies that the manifest exists for every active Pool on its node; if the manifest is missing while the Pool CRD still exists, the daemon rewrites it. This makes manifest recovery as automatic as mirror-pod recovery — the agent is the authoritative writer of the manifest, and the CRD (not the manifest) is the source of truth for desired state.

### Component/Object Responsibility Matrix

| Layer | Component/Object | Responsibilities | Interaction with node-ctl | Deployment Form |
|-------|-----------------|------------------|--------------------------|-----------------|
| Global policy layer | SandboxPool Controller | Class→Pool mapping + node selection + policy snapshot sync + Phase aggregation + Finalizer + node check | ❌ None | Deployment + leader election |
| Node execution layer | placeholder-agent daemon | manifest management + node-ctl full proxy + CRD Watch + Conditions reporting | ✅ Sole interaction party | host-level systemd service |
| Node execution layer | placeholder-agent shim | containerd runtime shim v2 (Task API), stateless TTRPC proxy; business logic moved to daemon, resource adjustment in daemon.Create (shim forwards) | ✅ via daemon (shim forwards via TTRPC, daemon calls node-ctl) | containerd runtime plugin (config.toml registration) |
| Node execution layer | Static Pod (mirror pod) | Placeholder locking of scheduling resources (resources.requests/limits) + RuntimeClass: placeholder routes to placeholder-agent shim + pause-only (no workload process); cgroup creation skipped via shim, not by default K8s behavior | Indirect: node-ctl uses its carved-out resources to create sandboxes | kubelet auto-maintained from manifest |
| Node execution layer | node-ctl | sandbox create/suspend/resume/delete + resource overcommit coordination + snapshot management (black-box, only interface contracts defined) | N/A (itself) | Existing implementation |

#### Shim Task v2 Contract: Synthetic Implementation for No-Workload-Process

The placeholder-agent shim registers as a containerd runtime shim v2 (Task API). Since the placeholder Pod has **no workload process** (pause:3.9 only satisfies Pod schema), the shim implements the full Task v2 interface synthetically, without relying on a real process:

| Task v2 Method | Synthetic Behavior | Notes |
|---------------|-------------------|-------|
| `Create` | Generates a synthetic task ID (no real PID); forwards to daemon.Create for resource policy alignment | daemon is the business logic carrier |
| `Start` | Marks the task as Running in in-memory state; returns success (no real process started) | Signal source for `PlaceholderPodReady=True` |
| `State` | Returns in-memory Running status; PID returns 0 (synthetic, indicating no real process) | CRI readiness checks depend on this method |
| `Kill` | Marks task as Exited (no real signal sent); does not trigger node-ctl stop | Only affects shim-layer state, no cascade to node-ctl |
| `Wait` | Returns a channel that fires only on `Kill`/`Shutdown` with a synthetic exit code (0) | No real process exit event |
| `Delete` | Cleans up in-memory task state; does not call node-ctl delete | node-ctl lifecycle managed by daemon on Pool deletion |
| `Connect` | Returns success (no real process to connect to) | Used for kubelet reconnect scenarios |
| `Shutdown` | Cleans up all in-memory shim state; notifies daemon to release associated resources | Called by containerd when removing the sandbox |

> **Design Constraint**: The shim is a stateless TTRPC proxy — all task state lives in the shim process's memory and is lost on process restart. The daemon is the persistent state carrier (CRD Watch + manifest management); on daemon restart, it rebuilds shim state via GET SandboxPool spec (write config file + restart node-ctl for alignment). This ensures shim crashes do not affect Pool state consistency.

> **Phase 2 Acceptance Criterion**: Before `PlaceholderPodReady` is derived from "shim task started (Running)," Phase 2 must include a minimal shim spike/e2e covering: create/start → CRI readiness check → stop/remove → wait/exit events → reconnect (kubelet reconnect) behavior.

### Status Writers and SSA Coordination

SandboxPool status is managed by two components via the SSA (Server-Side Apply) field manager mechanism, ensuring non-overlapping writes. Each component declares field ownership using a unique field manager identifier:

| Field Manager | Component | Managed Fields | Write Timing |
|-----------|-----------|---------------|--------------|
| `placeholder-agent` | placeholder-agent | placeholderPod, placeholderAgent, override, resize, nodeCtl, poolInfo, conditions (non-NodeNotFound/PlaceholderAgentHealthy), lastAppliedGeneration | Event-driven SSA Apply (only on status field changes); heartbeat via Lease every 10s |
| `sandboxpool-controller` | SandboxPool Controller | **phase**, conditions (NodeNotFound, PlaceholderAgentHealthy) | SSA Apply during Pool Reconcile |

This "data source and aggregator separated" design ensures Phase can still be updated in extreme scenarios — when a node is deleted, the Controller sets Phase=Unready based on the NodeNotFound Condition; when the agent crashes but the Node still exists, the Controller detects the expired Lease (TTL > 40s with no renewal) and sets `PlaceholderAgentHealthy=False`, downgrading Phase to Degraded/Unready.

> **Lease-Based Heartbeat**: Each placeholder-agent maintains a `coordination.k8s.io/Lease` object (name = Pool name, cluster-scoped) and renews it every 10s. The Controller Watches Leases; when a Lease's `renewTime` exceeds its TTL (default 40s = 4× renewal interval), the Controller sets `PlaceholderAgentHealthy=False` and downgrades Phase. This mirrors the kubelet heartbeat pattern: the Lease is a high-frequency small object (just renewTime + holderIdentity), while the status SSA Apply is event-driven — only triggered when a status field actually changes. In a 10,000-node cluster, steady-state load is ~1,000 Lease renewals/s (lightweight) with near-zero status Apply requests, versus ~333 status Apply/s if status were used for both heartbeat and state. The Lease renewal interval and TTL are configurable via `--lease-renew-interval` (default 10s) and `--lease-ttl` (default 40s).

> **Conditions SSA note**: The `conditions` field must be declared as a map list in the CRD schema (`x-kubernetes-list-type: map`, `x-kubernetes-list-map-keys: [type]`) so that different field managers can own and update different condition entries without replacing the entire list.

## Design Details

### CRD Data Model

#### SandboxPoolClass

**Group / Version / Kind / Scope**: `sandboxpool.agentcube.volcano.sh` / `v1alpha1` / `SandboxPoolClass` / `Cluster`

**Spec Definition**:

```go
type SandboxPoolClassSpec struct {
    // Target node selector; immutable after creation.
    //
    // Why immutable: the Selector determines the set of nodes a Class applies to.
    // Changing it would cause large-scale churn — nodes that no longer match would need
    // their Pools garbage-collected, and newly-matching nodes would need new Pools
    // created, resulting in a mass rebuild behavior. This mirrors how
    // Deployment.spec.selector is immutable in Kubernetes for the same reason.
    // No actual use case requiring mutation has been identified; the restriction can
    // be lifted in the future if a concrete requirement emerges.
    Selector metav1.LabelSelector `json:"selector"`

    // Per-node resource quota. Required; resources must be non-empty and include
    // at least `cpu` and `memory` keys. Other resource keys (e.g. hugepages,
    // extended resources) are optional.
    ResourcePolicy ResourcePolicy `json:"resourcePolicy"`

    // Placeholder Pod configuration
    PlaceholderPod *PlaceholderPodSpec `json:"placeholderPod,omitempty"`
}

// ResourcePolicy wraps a corev1.ResourceList for per-node resource quota definition.
// Using ResourceList (map[corev1.ResourceName]resource.Quantity) instead of hardcoded
// CPU/Memory fields preserves room for future extension — e.g. percentage-based specs
// or additional resource types — without API changes.
type ResourcePolicy struct {
    Resources corev1.ResourceList `json:"resources,omitempty"`
}

type PlaceholderPodSpec struct {
    Annotations     map[string]string `json:"annotations,omitempty"`
    Tolerations     []corev1.Toleration `json:"tolerations,omitempty"`
    RuntimeClassName *string           `json:"runtimeClassName,omitempty"`
    // Numeric pod priority. Static Pods do not resolve priorityClassName (no scheduler/admission
    // path), so a numeric value is required to participate in node-pressure eviction ordering.
    // See https://kubernetes.io/docs/concepts/scheduling-eviction/node-pressure-eviction/
    Priority *int32                    `json:"priority,omitempty"`
    // Declarative placeholder: currently placeholder-agent runs as host-level systemd,
    // manifest uses pause:3.9 as placeholder image; this field is reserved for future in-Pod execution mode
    AgentImage string `json:"agentImage,omitempty"`
}
```

**Status Definition**:

```go
type SandboxPoolClassStatus struct {
    ObservedGeneration int64 `json:"observedGeneration,omitempty"`
    TotalSandboxPools  int32 `json:"totalSandboxPools"`
    ReadySandboxPools  int32 `json:"readySandboxPools"`
    NotReadySandboxPools int32 `json:"notReadySandboxPools"`
    Conditions []metav1.Condition `json:"conditions,omitempty"`
}
```

#### SandboxPool

**Group / Version / Kind / Scope**: `sandboxpool.agentcube.volcano.sh` / `v1alpha1` / `SandboxPool` / `Cluster`

**Spec Definition**:

```go
type SandboxPoolSpec struct {
    // Reference to the parent SandboxPoolClass. Set by the Controller during Pool creation.
    // Immutable after creation — changing it would detach the Pool from its Class.
    ClassRef ClassRef `json:"classRef"`

    // Name of the node this Pool is bound to. Set by the Controller during Pool creation.
    // Immutable after creation — changing it would break the 1-Pool-per-node binding.
    // A single node cannot have Pools of different Classes — enforced by the Validating
    // Webhook and four-layer protection (see Validating Webhook section).
    NodeName string `json:"nodeName"`

    // Node-ctl configuration snapshot. Set by the Controller from Class spec.
    // placeholder-agent obtains the actual socket address via --node-ctl-socket
    // startup parameter; this field is informational and for future declarative use.
    // Optional; when nil, the agent uses its startup flag value.
    NodeCtl *NodeCtlConfig `json:"nodeCtl,omitempty"`

    // Resource policy snapshot synced by the Controller from Class.spec.resourcePolicy.
    // When Override.enabled=true, placeholder-agent prioritizes spec.override.resourcePolicy.
    // Optional in spec but always set by the Controller during creation.
    ResourcePolicy *ResourcePolicy `json:"resourcePolicy,omitempty"`

    // Operator override for node-specific adjustments. Optional.
    // When enabled, the agent uses the override's resourcePolicy instead of the Class snapshot.
    Override *OverrideSpec `json:"override,omitempty"`
}

type OverrideSpec struct {
    // Whether the override is active. When true, the agent uses the override's
    // resourcePolicy instead of the Class snapshot. Set by operators.
    Enabled bool `json:"enabled,omitempty"`

    // Human-readable reason for the override. Required when Enabled=true.
    // Provides audit traceability for why the override was applied.
    Reason string `json:"reason,omitempty"`

    // The override resource policy. Required when Enabled=true.
    // When nil and Enabled=true, the webhook rejects the Pool.
    ResourcePolicy *ResourcePolicy `json:"resourcePolicy,omitempty"`
}

type ClassRef struct {
    // Name of the SandboxPoolClass this Pool belongs to. Set by the Controller.
    Name string `json:"name"`
}

type NodeCtlConfig struct {
    // node-ctl socket endpoint. Currently only unix:// is supported.
    // placeholder-agent obtains the actual address via --node-ctl-socket;
    // this field is informational and reserved for future declarative reconciliation.
    Endpoint string `json:"endpoint,omitempty"`
}
```

**Status Definition**:

```go
type SandboxPoolStatus struct {
    // Computed and written by Controller via aggregation (based on Conditions)
    Phase PoolPhase `json:"phase"`

    // The following fields are written by placeholder-agent
    PlaceholderPod     *PlaceholderPodInfo `json:"placeholderPod,omitempty"`
    PlaceholderAgent   *PlaceholderAgentInfo `json:"placeholderAgent,omitempty"`
    Override           *OverrideInfo       `json:"override,omitempty"`
    Resize             *ResizeInfo         `json:"resize,omitempty"`
    NodeCtl            *NodeCtlStatus      `json:"nodeCtl,omitempty"`
    PoolInfo           *PoolInfo           `json:"poolInfo,omitempty"`
    LastAppliedGeneration int64           `json:"lastAppliedGeneration,omitempty"`
    Conditions         []metav1.Condition `json:"conditions,omitempty"`
}
```

**Sub-structure Definitions**:

```go
type PoolPhase string

const (
    PoolPhasePending  PoolPhase = "Pending"
    PoolPhaseReady    PoolPhase = "Ready"
    PoolPhaseDegraded PoolPhase = "Degraded"
    PoolPhaseUnready  PoolPhase = "Unready"
)

type PlaceholderPodInfo struct {
    Name  string        `json:"name,omitempty"`
    UID   string        `json:"uid,omitempty"`
    Phase corev1.PodPhase `json:"phase,omitempty"`
    IP    string        `json:"ip,omitempty"` // Only has value when --netns-enabled is set
}

type PlaceholderAgentInfo struct {
    Version        string       `json:"version,omitempty"`
    LastHeartbeat  *metav1.Time `json:"lastHeartbeat,omitempty"` // Updated by placeholder-agent on each status Apply; informational — Controller uses Lease TTL for liveness detection
    NodeCtlStarted bool         `json:"nodeCtlStarted,omitempty"`
    NodeCtlStartAt *metav1.Time `json:"nodeCtlStartAt,omitempty"`
}

type OverrideInfo struct {
    Enabled      bool         `json:"enabled,omitempty"`
    Reason       string       `json:"reason,omitempty"`
    Source       string       `json:"source,omitempty"`
    OverriddenAt *metav1.Time `json:"overriddenAt,omitempty"`
}

type ResizeInfo struct {
    Status          ResizeStatus      `json:"status,omitempty"`
    RequestedCPU    resource.Quantity `json:"requestedCpu,omitempty"`
    RequestedMemory resource.Quantity `json:"requestedMemory,omitempty"`
    DeferredReason  string            `json:"deferredReason,omitempty"`
    LastProbeTime   *metav1.Time      `json:"lastProbeTime,omitempty"`
}

type ResizeStatus string

const (
    ResizeInProgress  ResizeStatus = "InProgress"
    ResizeCompleted  ResizeStatus = "Completed"
    ResizeDeferred   ResizeStatus = "Deferred"
)
```

> **None state semantics**: `status.resize` is a pointer type `*ResizeInfo` with `omitempty`; the None state is represented by the field being `nil` (the `resize` key is omitted from JSON). It is never explicitly written in code. A non-nil pointer is only set when the state is not None. `LastProbeTime` is the last status-write time, not the Deferred timeout start point (timeout is computed from the in-memory `deferredStartedAt`).

```go
type NodeCtlStatus struct {
    Connected     bool         `json:"connected,omitempty"`
    LastHeartbeat *metav1.Time `json:"lastHeartbeat,omitempty"`
    DownSince     *metav1.Time `json:"downSince,omitempty"` // Used for Degraded→Unready 5min timeout judgment
}

type PoolInfo struct {
    Zone                   string `json:"zone,omitempty"`
    AllocatableMemoryBytes uint64 `json:"allocatableMemoryBytes,omitempty"`
    AllocatedMemoryBytes   uint64 `json:"allocatedMemoryBytes,omitempty"`
}
```

**Naming Rules**: SandboxPool naming format is `placeholder-{node-name}`, e.g. `placeholder-node-1`. Naming is deterministic per node (one Pool name per node), so Kubernetes API create conflict provides atomicity — when two Classes concurrently create a Pool for the same node, they produce the same name; the API Server accepts only the first, returning AlreadyExists for the second. Class ownership is identified via `spec.classRef.name` and the `sandbox-pool.io/class` Label; do not reverse-derive from the Pool name.

> **Long Node Name Truncation**: Kubernetes object names are limited to 253 characters. The `placeholder-` prefix is 12 characters, so the maximum node name length is 241 characters. If the node name exceeds 241 characters, truncate it to 232 characters and append `-` + the first 8 hex characters of SHA256(node-name) as a suffix, yielding a total length of `placeholder-` (12) + 232 + 1 + 8 = 253 ≤ 253. The truncated name remains deterministic per node, preserving create conflict atomicity.

**PrintColumn Configuration**:

```yaml
additionalPrinterColumns:
  - name: Phase
    type: string
    jsonPath: .status.phase
  - name: Node
    type: string
    jsonPath: .spec.nodeName
  - name: Class
    type: string
    jsonPath: .spec.classRef.name
  - name: Override
    type: boolean
    jsonPath: .spec.override.enabled
  - name: Resize
    type: string
    jsonPath: .status.resize.status
  - name: Zone
    type: string
    jsonPath: .status.poolInfo.zone
  - name: Allocatable
    type: integer
    jsonPath: .status.poolInfo.allocatableMemoryBytes
  - name: Allocated
    type: integer
    jsonPath: .status.poolInfo.allocatedMemoryBytes
  - name: Age
    type: date
    jsonPath: .metadata.creationTimestamp
```

#### RuntimeClass

```yaml
apiVersion: node.k8s.io/v1
kind: RuntimeClass
metadata:
  name: placeholder
handler: placeholder
overhead:
  podFixed:
    cpu: 10m
    memory: 16Mi
```

kubelet routes to the placeholder-agent shim via containerd based on the RuntimeClass handler name (`placeholder`, registered in containerd config.toml). The overhead values remain unchanged during VPA resize.

#### OwnerReference and Cascade Relationships

```
SandboxPoolClass
  ├── owns ──► SandboxPool (placeholder-node-1)
  ├── owns ──► SandboxPool (placeholder-node-2)
```

- SandboxPoolClass owns SandboxPool, using the Finalizer mechanism to control deletion ordering.
- The placeholder Pod (mirror pod) is associated with SandboxPool via **Label** (`sandbox-pool.io/pool-name`), not OwnerReference (kubelet does not preserve OwnerReference from the manifest when creating mirror pods).
- Finalizer over Foreground GC was chosen: allows the Controller to execute custom cleanup logic (waiting for placeholder-agent to remove the manifest).

### Phase State Machine

Phase is aggregated and computed by the SandboxPool Controller from Conditions and written to status. placeholder-agent only writes Conditions (reflecting node-local runtime state), not Phase.

```
                ┌───────────┐
                │  Pending  │ ← Initial state; placeholder Pod never ready
                └─────┬─────┘
                      │ PlaceholderPodReady=True + NodeCtlHealthy=True
                      ▼
                ┌───────────┐
 ┌─────────────▼│   Ready   │─────────────────────┐
 │           │  └─────┬─────┘                     │
 │           │        │                           │
 │  node-ctl │        │ Policy not synced         │ node-ctl ≥5min unreachable
 │  partial  │        │                           │ or placeholder Pod abnormal
 │  degrade  │        │                           │ (after was Ready)
 │  (<5min)  │        │                           │ or NodeNotFound=True
 │           └────────│                           │
 │            ┌───────▼───┐                       │
 │            │  Degraded │─── node-ctl ≥5min ────┤
 │            └───────────┘    unreachable        │
 │                                                ▼
 │ node-ctl recovered                       ┌───────────┐
 │ or policy re-synced                      │  Unready  │
 │ → Ready                                  └─────┬─────┘
 └────────────────────────────────────────────────┘
                                          PlaceholderPodReady=True
                                         + NodeCtlHealthy=True → Ready
```

| Phase | Entry Condition | Exit Condition |
|-------|----------------|----------------|
| `Pending` | PlaceholderPodReady=False (never Ready) | PlaceholderPodReady=True + NodeCtlHealthy=True → Ready |
| `Ready` | PlaceholderPodReady=True + NodeCtlHealthy=True + ResourceSynced=True (or ResizeDeferred=True) | NodeCtlHealthy=False → Degraded; NodeNotFound=True → Unready; PlaceholderAgentHealthy=False → Degraded |
| `Degraded` | NodeCtlHealthy=False (< 5min), or ResourceSynced=False (non-Deferred), or PlaceholderAgentHealthy=False (agent unreachable but Pod still shows Ready) | NodeCtlHealthy=True + ResourceSynced=True → Ready; NodeCtlHealthy=False ≥5min → Unready; PlaceholderAgentHealthy=True → re-evaluate all conditions |
| `Unready` | NodeCtlHealthy=False (≥ 5min), or PlaceholderPodReady=False (abnormal after being Ready), or NodeNotFound=True, or PlaceholderAgentHealthy=False + PlaceholderPodReady=False (was Ready) | PlaceholderPodReady=True + NodeCtlHealthy=True → Ready; NodeNotFound=False → re-evaluate all conditions; PlaceholderAgentHealthy=True → re-evaluate all conditions |

**Phase Computation Priority**: NodeNotFound=True → Unready > PlaceholderAgentHealthy=False + PlaceholderPodReady=False (never Ready) → Pending > PlaceholderAgentHealthy=False + PlaceholderPodReady=False (was Ready) → Unready > PlaceholderAgentHealthy=False + PlaceholderPodReady=True → Degraded > PlaceholderPodReady=False (never Ready) → Pending > PlaceholderPodReady=False (was Ready) → Unready > NodeCtlHealthy=False (< 5min) → Degraded > NodeCtlHealthy=False (≥ 5min) → Unready > ResourceSynced=False (non-Deferred) → Degraded > Ready

### Key Scenario Flows

#### Creation Flow

```
User creates SandboxPoolClass
        │
        ▼
SandboxPool Controller
  1. Selector matches target nodes
  2. Create SandboxPool CRD per node (with resourcePolicy snapshot)
  3. Aggregate and update Class Status
        │ Pool CRD created
        ▼
placeholder-agent daemon (systemd service)
  1. Watch detects new SandboxPool CRD (filtered by node label)
  2. Generate and write manifest file → /etc/kubernetes/manifests/sandbox-pool-{pool}.yaml
  3. kubelet detects new manifest → creates Static Pod
  4. kubelet → containerd (CRI) → starts placeholder-agent shim by runtimeClassName
  5. shim.Create (forwarded to daemon.Create) → write config file + restart node-ctl to align
  6. shim.Start → mark container as Running (node-ctl is started by the daemon, not the shim)
  7. On daemon startup, GET SandboxPool spec → write config file + restart node-ctl to align
  8. Periodically GetPoolState → SSA Apply SandboxPool status
  9. Health check endpoint ready
```

#### Update Flow

```
User modifies SandboxPoolClass.Spec (e.g. CPU from 8 to 12)
        │
        ▼
SandboxPool Controller detects Spec change
  → Update all SandboxPool.spec.resourcePolicy (policy snapshot sync)
        │
        ▼
placeholder-agent daemon Watches Pool spec change → update manifest file
        │
        ▼
kubelet detects manifest change → container/Pod rebuild (Static Pods have no InPlace resize capability)
        │
        ▼
containerd starts placeholder-agent shim by runtimeClassName → new resources applied in daemon.Create (forwarded via shim) → scale up/down handling → write config file + restart node-ctl
        │
        ▼
node-ctl receives new resource limit → adjust local cgroup config
```

#### Spec Change Impacts and Safety Guarantees

Impacts and safety guarantees during resource spec changes (SandboxPoolClass/SandboxPool resourcePolicy changes):

| Dimension | Behavior During Change | Safety Guarantee |
|-----------|----------------------|-------------------|
| **Shim call path** | kubelet detects manifest hash change → container/Pod rebuild (containerd starts placeholder-agent shim by runtimeClassName, new resources applied in daemon.Create (forwarded via shim TTRPC); Static Pods do not support in-place resize per KEP-1287, so only the rebuild path is taken) | placeholder-agent daemon (forwarded via shim) applies the new resource policy on the Create path |
| **node-ctl stability** | Scale-driven rebuild does NOT stop node-ctl; only genuine deletion (manifest removal) stops it | The daemon checks if the manifest file still exists when handling Pool deletion, distinguishing scale-driven rebuild from real teardown |
| **Node-level cgroup** | node-ctl keeps running during rebuild, cgroup limits uninterrupted | sandbox resources managed by node-ctl at node-level cgroup, decoupled from Pod `resources.requests` |
| **Scheduler view** | mirror pod is deleted+recreated by kubelet on manifest hash change (NOT patched), brief window in API server | Safety does NOT rely on scheduler view consistency — kubelet admit check uses local podManager (always contains static pod), which is the real safety gate; conflicting new Pods are admit-failed |
| **New static Pod startup** | Created locally by kubelet, does not go through scheduler | Regardless of mirror pod state, the new static Pod starts normally |
| **Downscale watermark protection** | Downscale checks node-ctl watermark; if above risk threshold, deferred to Deferred status | Prevents downscale from causing OOM in running sandboxes; auto-retries when watermark is safe |

> **Staged Resize Ordering** (prevents scheduler-visible requests from falling below applied node-ctl allocation):
>
> | Direction | Ordering | Rationale |
> |-----------|----------|-----------|
> | **Scale-up** | 1. Raise manifest `resources.requests` (kubelet sees higher reservation) → 2. shim.Create forwards to daemon → 3. daemon expands node-ctl allocation | Reserve first so the scheduler/kubelet never sees a gap between the Pod request and the actual node-ctl allocation |
> | **Scale-down (safe)** | 1. shim.Create forwards to daemon → 2. daemon shrinks node-ctl allocation (after watermark check passes) → 3. lower manifest `resources.requests` | Shrink node-ctl first so the old allocation is released before the Pod request drops; the local podManager keeps the old (higher) request until step 3, protecting the gap during the rebuild window |
> | **Scale-down (unsafe)** | 1. Watermark check fails → set `Resize status = Deferred`, retain last applied manifest request → 2. Auto-retry when watermark is safe → 3. Proceed with safe scale-down ordering above | The manifest request is NOT lowered while Deferred; the scheduler-visible request stays at the last applied value, preventing other Pods from being admitted into the gap |
>
> **Invariant**: At no point during resize may the scheduler-visible `resources.requests` fall below the currently applied node-ctl allocation. The resize test suite must assert this invariant by comparing the Pod's `resources.requests` against `status.poolInfo` allocation at each stage.
| **K8s version dependency** | No InPlacePodVerticalScaling Feature Gate dependency, no hard K8s version requirement | Resource adjustment implemented via containerd runtime shim, does not depend on community VPA capability |

> **Terminology note**: "VPA resize" in this design is an analogy describing vertical resource scaling behavior, and does not depend on K8s VPA CRD/Controller or InPlacePodVerticalScaling Feature Gate.

#### Deletion Flow

```
User deletes SandboxPoolClass
        │
        ▼
SandboxPool Controller detects deletion event → cascade delete all SandboxPool CRDs
        │
        ▼
placeholder-agent daemon Watches Pool deletion → delete manifest file
        │
        ▼
kubelet detects manifest removal → automatically stop Static Pod
  → containerd calls shim.Shutdown → shim cleanup; daemon stops node-ctl when Pool is deleted
  → containerd RemovePodSandbox → clean up shim state
        │
        ▼
SandboxPool Controller checks Finalizer
  → isPlaceholderAgentCleanupDone: query whether mirror Pod has been removed
  → mirror Pod removed → remove Finalizer → Pool deletion complete
  → Timeout (> 10min) → force remove Finalizer + Warning Event
        │
        ▼
Placeholder Pod resources unlocked → resources schedulable for regular Pods
```

#### Agent Startup Orphaned Manifest Cleanup

When the agent is unreachable during the Finalizer timeout window (> 10min), the Controller force-removes the Finalizer and the SandboxPool CRD disappears from the API, but the node-local Static Pod manifest file may still remain. When the agent recovers, the Watch does not replay the already-completed deletion event, leaving the manifest orphaned.

**Startup Cleanup Rule**:

```
placeholder-agent daemon startup
   │
   ├── 1. Scan manifest directory (/etc/kubernetes/manifests/) for all sandbox-pool-*.yaml
   │
   ├── 2. For each manifest, extract the associated Pool UID (annotation written into manifest)
   │
   ├── 3. GET SandboxPool CRD by name (extracted from manifest annotation)
   │      → Pool exists and UID matches → keep manifest (normal path)
   │      → Pool NotFound → delete manifest file (Pool was deleted while agent was down)
   │      → Pool exists but UID mismatch → delete manifest file (stale manifest from old Pool)
   │      → Transient/Forbidden/API-unavailable → preserve manifest, retry on next startup
   │
   ├── 4. kubelet detects manifest removal → auto-stops residual static Pod
   │
   └── 5. After cleanup completes, enter normal Watch loop
```

> **Design Constraint**: Cleanup runs only at daemon startup (not continuous polling), avoiding runtime overhead. The `sandbox-pool.io/pool-uid` annotation in the manifest is the authoritative orphan criterion — NotFound or UID mismatch triggers deletion; transient API errors preserve the manifest for retry on next startup, preventing accidental removal of active manifests during temporary API Server unavailability. Kubernetes cannot GET by UID directly; the agent GETs by name (extracted from the `sandbox-pool.io/pool-name` annotation) and then compares the returned object's UID against the `sandbox-pool.io/pool-uid` annotation.

#### Node Startup Sequence

```
Node startup
   │
   ├── 1. placeholder-agent binary pre-installed on host (OS image pre-install or node init script)
   │      also registered as containerd runtime handler (config.toml configuration)
   │
   ├── 2. placeholder-agent daemon auto-starts as a systemd service
   │      Listens on HTTP healthz: :8080/healthz
   │      Watches SandboxPool CRD (filtered by node label)
   │
   ├── 3. containerd finds placeholder-agent shim binary via config.toml runtime handler config
   │
   └── 4. placeholder-agent daemon Watches new SandboxPool CRD
          → writes manifest → kubelet creates Static Pod
          → kubelet → containerd → starts placeholder-agent shim by runtimeClassName
          (no startup circular dependency)
```

#### Placeholder Pod Field Effectiveness Model

```
                           K8s API Server
                           mirror pod read-only object (kubelet auto-maintained)
                           │
          ┌────────────────┼────────────────┐
          ▼                ▼                ▼
  ┌──────────────┐ ┌──────────────┐ ┌──────────────┐
  │ Scheduling   │ │ Identity     │ │ Pure         │
  │ semantics    │ │ semantics    │ │ placeholder  │
  │ (effective)  │ │ (query)      │ │ (validation) │
  ├──────────────┤ ├──────────────┤ ├──────────────┤
  │ resources    │ │ labels       │ │ image        │
  │ nodeName     │ │ annotations  │ │ command      │
  │ tolerations  │ │ name         │ │ env/ports    │
  │ priority     │ └──────────────┘ │ probes       │
  │ runtimeClass │                  │ securityCtx  │
  └──────┬───────┘                  │ volumes      │
         ▼                          └──────┬───────┘
  K8s scheduler                             │ actual behavior
  resource locking                          ▼ determined by host node config
```

### Condition Definitions

| Condition Type | Writer | Status=True Meaning | Status=False Meaning |
|---------------|--------|---------------------|---------------------|
| `PlaceholderPodReady` | placeholder-agent | Placeholder Pod Running (shim task started, i.e., `Start` has returned) | Placeholder Pod not ready |
| `ResourceSynced` | placeholder-agent | Resource policy synced to node-ctl | Unsynced policy changes exist |
| `NodeCtlHealthy` | placeholder-agent | node-ctl reachable and responding normally | node-ctl unreachable or abnormal response |
| `ResizeInProgress` | placeholder-agent | Scale-up or scale-down in progress | No resize in progress |
| `ResizeDeferred` | placeholder-agent | Downscale deferred due to high watermark | No deferred downscale operation |
| `PlaceholderAgentHealthy` | sandboxpool-controller | Agent Lease active (renewed within TTL, default 40s) | Agent never reported / Lease expired (TTL > 40s with no renewal, agent crashed but Node exists) |
| `NodeNotFound` | sandboxpool-controller | Node does not exist; Pool is orphaned | Node exists |

### Label and Annotation System

| Key | Applicable Resources | Description |
|-----|---------------------|-------------|
| `sandbox-pool.io/class` | SandboxPool, Pod | Associated Class name |
| `sandbox-pool.io/node` | SandboxPool, Pod | Target node name |
| `sandbox-pool.io/pool-name` | Pod | Associated Pool name (written to both Label and Annotation) |
| `sandbox-pool.io/pool-uid` | Pod | Associated Pool UID (Annotation only, written into manifest); used by agent at startup for orphaned manifest detection — manifests with non-matching UID are removed |
| `sandbox-pool.io/skip-cgroup` | Pod | Hint to skip cgroup creation (written to both Label and Annotation); requires placeholder shim support, not default K8s behavior |
| `sandbox-pool.io/manifest-hash` | Pod | manifest SHA256 hash (Annotation only) |

### RBAC Permission Model

| Role | Permission Scope | Description |
|------|-----------------|-------------|
| `sandbox-pool-controller` | ClusterRole | sandboxpoolclasses (get/list/watch), sandboxpools (CRUD+status), nodes (get/list/watch), pods (get/list), events (create/patch), leases (full) |
| `sandbox-pool-placeholder-agent` | ClusterRole | sandboxpools (get/list/watch), sandboxpools/status (patch), endpoints (full), events (create/patch), leases (create/get/update) |
| `sandbox-pool-admin` | ClusterRole | CRUD SandboxPoolClass + SandboxPool |
| `sandbox-pool-viewer` | ClusterRole | Get/List/Watch |
| `sandbox-pool-operator` | ClusterRole | Update SandboxPool (override operations) |

> **Security Boundary — Per-Node Status Write Scoping**: The `sandbox-pool-placeholder-agent` ClusterRole grants `sandboxpools/status (patch)` cluster-wide, which technically allows any node's agent to patch any Pool's status. To prevent a compromised node from forging another node's pool health, the ValidatingWebhook enforces a server-side write boundary: on a SandboxPool STATUS UPDATE, the webhook inspects the authenticated request identity (`userInfo.username` / `userInfo.groups` from the admission review) and rejects the request with `Forbidden` unless the caller is bound to `spec.nodeName`. Concretely, each placeholder-agent authenticates as a per-node identity — either a node-scoped client cert yielding `system:node:<nodeName>` in group `system:nodes`, or a dedicated per-node ServiceAccount whose name encodes the node name. The webhook extracts `<nodeName>` from the authenticated identity and compares it against `spec.nodeName`; only the agent running on that node may patch that Pool's status. This makes the client-side `spec.nodeName` watch filter a server-enforced invariant rather than a convention.

### Validating Webhook

The ValidatingWebhook enforces the field-level validation rules documented in the API type definitions above (see `SandboxPoolClassSpec` and `SandboxPoolSpec` field comments). Key invariants: `spec.selector` / `spec.nodeName` / `spec.classRef.name` immutability; `override.resourcePolicy` and `override.reason` required when `override.enabled=true`; single-Class-per-node enforced via four-layer protection below.

> **Four-Layer Protection (Single-Class-per-Node Invariant)**:
>
> | Layer | Mechanism | Atomicity | Description |
> |-------|-----------|-----------|-------------|
> | 1. Deterministic naming | Pool name = `placeholder-{node-name}` (truncated+hashed for long node names, see Naming Rules) | ✅ Kubernetes API create conflict | Two Classes concurrently creating a Pool for the same node → same name → API Server accepts only the first, returns AlreadyExists for the second → losing Class GETs the existing Pool; if `spec.classRef.name` differs, emits a `NodeConflict` Warning Event (identifying the winning Class) and surfaces the conflict in Class `status.conditions`; if classRef matches (idempotent retry), returns nil |
> | 2. Validating Webhook | Rejects CREATE if a Pool with a different Class exists for the same node | ❌ TOCTOU (non-atomic) | Defense-in-depth layer; Webhook list+check has a TOCTOU race and cannot be the sole guarantee |
> | 3. Controller `checkNodeConflict` fast-fail | `createSandboxPool` checks whether a Pool from a different Class exists on the same node before Create | N/A | If a conflict is found, the losing Class skips Create and logs a `NodeConflict` Warning Event (first-come, no deletion of the existing Pool) |
> | 4. placeholder-agent isolation | Only watches and serves the Pool matching its own node | N/A | Agent is unaffected by non-owning Class Pools |
>
> Layer 1 is the core atomic guarantee — leveraging Kubernetes API Server's create conflict semantics, with no need for additional lock objects or Node annotations. A concurrent-create integration test verifies this invariant.

### K8s Version Compatibility

| Feature | Minimum K8s Version | Dependencies |
|---------|-------------------|--------------|
| Static Pod | 1.0 | None |
| mirror Pod auto-rebuild | 1.0 | None |
| ValidatingWebhook | 1.16 GA | None |
| CRD v1 | 1.16 GA | None |
| SSA Apply (fieldManager) | 1.18 GA | None |

## Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Phase update delay (≤30s) | Delay between Conditions change and Phase update | Alert rule sets `for ≥ 1m` buffer to avoid flapping |
| Conditions stuck at stale values when placeholder-agent unreachable | Phase may not reflect latest state | Controller detects agent heartbeat staleness via `PlaceholderAgentHealthy` Condition (expired Lease (TTL > 40s)), downgrading Phase to Degraded/Unready; NodeNotFound covers node deletion scenario |
| SSA Apply concurrent conflict | Both sides SSA Apply status simultaneously | SSA field manager field-level non-overwrite; conflict auto-retried on next Reconcile/reportStatus |
| selector immutability causes operational inconvenience | Adjusting node range requires deleting and recreating Class | Alternative approaches provided: taint+toleration, remove node Label |
| EverReady Condition precise tracking | Already implemented via sticky `ConditionEverReady` (set True on first Ready, never reset) | Implemented, no additional field needed |

## Test Plan

- **Unit tests**: Phase computation logic (computePhase), Conditions aggregation, SSA field manager conflict handling.
- **Integration tests**: Controller Reconcile full flow (Class→Pool create/delete/policy sync).
- **Concurrent create test**: Two Classes selecting the same node simultaneously; verify deterministic naming + Kubernetes create conflict allows only one Pool creation, losing Class GETs the existing Pool, detects classRef mismatch, emits `NodeConflict` Warning Event and surfaces conflict in Class status (Layer 1); pre-create `checkNodeConflict` logs `NodeConflict` Warning Event on the losing Class (Layer 3).
- **E2E tests**: Placeholder Pod lifecycle (create/resize/delete/mirror pod rebuild).
- **Fault injection tests**: node-ctl unreachable, API Server disconnect, node deletion, agent restart.
- **Heartbeat timeout fault injection**: Agent stops renewing its Lease; verify the Controller detects Lease TTL expiry (default 40s) and downgrades `PlaceholderAgentHealthy` to False and Phase to Degraded/Unready.
- **VPA resize tests**: Scale-up/scale-down/Deferred scenarios.

## Alternatives

| Alternative | Reason Not Chosen |
|------------|-------------------|
| namespace-scoped CRD | placeholder-agent is a node-local singleton (one per node) bound to one Pool; no namespace isolation needed |
| placeholder-agent computes Phase | When agent is unreachable, it cannot update Phase, leading to the contradiction of NodeNotFound=True but Phase=Ready |
| Redis external storage | Resource pool control plane only needs CRD state; introducing external storage increases operational complexity |
| API Pod model (non-Static Pod) | After deletion, must wait for Controller to rebuild, creating a resource locking risk window |
| Fast path (Override scale-up dual-path trigger) | Over-engineering; manifest standard path latency is acceptable (≤30s) |

## Implementation Plan

Phased implementation:

1. **Phase 1**: CRD definition + Controller basic Reconcile (Class→Pool mapping, Finalizer, policy snapshot sync).
2. **Phase 2**: placeholder-agent daemon + containerd shim (manifest management + shim v2 Task API synthetic implementation + Conditions reporting + minimal shim spike/e2e verifying create/start/stop/wait/reconnect).
3. **Phase 3**: Resource vertical scaling (VPA resize) (resource adjustment in daemon.Create (forwarded via shim) + watermark protection + Deferred mechanism).
4. **Phase 4**: Phase aggregation computation + NodeNotFound detection + failure recovery.
5. **Phase 5**: Observability (Metrics + alert rules) + security hardening (Webhook + RBAC).

### v1alpha1 Scope

v1alpha1 includes only core functionality: Class→Pool mapping, policy snapshot sync, Static Pod management, VPA resize, Phase aggregation computation. The following features are Alpha-level and disabled by default:

| Alpha Feature | Startup Parameter | Description |
|---------------|-------------------|-------------|
| Network Namespace | `--netns-enabled` | Creates an independent network namespace for the placeholder Pod, providing IP isolation |
