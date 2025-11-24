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

The agent has extremely high requirements for sandbox startup latency. The warm pool is one method to reduce sandbox startup time.

This proposal introduces a sandbox warm pool mechanism to significantly reduce sandbox instance preparation latency. By pre-creating and managing a pool of ready-to-use sandbox instances, the system can rapidly fulfill user requests without waiting for lengthy sandbox initialization processes.

The solution provides administrators with a programmatic interface to create, manage, and configure `SandboxWarmpool` resources on-demand. When a user requests a sandbox, the system first attempts to allocate from the warm pool via `SandboxClaim`, falling back to original sandbox creation only when necessary. This approach improves overall system responsiveness during scale events and provides a more consistent user experience.

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

- How admin to use the WarmPool Interface to Create a `SandboxWarmPool` and `SandboxTemplate`.
- How to use the warm pool with the create Sandbox handler in the API server.
- Definition of warmpool interface.

#### Non-Goals

<!--
What is out of scope for this KEP? Listing non-goals helps to focus discussion
and make progress.
-->

- Implement complex scheduling algorithms for warm pool instance distribution
- Handle cross-cluster or federated warm pool management

### Proposal

<!--
This is where we get down to the specifics of what the proposal actually is.
This should have enough detail that reviewers can understand exactly what
you're proposing, but should not include things like API designs or
implementation. What is the desired outcome and how do we measure success?.
The "Design Details" section below is for the real
nitty-gritty.
-->

#### sandboxWarmpool Interface

The SandboxWarmPool interface will expose standard CRUD operations through Kubernetes-style APIs:

Since using `SandboxWarmpool` requires `SandboxTemplate` and `SandboxClaim` to work together, we will create both `SandboxWarmpool` and `SandboxTemplate` within a create handler.

Add four new handlers to the API server.

```go
v1.HandleFunc("/admin/warmpool", s.handleCreateSandboxWarmpool).Methods("POST")
v1.HandleFunc("/admin/warmpool", s.handleGetSandboxWarmpool).Methods("GET")
v1.HandleFunc("/admin/warmpool", s.handleUpdateSandboxWarmpool).Methods("PUT")
v1.HandleFunc("/admin/warmpool", s.handleDeleteSandboxWarmpool).Methods("DELETE")
```

In the create handler process, parse the HTTP request body to retrieve the `metadata` configuration for `SandboxWarmpool`. Then create the `SandboxWarmpool` and `SandboxTemplate` resources based on the configuration.

In the read handler, parse the HTTP parameters to obtain the namespace and name of the `SandboxWarmpool` and `SandboxTemplate` resource. Then retrieve the resources and return there status.

In the update handler, parse the HTTP body to retrieve metadata. Then update the corresponding `SandboxTemplate` and `SandboxWarmpool` based on the metadata.

In the delete handler, parse the HTTP request parameters to obtain the namespace and name. Then delete the `SandboxWarmpool` and `SandboxTemplate` resource.

![sandboxWarmpool Interface](./images/warmpool.svg#center)

#### How to use SandboxWarmpool

Agent sandbox use `SandboxClaim` to link the `Sandbox` resource and the `Sandboxwarmpool`.

- First Parse metadata, image, and runtimeClassName from the HTTP request. Then look up the `SandboxTemplate` based on the labels of image and runtimeClassName.
- If a corresponding `SandboxTemplate` is found, it indicates that a corresponding `SandboxWarmpool` already exists. Then create a `SandboxClaim` to retrieve the pod from the warmpool.
- If no matching `SandboxTemplate` is found, proceed with the original create `Sandbox` process.

![warmpool Flowchart](./images/warmpool-flowchart.svg#center)

Therefore, we need to match the existing sandboxTemplate resource based on `image/runtimeClassName`, then create a sandboxClaim to establish the sandbox.

Because `SandboxClaim` depends on `SandboxTemplate`, it cannot completely replace the original sandbox interface.

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