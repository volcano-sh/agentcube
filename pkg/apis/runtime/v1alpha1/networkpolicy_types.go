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
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// NetworkPolicyMode defines the network policy enforcement level for a sandbox.
// +kubebuilder:validation:Enum=None;Restricted;Custom
type NetworkPolicyMode string

const (
	// NetworkPolicyModeNone creates no NetworkPolicy (default, backward-compatible).
	NetworkPolicyModeNone NetworkPolicyMode = "None"
	// NetworkPolicyModeRestricted applies a deny-all baseline with explicit allow rules
	// for DNS egress and router ingress.
	NetworkPolicyModeRestricted NetworkPolicyMode = "Restricted"
	// NetworkPolicyModeCustom uses the raw rules from the Custom field.
	NetworkPolicyModeCustom NetworkPolicyMode = "Custom"
)

// SandboxNetworkPolicy defines the network isolation policy applied to each sandbox pod.
type SandboxNetworkPolicy struct {
	// Mode controls the network policy enforcement level.
	// "None" (default): no NetworkPolicy is created.
	// "Restricted": deny-all baseline; DNS egress and router ingress are always allowed.
	// "Custom": use the raw Ingress/Egress rules in the Custom field.
	// +kubebuilder:default=None
	// +optional
	Mode NetworkPolicyMode `json:"mode,omitempty"`

	// AllowedEgress lists additional egress allow rules applied when Mode=Restricted.
	// +optional
	AllowedEgress []SandboxEgressRule `json:"allowedEgress,omitempty"`

	// AllowDNS controls whether a DNS egress rule (port 53 UDP/TCP) is added when
	// Mode=Restricted. Defaults to true.
	// +optional
	AllowDNS *bool `json:"allowDNS,omitempty"`

	// AllowedIngress lists additional ingress allow rules when Mode=Restricted.
	// Router ingress is always permitted in Restricted mode regardless of this field.
	// +optional
	AllowedIngress []SandboxIngressRule `json:"allowedIngress,omitempty"`

	// Custom provides raw NetworkPolicy ingress/egress rules, used only when Mode=Custom.
	// +optional
	Custom *SandboxNetworkPolicyCustomRules `json:"custom,omitempty"`
}

// SandboxEgressRule describes an egress allow rule for a sandbox pod.
type SandboxEgressRule struct {
	// CIDRs is the list of destination CIDR blocks.
	// Each entry must be a valid CIDR notation (e.g. "10.0.0.0/8" or "2001:db8::/32").
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:items:Pattern=`^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$|^[0-9a-fA-F:]+\/\d{1,3}$`
	CIDRs []string `json:"cidrs,omitempty"`
	// Ports restricts the rule to these destination ports. An empty list matches all ports.
	// +optional
	Ports []SandboxNetworkPolicyPort `json:"ports,omitempty"`
}

// SandboxIngressRule describes an ingress allow rule for a sandbox pod.
type SandboxIngressRule struct {
	// CIDRs is the list of source CIDR blocks.
	// Each entry must be a valid CIDR notation (e.g. "10.0.0.0/8" or "2001:db8::/32").
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:items:Pattern=`^(\d{1,3}\.){3}\d{1,3}\/\d{1,2}$|^[0-9a-fA-F:]+\/\d{1,3}$`
	CIDRs []string `json:"cidrs,omitempty"`
	// Ports restricts the rule to these destination ports. An empty list matches all ports.
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

// SandboxNetworkPolicyCustomRules holds raw Kubernetes NetworkPolicy ingress/egress rules.
// Used only when Mode=Custom.
type SandboxNetworkPolicyCustomRules struct {
	// Ingress is the list of ingress rules.
	// +optional
	Ingress []networkingv1.NetworkPolicyIngressRule `json:"ingress,omitempty"`
	// Egress is the list of egress rules.
	// +optional
	Egress []networkingv1.NetworkPolicyEgressRule `json:"egress,omitempty"`
}
