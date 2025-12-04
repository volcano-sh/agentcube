package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CodeInterpreter defines the desired state of a code interpreter runtime environment.
//
// This runtime is designed for running potentially untrusted, LLM-generated code in an
// isolated sandbox, typically per user/session, with stricter security and resource controls
// compared to a generic AgentRuntime.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type CodeInterpreter struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the desired state of the CodeInterpreter.
	Spec CodeInterpreterSpec `json:"spec"`
	// Status represents the current state of the CodeInterpreter.
	Status CodeInterpreterStatus `json:"status,omitempty"`
}

// CodeInterpreterSpec describes how to create and manage code-interpreter sandboxes.
type CodeInterpreterSpec struct {
	// Ports is a list of ports that the code interpreter runtime will expose.
	// These ports are typically used by the router / apiserver to proxy HTTP or gRPC
	// traffic into the sandbox (e.g., /execute, /files, /health).
	// If not specified, defaults to use agentcube's code interpreter.
	// +optional
	Ports []TargetPort `json:"ports,omitempty"`

	// Template describes the template that will be used to create a code interpreter sandbox.
	// This SHOULD be more locked down than a generic agent runtime (e.g. no hostPath,
	// restricted capabilities, read-only root filesystem, etc.).
	// +kubebuilder:validation:Required
	Template *CodeInterpreterSandboxTemplate `json:"template"`

	// SessionTimeout describes the duration after which an inactive code-interpreter
	// session will be terminated. Any sandbox that has not received requests within
	// this duration is eligible for cleanup.
	// +kubebuilder:default="15m"
	SessionTimeout *metav1.Duration `json:"sessionTimeout,omitempty"`

	// MaxSessionDuration describes the maximum duration for a code-interpreter session.
	// After this duration, the session will be terminated regardless of activity, to
	// prevent long-lived sandboxes from accumulating unbounded state.
	// +kubebuilder:default="8h"
	MaxSessionDuration *metav1.Duration `json:"maxSessionDuration,omitempty"`

	// WarmPoolSize specifies the number of pre-warmed sandboxes to maintain
	// for this code interpreter runtime. Pre-warmed sandboxes can reduce startup
	// latency for new sessions at the cost of additional resource usage.
	// +optional
	WarmPoolSize *int32 `json:"warmPoolSize,omitempty"`
}

// CodeInterpreterStatus represents the observed state of a CodeInterpreter.
type CodeInterpreterStatus struct {
	// Conditions represent the latest available observations of the CodeInterpreter's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Ready indicates whether the CodeInterpreter is ready to serve requests
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// CodeInterpreterSandboxTemplate mirrors SandboxTemplate but is kept separate in case
// we want to evolve code-interpreter specific defaults independently in the future.
// For now, it can be used in controllers or validation if a distinct type is helpful.
type CodeInterpreterSandboxTemplate struct {
	// Labels to apply to the sandbox Pod.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations to apply to the sandbox Pod.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// RuntimeClassName specifies the Kubernetes RuntimeClass used to run the sandbox
	// (e.g., for kuasar / Kata-based isolation).
	// +optional
	RuntimeClassName *string `json:"runtimeClassName,omitempty"`

	// Image indicates the container image to use for the code interpreter runtime.
	Image string `json:"image,omitempty"`

	// Image pull policy.
	// One of Always, Never, IfNotPresent.
	// Defaults to Always if :latest tag is specified, or IfNotPresent otherwise.
	// Cannot be updated.
	// More info: https://kubernetes.io/docs/concepts/containers/images#updating-images
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecrets is an optional list of references to secrets in the same namespace to use for pulling any of the images used by this PodSpec.
	// If specified, these secrets will be passed to individual puller implementations for them to use.
	// More info: https://kubernetes.io/docs/concepts/containers/images#specifying-imagepullsecrets-on-a-pod
	// +optional
	// +patchMergeKey=name
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=name
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`

	// Environment specifies the environment variables to set in the code interpreter runtime.
	// +optional
	Environment []corev1.EnvVar `json:"environment,omitempty"`

	// Entrypoint array. Not executed within a shell.
	// The container image's ENTRYPOINT is used if this is not provided.
	// +optional
	// +listType=atomic
	Command []string `json:"command,omitempty"`

	// Arguments to the entrypoint.
	// The container image's CMD is used if this is not provided.
	// +optional
	Args []string `json:"args,omitempty"`

	// Compute Resources required by this container.
	// More info: https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`
}

// TargetPort defines a port that the runtime will expose.
type TargetPort struct {
	// PathPrefix is the path prefix to route to this port.
	// For example, if PathPrefix is "/api", requests to "/api/..." will be routed to this port.
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty"`
	// Name is the name of the port.
	// +optional
	Name string `json:"name,omitempty"`
	// Port is the port number.
	Port uint32 `json:"port"`
	// Protocol is the protocol of the port.
	// +kubebuilder:default=HTTP
	// +kubebuilder:validation:Enum=HTTP;HTTPS;
	Protocol ProtocolType `json:"protocol"`
}

// ProtocolType defines the protocol for a port.
type ProtocolType string

const (
	// ProtocolTypeHTTP indicates HTTP protocol
	ProtocolTypeHTTP ProtocolType = "HTTP"
	// ProtocolTypeHTTPS indicates HTTPS protocol
	ProtocolTypeHTTPS ProtocolType = "HTTPS"
)

// CodeInterpreterList contains a list of CodeInterpreter
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type CodeInterpreterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CodeInterpreter `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CodeInterpreter{}, &CodeInterpreterList{})
}
