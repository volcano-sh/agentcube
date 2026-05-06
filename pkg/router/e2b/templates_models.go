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
	"errors"
	"time"
)

// Template represents an E2B template
type Template struct {
	TemplateID   string        `json:"templateID"`
	Name         string        `json:"name"`
	Description  string        `json:"description,omitempty"`
	Aliases      []string      `json:"aliases,omitempty"`
	CreatedAt    time.Time     `json:"createdAt"`
	UpdatedAt    time.Time     `json:"updatedAt"`
	Public       bool          `json:"public"`
	State        TemplateState `json:"state"`
	StartCommand string        `json:"startCommand,omitempty"`
	EnvdVersion  string        `json:"envdVersion,omitempty"`
	MemoryMB     int           `json:"memoryMB,omitempty"`
	VCPUCount    int           `json:"vcpuCount,omitempty"`
	Dockerfile   string        `json:"dockerfile,omitempty"`
}

// TemplateState represents the state of a template
type TemplateState string

const (
	// TemplateStateReady indicates the template is ready to use
	TemplateStateReady TemplateState = "ready"
	// TemplateStateError indicates the template is in error state
	TemplateStateError TemplateState = "error"
	// TemplateStateBuilding indicates the template is being built
	TemplateStateBuilding TemplateState = "building"
)

// IsValidTemplateState checks if a template state is valid
func IsValidTemplateState(state TemplateState) bool {
	switch state {
	case TemplateStateReady, TemplateStateError, TemplateStateBuilding:
		return true
	}
	return false
}

// CreateTemplateRequest represents the request body for creating a template
type CreateTemplateRequest struct {
	Name         string   `json:"name" binding:"required"`
	Description  string   `json:"description,omitempty"`
	StartCommand string   `json:"startCommand,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
	Public       bool     `json:"public,omitempty"`
	MemoryMB     int      `json:"memoryMB,omitempty"`
	VCPUCount    int      `json:"vcpuCount,omitempty"`
	Dockerfile   string   `json:"dockerfile,omitempty"`
}

// Validate validates the CreateTemplateRequest
func (r *CreateTemplateRequest) Validate() error {
	if r.Name == "" {
		return errors.New("name is required")
	}
	if r.MemoryMB < 0 {
		return errors.New("memoryMB must be non-negative")
	}
	if r.VCPUCount < 0 {
		return errors.New("vcpuCount must be non-negative")
	}
	return nil
}

// UpdateTemplateRequest represents the request body for updating a template
type UpdateTemplateRequest struct {
	Description *string  `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Public      *bool    `json:"public,omitempty"`
}

// ListTemplatesParams represents query parameters for listing templates
type ListTemplatesParams struct {
	Limit  int   `form:"limit,default=100"`
	Offset int   `form:"offset,default=0"`
	Public *bool `form:"public,omitempty"`
}

// TemplateError represents an error response for template operations
type TemplateError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface
func (e *TemplateError) Error() string {
	return e.Message
}
