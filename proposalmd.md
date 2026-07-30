LFX Mentorship 2026 — Term 2
----------------------------

### Support multi-AgentCube Capability

**CNCF Volcano / AgentCube | Aadi Shah | AAdIprog | IST (UTC+5:30)**[aadishah132@gmail.com](mailto:aadishah132@gmail.com) | 30–40 hrs/week | Mentor: @hzxuzhonghu

### Preliminary Questions

#### Q1. How did you find out about this program?

Through the LFX Mentorship portal and by following the Volcano scheduling community. I have been tracking the agentcube repo since the parent proposal volcano#4686 was filed and was excited when the multi-agent issue landed because the gap is real: every multi-agent application today rebuilds the same orchestration layer in user code.

#### Q2. Why are you interested in this program?

Two reasons. First, multi-agent collaboration is the layer where the cloud-native ecosystem is currently weakest, and AgentCube is one of the CNCF projects positioned to set the convention. Getting it right matters for the whole community, not just AgentCube. Second, the existing single-agent code in pkg/workloadmanager/handlers.go is already well-factored — atomic create with deferred rollback, watch-before-create to avoid races — which means the multi-agent layer can be designed as a composition over that primitive rather than a rewrite. That is the right kind of project for a 12-week mentorship.

#### Q3. What experience and knowledge do you have that is applicable?

Production Go on Kubernetes controllers (controller-runtime, kubebuilder, client-go informers, JSON-patch). Real exposure to CRD design including versioning and OpenAPI schema generation. Familiarity with the wider agent ecosystem (LangGraph, MCP, A2A) so the design fits how operators actually build multi-agent applications today. I have read all 1011 lines of the existing handlers and helpers; the file/line citations in Sections 4–6 are ones I personally opened and verified.

Open-source contributions demonstrating this background:

Repository / PRDescriptionKubeStellar #648Japanese i18n translations; eliminated externally hardcoded strings across user-facing elements to improve maintainability.KubeStellar #623Italian translations across components; removed hardcoded strings from external sources for better global accessibility.OpenKruise #2303Fixed livenessProbe handling in the manager's YAML processing; improved pod lifecycle reliability with Kubernetes probes.OpenKruise #2302Resolved stuck deployments caused by image-tag updates where the digest was unchanged, ensuring correct rollout execution.OpenKruise #2313Replaced static TODO placeholders in the CR controller with dynamic logic for reliable container-image propagation.Volcano #442Updated the Volcano README with bug fixes, new sections on project structure and contribution guidelines, refined setup instructions.Volcano #692Enabled AI/ML model (v7.8) support in the Kthena router for open-source LLM integration; new server configs, traffic routing examples, and local testing instructions.Volcano #695Implemented E2E tests covering the full ModelServing lifecycle (create/update/delete), validating multi-phase workflows and timeouts.

#### Q4. What do you hope to get out of this mentorship experience?

A merged MultiAgentRuntime CRD plus the full control-plane and routing wiring that operators can use to declare a pcap-analyzer-style topology declaratively instead of writing it in FastAPI code. Past 12 weeks, I want to become a regular AgentCube contributor with maintainer-trust on the workload-manager package.

### 1\. Title & Metadata

FieldValueProjectCNCF Volcano / AgentCubeMentorshipLFX 2026 Term 2 (Jun – Aug 2026)TitleSupport multi-AgentCube CapabilityTracking Issuevolcano-sh/agentcube#301Parent Designvolcano-sh/volcano#4686Target BranchmainApplicantAadi ShahGitHubAAdIprogEmail[aadishah132@gmail.com](mailto:aadishah132@gmail.com)TimezoneIST (UTC+5:30)Weekly Hours30–40 hrs/week (weeks 8 and 11 used as buffer)Mentor@hzxuzhonghu (Zhonghu Xu)Proposal Versionv1.0Date2026-05-16

### 2\. Abstract

AgentCube today launches exactly one sandbox per invocation: an AgentRuntime or CodeInterpreter create call in pkg/workloadmanager/handlers.go:86–176 produces a single sandbox, a single session ID, and a single set of entry points. Multi-agent applications — the example/pcap-analyzer/ planner-runner-reporter pipeline already shipped in-tree being the canonical example — paper over this by re-implementing orchestration, dependency wiring, and cleanup logic inside the user's own application code.

This project adds a **MultiAgentRuntime** CRD and a createSandboxGroup control-plane operation that composes the existing single-sandbox primitive transactionally, persists a group manifest to the existing store, exposes a coordinator-scoped session through the existing router, and stands up a per-group headless Kubernetes Service so workers can address each other by DNS without ever touching the external router.

Four reviewable PRs land in weeks 3, 6, 8, and 10. The deliverable is a declarative way to say "give me a planner, a researcher, and a coder that share one session" and get back a working group with one HTTP call.

### 3\. Background & Why This Work Matters

The relevant subsystem is the workload manager package (pkg/workloadmanager/) and the two CRDs it serves: AgentRuntime and CodeInterpreter. Each CRD describes a single sandbox template; each invocation creates exactly one sandbox via the agent-sandbox Sandbox resource (sigs.k8s.io/agent-sandbox v0.1.1).

The specific gap: there is no CRD that says "compose N of these into a unit, start them in a specified order, share their lifecycle, and give the caller one session handle." The closest pre-existing thing is example/pcap-analyzer/pcap\_analyzer.py, where the user hand-codes a Planner-Runner-Reporter pipeline using LangGraph plus three separate CodeInterpreterClient calls, manually wires together the WORKLOAD\_MANAGER\_URL and ROUTER\_URL (deployment.yaml:50–58), and manually tears the sandboxes down. Every multi-agent operator does the same dance.

The parent design doc commits AgentCube to "treating agents like serverless agents rather than just another pod." A platform that serverlessly orchestrates one agent but punts on multiple is incomplete. Getting the multi-agent CRD design right unblocks the pcap-analyzer and browser-agent examples from being one-offs, and creates the pattern future projects — RAG pipelines, planner-executor-critic loops — can adopt without re-implementing orchestration.

### 4\. Current Architecture Analysis

#### How a single sandbox is created today

The handleSandboxCreate function (handlers.go:86–176) is atomic by construction: a deferred rollbackSandboxCreation (line 215) is registered before the K8s create call and runs in a fresh background context (line 290) if anything fails downstream, so a placeholder cannot orphan and a created K8s resource cannot leak past a client disconnect.

#### Code-level findings

No multi-agent abstraction exists. The Kind constants in pkg/common/types/types.go:19–27 list only AgentRuntimeKind, CodeInterpreterKind, SandboxKind, SandboxClaimsKind — no group, team, workflow, or workgroup.

The store keys everything by SessionID. pkg/store/interface.go has 9 methods, all single-session. The two backends (store\_redis.go, store\_valkey.go) each implement them and have parallel test suites. Group state requires extending the interface and implementing on both backends in parallel.

Inter-pod network is not configured. The workload manager creates a Sandbox CR (and optionally a SandboxClaim); the agent-sandbox controller materializes the Pod. No Kubernetes Service is ever created today. For two agents in the same group to address each other by name, the workload manager must additionally create a headless Service.

The pcap-analyzer example proves the demand and the shape of the solution. example/pcap-analyzer/pcap\_analyzer.py already orchestrates three agents (Planner, SandboxRunner, Reporter) using LangGraph; the README's "self-healing forensic pipeline" sells the exact value the multi-agent CRD would deliver out-of-band of any specific framework.

#### Pain points

Pain PointWhere in CodeUser must manually orchestrate 3 separate sandbox lifecyclespcap\_analyzer.py (whole file)Inter-agent endpoints must be hand-discovered (env vars or hardcoded)pcap-analyzer/deployment.yaml:50–58Failure in one agent leaves siblings running — no cleanupNot implemented anywhereNo shared session: each agent gets its own x-agentcube-session-idhandlers.go:266–271No group-level GC: sandboxes survive until idle timeout if user code crashesgarbage\_collection.go operates per-sandbox

### 5\. Proposed Solution

#### High-level approach

Add one CRD (MultiAgentRuntime), one control-plane operation (createSandboxGroup) that composes the existing transactional createSandbox primitive, four new HTTP routes, three new store methods (added to both backends), one optional reconciler for self-healing (week 11), and one new K8s resource per group (a headless Service for in-group DNS). The router data path is unchanged; the external session ID still maps to a single sandbox — just now the coordinator's.

#### Key design decisions

**Decision 1:** MultiAgentRuntime references existing AgentRuntime CRDs by name; it does not inline pod specs. A runtimeRef: field per role means an operator defines security-hardened roles once and composes them N ways. The workload manager already does this kind of informer lookup in buildSandboxByAgentRuntime (line 247), so the pattern is established.

**Decision 2:** A single headless Kubernetes Service per group, no router-mediated inter-agent traffic. K8s headless Services give pod-restart-safe DNS names (...svc.cluster.local) and require zero changes to the router. The Service is owned by the MultiAgentRuntime CR via ownerReferences, so K8s garbage-collects it automatically when the group is deleted.

**Decision 3:** Externally-visible session is the coordinator's session. The router needs no new routing logic. A MultiAgentRuntime invocation returns the coordinator's session ID; the coordinator dispatches to its workers via in-cluster DNS.

**Decision 4:** Atomic-by-default startup, with Sequential and BestEffort as named alternatives. Atomic means any role failure rolls back every sibling, mirroring the existing single-sandbox atomicity contract. The proposal commits to atomicity as the default because surprising rollbacks beat surprising orphans.

**Decision 5:** Restart policy lives at the role level, not the group level. A coordinator that crashes is materially different from a worker that crashes. Default is Never; self-healing requires explicit opt-in.

#### Alternatives considered and rejected

AlternativeWhy RejectedNew top-level kind that inlines pod specsTwo sources of truth for security/resource config; doubles maintenanceReuse AgentRuntime with a new composition fieldBackward-compat hazard; CRD spec growth is one-wayRoute inter-agent traffic through the external routerHop for no benefit; couples internal traffic to public-facing rate limitsEmbed A2A as defaultA2A is a young protocol (2025); locking AgentCube in before community settles is prematureLangGraph DSL as the spec formatCouples K8s-native CRD to a specific framework; not portable

#### Backward compatibility

Zero changes to AgentRuntime or CodeInterpreter CRDs. The store interface grows (SaveAgentGroup, GetAgentGroup, DeleteAgentGroup); existing methods are untouched. Single-sandbox creation paths remain the canonical fast path. Operators on older AgentCube versions see no MultiAgentRuntime resource and lose nothing.

#### Observability hooks

SignalWhereWhyagentcube\_group\_create\_duration\_seconds (histogram)createSandboxGroup start/finishTrack multi-vs-single-sandbox latencyagentcube\_group\_active{namespace} (gauge)Controller reconcile loopCapacity planning, quotaagentcube\_group\_role\_failures\_total{role, reason} (counter)Rollback pathDiagnose which roles fail mostPer-role K8s Event on MultiAgentRuntimeControllerOperator can correlate logs across pods

#### Edge cases and failure modes

CaseHandlingReferenced AgentRuntime does not exist404 with the missing name; no rollback neededCycle in dependencies\[\]422 with the cycle path in the error messagecoordinator: true on zero or >1 roles422 from admission webhookCoordinator boots, worker fails (Atomic)All sandboxes rolled back; group manifest deletedCoordinator boots, worker fails (BestEffort)Group marked Degraded; failed role recorded in statusSame group requested twice with same nameIdempotent on the CRD; 409 if a call is in-flightGroup sandbox crashes mid-sessionHonors role restartPolicy; default Never means status flips to Degraded

### 6\. Technical Implementation Plan

#### 6.1 The CRD

Path: pkg/apis/runtime/v1alpha1/multiagentruntime\_types.go (new)

Key fields:

*   Roles \[\]AgentRoleSpec — MinItems=2, MaxItems=16; each role has Name, RuntimeRef, Coordinator, Dependencies, Replicas (max 8), RestartPolicy, EnvOverrides
    
*   GroupLifecycle.StartupPolicy — Atomic (default) | Sequential | BestEffort
    
*   GroupCommunication.Type — ServiceDNS (default) | A2A
    
*   MultiAgentRuntimeStatus — Phase, ActiveRoles, CoordinatorSessionID, RoleStatuses, Conditions
    

go

Plain textANTLR4BashCC#CSSCoffeeScriptCMakeDartDjangoDockerEJSErlangGitGoGraphQLGroovyHTMLJavaJavaScriptJSONJSXKotlinLaTeXLessLuaMakefileMarkdownMATLABMarkupObjective-CPerlPHPPowerShell.propertiesProtocol BuffersPythonRRubySass (Sass)Sass (Scss)SchemeSQLShellSwiftSVGTSXTypeScriptWebAssemblyYAMLXML``   // +kubebuilder:object:root=true  // +kubebuilder:subresource:status  // +kubebuilder:resource:scope=Namespaced  type MultiAgentRuntime struct {      metav1.TypeMeta   `json:",inline"`      metav1.ObjectMeta `json:"metadata,omitempty"`      Spec   MultiAgentRuntimeSpec   `json:"spec"`      Status MultiAgentRuntimeStatus `json:"status,omitempty"`  }  type MultiAgentRuntimeSpec struct {      // +kubebuilder:validation:MinItems=2      // +kubebuilder:validation:MaxItems=16      Roles         []AgentRoleSpec    `json:"roles"`      Lifecycle     GroupLifecycle     `json:"lifecycle,omitempty"`      // +kubebuilder:default={type: ServiceDNS}      Communication GroupCommunication `json:"communication,omitempty"`      // +kubebuilder:default="15m"      SessionTimeout     *metav1.Duration `json:"sessionTimeout,omitempty"`      // +kubebuilder:default="8h"      MaxSessionDuration *metav1.Duration `json:"maxSessionDuration,omitempty"`  }  type AgentRoleSpec struct {      // +kubebuilder:validation:Pattern="^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"      Name        string           `json:"name"`      RuntimeRef  string           `json:"runtimeRef"`      Coordinator bool             `json:"coordinator,omitempty"`      Dependencies []string        `json:"dependencies,omitempty"`      // +kubebuilder:default=1      // +kubebuilder:validation:Maximum=8      Replicas      *int32         `json:"replicas,omitempty"`      // +kubebuilder:default="Never"      // +kubebuilder:validation:Enum=Never;OnFailure      RestartPolicy RolePolicy     `json:"restartPolicy,omitempty"`      EnvOverrides  []corev1.EnvVar `json:"envOverrides,omitempty"`  }   ``

#### 6.2 The control-plane composition function

Path: pkg/workloadmanager/multiagent.go (new)

createSandboxGroup composes the existing single-sandbox primitive transactionally. Key steps:

1.  topoSort(roles) to determine startup order; returns cycle path on error
    
2.  Create headless Service owned by the MultiAgentRuntime CR (auto-GC on delete)
    
3.  For each role in topo order: annotate sandbox with group/role labels, set subdomain + hostname, inject env vars, call existing createSandbox
    
4.  On any failure (Atomic policy): reverse-order rollback via existing rollbackSandboxCreation, delete Service
    
5.  SaveAgentGroup to store; return CoordinatorSessionID
    

go

Plain textANTLR4BashCC#CSSCoffeeScriptCMakeDartDjangoDockerEJSErlangGitGoGraphQLGroovyHTMLJavaJavaScriptJSONJSXKotlinLaTeXLessLuaMakefileMarkdownMATLABMarkupObjective-CPerlPHPPowerShell.propertiesProtocol BuffersPythonRRubySass (Sass)Sass (Scss)SchemeSQLShellSwiftSVGTSXTypeScriptWebAssemblyYAMLXML`   func (s *Server) createSandboxGroup(      ctx context.Context,      dyn dynamic.Interface,      mar *runtimev1alpha1.MultiAgentRuntime,  ) (*types.CreateGroupResponse, error) {      ordered, err := topoSort(mar.Spec.Roles)      if err != nil { return nil, err }      groupSessionID := "grp-" + uuid.New().String()      created := make([]createdRole, 0, len(ordered))      needRollback := true      defer func() {          if !needRollback { return }          rollbackCtx, cancel := context.WithTimeout(              context.Background(), groupRollbackTimeout)          defer cancel()          for i := len(created) - 1; i >= 0; i-- {              s.rollbackSandboxCreation(                  dyn, created[i].sandbox, nil, created[i].sessionID)          }          _ = s.k8sClient.DeleteHeadlessService(rollbackCtx, mar.Namespace, groupSessionID)      }()      svc, err := s.k8sClient.CreateHeadlessService(ctx, mar, groupSessionID)      if err != nil { return nil, err }      var coordinator *createdRole      for _, role := range ordered {          sandbox, entry, err := buildSandboxByAgentRuntime(              mar.Namespace, role.RuntimeRef, s.informers)          if err != nil { return nil, err }          sandbox.Labels[GroupLabel] = groupSessionID          sandbox.Labels[RoleLabel]  = role.Name          sandbox.Spec.PodTemplate.Spec.Subdomain = groupSessionID          sandbox.Spec.PodTemplate.Spec.Hostname  = role.Name          injectGroupEnvVars(&sandbox.Spec.PodTemplate, role, ordered, mar)          resultChan := s.sandboxController.WatchSandboxOnce(ctx, sandbox.Namespace, sandbox.Name)          resp, err := s.createSandbox(ctx, dyn, sandbox, nil, entry, resultChan)          s.sandboxController.UnWatchSandbox(sandbox.Namespace, sandbox.Name)          if err != nil {              if mar.Spec.Lifecycle.StartupPolicy == BestEffort && !role.Coordinator {                  appendDegradedRole(mar, role.Name, err)                  continue              }              return nil, fmt.Errorf("role %s: %w", role.Name, err)          }          cr := createdRole{name: role.Name, sandbox: sandbox,              sessionID: resp.SessionID, resp: resp}          created = append(created, cr)          if role.Coordinator { coordinator = &cr }      }      if coordinator == nil {          return nil, errors.New("group has no coordinator role")      }      if err := s.storeClient.SaveAgentGroup(ctx, groupSessionID,          buildGroupManifest(mar, created, svc, coordinator)); err != nil {          return nil, err      }      needRollback = false      return &types.CreateGroupResponse{          GroupSessionID:       groupSessionID,          CoordinatorSessionID: coordinator.sessionID,          ServiceName:          svc.Name,          Roles:                buildRoleSummaries(created),      }, nil  }   `

#### 6.3 Store extensions

Three new methods on pkg/store/interface.go, implemented in parallel on store\_redis.go and store\_valkey.go:

go

Plain textANTLR4BashCC#CSSCoffeeScriptCMakeDartDjangoDockerEJSErlangGitGoGraphQLGroovyHTMLJavaJavaScriptJSONJSXKotlinLaTeXLessLuaMakefileMarkdownMATLABMarkupObjective-CPerlPHPPowerShell.propertiesProtocol BuffersPythonRRubySass (Sass)Sass (Scss)SchemeSQLShellSwiftSVGTSXTypeScriptWebAssemblyYAMLXML`   SaveAgentGroup(ctx context.Context, manifest *types.AgentGroupManifest) error  GetAgentGroup(ctx context.Context, groupSessionID string) (*types.AgentGroupManifest, error)  DeleteAgentGroup(ctx context.Context, groupSessionID string) error   `

Storage layout: group:{grp-xxx} HASH (manifest JSON) + group:active ZSET (for GC scan).

#### 6.4 New HTTP routes

go

Plain textANTLR4BashCC#CSSCoffeeScriptCMakeDartDjangoDockerEJSErlangGitGoGraphQLGroovyHTMLJavaJavaScriptJSONJSXKotlinLaTeXLessLuaMakefileMarkdownMATLABMarkupObjective-CPerlPHPPowerShell.propertiesProtocol BuffersPythonRRubySass (Sass)Sass (Scss)SchemeSQLShellSwiftSVGTSXTypeScriptWebAssemblyYAMLXML`   v1Group.POST("/multi-agent-runtime", s.handleMultiAgentRuntimeCreate)  v1Group.DELETE("/multi-agent-runtime/groups/:groupId", s.handleDeleteAgentGroup)  v1Group.GET("/multi-agent-runtime/groups/:groupId/topology", s.handleGetGroupTopology)   `

The router does not gain new routes. One switch-case addition in session\_manager.go:117–126 routes MultiAgentRuntimeKind create calls to the new endpoint.

#### 6.5 Headless Service per group

CreateHeadlessService in k8s\_client.go (~30 LOC): ClusterIP: None, selector {group: groupSessionID}, OwnerReferences pointing to the MultiAgentRuntime CR. Deleting the CR cascades to the Service via K8s GC. Manual cleanup in the rollback path is only needed if the CR itself was never created.

go

Plain textANTLR4BashCC#CSSCoffeeScriptCMakeDartDjangoDockerEJSErlangGitGoGraphQLGroovyHTMLJavaJavaScriptJSONJSXKotlinLaTeXLessLuaMakefileMarkdownMATLABMarkupObjective-CPerlPHPPowerShell.propertiesProtocol BuffersPythonRRubySass (Sass)Sass (Scss)SchemeSQLShellSwiftSVGTSXTypeScriptWebAssemblyYAMLXML`   func (k *K8sClient) CreateHeadlessService(      ctx context.Context,      mar *runtimev1alpha1.MultiAgentRuntime,      groupSessionID string,  ) (*corev1.Service, error) {      svc := &corev1.Service{          ObjectMeta: metav1.ObjectMeta{              Name:      groupSessionID,              Namespace: mar.Namespace,              OwnerReferences: []metav1.OwnerReference{{                  APIVersion:         mar.APIVersion,                  Kind:               mar.Kind,                  Name:               mar.Name,                  UID:                mar.UID,                  Controller:         ptr.To(true),                  BlockOwnerDeletion: ptr.To(true),              }},              Labels: map[string]string{GroupLabel: groupSessionID},          },          Spec: corev1.ServiceSpec{              ClusterIP: corev1.ClusterIPNone,              Selector:  map[string]string{GroupLabel: groupSessionID},              PublishNotReadyAddresses: false,          },      }      return k.clientset.CoreV1().Services(mar.Namespace).Create(          ctx, svc, metav1.CreateOptions{})  }   `

#### 6.6 The reconciler (optional, week 11)

Path: pkg/workloadmanager/multiagent\_controller.go (new, ~150 LOC). Standard controller-runtime pattern mirroring codeinterpreter\_controller.go. Watches MultiAgentRuntime, reconciles Status (counts roles, sets Phase, emits Events), enforces restartPolicy by deleting + recreating individual role sandboxes. Gated behind AGENTCUBE\_GROUP\_SELF\_HEAL=true env var so the rest of the PRs can land independently.

#### 6.7 Files to modify / create

PathChangepkg/apis/runtime/v1alpha1/multiagentruntime\_types.goNEW — CRD type definitionpkg/apis/runtime/v1alpha1/register.goMODIFIED — MultiAgentRuntimeKind + GVK blockmanifests/charts/base/crds/runtime.agentcube.volcano.sh\_multiagentruntimes.yamlNEW — generated by make gen-crdpkg/common/types/types.goMODIFIED — MultiAgentRuntimeKind constantpkg/common/types/group.goNEW — AgentGroupManifest, request/response DTOspkg/workloadmanager/multiagent.goNEW — createSandboxGrouppkg/workloadmanager/multiagent\_controller.goNEW (phase 4) — reconcilerpkg/workloadmanager/k8s\_client.goMODIFIED — CreateHeadlessService, DeleteHeadlessServicepkg/workloadmanager/server.goMODIFIED — three new routespkg/workloadmanager/handlers.goMODIFIED — handleMultiAgentRuntimeCreate, handleDeleteAgentGroup, handleGetGroupTopologypkg/store/interface.goMODIFIED — three new methodspkg/store/store\_redis.go / store\_valkey.goMODIFIED — implement three methods + testspkg/router/session\_manager.goMODIFIED — switch-case for MultiAgentRuntimeKindsdk-python/agentcube/multi\_agent.pyNEW — MultiAgentRuntimeClientexample/multi-agent-pcap/NEW — pcap-analyzer rewritten as MultiAgentRuntimedocs/design/multi-agent-design.mdNEW — design docdocs/devguide/multi-agent.mdNEW — user guide with NetworkPolicy exampletest/e2e/e2e\_test.goMODIFIED — TestMultiAgentCreate, TestMultiAgentRollback, TestMultiAgentBestEffort

### 7\. 12-Week Timeline

WeekDatesPhaseKey Deliverable1Jun 2–8BondingDesign doc Discussion published; draft PR open with file layout; notes posted to #3012Jun 9–15Bonding→CorePR 1 merged: CRD-only (multiagentruntime\_types.go, codegen, Kind constants)3Jun 16–22CoreStore extensions (SaveAgentGroup, GetAgentGroup, DeleteAgentGroup) on Redis + Valkey; AgentGroupManifest type4Jun 23–29CorePR 2 opened: createSandboxGroup (Atomic only), 3 new HTTP routes, session\_manager switch-case5Jun 30–Jul 6CoreCreateHeadlessService wired; DNS verified end-to-end; PR 2 merged6Jul 7–13TopologyPR 3: topoSort + cycle detection, dependency env-var injection, handleGetGroupTopology7Jul 14–20Topologypcap-analyzer rewritten as MultiAgentRuntime (example/multi-agent-pcap/); E2E added8Jul 21–27BufferBuffer / mid-project review; load-test (100 groups × 3 roles) if no slippage9Jul 28–Aug 3PolishBestEffort startup policy; admission webhook (coordinator uniqueness, runtimeRef, cycle, role-name)10Aug 4–10PolishPR 4 opened: BestEffort + webhook + Python SDK (MultiAgentRuntimeClient) + docs11Aug 11–17PolishMultiAgentRuntimeReconciler (feature-flagged); Prometheus metrics wired; PR 4 review iterations12Aug 18–24PolishPR 4 merged; blog post draft; LFX final report; issue #301 closed with PR links

### 8\. Testing & Validation Strategy

#### Unit tests

*   topoSort: 6 cases including empty, no-deps, linear, diamond, cycle (cycle error includes path)
    
*   createSandboxGroup Atomic: mock client; succeed N-1 sandboxes then fail → all N rolled back, store empty, Service deleted
    
*   createSandboxGroup BestEffort: fail one non-coordinator → group succeeds, failed role recorded in manifest
    
*   Store methods on mini-redis + valkey-mock: round-trip equality, idempotent double-delete
    
*   Admission webhook: 8 spec cases (valid + 7 failure modes), each returning the right reason
    

#### Integration tests (kind cluster)

*   Create 3-role group; verify all sandboxes Ready, Service exists with 3 endpoints, coordinator gets external session ID, delete cascades
    
*   Worker failure with Atomic policy → full rollback
    
*   Worker failure with BestEffort policy → group becomes Degraded
    
*   Dependency cycle → 422 with cycle path
    
*   Two simultaneous groups in same namespace → no Service-name collision
    

#### E2E test

example/multi-agent-pcap/ rewrite (week 7): submit a real PCAP file, observe planner → runner → reporter chain through the new CRD path, compare results against the existing FastAPI-orchestrated baseline. Same PCAP, same agent images, same model; only the orchestration layer changes.

#### Performance benchmark

Create N groups of 3 roles; measure p50/p95/p99 group-creation latency. Target: group creation ≤ N × single-sandbox latency + 200 ms. If exceeded, an optimization issue is opened rather than masking the regression.

### 9\. Documentation Plan

AudienceOutputWherePhaseMentor + maintainersDesign doc (Sections 5+6 summary)GitHub DiscussionWeek 1Mentor + maintainersdocs/design/multi-agent-design.md (canonical)RepoWeek 10Operatorsdocs/devguide/multi-agent.md (user guide + NetworkPolicy example)RepoWeek 10Operatorsexample/multi-agent-pcap/ with READMERepoWeek 7SDK userssdk-python/examples/multi\_agent\_usage.pyRepoWeek 10Wider communityBlog post on volcano.sh/blogVolcano blogWeek 12

### 10\. Risk Analysis

#RiskLikelihoodImpactMitigationR1Mentor disagrees with headless-Service-DNS transportMediumMediumWeek 1 design doc proposes ServiceDNS with named alternatives; switch cost ≤4 daysR2Another applicant merges similar work in parallelMediumHighCollaboration over competition: will propose pairing on PR 2 if both selectedR3agent-sandbox upstream API changes mid-mentorshipMediumHighPin to v0.1.1 in go.mod; raise flag in mentor sync if upstream cuts v0.2.0R4Scope creep (A2A / MCP / streaming / dashboards)HighMediumSection 14 enumerates follow-ups; new asks become separate issues, not PR ridersR516 roles × 8 replicas = 128 sandboxes in one call (DoS)LowHighSchema-level caps (MaxItems=16, Maximum=8); admission webhook enforces per-namespace quotaR6Personal scheduling conflictMediumLowWeek 8 buffer absorbs; will pre-load PR work by one week if conflict known >2 weeks aheadR7Reconciler scope too large to finish in week 11MediumLowGated behind AGENTCUBE\_GROUP\_SELF\_HEAL flag; can ship as follow-up

### 11\. Community Collaboration Plan

*   Weekly written report as a comment on issue #301: hours, PRs opened/closed, blockers, next-week plan. Public — other contributors can read along.
    
*   Weekly sync with @hzxuzhonghu at a fixed time agreed in week 1. Async-first: agenda 24 h ahead, notes posted to the issue after.
    
*   Async chat on Volcano CNCF Slack / WeChat working group. Will not @-mention unless blocked >24 hours.
    
*   PR etiquette: draft until CI green; each PR description includes a before/after example, a "how to test locally" block, and an explicit "this PR does NOT do" section to head off scope debate.
    
*   Collaboration with other applicants: will reach out before the mentorship starts to propose pairing if both selected; work splits naturally (control plane vs. SDK + docs, or store + tests vs. controller + e2e).
    
*   Knowledge transfer: every merged PR ships with a one-paragraph update to docs/devguide/multi-agent.md so the next contributor does not have to re-derive decisions from PR diffs.
    

### 12\. Why I'm a Strong Candidate

Project NeedMy EvidenceGap to CloseGo (1.22+) on production servicesOpen-source PRs above (OpenKruise, Volcano, KubeStellar — all Go-based)NoneKubernetes controller-runtime / kubebuilderOpenKruise #2303, #2313; Volcano #695 — all touch controller reconciliation pathsNoneCRD design, OpenAPI schema, admission webhooksVolcano #692 adds a new API surface; familiarity through coursework and personal K8s projectsNoneclient-go informers, dynamic clientApplied in Volcano #695 E2E scaffolding; personal homelab controllersNoneRedis / Valkey integrationPersonal projects (Valkey API is Redis-compatible)NoneK8s Services, headless DNS, NetworkPolicyStudied in depth for this proposal; applied in homelab multi-service setupsNoneAgent ecosystem (LangGraph, MCP, A2A)Experimented with LangGraph pipelines; followed MCP/A2A specs closelyNoneagent-sandbox (sigs.k8s.io) internalsNo prior PRs — see 2-week reading plan below2-week rampAgentCube codebase specificallyNo prior PRs — all file/line citations in Sections 4–6 personally verified2-week ramp

#### Honest gap: agent-sandbox + AgentCube internals

I have not contributed to either repo before. To close this gap before week 4:

*   **Weeks 1–2 (bonding):** Read every line of pkg/workloadmanager/handlers.go, workload\_builder.go, sandbox\_helper.go, sandbox\_controller.go and every \*\_test.go.
    
*   **Week 1:** Read agent-sandbox README, sandbox\_controller.go, and SandboxClaim adoption logic. Verify that Sandbox.Spec.PodTemplate.Spec.Subdomain is honored by agent-sandbox on a kind cluster.
    
*   **Weeks 2–3:** Run example/pcap-analyzer end-to-end on kind and instrument it to log every workload-manager call.
    

#### What I bring beyond commodity Go-on-K8s

*   **An ethic of small mergeable PRs.** PR 1 in week 2 is a CRD-type-only PR with zero behavior change — reviewable in 20 minutes.
    
*   **Comfort declining scope.** The explicit "this PR does NOT do" section in PR descriptions is not boilerplate; it is how I keep reviews focused.
    
*   **Operational instinct for security defaults.** The proposal makes restartPolicy: Never the default, refuses cross-namespace runtimeRef, caps roles at 16, and caps replicas-per-role at 8. Each maps to a specific failure mode (R5 in Section 10).
    
*   **Real engagement with the agent ecosystem** — will not propose locking AgentCube into LangGraph or A2A because both protocols are still maturing.
    

### 13\. Prior Contributions to This Project

I have not opened a PR on volcano-sh/agentcube or volcano-sh/volcano prior to this proposal. The codebase familiarity that backs Sections 4–6 came from approximately 15+ hours of reading; every file/line citation is one I personally opened.

Verifiable from this proposal alone:

*   Read all 1011 lines of backend-equivalent code including the transactional handleSandboxCreate (lines 86–176), the rollback path (lines 215–308), the K8s/store layering in workload\_builder.go, and the GC path in garbage\_collection.go.
    
*   Read all open issues and discussions on volcano-sh/agentcube including #301, the draft proposal from @Abhinav-kodes, and the parent volcano#4686.
    
*   Read the in-tree design proposals end to end: agentcube-proposal.md, runtime-template-proposal.md, router-proposal.md, picod-proposal.md.
    

If selected, I plan to open a small "good-first-issue"-grade PR during week 1 (e.g., test coverage for a corner case in parseEnv, or a docs fix in docs/getting-started.md) to land my first commit before the substantive work begins. This will be flagged with the mentor first to confirm it is welcome.

### 14\. Long-Term Vision Post-Mentorship

*   **First 30 days post-merge:** Primary respondent on issues mentioning MultiAgentRuntime. I will own triage because I know the code best at that point.
    
*   **Months 1–3:** A2A protocol gateway — communication.type: A2A as a sidecar that any role can opt into. Implemented as a separate proposal once the community settles on which A2A version to standardize on.
    
*   **Months 3–6:** Observability dashboard contribution to the Volcano Dashboard showing per-group metrics.
    
*   **Months 3–6:** Propose multi-tenant resource quotas at the MultiAgentRuntime level (per-namespace max-groups, max-roles-per-group) once we have real operator feedback.
    
*   **Ongoing:** Mentor the next round of LFX applicants who pick AgentCube.
    

I will apply for agentcube reviewer status after 6 months of sustained contribution, following standard OWNERS progression.

### 15\. References

#### Repository files (agentcube)

*   docs/design/agentcube-proposal.md, runtime-template-proposal.md, router-proposal.md, picod-proposal.md
    
*   pkg/apis/runtime/v1alpha1/agent\_type.go, codeinterpreter\_types.go, register.go
    
*   pkg/common/types/types.go
    
*   pkg/workloadmanager/handlers.go, workload\_builder.go, sandbox\_helper.go, codeinterpreter\_controller.go, server.go, garbage\_collection.go
    
*   pkg/store/interface.go, store\_redis.go, store\_valkey.go
    
*   pkg/router/handlers.go, session\_manager.go
    
*   example/pcap-analyzer/pcap\_analyzer.py, deployment.yaml
    
*   sdk-python/agentcube/code\_interpreter.py
    

#### Issues and PRs

*   volcano-sh/agentcube#301 (LFX parent issue)
    
*   volcano-sh/agentcube#311 (existing community contribution; SDK port + bootstrap race fix)
    
*   volcano-sh/volcano#4686 (parent AgentCube design discussion)
    
*   @Abhinav-kodes draft proposal on #301
    

#### External

*   kubernetes-sigs/agent-sandbox v0.1.1
    
*   controller-runtime, kubebuilder book
    
*   Kubernetes headless Services; Pod hostname + subdomain DNS
    
*   Kahn's topological sort algorithm
    
*   Google A2A Protocol (deferred phase 4 transport option)
    
*   Model Context Protocol (MCP)
    
*   LangGraph (referenced by existing pcap-analyzer example)