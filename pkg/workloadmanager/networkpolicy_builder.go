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

package workloadmanager

import (
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// k8sNamespaceLabelKey is the label automatically applied to every Namespace by
// Kubernetes 1.22+ containing the namespace's own name. Using it as a
// NamespaceSelector key avoids requiring users to manually label namespaces.
const k8sNamespaceLabelKey = "kubernetes.io/metadata.name"

// buildNetworkPolicy returns a NetworkPolicy for the given sandbox, or nil when Mode=None.
func buildNetworkPolicy(sandboxName, namespace string, spec *runtimev1alpha1.SandboxNetworkPolicy, routerSelector map[string]string, routerNamespace string) *networkingv1.NetworkPolicy {
	if spec == nil || spec.Mode == runtimev1alpha1.NetworkPolicyModeNone || spec.Mode == "" {
		return nil
	}

	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      sandboxName,
			Namespace: namespace,
			Labels: map[string]string{
				SandboxNameLabelKey: sandboxName,
				"managed-by":        "agentcube-workload-manager",
			},
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{
					SandboxNameLabelKey: sandboxName,
				},
			},
			PolicyTypes: []networkingv1.PolicyType{
				networkingv1.PolicyTypeIngress,
				networkingv1.PolicyTypeEgress,
			},
		},
	}

	switch spec.Mode {
	case runtimev1alpha1.NetworkPolicyModeRestricted:
		np.Spec.Ingress = buildRestrictedIngressRules(spec, routerSelector, routerNamespace)
		np.Spec.Egress = buildRestrictedEgressRules(spec)
	case runtimev1alpha1.NetworkPolicyModeCustom:
		if spec.Custom != nil {
			np.Spec.Ingress = spec.Custom.Ingress
			np.Spec.Egress = spec.Custom.Egress
		}
	}

	return np
}

func buildRestrictedIngressRules(spec *runtimev1alpha1.SandboxNetworkPolicy, routerSelector map[string]string, routerNamespace string) []networkingv1.NetworkPolicyIngressRule {
	// Router ingress is always allowed in Restricted mode. NamespaceSelector is set
	// so the rule works even when the router runs in a namespace distinct from the
	// sandbox (the common production layout: router in agentcube-system, sandboxes
	// in user namespaces).
	routerPeer := networkingv1.NetworkPolicyPeer{
		PodSelector: &metav1.LabelSelector{MatchLabels: routerSelector},
	}
	if routerNamespace != "" {
		routerPeer.NamespaceSelector = &metav1.LabelSelector{
			MatchLabels: map[string]string{k8sNamespaceLabelKey: routerNamespace},
		}
	}
	rules := []networkingv1.NetworkPolicyIngressRule{
		{From: []networkingv1.NetworkPolicyPeer{routerPeer}},
	}
	for _, r := range spec.AllowedIngress {
		rule := networkingv1.NetworkPolicyIngressRule{}
		for _, cidr := range r.CIDRs {
			rule.From = append(rule.From, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: cidr},
			})
		}
		rule.Ports = toNetworkPolicyPorts(r.Ports)
		rules = append(rules, rule)
	}
	return rules
}

func buildRestrictedEgressRules(spec *runtimev1alpha1.SandboxNetworkPolicy) []networkingv1.NetworkPolicyEgressRule {
	var rules []networkingv1.NetworkPolicyEgressRule

	// Allow DNS by default (unless explicitly disabled).
	if spec.AllowDNS == nil || *spec.AllowDNS {
		tcp := corev1.ProtocolTCP
		udp := corev1.ProtocolUDP
		dnsPort := intstr.FromInt32(53)
		rules = append(rules, networkingv1.NetworkPolicyEgressRule{
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &udp, Port: &dnsPort},
				{Protocol: &tcp, Port: &dnsPort},
			},
		})
	}

	for _, r := range spec.AllowedEgress {
		rule := networkingv1.NetworkPolicyEgressRule{}
		for _, cidr := range r.CIDRs {
			rule.To = append(rule.To, networkingv1.NetworkPolicyPeer{
				IPBlock: &networkingv1.IPBlock{CIDR: cidr},
			})
		}
		rule.Ports = toNetworkPolicyPorts(r.Ports)
		rules = append(rules, rule)
	}
	return rules
}

func toNetworkPolicyPorts(ports []runtimev1alpha1.SandboxNetworkPolicyPort) []networkingv1.NetworkPolicyPort {
	if len(ports) == 0 {
		return nil
	}
	out := make([]networkingv1.NetworkPolicyPort, 0, len(ports))
	for i := range ports {
		// Copy values rather than aliasing pointers into the user's spec, so later
		// mutation of the source slice cannot affect the generated NetworkPolicy.
		port := ports[i].Port
		entry := networkingv1.NetworkPolicyPort{Port: &port}
		if ports[i].Protocol != nil {
			proto := *ports[i].Protocol
			entry.Protocol = &proto
		}
		out = append(out, entry)
	}
	return out
}
