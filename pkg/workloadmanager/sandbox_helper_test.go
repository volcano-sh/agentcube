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
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
)

const testPodIP = "10.0.0.1"

func TestBuildSandboxPlaceHolder(t *testing.T) {
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
		},
	}

	entry := &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "test-session-123",
	}

	result := buildSandboxPlaceHolder(sandbox, entry)

	assert.NotNil(t, result)
	assert.Equal(t, types.AgentRuntimeKind, result.Kind)
	assert.Equal(t, "test-session-123", result.SessionID)
	assert.Equal(t, "default", result.SandboxNamespace)
	assert.Equal(t, "test-sandbox", result.Name)
	assert.Equal(t, "creating", result.Status)
	assert.WithinDuration(t, time.Now().Add(DefaultSandboxTTL), result.ExpiresAt, 1*time.Second)
}

func TestBuildSandboxPlaceHolder_CodeInterpreter(t *testing.T) {
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-sandbox",
			Namespace: "test-ns",
		},
	}

	entry := &sandboxEntry{
		Kind:      types.CodeInterpreterKind,
		SessionID: "ci-session-456",
	}

	result := buildSandboxPlaceHolder(sandbox, entry)

	assert.Equal(t, types.CodeInterpreterKind, result.Kind)
	assert.Equal(t, "ci-session-456", result.SessionID)
	assert.Equal(t, "test-ns", result.SandboxNamespace)
	assert.Equal(t, "ci-sandbox", result.Name)
}

func TestBuildSandboxInfo(t *testing.T) {
	now := time.Now()
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-sandbox",
			Namespace:         "default",
			UID:               "test-uid-123",
			CreationTimestamp: metav1.NewTime(now),
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(sandboxv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	podIP := testPodIP
	entry := &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "test-session-123",
		Ports: []runtimev1alpha1.TargetPort{
			{
				Port:       8080,
				Protocol:   runtimev1alpha1.ProtocolTypeHTTP,
				PathPrefix: "/api",
			},
			{
				Port:       9090,
				Protocol:   runtimev1alpha1.ProtocolTypeHTTP,
				PathPrefix: "/metrics",
			},
		},
	}

	result := buildSandboxInfo(sandbox, podIP, entry)

	assert.NotNil(t, result)
	assert.Equal(t, types.AgentRuntimeKind, result.Kind)
	assert.Equal(t, "test-uid-123", result.SandboxID)
	assert.Equal(t, "test-sandbox", result.Name)
	assert.Equal(t, "default", result.SandboxNamespace)
	assert.Equal(t, "test-session-123", result.SessionID)
	assert.Equal(t, "running", result.Status)
	assert.Equal(t, now, result.CreatedAt)
	assert.WithinDuration(t, now.Add(DefaultSandboxTTL), result.ExpiresAt, 1*time.Second)

	// Verify entry points
	assert.Len(t, result.EntryPoints, 2)
	assert.Equal(t, "/api", result.EntryPoints[0].Path)
	assert.Equal(t, "HTTP", result.EntryPoints[0].Protocol)
	assert.Equal(t, testPodIP+":8080", result.EntryPoints[0].Endpoint)

	assert.Equal(t, "/metrics", result.EntryPoints[1].Path)
	assert.Equal(t, "HTTP", result.EntryPoints[1].Protocol)
	assert.Equal(t, testPodIP+":9090", result.EntryPoints[1].Endpoint)
}

func TestBuildSandboxInfo_WithShutdownTime(t *testing.T) {
	now := time.Now()
	shutdownTime := now.Add(2 * time.Hour)
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-sandbox",
			Namespace:         "default",
			UID:               "test-uid-123",
			CreationTimestamp: metav1.NewTime(now),
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			ShutdownTime: &metav1.Time{Time: shutdownTime},
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(sandboxv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	podIP := testPodIP
	entry := &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "test-session-123",
		Ports:     []runtimev1alpha1.TargetPort{},
	}

	result := buildSandboxInfo(sandbox, podIP, entry)

	assert.WithinDuration(t, shutdownTime, result.ExpiresAt, 1*time.Second)
}

func TestBuildSandboxInfo_NoPorts(t *testing.T) {
	now := time.Now()
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-sandbox",
			Namespace:         "default",
			UID:               "test-uid-123",
			CreationTimestamp: metav1.NewTime(now),
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(sandboxv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	podIP := testPodIP
	entry := &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "test-session-123",
		Ports:     []runtimev1alpha1.TargetPort{},
	}

	result := buildSandboxInfo(sandbox, podIP, entry)

	assert.Empty(t, result.EntryPoints)
}

func TestBuildSandboxInfo_EmptyPodIP(t *testing.T) {
	now := time.Now()
	sandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-sandbox",
			Namespace:         "default",
			UID:               "test-uid-123",
			CreationTimestamp: metav1.NewTime(now),
		},
		Status: sandboxv1alpha1.SandboxStatus{
			Conditions: []metav1.Condition{
				{
					Type:   string(sandboxv1alpha1.SandboxConditionReady),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	podIP := ""
	entry := &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "test-session-123",
		Ports: []runtimev1alpha1.TargetPort{
			{
				Port:       8080,
				Protocol:   runtimev1alpha1.ProtocolTypeHTTP,
				PathPrefix: "/api",
			},
		},
	}

	result := buildSandboxInfo(sandbox, podIP, entry)

	assert.Equal(t, ":8080", result.EntryPoints[0].Endpoint)
}

func TestGetSandboxStatus(t *testing.T) {
	tests := []struct {
		name     string
		sandbox  *sandboxv1alpha1.Sandbox
		expected string
	}{
		{
			name: "ready condition true",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: "running",
		},
		{
			name: "ready condition false",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expected: "unknown",
		},
		{
			name: "ready condition unknown",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionUnknown,
						},
					},
				},
			},
			expected: "unknown",
		},
		{
			name: "no conditions",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{},
				},
			},
			expected: "unknown",
		},
		{
			name: "nil conditions",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: nil,
				},
			},
			expected: "unknown",
		},
		{
			name: "other condition type",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "OtherCondition",
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: "unknown",
		},
		{
			name: "multiple conditions with ready true",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "OtherCondition",
							Status: metav1.ConditionFalse,
						},
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: "running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSandboxStatus(tt.sandbox)
			assert.Equal(t, tt.expected, result)
		})
	}
}
