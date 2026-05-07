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

package e2b

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

func TestNewMapper(t *testing.T) {
	tests := []struct {
		name            string
		envdVersion     string
		expectedVersion string
	}{
		{
			name:            "with version",
			envdVersion:     "v2.0.0",
			expectedVersion: "v2.0.0",
		},
		{
			name:            "empty version uses default",
			envdVersion:     "",
			expectedVersion: "v1.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mapper := NewMapper(tt.envdVersion)
			assert.Equal(t, tt.expectedVersion, mapper.GetEnvdVersion())
		})
	}
}

func TestMapper_ToE2BSandbox(t *testing.T) {
	mapper := NewMapper("v1.0.0")

	sandbox := &types.SandboxInfo{
		SandboxID:        "pod-123",
		SandboxNamespace: "test-namespace",
		Name:             "my-template",
		SessionID:        "session-456",
		Status:           "running",
		E2BSandboxID:     "e2b-sandbox-789",
		TemplateID:       "my-template-id",
	}

	result := mapper.ToE2BSandbox(sandbox, "api-key-hash", "sb.e2b.app")

	assert.Equal(t, "api-key-hash", result.ClientID)
	assert.Equal(t, "v1.0.0", result.EnvdVersion)
	assert.Equal(t, "e2b-sandbox-789", result.SandboxID)
	assert.Equal(t, "my-template-id", result.TemplateID)
	assert.Equal(t, "e2b-sandbox-789.sb.e2b.app", result.Domain)
}

func TestMapper_ToE2BListedSandbox(t *testing.T) {
	mapper := NewMapper("v1.0.0")
	now := time.Now()

	sandbox := &types.SandboxInfo{
		SandboxID:        "pod-123",
		SandboxNamespace: "test-namespace",
		Name:             "my-template",
		SessionID:        "session-456",
		CreatedAt:        now,
		ExpiresAt:        now.Add(60 * time.Second),
		Status:           "running",
		E2BSandboxID:     "e2b-sandbox-789",
		TemplateID:       "my-template-id",
		Kind:             "CodeInterpreter",
	}

	result := mapper.ToE2BListedSandbox(sandbox, "api-key-hash")

	assert.Equal(t, "api-key-hash", result.ClientID)
	assert.Equal(t, 2, result.CPUCount)
	assert.Equal(t, 5120, result.DiskSizeMB)
	assert.Equal(t, now.Add(60*time.Second), result.EndAt)
	assert.Equal(t, "v1.0.0", result.EnvdVersion)
	assert.Equal(t, 4096, result.MemoryMB)
	assert.Equal(t, "e2b-sandbox-789", result.SandboxID)
	assert.Equal(t, now, result.StartedAt)
	assert.Equal(t, SandboxStateRunning, result.State)
	assert.Equal(t, "my-template-id", result.TemplateID)
	assert.Equal(t, map[string]interface{}{"agentcube.kind": "CodeInterpreter"}, result.Metadata)
}

func TestMapper_ToE2BSandboxDetail(t *testing.T) {
	mapper := NewMapper("v1.0.0")
	now := time.Now()

	sandbox := &types.SandboxInfo{
		SandboxID:        "pod-123",
		SandboxNamespace: "test-namespace",
		Name:             "my-template",
		SessionID:        "session-456",
		CreatedAt:        now,
		ExpiresAt:        now.Add(60 * time.Second),
		Status:           "running",
		E2BSandboxID:     "e2b-sandbox-789",
		TemplateID:       "my-template-id",
		Kind:             "CodeInterpreter",
		EntryPoints: []types.SandboxEntryPoint{
			{Path: "/", Protocol: "http", Endpoint: "10.0.0.1:8080"},
		},
	}

	result := mapper.ToE2BSandboxDetail(sandbox, "api-key-hash", "sb.e2b.app")

	assert.Equal(t, "api-key-hash", result.ClientID)
	assert.Equal(t, 2, result.CPUCount)
	assert.Equal(t, 5120, result.DiskSizeMB)
	assert.Equal(t, now.Add(60*time.Second), result.EndAt)
	assert.Equal(t, "v1.0.0", result.EnvdVersion)
	assert.Equal(t, 4096, result.MemoryMB)
	assert.Equal(t, "e2b-sandbox-789", result.SandboxID)
	assert.Equal(t, now, result.StartedAt)
	assert.Equal(t, SandboxStateRunning, result.State)
	assert.Equal(t, "my-template-id", result.TemplateID)
	assert.Equal(t, map[string]interface{}{"agentcube.kind": "CodeInterpreter"}, result.Metadata)
	assert.Equal(t, "e2b-sandbox-789.sb.e2b.app", result.Domain)
}

func TestMapStatusToState(t *testing.T) {
	tests := []struct {
		status   string
		expected SandboxState
	}{
		{"running", SandboxStateRunning},
		{"pending", SandboxStateRunning},
		{"", SandboxStateRunning},
		{"succeeded", SandboxStateRunning},
		{"failed", SandboxStateRunning},
		{"paused", SandboxStatePaused},
		{"unknown", SandboxStateRunning},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := mapStatusToState(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateExpiry(t *testing.T) {
	tests := []struct {
		timeout  int
		expected time.Duration
	}{
		{60, 60 * time.Second},
		{300, 300 * time.Second},
		{0, 900 * time.Second},  // Default when 0
		{-1, 900 * time.Second}, // Default when negative
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.timeout)), func(t *testing.T) {
			result := CalculateExpiry(tt.timeout)
			// Check that the result is within a reasonable time window
			expectedTime := time.Now().Add(tt.expected)
			assert.WithinDuration(t, expectedTime, result, time.Second)
		})
	}
}
