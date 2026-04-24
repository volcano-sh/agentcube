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
	"testing"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var (
	testRouterSelector  = map[string]string{"app": "agentcube-router"}
	testRouterNamespace = "agentcube-system"
)

func TestBuildNetworkPolicy_NilSpec(t *testing.T) {
	// nil spec → no NP (no enforcement)
	np := buildNetworkPolicy("sandbox-1", "default", nil, testRouterSelector, testRouterNamespace)
	assert.Nil(t, np)
}

func TestBuildNetworkPolicy_Metadata(t *testing.T) {
	np := buildNetworkPolicy("my-sandbox", "my-ns", &runtimev1alpha1.SandboxNetworkPolicy{},
		testRouterSelector, testRouterNamespace)
	require.NotNil(t, np)

	assert.Equal(t, "my-sandbox", np.Name)
	assert.Equal(t, "my-ns", np.Namespace)
	assert.Equal(t, "my-sandbox", np.Labels[SandboxNameLabelKey])
	assert.Equal(t, map[string]string{SandboxNameLabelKey: "my-sandbox"}, np.Spec.PodSelector.MatchLabels)
	assert.Contains(t, np.Spec.PolicyTypes, networkingv1.PolicyTypeIngress)
	assert.Contains(t, np.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)
}

func TestBuildNetworkPolicy_DefaultRules(t *testing.T) {
	// Non-nil spec with no extra rules → router ingress + DNS egress
	np := buildNetworkPolicy("sb", "ns", &runtimev1alpha1.SandboxNetworkPolicy{},
		testRouterSelector, testRouterNamespace)
	require.NotNil(t, np)

	// Ingress: only router rule by default
	require.Len(t, np.Spec.Ingress, 1)
	require.Len(t, np.Spec.Ingress[0].From, 1)
	assert.Equal(t, testRouterSelector, np.Spec.Ingress[0].From[0].PodSelector.MatchLabels)

	// Egress: DNS rule (UDP+TCP 53) by default
	require.Len(t, np.Spec.Egress, 1)
	dnsRule := np.Spec.Egress[0]
	assert.Empty(t, dnsRule.To) // unrestricted destination, only port-limited
	require.Len(t, dnsRule.Ports, 2)
	ports := map[corev1.Protocol]bool{}
	for _, p := range dnsRule.Ports {
		require.NotNil(t, p.Protocol)
		assert.Equal(t, intstr.FromInt32(53), *p.Port)
		ports[*p.Protocol] = true
	}
	assert.True(t, ports[corev1.ProtocolUDP], "expected UDP DNS port")
	assert.True(t, ports[corev1.ProtocolTCP], "expected TCP DNS port")
}

func TestBuildNetworkPolicy_Egress(t *testing.T) {
	tcp := corev1.ProtocolTCP
	np := buildNetworkPolicy("sb", "ns", &runtimev1alpha1.SandboxNetworkPolicy{
		Egress: []runtimev1alpha1.SandboxNetworkPolicyRule{
			{
				CIDRs: []string{"10.0.0.0/8"},
				Ports: []runtimev1alpha1.SandboxNetworkPolicyPort{
					{Protocol: &tcp, Port: intstr.FromInt32(443)},
				},
			},
		},
	}, testRouterSelector, testRouterNamespace)
	require.NotNil(t, np)

	// DNS rule (index 0) + custom egress rule (index 1)
	require.Len(t, np.Spec.Egress, 2)
	customRule := np.Spec.Egress[1]
	require.Len(t, customRule.To, 1)
	assert.Equal(t, "10.0.0.0/8", customRule.To[0].IPBlock.CIDR)
	require.Len(t, customRule.Ports, 1)
	assert.Equal(t, intstr.FromInt32(443), *customRule.Ports[0].Port)
	assert.Equal(t, corev1.ProtocolTCP, *customRule.Ports[0].Protocol)
}

func TestBuildNetworkPolicy_Ingress(t *testing.T) {
	np := buildNetworkPolicy("sb", "ns", &runtimev1alpha1.SandboxNetworkPolicy{
		Ingress: []runtimev1alpha1.SandboxNetworkPolicyRule{
			{CIDRs: []string{"192.168.1.0/24"}},
		},
	}, testRouterSelector, testRouterNamespace)
	require.NotNil(t, np)

	// router rule (index 0) + custom ingress (index 1)
	require.Len(t, np.Spec.Ingress, 2)
	customRule := np.Spec.Ingress[1]
	require.Len(t, customRule.From, 1)
	assert.Equal(t, "192.168.1.0/24", customRule.From[0].IPBlock.CIDR)
}

func TestBuildNetworkPolicy_RouterSelector(t *testing.T) {
	customSelector := map[string]string{"app": "my-custom-router", "tier": "proxy"}
	np := buildNetworkPolicy("sb", "ns", &runtimev1alpha1.SandboxNetworkPolicy{},
		customSelector, testRouterNamespace)
	require.NotNil(t, np)

	require.Len(t, np.Spec.Ingress, 1)
	ingressPeer := np.Spec.Ingress[0].From[0]
	require.NotNil(t, ingressPeer.PodSelector)
	assert.Equal(t, customSelector, ingressPeer.PodSelector.MatchLabels)
}

func TestBuildNetworkPolicy_RouterCrossNamespace(t *testing.T) {
	// When routerNamespace is set, the router peer must carry a NamespaceSelector
	// using the standard kubernetes.io/metadata.name label so the rule works when
	// the sandbox is in a different namespace than the router.
	np := buildNetworkPolicy("sb", "user-ns", &runtimev1alpha1.SandboxNetworkPolicy{},
		testRouterSelector, "agentcube-system")
	require.NotNil(t, np)

	require.Len(t, np.Spec.Ingress, 1)
	routerPeer := np.Spec.Ingress[0].From[0]
	require.NotNil(t, routerPeer.PodSelector)
	require.NotNil(t, routerPeer.NamespaceSelector)
	assert.Equal(t, map[string]string{k8sNamespaceLabelKey: "agentcube-system"}, routerPeer.NamespaceSelector.MatchLabels)
}

func TestBuildNetworkPolicy_RouterNamespaceAlwaysSet(t *testing.T) {
	// NamespaceSelector must always be present on the router peer so that the rule
	// works across namespaces. buildIngressRules fatals on empty namespace,
	// so this test confirms a non-empty namespace produces the expected selector.
	np := buildNetworkPolicy("sb", "ns", &runtimev1alpha1.SandboxNetworkPolicy{},
		testRouterSelector, testRouterNamespace)
	require.NotNil(t, np)

	require.Len(t, np.Spec.Ingress, 1)
	routerPeer := np.Spec.Ingress[0].From[0]
	require.NotNil(t, routerPeer.NamespaceSelector)
	assert.Equal(t, testRouterNamespace, routerPeer.NamespaceSelector.MatchLabels[k8sNamespaceLabelKey])
}

func TestBuildNetworkPolicy_SandboxNameLabel(t *testing.T) {
	np := buildNetworkPolicy("unique-sandbox", "test-ns", &runtimev1alpha1.SandboxNetworkPolicy{},
		testRouterSelector, testRouterNamespace)
	require.NotNil(t, np)

	assert.Equal(t, metav1.LabelSelector{
		MatchLabels: map[string]string{SandboxNameLabelKey: "unique-sandbox"},
	}, np.Spec.PodSelector)
}

// TestEffectiveNetworkPolicy verifies "replace, not merge" semantics between
// template-level default and per-session override.
func TestEffectiveNetworkPolicy(t *testing.T) {
	template := &runtimev1alpha1.SandboxNetworkPolicy{
		Egress: []runtimev1alpha1.SandboxNetworkPolicyRule{
			{CIDRs: []string{"10.0.0.0/8"}},
		},
	}
	session := &runtimev1alpha1.SandboxNetworkPolicy{
		Ingress: []runtimev1alpha1.SandboxNetworkPolicyRule{
			{CIDRs: []string{"192.168.0.0/16"}},
		},
	}

	// session nil → template wins
	assert.Equal(t, template, effectiveNetworkPolicy(nil, template))
	// session set → replaces template entirely (no merge)
	assert.Equal(t, session, effectiveNetworkPolicy(session, template))
	// both nil → nil (no NP)
	assert.Nil(t, effectiveNetworkPolicy(nil, nil))
	// template nil, session set → session
	assert.Equal(t, session, effectiveNetworkPolicy(session, nil))
}

// TestToNetworkPolicyPorts_NoAliasing verifies mutations of the source slice
// after build do not affect the generated NetworkPolicy ports.
func TestToNetworkPolicyPorts_NoAliasing(t *testing.T) {
	tcp := corev1.ProtocolTCP
	src := []runtimev1alpha1.SandboxNetworkPolicyPort{
		{Protocol: &tcp, Port: intstr.FromInt32(443)},
	}
	out := toNetworkPolicyPorts(src)
	require.Len(t, out, 1)

	// Mutate the source after the call.
	udp := corev1.ProtocolUDP
	src[0].Protocol = &udp
	src[0].Port = intstr.FromInt32(80)

	assert.Equal(t, corev1.ProtocolTCP, *out[0].Protocol, "output protocol must not track source mutation")
	assert.Equal(t, int32(443), out[0].Port.IntVal, "output port must not track source mutation")
}
