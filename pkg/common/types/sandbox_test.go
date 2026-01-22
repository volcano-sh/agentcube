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
	"testing"

	"github.com/stretchr/testify/assert"
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
			},
			wantError: false,
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
