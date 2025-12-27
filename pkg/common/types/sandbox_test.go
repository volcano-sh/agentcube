package types

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSandboxInfo_Serialization(t *testing.T) {
	now := time.Now().Round(time.Second)
	info := SandboxInfo{
		Kind:             SandboxKind,
		SandboxID:        "sb-123",
		SandboxNamespace: "default",
		Name:             "test-sandbox",
		EntryPoints: []SandboxEntryPoints{
			{
				Path:     "/",
				Protocol: "HTTP",
				Endpoint: "1.2.3.4:80",
			},
		},
		SessionID: "sess-456",
		CreatedAt: now,
		ExpiresAt: now.Add(time.Hour),
		Status:    "Running",
	}

	data, err := json.Marshal(info)
	assert.NoError(t, err)

	var decoded SandboxInfo
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, info.Kind, decoded.Kind)
	assert.Equal(t, info.SandboxID, decoded.SandboxID)
	assert.Equal(t, info.SandboxNamespace, decoded.SandboxNamespace)
	assert.Equal(t, info.Name, decoded.Name)
	assert.Equal(t, info.EntryPoints, decoded.EntryPoints)
	assert.Equal(t, info.SessionID, decoded.SessionID)
	assert.True(t, info.CreatedAt.Equal(decoded.CreatedAt))
	assert.True(t, info.ExpiresAt.Equal(decoded.ExpiresAt))
	assert.Equal(t, info.Status, decoded.Status)
}

func TestCreateSandboxRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateSandboxRequest
		wantErr bool
		errText string
	}{
		{
			name: "valid agent runtime",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Name:      "test",
				Namespace: "default",
			},
			wantErr: false,
		},
		{
			name: "valid code interpreter",
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
				Kind:      "Invalid",
				Name:      "test",
				Namespace: "default",
			},
			wantErr: true,
			errText: "invalid kind Invalid",
		},
		{
			name: "missing name",
			req: CreateSandboxRequest{
				Kind:      AgentRuntimeKind,
				Namespace: "default",
			},
			wantErr: true,
			errText: "name is required",
		},
		{
			name: "missing namespace",
			req: CreateSandboxRequest{
				Kind: AgentRuntimeKind,
				Name: "test",
			},
			wantErr: true,
			errText: "namespace is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errText != "" {
					assert.Contains(t, err.Error(), tt.errText)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCreateSandboxResponse_Serialization(t *testing.T) {
	resp := CreateSandboxResponse{
		SessionID:   "sess-123",
		SandboxID:   "sb-456",
		SandboxName: "test-sb",
		EntryPoints: []SandboxEntryPoints{
			{
				Path:     "/init",
				Protocol: "HTTP",
				Endpoint: "1.2.3.4:8080",
			},
		},
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded CreateSandboxResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, resp, decoded)
}
