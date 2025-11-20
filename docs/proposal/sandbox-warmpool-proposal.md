---
title: Introduce sandbox warm pool
authors:
- "@LiZhencheng9527" # Authors' GitHub accounts here.
reviewers:
- "@robot"
- TBD
approvers:
- "@robot"
- TBD

creation-date: 2025-11-20

---

## Introduce sandbox warm pool

<!--
This is the title of your KEP. Keep it short, simple, and descriptive. A good
title can help communicate what the KEP is and should be considered as part of
any review.
-->

### Summary

<!--
This section is incredibly important for producing high-quality, user-focused
documentation such as release notes or a development roadmap.

A good summary is probably at least a paragraph in length.
-->

Provide administrators with a programmatic interface to create and manage sandbox warm pool resources on-demand. This capability significantly reduces sandbox instance preparation latency, enabling rapid fulfillment of user requests and improving overall system responsiveness during scale events.

### Motivation

<!--
This section is for explicitly listing the motivation, goals, and non-goals of
this KEP.  Describe why the change is important and the benefits to users.
-->

Warm pools represent a proven technique for ensuring rapid instance startup and service provisioning, thereby dramatically reducing the time required for horizontal scaling operations.

In agent-based environments, minimizing sandbox preparation time is critical for maintaining responsive infrastructure. To address this requirement, we propose introducing a dedicated administrative interface enabling the creation and management of sandbox warm pools with configurable capacity targets.

#### Goals

<!--
List the specific goals of the KEP. What is it trying to achieve? How will we
know that this has succeeded?
-->

Interface provided for admin to create, delete, upgrade, and read `SandboxWarmpool` resource.

#### Non-Goals

<!--
What is out of scope for this KEP? Listing non-goals helps to focus discussion
and make progress.
-->

### Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?.
The "Design Details" section below is for the real
nitty-gritty.
-->

#### sandboxWarmpool Inferface

The SandboxWarmPool interface will expose standard CRUD operations through Kubernetes-style APIs:

- **Create**: Initialize a new warm pool with specified target capacity and configuration parameters
- **Read**: Retrieve current status, capacity utilization, and health metrics.
- **Update**: Modify pool parameters including replicas, image, and resource allocations
- **Delete**: Gracefully terminate warm pool instances and clean up associated resources

Add four new handlers to the API server.

```go
v1.HandleFunc("/admin/warmpool", s.handleCreateSandboxWarmpool).Methods("POST")
v1.HandleFunc("/admin/warmpool/{namespace}/{name}", s.handleGetSandboxWarmpool).Methods("GET")
v1.HandleFunc("/admin/warmpool/{namespace}/{name}", s.handleUpdateSandboxWarmpool).Methods("PUT")
v1.HandleFunc("/admin/warmpool/{namespace}/{name}", s.handleDeleteSandboxWarmpool).Methods("DELETE")

v1.HandleFunc("/admin/warmpool", s.handleListSandboxWarmpool).Methods("GET")
v1.HandleFunc("/admin/warmpool?namespace={ns}", s.handleListNamespaceSandboxWarmpool).Methods("GET")
```

In the create handler, parse the HTTP request body to obtain the YAML configuration for the `SandboxWarmpool`. Then create the `SandboxWarmpool` resource based on the YAML.

In the read handler, parse the HTTP request parameters to obtain the namespace and name of the `SandboxWarmpool`. Then retrieve the `SandboxWarmpool` resource and return its status.

In the update handler, parse the HTTP request parameters to obtain the namespace and name of the `SandboxWarmpool`. Subsequently, retrieve the `SandboxWarmpool` resource and update its configuration based on the YAML file in the request body.

In the delete handler, parse the HTTP request parameters to obtain the namespace and name of the `SandboxWarmpool`. Then delete the `SandboxWarmpool` resource.

In the list handler, retrieve all `SandboxWarmpool` resources and return their status. Or list all `SandboxWarmpool` resources in a namespace.

#### sandboxTemplate Inferface

When using `sandboxWarmpool`, `sandboxClaim` must be utilized. And `sandboxClaim` depends on `sandboxTemplate`. So we need to provide an interface for the administrator to create and manage sandboxTemplate resources.

```go
v1.HandleFunc("/admin/template", s.handleCreateSandboxtemplate).Methods("POST")
v1.HandleFunc("/admin/template/{namespace}/{name}", s.handleGetSandboxtemplate).Methods("GET")
v1.HandleFunc("/admin/template/{namespace}/{name}", s.handleUpdateSandboxtemplate).Methods("PUT")
v1.HandleFunc("/admin/template/{namespace}/{name}", s.handleDeleteSandboxtemplate).Methods("DELETE")

v1.HandleFunc("/admin/template", s.handleListSandboxtemplate).Methods("GET")
v1.HandleFunc("/admin/template?namespace={ns}", s.handleListNamespaceSandboxtemplate).Methods("GET")
```

In the create handler, parse the HTTP request body to obtain the YAML configuration for the `SandboxTemplate`. Then create the `SandboxTemplate` resource based on the YAML. And add two labels to it: `agentcube/sandbox-image` and `agentcube/sandbox-runtime`. For easy to find the template.

In the read handler, parse the HTTP request parameters to obtain the namespace and name of the `SandboxTemplate`. Then retrieve the `SandboxTemplate` resource and return its status(At this stage, the agent-sandbox project does not provide).

In the update handler, parse the HTTP request parameters to obtain the namespace and name of the `SandboxTemplate`. Subsequently, retrieve the `SandboxTemplate` resource and update its configuration based on the YAML file in the request body.

In the delete handler, parse the HTTP request parameters to obtain the namespace and name of the `SandboxTemplate`. Then delete the `SandboxTemplate` resource.

In the list handler, retrieve all `SandboxTemplate` resources and return their status. Or list all `SandboxTemplate` resources in a namespace.

#### How to use SandboxWarmpool

Agent sandbox use `sandboxClaim` to link the sandbox resource and the sandboxwarmpool.

- First get the `SandboxTemplate` based on the `SandboxClaim`
- Check whether there is a `sandbox` under this `sandboxClaim`.
- If not, creating the `sandbox`.
- Based on the label specified in `agents.x-k8s.io/sandbox-template-ref-hash`, value is `sandboxClaim.Spec.TemplateRef.Name`, locate the pod within the warm pool. Then, from the pods using the warm pool as their controllerRef, select the pod with the longest creation time.
- If not found in the warm pool, create a sandbox based on the matching sandboxTemplate.

Therefore, we need to match the existing sandboxTemplate resource based on `image/runntimeClassName`, then create a sandboxClaim to establish the sandbox.

Because sandboxClaim depends on sandboxTemplate, it cannot completely replace the original sandbox interface.

Therefore, add logic to the `handleCreateSandbox` function to create a `sandboxClaim` based on the `sandboxTemplate` retrieved using the `image` and `runtimeClassName`.

When creating the sandboxTemplate, we added two labels to it: `agentcube/sandbox-image` and `agentcube/sandbox-runtime`. When you need to look it up, use a `labelSelector` to locate the `sandboxTemplate`. Then create a sandboxClaim based on the sandboxTemplate.Name if it found. If sandboxTemplate cannot be found, proceed with the previous create sandbox operation.

#### Permission Management

Creating `sandboxWarmpool`, `sandboxClaim`, and `sandboxTemplate` requires the corresponding permissions. When creating the `sandbox` resource, a `KubeClient` was created using the `userToken` and `serviceAccount` to verify the user's permissions.

```go
userClient, clientErr := s.k8sClient.GetOrCreateUserK8sClient(userToken, userNamespace, serviceAccountName)
```

Therefore, the logic from previous logic can also be reused in `create sandboxClaim`, `sandboxTemplate`, and `sandboxWarmpool`. This enables reuse of the `kubeclient` interface and `clientCache`.

#### User Stories (Optional)

<!--
Detail the things that people will be able to do if this KEP is implemented.
Include as much detail as possible so that people can understand the "how" of
the system. The goal here is to make this feel real for users without getting
bogged down.
-->

##### Story 1

##### Story 2

#### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

#### Risks and Mitigations

<!--
What are the risks of this proposal, and how do we mitigate?

How will security be reviewed, and by whom?

How will UX be reviewed, and by whom?

Consider including folks who also work outside the SIG or subproject.
-->

### Design Details

<!--
This section should contain enough information that the specifics of your
change are understandable. This may include API specs (though not always
required) or even code snippets. If there's any ambiguity about HOW your
proposal will be implemented, this is the place to discuss them.
-->



#### Test Plan

<!--
**Note:** *Not required until targeted at a release.*

Consider the following in developing a test plan for this enhancement:
- Will there be e2e and integration tests, in addition to unit tests?
- How will it be tested in isolation vs with other components?

No need to outline all test cases, just the general strategy. Anything
that would count as tricky in the implementation, and anything particularly
challenging to test, should be called out.

-->

### Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->

<!--
Note: This is a simplified version of kubernetes enhancement proposal template.
https://github.com/kubernetes/enhancements/tree/3317d4cb548c396a430d1c1ac6625226018adf6a/keps/NNNN-kep-template
-->