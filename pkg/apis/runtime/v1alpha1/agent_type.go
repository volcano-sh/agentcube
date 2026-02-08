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
)

// AgentRuntime defines the desired state of an agent runtime environment.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type AgentRuntime struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the desired state of the AgentRuntime.
	Spec AgentRuntimeSpec `json:"spec"`
	// Status represents the current state of the AgentRuntime.
	Status AgentRuntimeStatus `json:"status,omitempty"`
}

// AgentRuntimeSpec describes how to create and manage agent runtime sandboxes.
type AgentRuntimeSpec struct {
	// Ports is a list of ports that the agent runtime will expose.
	Ports []TargetPort `json:"targetPort"`

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

// AgentRuntimeStatus represents the observed state of an AgentRuntime.
type AgentRuntimeStatus struct {
	// Conditions represent the latest available observations of the AgentRuntime's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Ready indicates whether the AgentRuntime is ready to serve requests
	// +optional
	Ready bool `json:"ready,omitempty"`
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

// AgentRuntimeList contains a list of AgentRuntime
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type AgentRuntimeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AgentRuntime `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AgentRuntime{}, &AgentRuntimeList{})
}
