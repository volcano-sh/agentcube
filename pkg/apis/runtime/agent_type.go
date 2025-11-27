package runtime

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentRuntime defines the desired state of an agent runtime environment.
type AgentRuntime struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero" protobuf:"bytes,1,opt,name=metadata"`
	// Spec defines the desired state of the AgentRuntime.
	Spec AgentRuntimeSpec `json:"spec" protobuf:"bytes,2,opt,name=spec"`
	// Status represents the current state of the AgentRuntime.
	Status AgentRuntimeStatus `json:"status" protobuf:"bytes,3,opt,name=status"`
}

type AgentRuntimeSpec struct {
	// Ports is a list of ports that the agent runtime will expose.
	Ports []TargetPort

	// PodTemplate describes the template that will be used to create an agent sandbox.
	// +kubebuilder:validation:Required
	Template *SandboxTemplate `json:"podTemplate" protobuf:"bytes,1,opt,name=podTemplate"`

	// SessionTimeout describes the duration after which an inactive session will be terminated.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="15m"
	SessionTimeout *metav1.Duration `json:"sessionTimeout,omitempty" protobuf:"bytes,2,opt,name=sessionTimeout"`

	// MaxSessionDuration describes the maximum duration for a session.
	// After this duration, the session will be terminated no matter active or inactive.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="8h"
	MaxSessionDuration *metav1.Duration `json:"maxSessionDuration,omitempty" protobuf:"bytes,3,opt,name=maxSessionDuration"`
}

type ProtocolType string

const (
	ProtocolTypeHTTP  ProtocolType = "HTTP"
	ProtocolTypeHTTPS ProtocolType = "HTTPS"
)

type TargetPort struct {
	// PathPrefix is the path prefix to route to this port.
	// For example, if PathPrefix is "/api", requests to "/api/..." will be routed to this port.
	// +optional
	PathPrefix string `json:"pathPrefix,omitempty" protobuf:"bytes,4,opt,name=pathPrefix"`
	// Name is the name of the port.
	// +optional
	Name string `json:"name,omitempty" protobuf:"bytes,1,opt,name=name"`
	// Port is the port number.
	Port uint32 `json:"port" protobuf:"varint,2,opt,name=port"`
	// Protocol is the protocol of the port.
	// +kubebuilder:default=HTTP
	// +kubebuilder:validation:Enum=HTTP;HTTPS;
	Protocol ProtocolType `json:"protocol" protobuf:"bytes,3,opt,name=protocol"`
}

type AgentRuntimeStatus struct {
}

type SandboxTemplate struct {
	// Labels to apply to the sandbox Pod.
	// +optional
	Labels map[string]string `json:"labels,omitempty" protobuf:"bytes,1,rep,name=labels"`

	// Annotations to apply to the sandbox Pod.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty" protobuf:"bytes,2,rep,name=annotations"`

	// Spec is the Pod's spec
	// +kubebuilder:validation:Required
	Spec corev1.PodSpec `json:"spec" protobuf:"bytes,3,opt,name=spec"`
}
