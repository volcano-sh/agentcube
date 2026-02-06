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

const sandboxHelperTestPodIP = "10.0.0.1"

// Note: TestBuildSandboxPlaceHolder and TestBuildSandboxPlaceHolder_CodeInterpreter
// removed - they only verified that struct fields match input parameters, which is
// trivial field copying behavior.

func TestBuildSandboxInfo_TableDriven(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		setupSandbox   func() *sandboxv1alpha1.Sandbox
		podIP          string
		entry          *sandboxEntry
		validateResult func(t *testing.T, result *types.SandboxInfo)
	}{
		{
			name: "basic sandbox with ports",
			setupSandbox: func() *sandboxv1alpha1.Sandbox {
				return &sandboxv1alpha1.Sandbox{
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
			},
			podIP: sandboxHelperTestPodIP,
			entry: &sandboxEntry{
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
			},
			validateResult: func(t *testing.T, result *types.SandboxInfo) {
				assert.Equal(t, "running", result.Status)
				assert.Len(t, result.EntryPoints, 2)
				assert.Equal(t, "/api", result.EntryPoints[0].Path)
				assert.Equal(t, sandboxHelperTestPodIP+":8080", result.EntryPoints[0].Endpoint)
				assert.Equal(t, "/metrics", result.EntryPoints[1].Path)
				assert.Equal(t, sandboxHelperTestPodIP+":9090", result.EntryPoints[1].Endpoint)
			},
		},
		{
			name: "sandbox with shutdown time",
			setupSandbox: func() *sandboxv1alpha1.Sandbox {
				shutdownTime := now.Add(2 * time.Hour)
				return &sandboxv1alpha1.Sandbox{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "test-sandbox",
						Namespace:         "default",
						UID:               "test-uid-123",
						CreationTimestamp: metav1.NewTime(now),
					},
					Spec: sandboxv1alpha1.SandboxSpec{
						Lifecycle: sandboxv1alpha1.Lifecycle{
							ShutdownTime: &metav1.Time{Time: shutdownTime},
						},
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
			},
			podIP: sandboxHelperTestPodIP,
			entry: &sandboxEntry{
				Kind:      types.AgentRuntimeKind,
				SessionID: "test-session-123",
				Ports:     []runtimev1alpha1.TargetPort{},
			},
			validateResult: func(t *testing.T, result *types.SandboxInfo) {
				// ShutdownTime is now + 2h in setupSandbox
				expectedShutdown := now.Add(2 * time.Hour)
				assert.WithinDuration(t, expectedShutdown, result.ExpiresAt, 1*time.Second)
			},
		},
		{
			name: "sandbox with no ports",
			setupSandbox: func() *sandboxv1alpha1.Sandbox {
				return &sandboxv1alpha1.Sandbox{
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
			},
			podIP: sandboxHelperTestPodIP,
			entry: &sandboxEntry{
				Kind:      types.AgentRuntimeKind,
				SessionID: "test-session-123",
				Ports:     []runtimev1alpha1.TargetPort{},
			},
			validateResult: func(t *testing.T, result *types.SandboxInfo) {
				assert.Empty(t, result.EntryPoints)
			},
		},
		{
			name: "sandbox with empty pod IP",
			setupSandbox: func() *sandboxv1alpha1.Sandbox {
				return &sandboxv1alpha1.Sandbox{
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
			},
			podIP: "",
			entry: &sandboxEntry{
				Kind:      types.AgentRuntimeKind,
				SessionID: "test-session-123",
				Ports: []runtimev1alpha1.TargetPort{
					{
						Port:       8080,
						Protocol:   runtimev1alpha1.ProtocolTypeHTTP,
						PathPrefix: "/api",
					},
				},
			},
			validateResult: func(t *testing.T, result *types.SandboxInfo) {
				assert.Equal(t, ":8080", result.EntryPoints[0].Endpoint)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sandbox := tt.setupSandbox()
			result := buildSandboxInfo(sandbox, tt.podIP, tt.entry)
			tt.validateResult(t, result)
		})
	}
}

func TestGetSandboxStatus_TableDriven(t *testing.T) {
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
