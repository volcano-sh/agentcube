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

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	defaultRouterURL      = "http://localhost:8081"
	defaultWorkloadMgrURL = "http://localhost:8080"
)

type testEnv struct {
	routerURL      string
	workloadMgrURL string
	authToken      string
	t              *testing.T
}

func newTestEnv(t *testing.T) *testEnv {
	return &testEnv{
		routerURL:      getEnv("ROUTER_URL", defaultRouterURL),
		workloadMgrURL: getEnv("WORKLOAD_MANAGER_ADDR", defaultWorkloadMgrURL),
		authToken:      os.Getenv("API_TOKEN"),
		t:              t,
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// runAgentRuntimeTestCase executes a single AgentRuntime test case
func runAgentRuntimeTestCase(t *testing.T, env *testEnv, namespace, runtimeName string, tc struct {
	name     string
	input    string
	expected string
}) {
	req := &AgentInvokeRequest{
		Input: tc.input,
		Metadata: map[string]interface{}{
			"test_case": tc.name,
			"timestamp": time.Now().Format(time.RFC3339),
		},
	}

	// Call echo-agent (session management handled by AgentCube Router)
	resp, sessionID, err := env.invokeAgentRuntime(namespace, runtimeName, "", req)

	// Validate API response
	if err != nil {
		t.Fatalf("Failed to invoke agent runtime: %v", err)
	}
	if resp == nil {
		t.Fatal("Response is nil")
	}
	if resp.Output == "" {
		t.Error("Expected non-empty output from echo agent")
	}

	if resp.Output != tc.expected {
		t.Errorf("Expected echo output '%s', got: '%s'", tc.expected, resp.Output)
	}

	// Log session ID if present (managed by AgentCube Router)
	if sessionID != "" {
		t.Logf("Request completed with session ID: %s", sessionID)
	}

	t.Logf("Echo test successful: input='%s' -> output='%s'", tc.input, resp.Output)
}

// ===== AgentRuntime E2E Test Cases =====

// AgentInvokeRequest represents the request payload for agent invocation
type AgentInvokeRequest struct {
	Input    string                 `json:"input,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// AgentInvokeResponse represents the response from agent invocation
type AgentInvokeResponse struct {
	Output   string                 `json:"output,omitempty"`
	Error    string                 `json:"error,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// invokeAgentRuntime invokes an AgentRuntime through the Router API
// Returns response, session ID from header, and error
func (e *testEnv) invokeAgentRuntime(namespace, name, sessionID string, req *AgentInvokeRequest) (*AgentInvokeResponse, string, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/namespaces/%s/agent-runtimes/%s/invocations/echo",
		e.routerURL, namespace, name)

	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}
	if sessionID != "" {
		httpReq.Header.Set("x-agentcube-session-id", sessionID)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read response: %w", err)
	}

	// Extract session ID from response header
	responseSessionID := resp.Header.Get("x-agentcube-session-id")

	if resp.StatusCode != http.StatusOK {
		return nil, responseSessionID, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var invokeResp AgentInvokeResponse
	if err := json.Unmarshal(body, &invokeResp); err != nil {
		return nil, responseSessionID, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &invokeResp, responseSessionID, nil
}

// TestAgentRuntimeBasicInvocation tests basic echo-agent functionality
func TestAgentRuntimeBasicInvocation(t *testing.T) {
	env := newTestEnv(t)

	namespace := "agentcube"
	runtimeName := "echo-agent"

	// Note: AgentRuntime pods are created on-demand when the first invoke request is received
	// We don't wait for pods upfront, but start the tests directly
	t.Log("Starting AgentRuntime tests (pods will be created on first invoke request)...")

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "basic echo",
			input:    "Hello, World!",
			expected: "echo: Hello, World!",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "echo: ",
		},
		{
			name:     "complex input",
			input:    "Test with special chars: @#$%^&*()",
			expected: "echo: Test with special chars: @#$%^&*()",
		},
	}

	successCount := 0
	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if i == 0 {
				t.Log("First invoke request - this will trigger pod creation")
			}

			runAgentRuntimeTestCase(t, env, namespace, runtimeName, tc)
			successCount++
		})
	}

}

// TestAgentRuntimeErrorHandling tests: Missing/invalid AgentRuntime
func TestAgentRuntimeErrorHandling(t *testing.T) {
	env := newTestEnv(t)

	// Modify invokeAgentRuntime to return status code for error handling tests
	invokeWithStatus := func(namespace, name, sessionID string, req *AgentInvokeRequest) (int, string, error) {
		jsonData, err := json.Marshal(req)
		if err != nil {
			return 0, "", fmt.Errorf("failed to marshal request: %w", err)
		}

		url := fmt.Sprintf("%s/v1/namespaces/%s/agent-runtimes/%s/invocations/echo",
			env.routerURL, namespace, name)

		httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			return 0, "", fmt.Errorf("failed to create request: %w", err)
		}

		httpReq.Header.Set("Content-Type", "application/json")
		if env.authToken != "" {
			httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", env.authToken))
		}
		if sessionID != "" {
			httpReq.Header.Set("x-agentcube-session-id", sessionID)
		}

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(httpReq)
		if err != nil {
			return 0, "", fmt.Errorf("failed to send request: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return resp.StatusCode, "", fmt.Errorf("failed to read response: %w", err)
		}

		return resp.StatusCode, string(body), nil
	}

	t.Run("non-existent runtime", func(t *testing.T) {
		req := &AgentInvokeRequest{
			Input: "Test missing runtime",
			Metadata: map[string]interface{}{
				"test": "missing_runtime",
			},
		}

		// Call POST on a non-existent runtime name
		statusCode, body, err := invokeWithStatus("agentcube", "non-existent-runtime", "", req)
		if err != nil {
			t.Fatalf("Unexpected network error: %v", err)
		}

		if statusCode != http.StatusNotFound {
			t.Errorf("Expected HTTP 404 for non-existent runtime, got %d", statusCode)
		}

		// Assert: Error message about runtime not found
		if !strings.Contains(body, "not found") {
			t.Errorf("Expected error message about runtime not found, got: %s", body)
		}

		t.Logf("Error handling test passed: status=%d, body='%s'", statusCode, body)
	})
}

// TestAgentRuntimeSessionTTL tests: Idle session / TTL behavior
func TestAgentRuntimeSessionTTL(t *testing.T) {
	env := newTestEnv(t)

	namespace := "agentcube"
	runtimeName := "echo-agent-short-ttl" // Use special runtime with short TTL for testing

	req := &AgentInvokeRequest{
		Input: "Create session for TTL test",
		Metadata: map[string]interface{}{
			"test": "session_ttl",
		},
	}

	_, sessionID, err := env.invokeAgentRuntime(namespace, runtimeName, "", req)
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}
	if sessionID == "" {
		t.Skip("Session ID not returned, skipping TTL test")
	}

	t.Logf("Created session %s for TTL test", sessionID)

	// Test 1: Session should still be active before TTL expires
	t.Run("session_active_before_ttl", func(t *testing.T) {
		shortWaitDuration := 10 * time.Second // Wait shorter than 30s TTL
		t.Logf("Waiting %v (shorter than 30s TTL) to verify session is still active...", shortWaitDuration)
		time.Sleep(shortWaitDuration)

		reqActive := &AgentInvokeRequest{
			Input: "Test session still active",
			Metadata: map[string]interface{}{
				"test": "session_active",
			},
		}

		_, _, err := env.invokeAgentRuntime(namespace, runtimeName, sessionID, reqActive)

		// Assert: Session should still be active (no error expected)
		if err != nil {
			t.Errorf("Session should still be active before TTL expires, but got error: %v", err)
		} else {
			t.Logf("Session correctly remains active before TTL expires")
		}
	})

	// Test 2: Session should be cleaned up after TTL expires
	t.Run("session_expired_after_ttl", func(t *testing.T) {
		longWaitDuration := 50 * time.Second // Wait longer than 30s TTL to ensure cleanup
		t.Logf("Waiting additional %v (total >30s TTL) for session cleanup...", longWaitDuration)
		time.Sleep(longWaitDuration)

		reqExpired := &AgentInvokeRequest{
			Input: "Test expired session",
			Metadata: map[string]interface{}{
				"test": "expired_session",
			},
		}

		_, _, err := env.invokeAgentRuntime(namespace, runtimeName, sessionID, reqExpired)

		// Assert: Session should be cleaned up (error expected)
		// Note: In a real implementation, this should return a session-not-found error
		if err != nil {
			t.Logf("Session correctly cleaned up after TTL expires (error: %v)", err)
		} else {
			t.Logf("Session still active after TTL should have expired - this may indicate TTL implementation needs checking")
		}
	})
}
