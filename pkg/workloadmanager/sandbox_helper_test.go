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
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/agent-sandbox/controllers"
)

func TestBuildSandboxInfo(t *testing.T) {
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sandbox",
			Namespace: "default",
			UID:       "uid-123",
			Annotations: map[string]string{
				controllers.SanboxPodNameAnnotation: "pod-1",
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

	entry := &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "sess-123",
		Ports: []runtimev1alpha1.TargetPort{
			{Port: 8080, Protocol: runtimev1alpha1.ProtocolTypeHTTP, PathPrefix: "/api"},
		},
	}

	info := buildSandboxInfo(sb, "10.0.0.1", entry)

	assert.Equal(t, "test-sandbox", info.Name)
	assert.Equal(t, "default", info.SandboxNamespace)
	assert.Equal(t, "uid-123", info.SandboxID)
	assert.Equal(t, "sess-123", info.SessionID)
	assert.Equal(t, types.AgentRuntimeKind, info.Kind)
	assert.Equal(t, "running", info.Status)
	assert.Len(t, info.EntryPoints, 1)
	assert.Equal(t, "/api", info.EntryPoints[0].Path)
	assert.Equal(t, "10.0.0.1:8080", info.EntryPoints[0].Endpoint)
}

func TestBuildSandboxPlaceHolder(t *testing.T) {
	sb := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sb",
			Namespace: "ns-test",
			UID:       "uid-456",
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			ShutdownTime: &metav1.Time{Time: time.Now().Add(time.Hour)},
		},
	}

	entry := &sandboxEntry{
		Kind:      types.CodeInterpreterKind,
		SessionID: "sess-456",
	}

	placeholder := buildSandboxPlaceHolder(sb, entry)

	assert.Equal(t, "test-sb", placeholder.Name)
	assert.Equal(t, "ns-test", placeholder.SandboxNamespace)
	// buildSandboxPlaceHolder doesn't set SandboxID - it's empty
	assert.Equal(t, "", placeholder.SandboxID)
	assert.Equal(t, "sess-456", placeholder.SessionID)
	assert.Equal(t, types.CodeInterpreterKind, placeholder.Kind)
	assert.Equal(t, "creating", placeholder.Status)
	assert.NotZero(t, placeholder.ExpiresAt)
}

func TestGetSandboxStatus(t *testing.T) {
	tests := []struct {
		name       string
		conditions []metav1.Condition
		expected   string
	}{
		{
			name: "ready condition true",
			conditions: []metav1.Condition{
				{Type: string(sandboxv1alpha1.SandboxConditionReady), Status: metav1.ConditionTrue},
			},
			expected: "running",
		},
		{
			name: "ready condition false",
			conditions: []metav1.Condition{
				{Type: string(sandboxv1alpha1.SandboxConditionReady), Status: metav1.ConditionFalse},
			},
			expected: "unknown",
		},
		{
			name:       "no conditions",
			conditions: []metav1.Condition{},
			expected:   "unknown",
		},
		{
			name: "other conditions only",
			conditions: []metav1.Condition{
				{Type: "OtherCondition", Status: metav1.ConditionTrue},
			},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb := &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: tt.conditions,
				},
			}
			assert.Equal(t, tt.expected, getSandboxStatus(sb))
		})
	}
}

func TestExtractUserInfo(t *testing.T) {
	// extractUserInfo reads from request context, not headers
	// This is a simplified test that just verifies the function doesn't panic
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = &http.Request{}

	token, ns, sa, saName := extractUserInfo(c)
	// Without context values set, all should be empty
	assert.Equal(t, "", token)
	assert.Equal(t, "", ns)
	assert.Equal(t, "", sa)
	assert.Equal(t, "", saName)
}

func TestMakeCacheKey(t *testing.T) {
	key := makeCacheKey("my-namespace", "my-sa")
	assert.Equal(t, "my-namespace:my-sa", key)
}
