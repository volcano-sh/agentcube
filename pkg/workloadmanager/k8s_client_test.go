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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	listersv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"

	cubefake "github.com/volcano-sh/agentcube/client-go/clientset/versioned/fake"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

// Helper function to create a pod with owner reference
func createPodWithOwner(name, namespace, sandboxName string, phase corev1.PodPhase, podIP, nodeName string) *corev1.Pod {
	controller := true
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				SandboxNameLabelKey: sandboxName,
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "Sandbox",
					Name:       sandboxName,
					Controller: &controller,
				},
			},
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
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

// TestGetSandboxPodInfo_Success verifies GetSandboxPodInfo returns IP and nodeName when pod is present and valid
func TestGetSandboxPodInfo_Success(t *testing.T) {
	// Setup: Create a mock pod lister with a valid running pod
	pod := createPodWithOwner("test-pod", "test-namespace", "test-sandbox", corev1.PodRunning, "10.0.0.1", "node-1")
	mockPodLister := newMockPodLister()
	mockPodLister.addPod(pod)

	client := &K8sClient{
		podLister: mockPodLister,
	}

	// Execute
	ip, nodeName, err := client.GetSandboxPodInfo(context.Background(), "test-namespace", "test-sandbox", "")

	// Verify
	assert.NoError(t, err, "Expected no error for valid pod")
	assert.Equal(t, "10.0.0.1", ip, "Expected IP to match pod IP")
	assert.Equal(t, "node-1", nodeName, "Expected nodeName to match pod spec")
}

// TestGetSandboxPodInfoReadsNamedPodFromLiveAPI verifies that when a podName is
// provided, GetSandboxPodInfo reads the Pod from the live API server rather than
// the informer cache, so that a stale cache entry cannot reject a ready Sandbox.
func TestGetSandboxPodInfoReadsNamedPodFromLiveAPI(t *testing.T) {
	pod := createPodWithOwner("warm-pool-pod", "test-namespace", "warm-pool-sandbox", corev1.PodRunning, "10.0.0.2", "node-warm")
	pod.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"}

	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/test-namespace/pods/warm-pool-pod" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(pod); err != nil {
			t.Errorf("encode pod response: %v", err)
		}
	}))
	defer apiServer.Close()

	clientset, err := kubernetes.NewForConfig(&rest.Config{Host: apiServer.URL})
	if !assert.NoError(t, err) {
		return
	}
	stalePod := pod.DeepCopy()
	stalePod.Status.Phase = corev1.PodPending
	stalePod.Status.PodIP = ""
	stalePod.Spec.NodeName = ""
	staleLister := newMockPodLister()
	staleLister.addPod(stalePod)
	client := &K8sClient{clientset: clientset, podLister: staleLister}

	ip, nodeName, err := client.GetSandboxPodInfo(context.Background(), "test-namespace", "warm-pool-sandbox", "warm-pool-pod")

	assert.NoError(t, err)
	assert.Equal(t, "10.0.0.2", ip)
	assert.Equal(t, "node-warm", nodeName)
}

// TestGetSandboxPodInfo_PodNotFound verifies GetSandboxPodInfo returns error when pod is not found
func TestGetSandboxPodInfo_PodNotFound(t *testing.T) {
	// Setup: Create a mock pod lister with pod that has wrong label
	mockPodLister := newMockPodLister()
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "test-namespace",
			Labels: map[string]string{
				SandboxNameLabelKey: "other-sandbox",
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
	ip, nodeName, err := client.GetSandboxPodInfo(context.Background(), "test-namespace", "test-sandbox", "")

	// Verify
	assert.Error(t, err, "Expected error when pod not found")
	assert.Empty(t, ip, "Expected empty IP when error occurs")
	assert.Empty(t, nodeName, "Expected empty nodeName when error occurs")
	assert.Contains(t, err.Error(), "no pod found for sandbox test-sandbox", "Error message should indicate pod not found")
}

// TestGetSandboxPodInfo_InvalidPodStatus verifies GetSandboxPodInfo returns error when pod status is invalid
func TestGetSandboxPodInfo_InvalidPodStatus(t *testing.T) {
	// Test cases: pod not running or pod without IP
	testCases := []struct {
		name     string
		phase    corev1.PodPhase
		podIP    string
		nodeName string
		errMsg   string
	}{
		{
			name:     "pod not running",
			phase:    corev1.PodPending,
			podIP:    "10.0.0.1",
			nodeName: "node-1",
			errMsg:   "pod not running yet",
		},
		{
			name:     "pod without IP",
			phase:    corev1.PodRunning,
			podIP:    "",
			nodeName: "node-2",
			errMsg:   "pod IP not assigned yet",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup: Create a mock pod lister with invalid pod status
			pod := createPodWithOwner("test-pod", "test-namespace", "test-sandbox", tc.phase, tc.podIP, tc.nodeName)
			mockPodLister := newMockPodLister()
			mockPodLister.addPod(pod)

			client := &K8sClient{
				podLister: mockPodLister,
			}

			// Execute
			ip, nodeName, err := client.GetSandboxPodInfo(context.Background(), "test-namespace", "test-sandbox", "")

			// Verify
			assert.Error(t, err, "Expected error for invalid pod status")
			assert.Empty(t, ip, "Expected empty IP when error occurs")
			assert.Empty(t, nodeName, "Expected empty nodeName when error occurs")
			assert.Contains(t, err.Error(), tc.errMsg, "Error message should indicate the issue")
		})
	}
}

// TestPatchWorkloadLastNode verifies the last-node annotation is written to the
// correct workload kind, and that an unsupported kind is rejected.
func TestPatchWorkloadLastNode(t *testing.T) {
	const (
		namespace = "test-namespace"
		nodeName  = "node-a"
	)

	t.Run("AgentRuntime", func(t *testing.T) {
		ar := &runtimev1alpha1.AgentRuntime{
			ObjectMeta: metav1.ObjectMeta{Name: "ar-1", Namespace: namespace},
		}
		cs := cubefake.NewSimpleClientset(ar)
		client := &K8sClient{cubeClientset: cs}

		err := client.PatchWorkloadLastNode(context.Background(), namespace, runtimev1alpha1.AgentRuntimeKind, "ar-1", nodeName)
		require.NoError(t, err)

		got, err := cs.RuntimeV1alpha1().AgentRuntimes(namespace).Get(context.Background(), "ar-1", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, nodeName, got.Annotations[LastNodeAnnotationKey])
	})

	t.Run("CodeInterpreter", func(t *testing.T) {
		ci := &runtimev1alpha1.CodeInterpreter{
			ObjectMeta: metav1.ObjectMeta{Name: "ci-1", Namespace: namespace},
		}
		cs := cubefake.NewSimpleClientset(ci)
		client := &K8sClient{cubeClientset: cs}

		err := client.PatchWorkloadLastNode(context.Background(), namespace, runtimev1alpha1.CodeInterpreterKind, "ci-1", nodeName)
		require.NoError(t, err)

		got, err := cs.RuntimeV1alpha1().CodeInterpreters(namespace).Get(context.Background(), "ci-1", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, nodeName, got.Annotations[LastNodeAnnotationKey])
	})

	t.Run("unsupported kind", func(t *testing.T) {
		client := &K8sClient{cubeClientset: cubefake.NewSimpleClientset()}
		err := client.PatchWorkloadLastNode(context.Background(), namespace, "Sandbox", "x", nodeName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unsupported workload kind")
	})

	t.Run("non-existent resource returns wrapped error", func(t *testing.T) {
		// Patch on a missing AgentRuntime produces an API error that is wrapped.
		client := &K8sClient{cubeClientset: cubefake.NewSimpleClientset()}
		err := client.PatchWorkloadLastNode(context.Background(), namespace, runtimev1alpha1.AgentRuntimeKind, "missing", nodeName)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "patch AgentRuntime")
		assert.Contains(t, err.Error(), namespace)
		assert.Contains(t, err.Error(), "missing")
	})
}
