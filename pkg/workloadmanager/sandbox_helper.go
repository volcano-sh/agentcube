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

package workloadmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"

	"k8s.io/klog/v2"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
)

const (
	defaultSandboxReadyProbeTimeout  = 15 * time.Second
	defaultSandboxReadyProbeInterval = 1 * time.Second
	defaultSandboxReadyDialTimeout   = 1 * time.Second
	defaultPicoInitTimeout           = 5 * time.Second

	sandboxStatusReady    = "ready"
	sandboxStatusNotReady = "not-ready"

	// defaultPicoDPort is the fallback port for the PicoD /init management endpoint.
	defaultPicoDPort uint32 = 8080
)

var sandboxEntrypointDial = func(ctx context.Context, endpoint string, timeout time.Duration) error {
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return err
	}
	return conn.Close()
}

func buildSandboxPlaceHolder(sandboxCR *sandboxv1alpha1.Sandbox, entry *sandboxEntry) *types.SandboxInfo {
	var expiresAt time.Time
	if sandboxCR.Spec.Lifecycle.ShutdownTime != nil {
		expiresAt = sandboxCR.Spec.Lifecycle.ShutdownTime.Time
	} else {
		expiresAt = time.Now().Add(DefaultSandboxTTL)
	}
	idleTimeout := entry.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultSandboxIdleTimeout
	}
	return &types.SandboxInfo{
		Kind:             entry.Kind,
		SessionID:        entry.SessionID,
		SandboxNamespace: sandboxCR.GetNamespace(),
		Name:             sandboxCR.GetName(),
		ExpiresAt:        expiresAt,
		Status:           "creating",
		IdleTimeout:      metav1.Duration{Duration: idleTimeout},
	}
}

func buildSandboxInfo(sandbox *sandboxv1alpha1.Sandbox, podIP string, entry *sandboxEntry) *types.SandboxInfo {
	createdAt := sandbox.GetCreationTimestamp().Time
	expiresAt := createdAt.Add(DefaultSandboxTTL)
	if sandbox.Spec.Lifecycle.ShutdownTime != nil {
		expiresAt = sandbox.Spec.Lifecycle.ShutdownTime.Time
	}
	accesses := make([]types.SandboxEntryPoint, 0, len(entry.Ports))
	for _, port := range entry.Ports {
		accesses = append(accesses, types.SandboxEntryPoint{
			Path:     port.PathPrefix,
			Protocol: string(port.Protocol),
			Endpoint: net.JoinHostPort(podIP, strconv.Itoa(int(port.Port))),
		})
	}
	idleTimeout := entry.IdleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultSandboxIdleTimeout
	}
	return &types.SandboxInfo{
		Kind:              entry.Kind,
		SandboxID:         string(sandbox.GetUID()),
		Name:              sandbox.GetName(),
		SandboxNamespace:  sandbox.GetNamespace(),
		EntryPoints:       accesses,
		SessionID:         entry.SessionID,
		CreatedAt:         createdAt,
		ExpiresAt:         expiresAt,
		Status:            getSandboxStatus(sandbox),
		AuthMode:          string(entry.AuthMode),
		IdleTimeout:       metav1.Duration{Duration: idleTimeout},
		SessionPrivateKey: "", // Explicitly zeroed: json:"-" prevents JSON marshaling, but this guards
		// against accidental key exposure via fmt.Sprintf("%+v", info) style log calls.
	}
}

// getSandboxStatus extracts status from Sandbox CRD conditions.
// Returns sandboxStatusReady when the sandbox is ready, sandboxStatusNotReady otherwise.
func getSandboxStatus(sandbox *sandboxv1alpha1.Sandbox) string {
	for _, condition := range sandbox.Status.Conditions {
		if condition.Type == string(sandboxv1alpha1.SandboxConditionReady) && condition.Status == metav1.ConditionTrue {
			return sandboxStatusReady
		}
	}
	return sandboxStatusNotReady
}

func (s *Server) waitForSandboxEntryPointsReady(ctx context.Context, podIP string, entry *sandboxEntry) error {
	if entry == nil || len(entry.Ports) == 0 {
		return nil
	}

	probeTimeout := defaultSandboxReadyProbeTimeout
	probeInterval := defaultSandboxReadyProbeInterval
	if s != nil && s.config != nil {
		if s.config.SandboxReadyProbeTimeout > 0 {
			probeTimeout = s.config.SandboxReadyProbeTimeout
		}
		if s.config.SandboxReadyProbeInterval > 0 {
			probeInterval = s.config.SandboxReadyProbeInterval
		}
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	var lastErr error
	for {
		lastErr = probeSandboxEntryPoints(probeCtx, podIP, entry.Ports, probeInterval)
		if lastErr == nil {
			return nil
		}

		select {
		case <-probeCtx.Done():
			return fmt.Errorf("sandbox entrypoints not ready before timeout: %w", lastErr)
		case <-time.After(probeInterval):
		}
	}
}

func probeSandboxEntryPoints(ctx context.Context, podIP string, ports []runtimev1alpha1.TargetPort, probeInterval time.Duration) error {
	dialTimeout := probeInterval
	if dialTimeout <= 0 || dialTimeout > defaultSandboxReadyDialTimeout {
		dialTimeout = defaultSandboxReadyDialTimeout
	}

	for _, port := range ports {
		endpoint := net.JoinHostPort(podIP, strconv.Itoa(int(port.Port)))
		if err := sandboxEntrypointDial(ctx, endpoint, dialTimeout); err != nil {
			return fmt.Errorf("entrypoint %s not reachable: %w", endpoint, err)
		}
	}

	return nil
}

// picodInitPortName is the well-known port name used for PicoD's management HTTP API.
// The /init call is always sent here, regardless of what other ports are present.
const picodInitPortName = "picod"

// findPicoDInitPort returns the port to use for the /init call.
// It prefers a port whose name matches picodInitPortName; falls back to port 8080.
func findPicoDInitPort(ports []runtimev1alpha1.TargetPort) uint32 {
	for _, p := range ports {
		if p.Name == picodInitPortName {
			return p.Port
		}
	}
	// Fallback: use defaultPicoDPort. The absence of a named port is a misconfiguration —
	// log a warning so operators can fix the runtime spec.
	klog.Warningf("no port named %q found in sandbox entry; using fallback port %d for /init", picodInitPortName, defaultPicoDPort)
	return defaultPicoDPort
}

func (s *Server) initializePicoD(ctx context.Context, podIP string, entry *sandboxEntry) error {
	if entry == nil || entry.AuthMode != runtimev1alpha1.AuthModePicoD {
		return nil
	}
	if s.bootstrapAuth == nil {
		return fmt.Errorf("initializePicoD: bootstrapAuth is not configured")
	}
	if s.httpClient == nil {
		return fmt.Errorf("initializePicoD: httpClient is not configured")
	}

	// Use the configured timeout so operators can tune it; fall back to the
	// compile-time default for tests where s.config may be nil.
	initTimeout := defaultPicoInitTimeout
	if s.config != nil && s.config.PicoInitTimeout > 0 {
		initTimeout = s.config.PicoInitTimeout
	}
	initCtx, cancel := context.WithTimeout(ctx, initTimeout)
	defer cancel()

	port := findPicoDInitPort(entry.Ports)
	endpoint := fmt.Sprintf("http://%s:%d/init", podIP, port)

	privPEM, pubPEM, err := GenerateSessionKeyPair()
	if err != nil {
		return fmt.Errorf("failed to generate session key pair: %w", err)
	}

	// Use the struct-based manager (not a global function) so tests can inject
	// an isolated BootstrapAuthManager instance.
	token, err := s.bootstrapAuth.GenerateInitJWT(entry.SessionID, pubPEM)
	if err != nil {
		return fmt.Errorf("failed to generate init JWT: %w", err)
	}

	payload := map[string]string{"token": token}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal /init request for %s: %w", endpoint, err)
	}

	resp, err := s.sendInitRequestWithRetry(initCtx, endpoint, bodyBytes)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 409 Conflict is NOT treated as success. A retry would have generated a
		// NEW key pair; if PicoD already holds an earlier public key, returning
		// success here would persist the wrong private key and break every
		// subsequent request with a 401. Treat 409 as a terminal error so the
		// sandbox is rolled back and re-created with a clean key pair.
		var errResp struct {
			Error string `json:"error"`
		}
		if decErr := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&errResp); decErr == nil && errResp.Error != "" {
			return fmt.Errorf("POST /init %s returned %s: %s", endpoint, resp.Status, errResp.Error)
		}
		return fmt.Errorf("POST /init %s returned status: %s", endpoint, resp.Status)
	}

	// Assign SessionPrivateKey ONLY after confirmed 200 OK or 409 (already initialized).
	// This prevents a partial-init state where the Router holds a key
	// that PicoD never accepted.
	entry.SessionPrivateKey = privPEM
	return nil
}

func (s *Server) sendInitRequestWithRetry(ctx context.Context, endpoint string, bodyBytes []byte) (*http.Response, error) {
	var resp *http.Response
	var err error
	maxRetries := 3

	for i := 0; i < maxRetries; i++ {
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to build /init request for %s: %w", endpoint, err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = s.httpClient.Do(req)
		if err == nil {
			return resp, nil
		}
		klog.V(4).Infof("POST /init failed on attempt %d/%d: %v", i+1, maxRetries, err)
		if i < maxRetries-1 {
			// Add jitter (400–700ms) to avoid thundering herd when multiple
			// WorkloadManager replicas provision sandboxes concurrently.
			//nolint:gosec // non-cryptographic jitter for backoff
			jitter := time.Duration(400+rand.Intn(300)) * time.Millisecond
			select {
			case <-time.After(jitter):
			case <-ctx.Done():
				return nil, fmt.Errorf("POST /init canceled during backoff: %w", ctx.Err())
			}
		}
	}
	return nil, fmt.Errorf("POST /init failed after %d attempts: %w", maxRetries, err)
}
