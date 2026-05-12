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
	"k8s.io/apimachinery/pkg/util/intstr"
)

// SandboxNetworkPolicy defines the network isolation policy applied to each sandbox pod.
// When set (non-nil), a deny-all NetworkPolicy is created with explicit allow rules for
// router ingress and DNS egress. Additional rules can be added via Ingress and
// Egress. Leave this field unset (nil) to disable network policy enforcement.
type SandboxNetworkPolicy struct {
	// Egress lists additional egress allow rules beyond the default DNS rule.
	// +optional
	Egress []SandboxNetworkPolicyRule `json:"egress,omitempty"`

	// Ingress lists additional ingress allow rules.
	// Router ingress is always permitted regardless of this field.
	// +optional
	Ingress []SandboxNetworkPolicyRule `json:"ingress,omitempty"`
}

// SandboxNetworkPolicyRule describes a single ingress or egress allow rule for a sandbox pod.
type SandboxNetworkPolicyRule struct {
	// CIDRs is the list of CIDR blocks for this rule (source for ingress, destination for egress).
	// Each entry must be valid CIDR notation (e.g. "10.0.0.0/8" or "2001:db8::/32").
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:items:Pattern=`^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$|^[0-9a-fA-F:]+\/\d{1,3}$`
	CIDRs []string `json:"cidrs,omitempty"`
	// Ports restricts the rule to these ports. An empty list matches all ports.
	// +optional
	Ports []SandboxNetworkPolicyPort `json:"ports,omitempty"`
}

// SandboxNetworkPolicyPort describes a port for a sandbox network policy rule.
type SandboxNetworkPolicyPort struct {
	// Protocol is TCP or UDP. Defaults to TCP.
	// +optional
	Protocol *corev1.Protocol `json:"protocol,omitempty"`
	// Port is the destination port number or named port.
	Port intstr.IntOrString `json:"port"`
}
