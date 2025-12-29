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
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	listersv1 "k8s.io/client-go/listers/core/v1"
)

// Helper function to create a pod with owner reference
func createPodWithOwner(name, namespace, sandboxName string, phase corev1.PodPhase, podIP string) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"sandbox-name": sandboxName,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Sandbox",
					Name:       sandboxName,
					Controller: &controller,
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: phase,
			PodIP: podIP,
		},
	}
}

// mockPodNamespaceLister is a mock implementation of listersv1.PodNamespaceLister
type mockPodNamespaceLister struct {
	pods []*corev1.Pod
}

func (m *mockPodNamespaceLister) List(selector labels.Selector) ([]*corev1.Pod, error) {
	if selector == nil {
		return m.pods, nil
	}
	result := make([]*corev1.Pod, 0)
	for _, pod := range m.pods {
		if selector.Matches(labels.Set(pod.Labels)) {
			result = append(result, pod)
		}
	}
	return result, nil
}

func (m *mockPodNamespaceLister) Get(name string) (*corev1.Pod, error) {
	for _, pod := range m.pods {
		if pod.Name == name {
			return pod, nil
		}
	}
	return nil, nil
}

// mockPodLister is a mock implementation of listersv1.PodLister
type mockPodLister struct {
	podsByNamespace map[string][]*corev1.Pod
}

func newMockPodLister() *mockPodLister {
	return &mockPodLister{
		podsByNamespace: make(map[string][]*corev1.Pod),
	}
}

func (m *mockPodLister) List(selector labels.Selector) ([]*corev1.Pod, error) {
	result := make([]*corev1.Pod, 0)
	for _, pods := range m.podsByNamespace {
		for _, pod := range pods {
			if selector == nil || selector.Matches(labels.Set(pod.Labels)) {
				result = append(result, pod)
			}
		}
	}
	return result, nil
}

func (m *mockPodLister) Pods(namespace string) listersv1.PodNamespaceLister {
	return &mockPodNamespaceLister{
		pods: m.podsByNamespace[namespace],
	}
}

func (m *mockPodLister) addPod(pod *corev1.Pod) {
	if m.podsByNamespace[pod.Namespace] == nil {
		m.podsByNamespace[pod.Namespace] = make([]*corev1.Pod, 0)
	}
	m.podsByNamespace[pod.Namespace] = append(m.podsByNamespace[pod.Namespace], pod)
}

// TestGetSandboxPodIP_Success verifies GetSandboxPodIP returns IP when pod is present and valid
func TestGetSandboxPodIP_Success(t *testing.T) {
	// Setup: Create a mock pod lister with a valid running pod
	pod := createPodWithOwner("test-pod", "test-namespace", "test-sandbox", corev1.PodRunning, "10.0.0.1")
	mockPodLister := newMockPodLister()
	mockPodLister.addPod(pod)

	client := &K8sClient{
		podLister: mockPodLister,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox", "")

	// Verify
	assert.NoError(t, err, "Expected no error for valid pod")
	assert.Equal(t, "10.0.0.1", ip, "Expected IP to match pod IP")
}

// TestGetSandboxPodIP_PodNotFound verifies GetSandboxPodIP returns error when pod is not found
func TestGetSandboxPodIP_PodNotFound(t *testing.T) {
	// Setup: Create a mock pod lister with pod that has wrong label
	mockPodLister := newMockPodLister()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
			Labels: map[string]string{
				"sandbox-name": "other-sandbox",
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.0.0.1",
		},
	}
	mockPodLister.addPod(pod)

	client := &K8sClient{
		podLister: mockPodLister,
	}

	// Execute
	ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox", "")

	// Verify
	assert.Error(t, err, "Expected error when pod not found")
	assert.Empty(t, ip, "Expected empty IP when error occurs")
	assert.Contains(t, err.Error(), "no pod found for sandbox test-sandbox", "Error message should indicate pod not found")
}

// TestGetSandboxPodIP_InvalidPodStatus verifies GetSandboxPodIP returns error when pod status is invalid
func TestGetSandboxPodIP_InvalidPodStatus(t *testing.T) {
	// Test cases: pod not running or pod without IP
	testCases := []struct {
		name   string
		phase  corev1.PodPhase
		podIP  string
		errMsg string
	}{
		{
			name:   "pod not running",
			phase:  corev1.PodPending,
			podIP:  "10.0.0.1",
			errMsg: "pod not running yet",
		},
		{
			name:   "pod without IP",
			phase:  corev1.PodRunning,
			podIP:  "",
			errMsg: "pod IP not assigned yet",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup: Create a mock pod lister with invalid pod status
			pod := createPodWithOwner("test-pod", "test-namespace", "test-sandbox", tc.phase, tc.podIP)
			mockPodLister := newMockPodLister()
			mockPodLister.addPod(pod)

			client := &K8sClient{
				podLister: mockPodLister,
			}

			// Execute
			ip, err := client.GetSandboxPodIP(context.Background(), "test-namespace", "test-sandbox", "")

			// Verify
			assert.Error(t, err, "Expected error for invalid pod status")
			assert.Empty(t, ip, "Expected empty IP when error occurs")
			assert.Contains(t, err.Error(), tc.errMsg, "Error message should indicate the issue")
		})
	}
}
