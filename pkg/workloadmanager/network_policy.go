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

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// buildNetworkPolicy constructs a NetworkPolicy for a sandbox pod.
// The policy selects the pod via the SandboxNameLabelKey label.
// If spec is nil, a default deny-all policy is returned to enforce isolation.
func buildNetworkPolicy(namespace, sandboxName string, spec *runtimev1alpha1.SandboxNetworkPolicy) *networkingv1.NetworkPolicy {
	ingress := []networkingv1.NetworkPolicyIngressRule{}
	egress := []networkingv1.NetworkPolicyEgressRule{}

	if spec != nil {
		ingress = spec.Ingress
		egress = spec.Egress
	}

	return &networkingv1.NetworkPolicy{
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
			Ingress: ingress,
			Egress:  egress,
		},
	}
}

// createNetworkPolicy creates a NetworkPolicy for the sandbox using the system client.
func createNetworkPolicy(ctx context.Context, clientset kubernetes.Interface, np *networkingv1.NetworkPolicy) error {
	_, err := clientset.NetworkingV1().NetworkPolicies(np.Namespace).Create(ctx, np, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create network policy %s/%s: %w", np.Namespace, np.Name, err)
	}
	return nil
}

// deleteNetworkPolicy deletes the NetworkPolicy associated with a sandbox.
// Not-found errors are silently ignored.
func deleteNetworkPolicy(ctx context.Context, clientset kubernetes.Interface, namespace, name string) error {
	err := clientset.NetworkingV1().NetworkPolicies(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete network policy %s/%s: %w", namespace, name, err)
	}
	return nil
}
