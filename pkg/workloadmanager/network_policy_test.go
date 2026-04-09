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
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestBuildNetworkPolicy_DenyAll(t *testing.T) {
	np := buildNetworkPolicy("default", "my-sandbox", nil)

	assert.Equal(t, "my-sandbox", np.Name)
	assert.Equal(t, "default", np.Namespace)
	assert.Equal(t, "my-sandbox", np.Labels[SandboxNameLabelKey])
	assert.Empty(t, np.Spec.Ingress)
	assert.Empty(t, np.Spec.Egress)
	assert.Contains(t, np.Spec.PolicyTypes, networkingv1.PolicyTypeIngress)
	assert.Contains(t, np.Spec.PolicyTypes, networkingv1.PolicyTypeEgress)
	assert.Equal(t, map[string]string{SandboxNameLabelKey: "my-sandbox"}, np.Spec.PodSelector.MatchLabels)
}

func TestBuildNetworkPolicy_CustomRules(t *testing.T) {
	port := intstr.FromInt32(8080)
	proto := corev1.ProtocolTCP
	spec := &runtimev1alpha1.SandboxNetworkPolicy{
		Ingress: []networkingv1.NetworkPolicyIngressRule{
			{
				Ports: []networkingv1.NetworkPolicyPort{
					{Port: &port, Protocol: &proto},
				},
				From: []networkingv1.NetworkPolicyPeer{
					{
						PodSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{"app": "router"},
						},
					},
				},
			},
		},
		Egress: []networkingv1.NetworkPolicyEgressRule{},
	}

	np := buildNetworkPolicy("test-ns", "sandbox-abc", spec)

	assert.Equal(t, "sandbox-abc", np.Name)
	assert.Equal(t, "test-ns", np.Namespace)
	require.Len(t, np.Spec.Ingress, 1)
	assert.Equal(t, map[string]string{"app": "router"}, np.Spec.Ingress[0].From[0].PodSelector.MatchLabels)
	assert.Empty(t, np.Spec.Egress)
}

func TestCreateNetworkPolicy_Success(t *testing.T) {
	client := fake.NewSimpleClientset()
	np := buildNetworkPolicy("default", "sandbox-1", nil)

	err := createNetworkPolicy(context.Background(), client, np)

	require.NoError(t, err)
	got, err := client.NetworkingV1().NetworkPolicies("default").Get(context.Background(), "sandbox-1", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "sandbox-1", got.Name)
}

func TestCreateNetworkPolicy_Error(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("create", "networkpolicies", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("api server unavailable")
	})
	np := buildNetworkPolicy("default", "sandbox-1", nil)

	err := createNetworkPolicy(context.Background(), client, np)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create network policy")
}

func TestDeleteNetworkPolicy_Success(t *testing.T) {
	np := buildNetworkPolicy("default", "sandbox-1", nil)
	client := fake.NewSimpleClientset(np)

	err := deleteNetworkPolicy(context.Background(), client, "default", "sandbox-1")

	require.NoError(t, err)
	_, err = client.NetworkingV1().NetworkPolicies("default").Get(context.Background(), "sandbox-1", metav1.GetOptions{})
	assert.True(t, apierrors.IsNotFound(err))
}

func TestDeleteNetworkPolicy_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()

	// not-found must be silently ignored
	err := deleteNetworkPolicy(context.Background(), client, "default", "nonexistent")

	require.NoError(t, err)
}

func TestDeleteNetworkPolicy_Error(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.PrependReactor("delete", "networkpolicies", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("permission denied")
	})

	err := deleteNetworkPolicy(context.Background(), client, "default", "sandbox-1")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete network policy")
}
