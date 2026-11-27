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

// DefaultSandboxIdleTimeout is used when a session has no configured idle timeout.
const DefaultSandboxIdleTimeout = 15 * time.Minute

type SandboxInfo struct {
	Kind             string              `json:"kind"`
	SandboxID        string              `json:"sandboxId"`
	SandboxNamespace string              `json:"sandboxNamespace"`
	Name             string              `json:"name"`
	EntryPoints      []SandboxEntryPoint `json:"entryPoints"`
	SessionID        string              `json:"sessionId"`
	OwnerID          string              `json:"ownerId,omitempty"`
	CreatedAt        time.Time           `json:"createdAt"`
	ExpiresAt        time.Time           `json:"expiresAt"`
	// IdleTimeout is the per-sandbox idle timeout configured via SessionTimeout on the
	// AgentRuntime or CodeInterpreter spec. It is stored in the JSON blob so activity
	// updates can compute the session's idle expiry deadline.
	// metav1.Duration marshals as a human-readable string (e.g. "15m0s") rather than
	// a raw nanosecond integer, making the persisted JSON unambiguous.
	IdleTimeout metav1.Duration `json:"idleTimeout,omitempty"`
	Status      string          `json:"status"`
}

// IdleExpiryTime returns the deadline for activity at the given time.
// A zero activity time uses the sandbox's creation time.
func (s *SandboxInfo) IdleExpiryTime(at time.Time) time.Time {
	timeout := s.IdleTimeout.Duration
	if timeout == 0 {
		timeout = DefaultSandboxIdleTimeout
	}
	if at.IsZero() {
		at = s.CreatedAt
	}
	return at.Add(timeout)
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
	TTL *int64 `json:"ttl,omitempty"`
}

type CreateSandboxResponse struct {
	Kind        string              `json:"kind"`
	SessionID   string              `json:"sessionId"`
	SandboxID   string              `json:"sandboxId"`
	SandboxName string              `json:"sandboxName"`
	EntryPoints []SandboxEntryPoint `json:"entryPoints"`
	OwnerID     string              `json:"ownerId,omitempty"`
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
