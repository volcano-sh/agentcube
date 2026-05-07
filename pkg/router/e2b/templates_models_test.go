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
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTemplateStateConstants(t *testing.T) {
	tests := []struct {
		name     string
		state    TemplateState
		expected string
	}{
		{"ready", TemplateStateReady, "ready"},
		{"error", TemplateStateError, "error"},
		{"building", TemplateStateBuilding, "building"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.state))
		})
	}
}

func TestIsValidTemplateState(t *testing.T) {
	tests := []struct {
		name     string
		state    TemplateState
		expected bool
	}{
		{"valid ready", TemplateStateReady, true},
		{"valid error", TemplateStateError, true},
		{"valid building", TemplateStateBuilding, true},
		{"invalid empty", "", false},
		{"invalid unknown", "unknown", false},
		{"invalid running", "running", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsValidTemplateState(tt.state)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCreateTemplateRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     CreateTemplateRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid request with only name",
			req: CreateTemplateRequest{
				Name: "my-template",
			},
			wantErr: false,
		},
		{
			name: "valid request with all fields",
			req: CreateTemplateRequest{
				Name:         "my-template",
				Description:  "Test description",
				Dockerfile:   "FROM python:3.9",
				StartCommand: "python app.py",
				Aliases:      []string{"alias1", "alias2"},
				Public:       true,
				MemoryMB:     4096,
				VCPUCount:    2,
			},
			wantErr: false,
		},
		{
			name: "invalid - empty name",
			req: CreateTemplateRequest{
				Name: "",
			},
			wantErr: true,
			errMsg:  "name is required",
		},
		{
			name: "invalid - negative memory",
			req: CreateTemplateRequest{
				Name:     "my-template",
				MemoryMB: -1,
			},
			wantErr: true,
			errMsg:  "memoryMB must be non-negative",
		},
		{
			name: "invalid - negative cpu",
			req: CreateTemplateRequest{
				Name:      "my-template",
				VCPUCount: -2,
			},
			wantErr: true,
			errMsg:  "cpuCount must be non-negative",
		},
		{
			name: "valid - zero memory and cpu",
			req: CreateTemplateRequest{
				Name:      "my-template",
				MemoryMB:  0,
				VCPUCount: 0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestTemplateJSONSerialization(t *testing.T) {
	now := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	template := Template{
		TemplateID:   "my-template",
		Name:         "my-template",
		Description:  "Test template",
		Aliases:      []string{"alias1", "alias2"},
		CreatedAt:    now,
		UpdatedAt:    now,
		Public:       true,
		State:        TemplateStateReady,
		MemoryMB:     4096,
		VCPUCount:    2,
		StartCommand: "python app.py",
	}

	// Test marshaling
	data, err := json.Marshal(template)
	assert.NoError(t, err)

	// Verify JSON contains expected fields
	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"templateID":"my-template"`)
	assert.Contains(t, jsonStr, `"name":"my-template"`)
	assert.Contains(t, jsonStr, `"description":"Test template"`)
	assert.Contains(t, jsonStr, `"aliases":["alias1","alias2"]`)
	assert.Contains(t, jsonStr, `"public":true`)
	assert.Contains(t, jsonStr, `"state":"ready"`)
	assert.Contains(t, jsonStr, `"memoryMB":4096`)
	assert.Contains(t, jsonStr, `"vcpuCount":2`)
	assert.Contains(t, jsonStr, `"startCommand":"python app.py"`)

	// Test unmarshaling
	var decoded Template
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, template.TemplateID, decoded.TemplateID)
	assert.Equal(t, template.Name, decoded.Name)
	assert.Equal(t, template.Description, decoded.Description)
	assert.Equal(t, template.Aliases, decoded.Aliases)
	assert.Equal(t, template.Public, decoded.Public)
	assert.Equal(t, template.State, decoded.State)
	assert.Equal(t, template.MemoryMB, decoded.MemoryMB)
	assert.Equal(t, template.VCPUCount, decoded.VCPUCount)
	assert.Equal(t, template.StartCommand, decoded.StartCommand)
}

func TestTemplateJSONSerialization_OmittedFields(t *testing.T) {
	// Template with minimal fields
	template := Template{
		TemplateID: "minimal",
		Name:       "minimal",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		State:      TemplateStateReady,
	}

	data, err := json.Marshal(template)
	assert.NoError(t, err)

	jsonStr := string(data)
	// These should be present
	assert.Contains(t, jsonStr, `"templateID"`)
	assert.Contains(t, jsonStr, `"name"`)
	assert.Contains(t, jsonStr, `"state"`)

	// These should be omitted (zero values)
	assert.NotContains(t, jsonStr, `"description"`)
	assert.NotContains(t, jsonStr, `"aliases"`)
	assert.NotContains(t, jsonStr, `"memoryMB":0`)
	assert.NotContains(t, jsonStr, `"vcpuCount":0`)
	assert.NotContains(t, jsonStr, `"startCommand"`)
}

func TestCreateTemplateRequestJSON(t *testing.T) {
	req := CreateTemplateRequest{
		Name:         "my-template",
		Description:  "Test template",
		Dockerfile:   "FROM python:3.9\nRUN pip install pandas",
		StartCommand: "python app.py",
		Aliases:      []string{"alias1", "alias2"},
		Public:       true,
		MemoryMB:     4096,
		VCPUCount:    2,
	}

	// Test marshaling
	data, err := json.Marshal(req)
	assert.NoError(t, err)

	// Verify JSON
	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"name":"my-template"`)
	assert.Contains(t, jsonStr, `"description":"Test template"`)
	assert.Contains(t, jsonStr, `"public":true`)
	assert.Contains(t, jsonStr, `"memoryMB":4096`)
	assert.Contains(t, jsonStr, `"vcpuCount":2`)

	// Test unmarshaling
	var decoded CreateTemplateRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, req.Name, decoded.Name)
	assert.Equal(t, req.Description, decoded.Description)
	assert.Equal(t, req.Dockerfile, decoded.Dockerfile)
	assert.Equal(t, req.StartCommand, decoded.StartCommand)
	assert.Equal(t, req.Aliases, decoded.Aliases)
	assert.Equal(t, req.Public, decoded.Public)
	assert.Equal(t, req.MemoryMB, decoded.MemoryMB)
	assert.Equal(t, req.VCPUCount, decoded.VCPUCount)
}

func TestUpdateTemplateRequestJSON(t *testing.T) {
	public := true

	req := UpdateTemplateRequest{
		Description: strPtr("Updated description"),
		Aliases:     []string{"new-alias"},
		Public:      &public,
	}

	// Test marshaling
	data, err := json.Marshal(req)
	assert.NoError(t, err)

	// Verify JSON
	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"description":"Updated description"`)
	assert.Contains(t, jsonStr, `"public":true`)

	// Test unmarshaling
	var decoded UpdateTemplateRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)
	assert.Equal(t, *req.Description, *decoded.Description)
	assert.Equal(t, req.Aliases, decoded.Aliases)
	assert.Equal(t, *req.Public, *decoded.Public)
}

func TestUpdateTemplateRequestJSON_Partial(t *testing.T) {
	// Only update description
	req := UpdateTemplateRequest{
		Description: strPtr("Only description updated"),
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	// Verify only description is present
	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"description":"Only description updated"`)
	assert.NotContains(t, jsonStr, `"public"`)
	assert.NotContains(t, jsonStr, `"aliases"`)
}

func TestListTemplatesParamsDefaults(t *testing.T) {
	// Test default values
	params := ListTemplatesParams{
		Limit:  100,
		Offset: 0,
	}

	assert.Equal(t, 100, params.Limit)
	assert.Equal(t, 0, params.Offset)
	assert.Nil(t, params.Public)
}

func TestTemplateError(t *testing.T) {
	err := &TemplateError{
		Code:    404,
		Message: "template not found",
	}

	assert.Equal(t, "template not found", err.Error())
}

// Helper function
func strPtr(s string) *string {
	return &s
}
