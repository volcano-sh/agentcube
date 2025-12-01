package types

import (
	"fmt"
	"time"
)

type SandboxRedis struct {
	SandboxID      string          `json:"sandboxId"`
	SandboxName    string          `json:"sandboxName"`
	Accesses       []SandboxAccess `json:"accesses"`
	SessionID      string          `json:"sessionId"`
	CreatedAt      time.Time       `json:"createdAt"`
	ExpiresAt      time.Time       `json:"expiresAt"`
	LastActivityAt time.Time       `json:"lastActivityAt,omitempty"`
	Status         string          `json:"status"`
}

type SandboxAccess struct {
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"`
}

type CreateSandboxRequest struct {
	Kind      string            `json:"kind"`
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Auth      Auth              `json:"auth"`
	Metadata  map[string]string `json:"metadata"`
}

type Auth struct {
	Type  string `json:"type"`
	Token string `json:"token"`
}

type CreateSandboxResponse struct {
	SessionID   string          `json:"sessionId"`
	SandboxID   string          `json:"sandboxId"`
	SandboxName string          `json:"sandboxName"`
	Accesses    []SandboxAccess `json:"accesses"`
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
