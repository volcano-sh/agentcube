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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BrowserUse defines the desired state of a browser automation runtime environment.
//
// This runtime is designed for running browser automation workloads such as
// Playwright MCP servers, browser-use agents, or similar browser-in-sandbox tools.
// It provides an isolated, ephemeral browser environment per session with
// automatic lifecycle management.
// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
type BrowserUse struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// Spec defines the desired state of the BrowserUse runtime.
	Spec BrowserUseSpec `json:"spec"`
	// Status represents the current state of the BrowserUse runtime.
	Status BrowserUseStatus `json:"status,omitempty"`
}

// BrowserUseSpec describes how to create and manage browser automation sandboxes.
type BrowserUseSpec struct {
	// Ports is a list of ports that the browser runtime will expose.
	// Typically includes the browser automation protocol port (e.g., Playwright MCP on 8931)
	// and optionally a VNC port for visual debugging.
	Ports []TargetPort `json:"targetPort"`

	// PodTemplate describes the template that will be used to create a browser sandbox.
	// Browser workloads typically require:
	// - Sufficient shared memory (/dev/shm) for Chromium
	// - Network access for browsing target websites
	// - Optional VNC sidecar for visual debugging
	// +kubebuilder:validation:Required
	Template *SandboxTemplate `json:"podTemplate" protobuf:"bytes,1,opt,name=podTemplate"`

	// SessionTimeout describes the duration after which an inactive browser session
	// will be terminated. Browser sessions are typically longer-lived than code
	// interpreter sessions due to multi-step navigation tasks.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="30m"
	SessionTimeout *metav1.Duration `json:"sessionTimeout,omitempty" protobuf:"bytes,2,opt,name=sessionTimeout"`

	// MaxSessionDuration describes the maximum duration for a browser session.
	// After this duration, the session will be terminated regardless of activity.
	// +kubebuilder:validation:Required
	// +kubebuilder:default="8h"
	MaxSessionDuration *metav1.Duration `json:"maxSessionDuration,omitempty" protobuf:"bytes,3,opt,name=maxSessionDuration"`
}

// BrowserUseStatus represents the observed state of a BrowserUse runtime.
type BrowserUseStatus struct {
	// Conditions represent the latest available observations of the BrowserUse's state.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// BrowserUseList contains a list of BrowserUse
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
type BrowserUseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BrowserUse `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BrowserUse{}, &BrowserUseList{})
}
