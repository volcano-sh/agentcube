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
	"regexp"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// validTemplateNameRegex matches valid template name characters (alphanumeric, dash, underscore, dot)
var validTemplateNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9._]*[a-z0-9])?$`)

// Mapper handles conversion between E2B models and AgentCube models
type Mapper struct {
	// envdVersion is the version of envd running in sandboxes
	envdVersion string
	// sandboxDomain is the domain suffix for Sandbox API subdomains
	sandboxDomain string
}

// NewMapper creates a new Mapper instance
func NewMapper(envdVersion string, sandboxDomain ...string) *Mapper {
	if envdVersion == "" {
		envdVersion = "v1.0.0"
	}
	domain := ""
	if len(sandboxDomain) > 0 {
		domain = sandboxDomain[0]
	}
	return &Mapper{
		envdVersion:   envdVersion,
		sandboxDomain: domain,
	}
}

// ToE2BSandbox converts internal SandboxInfo to E2B Sandbox
func (m *Mapper) ToE2BSandbox(sandbox *types.SandboxInfo, clientID string, sandboxDomain string) *Sandbox {
	domain := ""
	if sandbox.E2BSandboxID != "" && sandboxDomain != "" {
		domain = sandbox.E2BSandboxID + "." + sandboxDomain
	}
	return &Sandbox{
		ClientID:    clientID,
		EnvdVersion: m.envdVersion,
		SandboxID:   sandbox.E2BSandboxID,
		TemplateID:  sandbox.TemplateID,
		Domain:      domain,
	}
}

// ToE2BListedSandbox converts internal SandboxInfo to E2B ListedSandbox
func (m *Mapper) ToE2BListedSandbox(sandbox *types.SandboxInfo, clientID string) *ListedSandbox {
	return &ListedSandbox{
		ClientID:    clientID,
		CPUCount:    2,    // Default value - TODO: get from actual resources
		DiskSizeMB:  5120, // Default 5GB - TODO: get from actual resources
		EndAt:       sandbox.ExpiresAt,
		EnvdVersion: m.envdVersion,
		MemoryMB:    4096, // Default 4GB - TODO: get from actual resources
		SandboxID:   sandbox.E2BSandboxID,
		StartedAt:   sandbox.CreatedAt,
		State:       mapStatusToState(sandbox.Status),
		TemplateID:  sandbox.TemplateID,
		Metadata:    map[string]interface{}{"agentcube.kind": sandbox.Kind},
	}
}

// ToE2BSandboxDetail converts internal SandboxInfo to E2B SandboxDetail
func (m *Mapper) ToE2BSandboxDetail(sandbox *types.SandboxInfo, clientID string, sandboxDomain string) *SandboxDetail {
	return &SandboxDetail{
		Sandbox:    *m.ToE2BSandbox(sandbox, clientID, sandboxDomain),
		CPUCount:   2,    // Default value - TODO: get from actual resources
		DiskSizeMB: 5120, // Default 5GB - TODO: get from actual resources
		MemoryMB:   4096, // Default 4GB - TODO: get from actual resources
		StartedAt:  sandbox.CreatedAt,
		EndAt:      sandbox.ExpiresAt,
		State:      mapStatusToState(sandbox.Status),
		Metadata:   map[string]interface{}{"agentcube.kind": sandbox.Kind},
	}
}

// mapStatusToState converts internal status to E2B SandboxState
func mapStatusToState(status string) SandboxState {
	switch status {
	case "paused":
		return SandboxStatePaused
	case "running", "pending", "", "succeeded", "failed":
		// Treat all non-paused states as running for E2B compatibility
		return SandboxStateRunning
	default:
		return SandboxStateRunning
	}
}

// GetEnvdVersion returns the configured envd version
func (m *Mapper) GetEnvdVersion() string {
	return m.envdVersion
}

// CalculateExpiry calculates the expiration time based on timeout
func CalculateExpiry(timeout int) time.Time {
	if timeout <= 0 {
		timeout = 900 // Default 15 minutes as per E2B spec
	}
	return time.Now().Add(time.Duration(timeout) * time.Second)
}
