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

package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
)

func TestCreateSandboxRequest_Validate(t *testing.T) {
	tests := []struct {
		name      string
		req       CreateSandboxRequest
		wantError bool
		errorMsg  string
	}{
		{
			name: "valid AgentRuntime request",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "default",
				Name:      "test-agent",
			},
			wantError: false,
		},
		{
			name: "valid CodeInterpreter request",
			req: CreateSandboxRequest{
				Kind:      CodeInterpreterKind,
				Namespace: "default",
				Name:      "test-ci",
				TTL:       ptr.To[int64](60),
			},
			wantError: false,
		},
		{
			name: "zero ttl",
			req: CreateSandboxRequest{
				Kind:      CodeInterpreterKind,
				Namespace: "default",
				Name:      "test-ci",
				TTL:       ptr.To[int64](0),
			},
			wantError: true,
			errorMsg:  "ttl must be greater than zero",
		},
		{
			name: "ttl overflows time.Duration",
			req: CreateSandboxRequest{
				Kind:      CodeInterpreterKind,
				Namespace: "default",
				Name:      "test-ci",
				TTL:       ptr.To[int64](1 << 62),
			},
			wantError: true,
			errorMsg:  "ttl exceeds the maximum supported duration",
		},
		{
			name: "invalid kind",
			req: CreateSandboxRequest{
				Kind:      "InvalidKind",
				Namespace: "default",
				Name:      "test",
			},
			wantError: true,
			errorMsg:  "invalid kind",
		},
		{
			name: "missing namespace",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "",
				Name:      "test-agent",
			},
			wantError: true,
			errorMsg:  "namespace is required",
		},
		{
			name: "missing name",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "default",
				Name:      "",
			},
			wantError: true,
			errorMsg:  "name is required",
		},
		{
			name: "empty string namespace",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "",
				Name:      "test",
			},
			wantError: true,
			errorMsg:  "namespace is required",
		},
		{
			name: "empty string name",
			req: CreateSandboxRequest{
				Kind:      CodeInterpreterKind,
				Namespace: "default",
				Name:      "",
			},
			wantError: true,
			errorMsg:  "name is required",
		},
		{
			name: "whitespace namespace",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "   ",
				Name:      "test",
			},
			wantError: false, // Whitespace is not trimmed, so it's considered non-empty
		},
		{
			name: "whitespace name",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "default",
				Name:      "   ",
			},
			wantError: false, // Whitespace is not trimmed, so it's considered non-empty
		},
		{
			name: "both missing namespace and name",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "",
				Name:      "",
			},
			wantError: true,
			errorMsg:  "namespace is required", // First validation error
		},
		{
			name: "invalid kind with valid namespace and name",
			req: CreateSandboxRequest{
				Kind:      "UnknownKind",
				Namespace: "default",
				Name:      "test",
			},
			wantError: true,
			errorMsg:  "invalid kind",
		},
		{
			name: "case sensitive kind check",
			req: CreateSandboxRequest{
				Kind:      "agentruntime", // lowercase
				Namespace: "default",
				Name:      "test",
			},
			wantError: true,
			errorMsg:  "invalid kind",
		},
		{
			name: "case sensitive kind check for CodeInterpreter",
			req: CreateSandboxRequest{
				Kind:      "codeinterpreter", // lowercase
				Namespace: "default",
				Name:      "test",
			},
			wantError: true,
			errorMsg:  "invalid kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateSandboxRequest_RequestedTTL(t *testing.T) {
	assert.Zero(t, (&CreateSandboxRequest{}).RequestedTTL())
	assert.Equal(t, 90*time.Second, (&CreateSandboxRequest{TTL: ptr.To[int64](90)}).RequestedTTL())
}

func TestUpdateSandboxTTLRequest_Validate(t *testing.T) {
	assert.NoError(t, (&UpdateSandboxTTLRequest{TTL: 120}).Validate())
	assert.NoError(t, (&UpdateSandboxTTLRequest{TTL: 120, Mode: "set"}).Validate())
	assert.NoError(t, (&UpdateSandboxTTLRequest{TTL: 120, Mode: "extend"}).Validate())
	assert.Equal(t, 120*time.Second, (&UpdateSandboxTTLRequest{TTL: 120}).RequestedTTL())

	assert.Error(t, (&UpdateSandboxTTLRequest{TTL: 0}).Validate())
	assert.Error(t, (&UpdateSandboxTTLRequest{TTL: -5}).Validate())
	assert.Error(t, (&UpdateSandboxTTLRequest{TTL: 1 << 62}).Validate())
	assert.Error(t, (&UpdateSandboxTTLRequest{TTL: 120, Mode: "invalid"}).Validate())
}

func TestCreateSandboxRequest_NonStringMetadataAndNormalize(t *testing.T) {
	rawJSON := `{"kind":"CodeInterpreter","name":"ci-1","namespace":"default","metadata":{"count":3,"flag":true,"env":"prod","nested":{"a":1},"empty":null}}`
	var req CreateSandboxRequest
	assert.NoError(t, json.Unmarshal([]byte(rawJSON), &req))
	assert.NoError(t, req.Validate())

	normalized := NormalizeMetadata(req.Metadata)
	assert.Equal(t, "3", normalized["count"])
	assert.Equal(t, "true", normalized["flag"])
	assert.Equal(t, "prod", normalized["env"])
	assert.NotContains(t, normalized, "empty")
	assert.Contains(t, normalized, "nested")

	assert.Nil(t, NormalizeMetadata(map[string]any{"onlyNil": nil}))
}
