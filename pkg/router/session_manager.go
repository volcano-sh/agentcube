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

package router

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// SessionManager defines the session management behavior on top of Store and the workload manager.
type SessionManager interface {
	// GetSandboxBySession returns the sandbox associated with the given sessionID.
	// When sessionID is empty, it creates a new sandbox by calling the external API.
	// When sessionID is not empty, it queries store for the sandbox.
	GetSandboxBySession(ctx context.Context, sessionID string, namespace string, name string, kind string) (*types.SandboxInfo, error)
}

// manager is the default implementation of the SessionManager interface.
type manager struct {
	storeClient     store.Store
	workloadMgrAddr string
	httpClient      *http.Client
}

// NewSessionManager returns a SessionManager implementation.
// storeClient is used to query sandbox information from store
// workloadMgrAddr is read from the environment variable WORKLOAD_MANAGER_ADDR.
func NewSessionManager(storeClient store.Store) (SessionManager, error) {
	workloadMgrAddr := os.Getenv("WORKLOAD_MANAGER_ADDR")
	if workloadMgrAddr == "" {
		return nil, fmt.Errorf("WORKLOAD_MANAGER_ADDR environment variable is not set")
	}

	return &manager{
		storeClient:     storeClient,
		workloadMgrAddr: workloadMgrAddr,
		httpClient: &http.Client{
			Timeout: 2 * time.Minute, // consistent with manager setting
			Transport: &http.Transport{
				ForceAttemptHTTP2:   true,
				MaxIdleConnsPerHost: 100,
				DisableCompression:  false,
			},
		},
	}, nil
}

// GetSandboxBySession returns the sandbox associated with the given sessionID.
// When sessionID is empty, it creates a new sandbox by calling the external API.
// When sessionID is not empty, it queries store for the sandbox.
func (m *manager) GetSandboxBySession(ctx context.Context, sessionID string, namespace string, name string, kind string) (*types.SandboxInfo, error) {
	// When sessionID is empty, create a new sandbox
	if sessionID == "" {
		return m.createSandbox(ctx, namespace, name, kind)
	}

	// When sessionID is not empty, query store
	sandbox, err := m.storeClient.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, NewSessionNotFoundError(sessionID)
		}
		return nil, fmt.Errorf("failed to get sandbox from store: %w", err)
	}

	return sandbox, nil
}

// createSandbox creates a new sandbox by calling the external workload manager API.
func (m *manager) createSandbox(ctx context.Context, namespace string, name string, kind string) (*types.SandboxInfo, error) {
	// Determine the API endpoint based on kind
	var endpoint string
	switch kind {
	case types.AgentRuntimeKind:
		endpoint = m.workloadMgrAddr + "/v1/agent-runtime"
	case types.CodeInterpreterKind:
		endpoint = m.workloadMgrAddr + "/v1/code-interpreter"
	default:
		return nil, fmt.Errorf("unsupported kind: %s", kind)
	}

	// Prepare the request body
	reqBody := &types.CreateSandboxRequest{
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("failed calling workload manager: %w", err))
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check response status
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, NewSandboxTemplateNotFoundError(namespace, name, kind)
		}
		// Also check for BadRequest with "not found" message (for backward compatibility)
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(respBody), "not found") {
			return nil, NewSandboxTemplateNotFoundError(namespace, name, kind)
		}
		return nil, NewInternalError(fmt.Errorf("workload manager returned status %d", resp.StatusCode))
	}

	// Parse response
	var res types.CreateSandboxResponse
	if err := json.Unmarshal(respBody, &res); err != nil {
		return nil, NewInternalError(fmt.Errorf("failed to unmarshal response: %w", err))
	}

	// Validate response
	if res.SessionID == "" {
		return nil, NewInternalError(fmt.Errorf("response with empty session id from workload manager"))
	}

	// Construct Sandbox Info from response
	sandbox := &types.SandboxInfo{
		SandboxID:   res.SandboxID,
		Name:        res.SandboxName,
		SessionID:   res.SessionID,
		EntryPoints: res.EntryPoints,
	}

	return sandbox, nil
}
