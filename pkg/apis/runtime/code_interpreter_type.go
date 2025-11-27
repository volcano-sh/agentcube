package runtime

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CodeInterpreter defines the desired state of a code interpreter runtime environment.
//
// This runtime is designed for running potentially untrusted, LLM-generated code in an
// isolated sandbox, typically per user/session, with stricter security and resource controls
// compared to a generic AgentRuntime.
type CodeInterpreter struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero" protobuf:"bytes,1,opt,name=metadata"`
	// Spec defines the desired state of the CodeInterpreter.
	Spec CodeInterpreterSpec `json:"spec" protobuf:"bytes,2,opt,name=spec"`
	// Status represents the current state of the CodeInterpreter.
	Status CodeInterpreterStatus `json:"status" protobuf:"bytes,3,opt,name=status"`
}

// CodeInterpreterSpec describes how to create and manage code-interpreter sandboxes.
type CodeInterpreterSpec struct {
	// Ports is a list of ports that the code interpreter runtime will expose.
	// These ports are typically used by the router / apiserver to proxy HTTP or gRPC
	// traffic into the sandbox (e.g., /execute, /files, /health).
	// If not specified, defaults to use agentcube's code interpreter.
	// +optional
	Ports []TargetPort `json:"ports,omitempty" protobuf:"bytes,1,rep,name=ports"`

	// Template describes the template that will be used to create a code interpreter sandbox.
	// This SHOULD be more locked down than a generic agent runtime (e.g. no hostPath,
	// restricted capabilities, read-only root filesystem, etc.).
	// +kubebuilder:validation:Required
	Template *CodeInterpreterSandboxTemplate `json:"template" protobuf:"bytes,2,opt,name=template"`

	// SessionTimeout describes the duration after which an inactive code-interpreter
	// session will be terminated. Any sandbox that has not received requests within
	// this duration is eligible for cleanup.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="15m"
	SessionTimeout *metav1.Duration `json:"sessionTimeout,omitempty" protobuf:"bytes,3,opt,name=sessionTimeout"`

	// MaxSessionDuration describes the maximum duration for a code-interpreter session.
	// After this duration, the session will be terminated regardless of activity, to
	// prevent long-lived sandboxes from accumulating unbounded state.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="8h"
	MaxSessionDuration *metav1.Duration `json:"maxSessionDuration,omitempty" protobuf:"bytes,4,opt,name=maxSessionDuration"`

	// Languages declares the set of languages/runtimes that this code interpreter
	// environment supports (e.g. ["python", "bash"]). This is metadata to help
	// routers / UIs select a compatible runtime.
	// +optional
	Languages []string `json:"languages,omitempty" protobuf:"bytes,6,rep,name=languages"`

	// WarmPoolSize specifies the number of pre-warmed sandboxes to maintain
	// for this code interpreter runtime. Pre-warmed sandboxes can reduce startup
	// latency for new sessions at the cost of additional resource usage.
	// +optional
	WarmPoolSize *int32 `json:"warmPoolSize,omitempty" protobuf:"varint,5,opt,name=warmPoolSize"`
}

// CodeInterpreterStatus represents the observed state of a CodeInterpreter.
type CodeInterpreterStatus struct {
}

// CodeInterpreterSandboxTemplate mirrors SandboxTemplate but is kept separate in case
// we want to evolve code-interpreter specific defaults independently in the future.
// For now, it can be used in controllers or validation if a distinct type is helpful.
type CodeInterpreterSandboxTemplate struct {
	// Labels to apply to the sandbox Pod.
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,1,rep,name=labels"`

	// Annotations to apply to the sandbox Pod.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,2,rep,name=annotations"`

	// RuntimeClassName specifies the Kubernetes RuntimeClass used to run the sandbox
	// (e.g., for kuasar / Kata-based isolation).
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty" protobuf:"bytes,3,opt,name=runtimeClassName"`

	// Image indicates the container image to use for the code interpreter runtime.
	Image string `json:"image,omitempty" protobuf:"bytes,4,opt,name=image"`

	// Environment specifies the environment variables to set in the code interpreter runtime.
	Environment []corev1.EnvVar `json:"environment,omitempty" protobuf:"bytes,5,rep,name=environment"`

	// Entrypoint array. Not executed within a shell.
	// The container image's ENTRYPOINT is used if this is not provided.
	// +optional
	// +listType=atomic
	Command []string `json:"command,omitempty" protobuf:"bytes,6,rep,name=command"`

	// Arguments to the entrypoint.
	// The container image's CMD is used if this is not provided.
	// +optional
	Args []string `json:"args,omitempty" protobuf:"bytes,7,rep,name=args"`

	// Compute Resources required by this container.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty" protobuf:"bytes,8,opt,name=resources"`
}
