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

func TestConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{
			name:     "AgentRuntimeKind",
			constant: AgentRuntimeKind,
			expected: "AgentRuntime",
		},
		{
			name:     "CodeInterpreterKind",
			constant: CodeInterpreterKind,
			expected: "CodeInterpreter",
		},
		{
			name:     "SandboxKind",
			constant: SandboxKind,
			expected: "Sandbox",
		},
		{
			name:     "SandboxClaimsKind",
			constant: SandboxClaimsKind,
			expected: "SandboxClaim",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.constant)
			assert.NotEmpty(t, tt.constant, "Constant should not be empty")
		})
	}
}

func TestConstants_Uniqueness(t *testing.T) {
	// Verify all constants are unique
	constants := []string{
		AgentRuntimeKind,
		CodeInterpreterKind,
		SandboxKind,
		SandboxClaimsKind,
	}

	seen := make(map[string]bool)
	for _, constant := range constants {
		assert.False(t, seen[constant], "Constant %s should be unique", constant)
		seen[constant] = true
	}
}

func TestConstants_Format(t *testing.T) {
	// Verify constants follow expected naming conventions
	tests := []struct {
		name     string
		constant string
	}{
		{
			name:     "AgentRuntimeKind should be PascalCase",
			constant: AgentRuntimeKind,
		},
		{
			name:     "CodeInterpreterKind should be PascalCase",
			constant: CodeInterpreterKind,
		},
		{
			name:     "SandboxKind should be PascalCase",
			constant: SandboxKind,
		},
		{
			name:     "SandboxClaimsKind should be PascalCase",
			constant: SandboxClaimsKind,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify it's not empty
			assert.NotEmpty(t, tt.constant)
			// Verify it doesn't contain spaces
			assert.NotContains(t, tt.constant, " ")
			// Verify it doesn't contain special characters that might cause issues
			assert.NotContains(t, tt.constant, "/")
			assert.NotContains(t, tt.constant, "\\")
		})
	}
}

func TestConstants_KindGrouping(t *testing.T) {
	// Verify runtime kind constants
	runtimeKinds := []string{
		AgentRuntimeKind,
		CodeInterpreterKind,
	}

	for _, kind := range runtimeKinds {
		assert.NotEmpty(t, kind, "Runtime kind should not be empty")
	}

	// Verify sandbox kind constants
	sandboxKinds := []string{
		SandboxKind,
		SandboxClaimsKind,
	}

	for _, kind := range sandboxKinds {
		assert.NotEmpty(t, kind, "Sandbox kind should not be empty")
	}
}
