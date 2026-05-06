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
	"time"
)

// SandboxState represents the state of a sandbox
type SandboxState string

const (
	// SandboxStateRunning indicates the sandbox is running
	SandboxStateRunning SandboxState = "running"
	// SandboxStatePaused indicates the sandbox is paused (not supported in Phase 1)
	SandboxStatePaused SandboxState = "paused"
)

// NewSandbox represents the request body for creating a new sandbox
type NewSandbox struct {
	TemplateID          string                 `json:"templateID" binding:"required"`
	Timeout             int                    `json:"timeout,omitempty"` // seconds, default: 900
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
	EnvVars             map[string]string      `json:"envVars,omitempty"`
	AutoPause           bool                   `json:"autoPause,omitempty"`
	AllowInternetAccess bool                   `json:"allowInternetAccess,omitempty"`
	Secure              bool                   `json:"secure,omitempty"`
}

// Sandbox represents a created sandbox response
type Sandbox struct {
	// Note: clientID identifies the API key owner (from namespace:clientID mapping)
	// sandboxID identifies the specific sandbox instance (unique per sandbox)
	ClientID           string `json:"clientID"`
	EnvdVersion        string `json:"envdVersion"`
	SandboxID          string `json:"sandboxID"`
	TemplateID         string `json:"templateID"`
	Alias              string `json:"alias,omitempty"`
	Domain             string `json:"domain,omitempty"`
	EnvdAccessToken    string `json:"envdAccessToken,omitempty"`
	TrafficAccessToken string `json:"trafficAccessToken,omitempty"`
}

// ListedSandbox represents a sandbox in the list response
type ListedSandbox struct {
	ClientID    string                 `json:"clientID"`
	CPUCount    int                    `json:"cpuCount"`
	DiskSizeMB  int                    `json:"diskSizeMB"`
	EndAt       time.Time              `json:"endAt"`
	EnvdVersion string                 `json:"envdVersion"`
	MemoryMB    int                    `json:"memoryMB"`
	SandboxID   string                 `json:"sandboxID"`
	StartedAt   time.Time              `json:"startedAt"`
	State       SandboxState           `json:"state"`
	TemplateID  string                 `json:"templateID"`
	Alias       string                 `json:"alias,omitempty"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`
}

// SandboxDetail represents detailed sandbox information
type SandboxDetail struct {
	Sandbox
	CPUCount            int                    `json:"cpuCount"`
	MemoryMB            int                    `json:"memoryMB"`
	DiskSizeMB          int                    `json:"diskSizeMB"`
	StartedAt           time.Time              `json:"startedAt"`
	EndAt               time.Time              `json:"endAt"`
	State               SandboxState           `json:"state"`
	AllowInternetAccess bool                   `json:"allowInternetAccess,omitempty"`
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}

// TimeoutRequest represents the request body for setting sandbox timeout
type TimeoutRequest struct {
	Timeout int `json:"timeout" binding:"required"` // seconds
}

// RefreshRequest represents the request body for refreshing sandbox
type RefreshRequest struct {
	Timeout int `json:"timeout,omitempty"` // seconds to add
}

// Error represents an error response
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ErrorCode represents E2B API error codes
type ErrorCode int

const (
	// ErrInvalidRequest represents a 400 bad request error
	ErrInvalidRequest ErrorCode = 400
	// ErrUnauthorized represents a 401 unauthorized error
	ErrUnauthorized ErrorCode = 401
	// ErrNotFound represents a 404 not found error
	ErrNotFound ErrorCode = 404
	// ErrConflict represents a 409 conflict error
	ErrConflict ErrorCode = 409
	// ErrTooManyRequests represents a 429 rate limit exceeded error
	ErrTooManyRequests ErrorCode = 429
	// ErrInternal represents a 500 internal server error
	ErrInternal ErrorCode = 500
	// ErrServiceUnavailable represents a 503 service unavailable error
	ErrServiceUnavailable ErrorCode = 503
)
