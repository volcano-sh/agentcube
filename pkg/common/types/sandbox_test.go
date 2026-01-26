/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

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
		name    string
		req     CreateSandboxRequest
		wantErr bool
	}{
		{
			name: "valid agent-runtime",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Name:      "test",
				Namespace: "default",
			},
			wantErr: false,
		},
		{
			name: "valid code-interpreter",
			req: CreateSandboxRequest{
				Kind:      CodeInterpreterKind,
				Name:      "test",
				Namespace: "default",
			},
			wantErr: false,
		},
		{
			name: "invalid kind",
			req: CreateSandboxRequest{
				Kind:      "invalid",
				Name:      "test",
				Namespace: "default",
			},
			wantErr: true,
		},
		{
			name: "missing name",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "default",
			},
			wantErr: true,
		},
		{
			name: "missing namespace",
			req: CreateSandboxRequest{
				Kind: AgentRuntimeKind,
				Name: "test",
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
