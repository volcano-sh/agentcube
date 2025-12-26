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
	"fmt"
	"time"
)

type SandboxInfo struct {
	Kind             string               `json:"kind"`
	SandboxID        string               `json:"sandboxId"`
	SandboxNamespace string               `json:"sandboxNamespace"`
	Name             string               `json:"name"`
	EntryPoints      []SandboxEntryPoints `json:"entryPoints"`
	SessionID        string               `json:"sessionId"`
	CreatedAt        time.Time            `json:"createdAt"`
	ExpiresAt        time.Time            `json:"expiresAt"`
	// LastActivityAt is intentionally omitted from this type.
	// Last activity is tracked in Store via a sorted set index.
	Status string `json:"status"`
}

type SandboxEntryPoints struct {
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"`
}

type CreateSandboxRequest struct {
	Kind               string            `json:"kind"`
	Name               string            `json:"name"`
	Namespace          string            `json:"namespace"`
	Auth               Auth              `json:"auth"`
	Metadata           map[string]string `json:"metadata"`
	PublicKey          string            `json:"publicKey,omitempty"`
	InitTimeoutSeconds int               `json:"initTimeoutSeconds,omitempty"`
}

type Auth struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type CreateSandboxResponse struct {
	SessionID   string               `json:"sessionId"`
	SandboxID   string               `json:"sandboxId"`
	SandboxName string               `json:"sandboxName"`
	EntryPoints []SandboxEntryPoints `json:"entryPoints"`
}

func (car *CreateSandboxRequest) Validate() error {
	switch car.Kind {
	case AgentRuntimeKind:
	case CodeInterpreterKind:
	default:
		return fmt.Errorf("invalid kind %s", car.Kind)
	}
	if car.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if car.Name == "" {
		return fmt.Errorf("name is required")
	}
	return nil
}
