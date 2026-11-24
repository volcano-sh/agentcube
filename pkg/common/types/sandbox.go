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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type SandboxInfo struct {
	Kind             string              `json:"kind"`
	SandboxID        string              `json:"sandboxId"`
	SandboxNamespace string              `json:"sandboxNamespace"`
	Name             string              `json:"name"`
	TemplateName     string              `json:"templateName,omitempty"`
	EntryPoints      []SandboxEntryPoint `json:"entryPoints,omitempty"`
	SessionID        string              `json:"sessionId"`
	OwnerID          string              `json:"ownerId,omitempty"`
	Metadata         map[string]string   `json:"metadata,omitempty"`
	CreatedAt        time.Time           `json:"createdAt"`
	ExpiresAt        time.Time           `json:"expiresAt"`
	// IdleTimeout is the per-sandbox idle timeout configured via SessionTimeout on the
	// AgentRuntime or CodeInterpreter spec. It is stored in the JSON blob so the
	// garbage collector can apply it per-sandbox rather than using a global constant.
	// metav1.Duration marshals as a human-readable string (e.g. "15m0s") rather than
	// a raw nanosecond integer, making the persisted JSON unambiguous.
	IdleTimeout metav1.Duration `json:"idleTimeout,omitempty"`
	// DisableIdleTimeout opts the session out of idle-timeout garbage collection so
	// lifecycle is governed strictly by ExpiresAt.
	DisableIdleTimeout bool   `json:"disableIdleTimeout,omitempty"`
	EnvdVersion        string `json:"envdVersion,omitempty"`
	EnvdAccessToken    string `json:"envdAccessToken,omitempty"`
	TrafficAccessToken string `json:"trafficAccessToken,omitempty"`
	PodIP              string `json:"podIp,omitempty"`
	// LastActivityAt is populated transiently from the store's last-activity sorted set
	// during ListInactiveSandboxes. It is intentionally excluded from JSON serialization.
	LastActivityAt time.Time `json:"-"`
	Status         string    `json:"status"`
}

type SandboxEntryPoint struct {
	Path     string `json:"path"`
	Protocol string `json:"protocol"`
	Endpoint string `json:"endpoint"`
}

type CreateSandboxRequest struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	// TTL is the client-requested maximum session lifetime in seconds. The
	// workload's MaxSessionDuration remains the upper bound.
	TTL                *int64            `json:"ttl,omitempty"`
	Metadata           map[string]any    `json:"metadata,omitempty"`
	EnvVars            map[string]string `json:"envVars,omitempty"`
	DisableIdleTimeout bool              `json:"disableIdleTimeout,omitempty"`
	InitEnvd           bool              `json:"initEnvd,omitempty"`
	EnvdPort           uint32            `json:"envdPort,omitempty"`
	EnvdAccessToken    string            `json:"envdAccessToken,omitempty"`
	TrafficAccessToken string            `json:"trafficAccessToken,omitempty"`
}

type CreateSandboxResponse struct {
	Kind               string              `json:"kind"`
	SessionID          string              `json:"sessionId"`
	SandboxID          string              `json:"sandboxId"`
	SandboxName        string              `json:"sandboxName"`
	EntryPoints        []SandboxEntryPoint `json:"entryPoints"`
	OwnerID            string              `json:"ownerId,omitempty"`
	EnvdVersion        string              `json:"envdVersion,omitempty"`
	EnvdAccessToken    string              `json:"envdAccessToken,omitempty"`
	TrafficAccessToken string              `json:"trafficAccessToken,omitempty"`
	CreatedAt          time.Time           `json:"createdAt,omitempty"`
	ExpiresAt          time.Time           `json:"expiresAt,omitempty"`
}

type UpdateSandboxTTLRequest struct {
	// TTL is the requested lifetime in seconds from now.
	TTL int64 `json:"ttl"`
	// Mode controls how the new expiration is applied:
	// - "set" (default): sets ExpiresAt to now + TTL
	// - "extend": sets ExpiresAt to max(currentExpiresAt, now + TTL)
	Mode string `json:"mode,omitempty"`
}

type UpdateSandboxTTLResponse struct {
	SessionID string    `json:"sessionId"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (r *UpdateSandboxTTLRequest) Validate() error {
	const maxDurationSeconds = (1<<63 - 1) / int64(time.Second)
	if r.TTL <= 0 {
		return fmt.Errorf("ttl must be greater than zero")
	}
	if r.TTL > maxDurationSeconds {
		return fmt.Errorf("ttl exceeds the maximum supported duration")
	}
	switch r.Mode {
	case "", "set", "extend":
	default:
		return fmt.Errorf("invalid ttl mode %q", r.Mode)
	}
	return nil
}

func (r *UpdateSandboxTTLRequest) RequestedTTL() time.Duration {
	return time.Duration(r.TTL) * time.Second
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
	if car.TTL != nil {
		const maxDurationSeconds = (1<<63 - 1) / int64(time.Second)
		if *car.TTL <= 0 {
			return fmt.Errorf("ttl must be greater than zero")
		}
		if *car.TTL > maxDurationSeconds {
			return fmt.Errorf("ttl exceeds the maximum supported duration")
		}
	}
	return nil
}

// RequestedTTL returns the requested session lifetime, or zero when the
// client left lifetime selection to the workload configuration.
func (car *CreateSandboxRequest) RequestedTTL() time.Duration {
	if car.TTL == nil {
		return 0
	}
	return time.Duration(*car.TTL) * time.Second
}

// NormalizeMetadata converts arbitrary JSON metadata values into flat string key-value pairs.
func NormalizeMetadata(raw map[string]any) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if v == nil {
			continue
		}
		if s, ok := v.(string); ok {
			out[k] = s
			continue
		}
		out[k] = fmt.Sprint(v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
