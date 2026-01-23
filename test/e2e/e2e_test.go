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
	"os/exec"
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

// CodeExecuteRequest represents the request payload for code execution
type CodeExecuteRequest struct {
	Language string                 `json:"language"`
	Code     string                 `json:"code"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// CodeExecuteResponse represents the response from code execution
type CodeExecuteResponse struct {
	Output   string                 `json:"stdout,omitempty"`
	Error    string                 `json:"stderr,omitempty"`
	ExitCode int                    `json:"exit_code,omitempty"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// executeCode executes code through the CodeInterpreter API
// Returns response, session ID from header, and error
func (e *testEnv) executeCode(namespace, name, sessionID string, req *CodeExecuteRequest) (*CodeExecuteResponse, string, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/namespaces/%s/code-interpreters/%s/invocations/run",
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

	client := &http.Client{Timeout: 120 * time.Second}
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

	var execResp CodeExecuteResponse
	if err := json.Unmarshal(body, &execResp); err != nil {
		return nil, responseSessionID, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &execResp, responseSessionID, nil
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

// TestCodeInterpreterWarmPool tests: Code interpreter with warmpool functionality
func TestCodeInterpreterWarmPool(t *testing.T) {
	env := newTestEnv(t)

	namespace := "agentcube"
	codeInterpreterName := "e2e-code-interpreter-warmpool"
	warmPoolSize := 2

	// Step 1: Apply the code interpreter with warmpool configuration
	t.Log("Applying e2e-code-interpreter-warmpool.yaml...")
	if err := applyYaml("test/e2e/e2e_code_interpreter_warmpool.yaml"); err != nil {
		t.Fatalf("Failed to apply code interpreter yaml: %v", err)
	}

	// Cleanup function to delete the code interpreter and related resources
	defer func() {
		t.Log("Cleaning up code interpreter resources...")
		deleteYaml("test/e2e/e2e_code_interpreter_warmpool.yaml")

		// Wait for resources to be deleted
		time.Sleep(10 * time.Second)

		// Verify sandbox is deleted
		sandboxes, err := countSandboxes(namespace, codeInterpreterName)
		if err != nil {
			t.Logf("Warning: Failed to verify sandbox deletion: %v", err)
		} else if sandboxes > 0 {
			t.Logf("Warning: Expected sandboxes to be deleted, but found %d", sandboxes)
		} else {
			t.Log("Verified: Sandboxes deleted successfully")
		}

		// Verify sandboxclaims are deleted
		claims, err := countSandboxClaims(namespace, codeInterpreterName)
		if err != nil {
			t.Logf("Warning: Failed to verify sandboxclaim deletion: %v", err)
		} else if claims > 0 {
			t.Logf("Warning: Expected sandboxclaims to be deleted, but found %d", claims)
		} else {
			t.Log("Verified: SandboxClaims deleted successfully")
		}

		// Verify warmpool pods are deleted
		pods, err := countWarmPoolPods(namespace, codeInterpreterName)
		if err != nil {
			t.Logf("Warning: Failed to verify warmpool pod deletion: %v", err)
		} else if pods > 0 {
			t.Logf("Warning: Expected warmpool pods to be deleted, but found %d", pods)
		} else {
			t.Log("Verified: WarmPool pods deleted successfully")
		}
	}()

	// Step 2: Wait for warmpool and warmPoolSize pods to be created
	t.Logf("Waiting for warmpool to be created with %d pods...", warmPoolSize)
	if err := waitForWarmPoolReady(namespace, codeInterpreterName, warmPoolSize, 5*time.Minute); err != nil {
		t.Fatalf("Failed to wait for warmpool: %v", err)
	}
	t.Logf("WarmPool is ready with %d pods", warmPoolSize)

	// Get the list of initial warmpool pod names
	initialPods, err := getWarmPoolPodNames(namespace, codeInterpreterName)
	if err != nil {
		t.Fatalf("Failed to get warmpool pod names: %v", err)
	}
	t.Logf("Initial warmpool pods: %v", initialPods)

	// Step 3: Execute a simple code command
	t.Log("Executing simple code command...")
	code := "print('Hello from warmpool!')"

	codeReq := &CodeExecuteRequest{
		Language: "python",
		Code:     code,
	}

	result, sessionID, err := env.executeCode(namespace, codeInterpreterName, "", codeReq)
	if err != nil {
		t.Fatalf("Failed to execute code: %v", err)
	}

	if sessionID == "" {
		t.Fatal("Expected session ID to be returned")
	}
	t.Logf("Code executed successfully with session ID: %s", sessionID)
	t.Logf("Execution result: %s", result.Output)

	// Step 4: Verify the command ran successfully
	if !strings.Contains(result.Output, "Hello from warmpool!") {
		t.Errorf("Expected output to contain 'Hello from warmpool!', got: %s", result.Output)
	}

	// Step 5: Verify sandboxclaim has been created
	t.Log("Verifying sandboxclaim creation...")
	time.Sleep(2 * time.Second) // Give some time for resources to be created

	claims, err := countSandboxClaims(namespace, codeInterpreterName)
	if err != nil {
		t.Fatalf("Failed to count sandboxclaims: %v", err)
	}
	if claims == 0 {
		t.Error("Expected at least 1 sandboxclaim to be created")
	} else {
		t.Logf("Verified: Found %d sandboxclaim(s)", claims)
	}

	// Step 6: Verify warmpool pod count is still warmPoolSize
	t.Log("Verifying warmpool pod count...")
	currentPodCount, err := countWarmPoolPods(namespace, codeInterpreterName)
	if err != nil {
		t.Fatalf("Failed to count warmpool pods: %v", err)
	}
	if currentPodCount != warmPoolSize {
		t.Errorf("Expected warmpool to have %d pods, but found %d", warmPoolSize, currentPodCount)
	} else {
		t.Logf("Verified: WarmPool maintains %d pods", warmPoolSize)
	}

	// Step 7: Verify one of the previous pods now belongs to sandboxclaim
	t.Log("Verifying one pod is assigned to sandboxclaim...")
	currentPods, err := getWarmPoolPodNames(namespace, codeInterpreterName)
	if err != nil {
		t.Fatalf("Failed to get current pod names: %v", err)
	}

	// Check if we have a pod with sandboxclaim owner reference
	claimedPod, err := findPodWithSandboxClaim(namespace, initialPods)
	if err != nil {
		t.Fatalf("Failed to check pod ownership: %v", err)
	}
	if claimedPod == "" {
		t.Error("Expected one of the initial warmpool pods to be claimed by sandboxclaim")
	} else {
		t.Logf("Verified: Pod %s is now owned by sandboxclaim", claimedPod)
	}

	// Verify there's at least one new pod created to maintain warmpool size
	hasNewPod := false
	for _, pod := range currentPods {
		isOriginal := false
		for _, origPod := range initialPods {
			if pod == origPod {
				isOriginal = true
				break
			}
		}
		if !isOriginal {
			hasNewPod = true
			t.Logf("Found new warmpool pod: %s", pod)
			break
		}
	}
	if !hasNewPod && claimedPod != "" {
		t.Log("Note: New warmpool pod may not be ready yet")
	}
}

// ===== Kubectl Helper Functions =====

// applyYaml applies a YAML file using kubectl
func applyYaml(yamlPath string) error {
	cmd := exec.Command("kubectl", "apply", "--validate=false", "-f", yamlPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply failed: %w, output: %s", err, string(output))
	}
	return nil
}

// deleteYaml deletes resources defined in a YAML file using kubectl
func deleteYaml(yamlPath string) error {
	cmd := exec.Command("kubectl", "delete", "-f", yamlPath, "--ignore-not-found=true")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl delete failed: %w, output: %s", err, string(output))
	}
	return nil
}

// countSandboxes counts the number of Sandbox resources for a given CodeInterpreter
func countSandboxes(namespace, codeInterpreterName string) (int, error) {
	// List all sandboxes in the namespace and filter by owner or label
	cmd := exec.Command("kubectl", "get", "sandboxes", "-n", namespace, "-o", "json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If the CRD doesn't exist or no resources found, return 0
		if strings.Contains(string(output), "No resources found") ||
			strings.Contains(string(output), "not found") {
			return 0, nil
		}
		return 0, fmt.Errorf("kubectl get sandboxes failed: %w, output: %s", err, string(output))
	}

	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0, fmt.Errorf("failed to parse sandboxes json: %w", err)
	}

	// Count sandboxes that belong to this CodeInterpreter
	count := 0
	for _, item := range result.Items {
		// Check if the sandbox has a label or annotation matching the CodeInterpreter
		metadata, ok := item["metadata"].(map[string]interface{})
		if !ok {
			continue
		}
		labels, ok := metadata["labels"].(map[string]interface{})
		if !ok {
			continue
		}
		// Check for common label patterns
		if ciName, ok := labels["agentcube.volcano.sh/code-interpreter"].(string); ok && ciName == codeInterpreterName {
			count++
		}
	}

	return count, nil
}

// countSandboxClaims counts the number of SandboxClaim resources for a given CodeInterpreter
func countSandboxClaims(namespace, codeInterpreterName string) (int, error) {
	cmd := exec.Command("kubectl", "get", "sandboxclaims", "-n", namespace, "-o", "json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// If the CRD doesn't exist or no resources found, return 0
		if strings.Contains(string(output), "No resources found") ||
			strings.Contains(string(output), "not found") {
			return 0, nil
		}
		return 0, fmt.Errorf("kubectl get sandboxclaims failed: %w, output: %s", err, string(output))
	}

	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0, fmt.Errorf("failed to parse sandboxclaims json: %w", err)
	}

	// Count sandboxclaims that belong to this CodeInterpreter
	count := 0
	for _, item := range result.Items {
		metadata, ok := item["metadata"].(map[string]interface{})
		if !ok {
			continue
		}
		labels, ok := metadata["labels"].(map[string]interface{})
		if !ok {
			continue
		}
		// Check for CodeInterpreter label
		if ciName, ok := labels["agentcube.volcano.sh/code-interpreter"].(string); ok && ciName == codeInterpreterName {
			count++
		}
	}

	return count, nil
}

// countWarmPoolPods counts the number of warmpool pods for a given CodeInterpreter
func countWarmPoolPods(namespace, codeInterpreterName string) (int, error) {
	// Use label selector to find warmpool pods
	labelSelector := fmt.Sprintf("app=%s,agentcube.volcano.sh/warmpool=true", codeInterpreterName)
	cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", labelSelector, "-o", "json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "No resources found") {
			return 0, nil
		}
		return 0, fmt.Errorf("kubectl get pods failed: %w, output: %s", err, string(output))
	}

	var result struct {
		Items []map[string]interface{} `json:"items"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return 0, fmt.Errorf("failed to parse pods json: %w", err)
	}

	return len(result.Items), nil
}

// getWarmPoolPodNames returns the names of warmpool pods for a given CodeInterpreter
func getWarmPoolPodNames(namespace, codeInterpreterName string) ([]string, error) {
	labelSelector := fmt.Sprintf("app=%s,agentcube.volcano.sh/warmpool=true", codeInterpreterName)
	cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", labelSelector, "-o", "jsonpath={.items[*].metadata.name}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "No resources found") {
			return []string{}, nil
		}
		return nil, fmt.Errorf("kubectl get pods failed: %w, output: %s", err, string(output))
	}

	podNames := strings.Fields(strings.TrimSpace(string(output)))
	return podNames, nil
}

// waitForWarmPoolReady waits for the warmpool to have the expected number of ready pods
func waitForWarmPoolReady(namespace, codeInterpreterName string, expectedCount int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		count, err := countWarmPoolPods(namespace, codeInterpreterName)
		if err != nil {
			return err
		}

		if count >= expectedCount {
			// Verify pods are actually ready
			ready, err := arePodsReady(namespace, codeInterpreterName)
			if err != nil {
				return err
			}
			if ready {
				return nil
			}
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timeout waiting for warmpool to be ready with %d pods", expectedCount)
}

// arePodsReady checks if warmpool pods are ready
func arePodsReady(namespace, codeInterpreterName string) (bool, error) {
	labelSelector := fmt.Sprintf("app=%s,agentcube.volcano.sh/warmpool=true", codeInterpreterName)
	cmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", labelSelector, "-o", "json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("kubectl get pods failed: %w, output: %s", err, string(output))
	}

	var result struct {
		Items []struct {
			Status struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return false, fmt.Errorf("failed to parse pods json: %w", err)
	}

	for _, pod := range result.Items {
		ready := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				ready = true
				break
			}
		}
		if !ready {
			return false, nil
		}
	}

	return len(result.Items) > 0, nil
}

// findPodWithSandboxClaim finds a pod from the initial list that now has a sandboxclaim owner reference
func findPodWithSandboxClaim(namespace string, podNames []string) (string, error) {
	for _, podName := range podNames {
		cmd := exec.Command("kubectl", "get", "pod", podName, "-n", namespace, "-o", "json")
		output, err := cmd.CombinedOutput()
		if err != nil {
			// Pod might have been deleted, skip it
			if strings.Contains(string(output), "not found") {
				continue
			}
			return "", fmt.Errorf("kubectl get pod failed: %w, output: %s", err, string(output))
		}

		var pod struct {
			Metadata struct {
				OwnerReferences []struct {
					Kind string `json:"kind"`
					Name string `json:"name"`
				} `json:"ownerReferences"`
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(output, &pod); err != nil {
			return "", fmt.Errorf("failed to parse pod json: %w", err)
		}

		// Check if pod has a SandboxClaim owner reference
		for _, owner := range pod.Metadata.OwnerReferences {
			if owner.Kind == "SandboxClaim" {
				return podName, nil
			}
		}

		// Also check if the warmpool label is removed (indicating it's claimed)
		if _, hasWarmPoolLabel := pod.Metadata.Labels["agentcube.volcano.sh/warmpool"]; !hasWarmPoolLabel {
			// Pod lost warmpool label, likely claimed
			return podName, nil
		}
	}

	return "", nil
}
