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
	"time"
)

const SandboxStateRunning = "running"

// CreateSandboxRequest matches E2B POST /v2/sandboxes and POST /sandboxes payloads.
type CreateSandboxRequest struct {
	TemplateID               string            `json:"templateID" binding:"required"`
	Timeout                  int               `json:"timeout,omitempty"`
	AutoPause                bool              `json:"autoPause,omitempty"`
	AllowInternetAccess      *bool             `json:"allowInternetAccess,omitempty"`
	AllowInternetAccessSnake *bool             `json:"allow_internet_access,omitempty"`
	Metadata                 map[string]any    `json:"metadata,omitempty"`
	EnvVars                  map[string]string `json:"envVars,omitempty"`
	Network                  json.RawMessage   `json:"network,omitempty"`
}

// ConnectSandboxRequest matches E2B POST /v2/sandboxes/{id}/connect payloads.
type ConnectSandboxRequest struct {
	Timeout int `json:"timeout,omitempty"`
}

// TimeoutRequest matches E2B POST /sandboxes/{id}/timeout payloads.
type TimeoutRequest struct {
	Timeout int `json:"timeout" binding:"required"`
}

// Sandbox matches E2B Sandbox response objects returned by create and connect.
type Sandbox struct {
	TemplateID      string  `json:"templateID"`
	SandboxID       string  `json:"sandboxID"`
	ClientID        string  `json:"clientID"`
	EnvdVersion     string  `json:"envdVersion"`
	Alias           string  `json:"alias,omitempty"`
	EnvdAccessToken string  `json:"envdAccessToken,omitempty"`
	Domain          *string `json:"domain,omitempty"`
}

// ListedSandbox matches E2B ListedSandbox items returned by GET /v2/sandboxes and GET /sandboxes.
type ListedSandbox struct {
	TemplateID  string            `json:"templateID"`
	SandboxID   string            `json:"sandboxID"`
	ClientID    string            `json:"clientID"`
	StartedAt   time.Time         `json:"startedAt"`
	EndAt       time.Time         `json:"endAt"`
	CPUCount    int               `json:"cpuCount"`
	MemoryMB    int               `json:"memoryMB"`
	DiskSizeMB  int               `json:"diskSizeMB"`
	State       string            `json:"state"`
	EnvdVersion string            `json:"envdVersion"`
	Alias       string            `json:"alias,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

// SandboxDetail matches E2B SandboxDetail returned by GET /sandboxes/{id}.
type SandboxDetail struct {
	Sandbox
	StartedAt  time.Time         `json:"startedAt"`
	EndAt      time.Time         `json:"endAt"`
	CPUCount   int               `json:"cpuCount"`
	MemoryMB   int               `json:"memoryMB"`
	DiskSizeMB int               `json:"diskSizeMB"`
	State      string            `json:"state"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// Template matches E2B Template items returned by GET /templates and GET /templates/{id}.
type Template struct {
	TemplateID  string    `json:"templateID"`
	BuildID     string    `json:"buildID"`
	CPUCount    int       `json:"cpuCount"`
	MemoryMB    int       `json:"memoryMB"`
	DiskSizeMB  int       `json:"diskSizeMB"`
	Public      bool      `json:"public"`
	Aliases     []string  `json:"aliases"`
	Names       []string  `json:"names"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	EnvdVersion string    `json:"envdVersion"`
	BuildStatus string    `json:"buildStatus"`
	Description string    `json:"description,omitempty"`
}

// ErrorResponse matches the E2B REST API error format.
type ErrorResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
