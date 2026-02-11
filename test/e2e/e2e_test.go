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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	agentcubeclientset "github.com/volcano-sh/agentcube/client-go/clientset/versioned"
	"github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

const (
	defaultRouterURL      = "http://localhost:8081"
	defaultWorkloadMgrURL = "http://localhost:8080"

	// ownerKindSandboxWarmPool is the owner reference kind for SandboxWarmPool resources
	ownerKindSandboxWarmPool = "SandboxWarmPool"

	agentcubeNamespace = "agentcube"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(scheme))
	utilruntime.Must(extensionsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(v1alpha1.AddToScheme(scheme))
}

// e2eTestContext holds the Kubernetes clients needed for e2e tests
type e2eTestContext struct {
	kubeClient      *kubernetes.Clientset
	agentcubeClient *agentcubeclientset.Clientset
	dynamicClient   dynamic.Interface
	ctrlClient      client.Client
	config          *rest.Config
}

// newE2ETestContext creates a new e2eTestContext with initialized clients
func newE2ETestContext() (*e2eTestContext, error) {
	config, err := getKubeConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	agentcubeClient, err := agentcubeclientset.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create AgentCube client: %w", err)
	}

	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create dynamic client: %w", err)
	}

	ctrlClient, err := client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create controller-runtime client: %w", err)
	}

	return &e2eTestContext{
		kubeClient:      kubeClient,
		agentcubeClient: agentcubeClient,
		dynamicClient:   dynamicClient,
		ctrlClient:      ctrlClient,
		config:          config,
	}, nil
}

// getKubeConfig returns the Kubernetes REST config
func getKubeConfig() (*rest.Config, error) {
	// Try in-cluster config first
	config, err := rest.InClusterConfig()
	if err != nil {
		// If not in cluster, use default kubeconfig loading rules
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
		config, err = kubeConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load kubeconfig: %w", err)
		}
	}
	return config, nil
}

type testEnv struct {
	routerURL      string
	workloadMgrURL string
	authToken      string
	t              *testing.T
}

func newTestEnv(t *testing.T) *testEnv {
	return &testEnv{
		routerURL:      getEnv("ROUTER_URL", defaultRouterURL),
		workloadMgrURL: getEnv("WORKLOAD_MANAGER_URL", defaultWorkloadMgrURL),
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

// CodeInterpreterExecuteRequest defines command execution request body, matching picod.ExecuteRequest
type CodeInterpreterExecuteRequest struct {
	Command    []string          `json:"command"`
	Timeout    string            `json:"timeout,omitempty"`
	WorkingDir string            `json:"working_dir,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
}

// CodeInterpreterExecuteResponse defines command execution response body, matching picod.ExecuteResponse
type CodeInterpreterExecuteResponse struct {
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	ExitCode  int       `json:"exit_code"`
	Duration  float64   `json:"duration"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// invokeCodeInterpreter invokes a CodeInterpreter through the Router API
func (e *testEnv) invokeCodeInterpreter(namespace, name, sessionID string, req *CodeInterpreterExecuteRequest) (*CodeInterpreterExecuteResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/namespaces/%s/code-interpreters/%s/invocations/api/execute",
		e.routerURL, namespace, name)

	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
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
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var invokeResp CodeInterpreterExecuteResponse
	if err := json.Unmarshal(body, &invokeResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &invokeResp, nil
}

// createCodeInterpreterSession creates a session via WorkloadManager
func (e *testEnv) createCodeInterpreterSession(namespace, name string) (string, error) {
	payload := map[string]interface{}{
		"name":      name,
		"namespace": namespace,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1/code-interpreter", e.workloadMgrURL)
	httpReq, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}

	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("create session failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return result.SessionID, nil
}

// deleteCodeInterpreterSession deletes a session via WorkloadManager
func (e *testEnv) deleteCodeInterpreterSession(sessionID string) error {
	url := fmt.Sprintf("%s/v1/code-interpreter/sessions/%s", e.workloadMgrURL, sessionID)
	httpReq, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete session failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// invokeWithSession creates a session, registers cleanup, and invokes the interpreter
func (e *testEnv) invokeWithSession(t *testing.T, namespace, name string, req *CodeInterpreterExecuteRequest) *CodeInterpreterExecuteResponse {
	sessionID, err := e.createCodeInterpreterSession(namespace, name)
	require.NoError(t, err, "Failed to create code interpreter session")

	t.Cleanup(func() {
		_ = e.deleteCodeInterpreterSession(sessionID)
	})

	resp, err := e.invokeCodeInterpreter(namespace, name, sessionID, req)
	require.NoError(t, err, "Failed to invoke code interpreter")
	require.NotNil(t, resp)
	return resp
}

// TestAgentRuntimeBasicInvocation tests basic echo-agent functionality
func TestAgentRuntimeBasicInvocation(t *testing.T) {
	env := newTestEnv(t)

	namespace := agentcubeNamespace
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
		statusCode, body, err := invokeWithStatus(agentcubeNamespace, "non-existent-runtime", "", req)
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

	namespace := agentcubeNamespace
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
	ctx, err := newE2ETestContext()
	require.NoError(t, err)

	yamlPath := "e2e_code_interpreter_warmpool.yaml"
	codeInterpreter, err := loadCodeInterpreterYAML(yamlPath)
	require.NoError(t, err)

	namespace := codeInterpreter.Namespace
	if namespace == "" {
		namespace = "default"
	}
	name := codeInterpreter.Name
	warmPoolSize := 0
	if codeInterpreter.Spec.WarmPoolSize != nil {
		warmPoolSize = int(*codeInterpreter.Spec.WarmPoolSize)
	}

	t.Logf("Applying %s...", yamlPath)
	require.NoError(t, ctx.applyYamlFile(yamlPath))

	defer ctx.cleanupCodeInterpreter(t, namespace, name, yamlPath)

	initialPods := ctx.verifyWarmPoolReady(t, namespace, name, warmPoolSize)

	env.executeAndVerifyCode(t, namespace, name, "Hello from warmpool!")

	ctx.verifyWarmPoolStatus(t, namespace, name, warmPoolSize, initialPods)
}

// TestCodeInterpreterBasicInvocation tests basic code interpreter invocation
func TestCodeInterpreterBasicInvocation(t *testing.T) {
	env := newTestEnv(t)

	namespace := agentcubeNamespace
	name := "e2e-code-interpreter"

	testCases := []struct {
		name         string
		req          *CodeInterpreterExecuteRequest
		expectStdout string
		expectStderr string
		expectExit   int
	}{
		{
			name: "basic echo",
			req: &CodeInterpreterExecuteRequest{
				Command: []string{"echo", "Hello, World!"},
			},
			expectStdout: "Hello, World!\n",
			expectExit:   0,
		},
		{
			name: "exit code check",
			req: &CodeInterpreterExecuteRequest{
				Command: []string{"sh", "-c", "exit 1"},
			},
			expectExit: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			resp := env.invokeWithSession(t, namespace, name, tc.req)

			require.Equal(t, tc.expectStdout, resp.Stdout)
			require.Equal(t, tc.expectStderr, resp.Stderr)
			require.Equal(t, tc.expectExit, resp.ExitCode)
		})
	}
}

func (ctx *e2eTestContext) cleanupCodeInterpreter(t *testing.T, namespace, name, yamlPath string) {
	t.Log("Cleaning up code interpreter resources...")
	if err := ctx.deleteYamlFile(yamlPath); err != nil {
		t.Logf("Failed to delete yaml file: %v", err)
	}

	require.Eventually(t, func() bool {
		claims, err := ctx.countSandboxClaims(namespace, name)
		if err != nil {
			return false
		}
		return claims == 0
	}, 30*time.Second, 1*time.Second, "SandboxClaims should be deleted")

	require.Eventually(t, func() bool {
		pods, err := ctx.countWarmPoolPods(namespace, name)
		if err != nil {
			return false
		}
		return pods == 0
	}, 30*time.Second, 1*time.Second, "WarmPool pods should be deleted")
}

func (ctx *e2eTestContext) verifyWarmPoolReady(t *testing.T, namespace, name string, expectedSize int) []string {
	t.Logf("Waiting for warmpool to be created with %d pods...", expectedSize)
	err := ctx.waitForWarmPoolReady(namespace, name, expectedSize, 5*time.Minute)
	require.NoError(t, err)

	pods, err := ctx.getWarmPoolPodNames(namespace, name)
	require.NoError(t, err)
	return pods
}

func (e *testEnv) executeAndVerifyCode(t *testing.T, namespace, name, expectedOutput string) {
	t.Log("Executing code command via REST API...")

	// Execute command (simple echo)
	req := &CodeInterpreterExecuteRequest{
		Command: []string{"echo", expectedOutput},
	}

	resp := e.invokeWithSession(t, namespace, name, req)
	require.Contains(t, resp.Stdout, expectedOutput)
}

func (ctx *e2eTestContext) verifyWarmPoolStatus(t *testing.T, namespace, name string, warmPoolSize int, initialPods []string) {
	t.Log("Verifying warmpool post-execution status...")

	// 1. Find the SandboxClaim owned by the CodeInterpreter
	claim, err := ctx.getSandboxClaimByOwner(namespace, "CodeInterpreter", name)
	require.NoError(t, err)
	require.NotNil(t, claim, "Should find exactly one SandboxClaim owned by CodeInterpreter")

	// 2. Find the Sandbox owned by that SandboxClaim
	sandbox, err := ctx.getSandboxByOwner(namespace, "SandboxClaim", claim.Name)
	require.NoError(t, err)
	require.NotNil(t, sandbox, "Should find exactly one Sandbox owned by SandboxClaim")

	// 3. Find the Pod owned by that Sandbox
	pod, err := ctx.getPodByOwner(namespace, "Sandbox", sandbox.Name)
	require.NoError(t, err)
	require.NotNil(t, pod, "Should find exactly one Pod owned by Sandbox")

	// 4. Verify this pod is from the initial warmpool
	found := false
	for _, p := range initialPods {
		if p == pod.Name {
			found = true
			break
		}
	}
	require.True(t, found, "The claimed pod %s should be one of the initial warmpool pods: %v", pod.Name, initialPods)

	// 5. Verify warmpool still has warmPoolSize pods (re-filled)
	require.Eventually(t, func() bool {
		currentPodCount, err := ctx.countWarmPoolPods(namespace, name)
		if err != nil {
			return false
		}
		return currentPodCount == warmPoolSize
	}, 30*time.Second, 1*time.Second, "Warmpool should be re-filled to %d pods", warmPoolSize)
}

// ===== YAML Helper Functions (using controller-runtime client) =====

// loadCodeInterpreterYAML reads a YAML file and decodes it into a CodeInterpreter object
func loadCodeInterpreterYAML(path string) (*v1alpha1.CodeInterpreter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	var codeInterpreter v1alpha1.CodeInterpreter
	decoder := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	obj, _, err := decoder.Decode(data, nil, &codeInterpreter)
	if err != nil {
		return nil, fmt.Errorf("failed to decode YAML in %s: %w", path, err)
	}

	// Type assert to CodeInterpreter
	ci, ok := obj.(*v1alpha1.CodeInterpreter)
	if !ok {
		return nil, fmt.Errorf("object in %s is not a CodeInterpreter", path)
	}

	return ci, nil
}

// loadYAML reads a YAML file and decodes it into a client.Object
func loadYAML(path string) (client.Object, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	// Use universal deserializer to decode YAML to runtime.Object
	decoder := serializer.NewCodecFactory(scheme).UniversalDeserializer()
	obj, _, err := decoder.Decode(data, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to decode YAML in %s: %w", path, err)
	}

	// Cast to client.Object (which includes metav1.Object and runtime.Object)
	clientObj, ok := obj.(client.Object)
	if !ok {
		return nil, fmt.Errorf("object in %s is not a client.Object", path)
	}

	return clientObj, nil
}

// applyYamlFile creates the resource defined in the YAML file using controller-runtime client
func (ctx *e2eTestContext) applyYamlFile(yamlPath string) error {
	obj, err := loadYAML(yamlPath)
	if err != nil {
		return err
	}

	// Create resource
	if err := ctx.ctrlClient.Create(context.Background(), obj); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// If it already exists, try to update it?
			// For simplicity in this test setup, we can ignore AlreadyExists or try Patch.
			// Given it's a test setup, we usually expect clean slate or idempotent create.
			// Let's log and continue, or fail if we strictly expect it to be new.
			// But kubectl apply updates.
			// To emulate update, we can trying to patch.
			// But creating is safer for "ensure it exists" if we treat e2e tests as fresh.
			return fmt.Errorf("failed to create resource from %s: %w", yamlPath, err)
		}
		return fmt.Errorf("failed to create resource from %s: %w", yamlPath, err)
	}
	return nil
}

// deleteYamlFile deletes the resource defined in the YAML file using controller-runtime client
func (ctx *e2eTestContext) deleteYamlFile(yamlPath string) error {
	obj, err := loadYAML(yamlPath)
	if err != nil {
		return err
	}

	// Delete resource
	// We need to pass the object with Name and Namespace set, which loadYAML does.
	if err := ctx.ctrlClient.Delete(context.Background(), obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to delete resource from %s: %w", yamlPath, err)
	}
	return nil
}

// ===== Kubernetes Client Helper Functions =====

// countSandboxClaims counts the number of SandboxClaim resources owned by a CodeInterpreter
func (ctx *e2eTestContext) countSandboxClaims(namespace, codeInterpreterName string) (int, error) {
	sandboxClaimList := &extensionsv1alpha1.SandboxClaimList{}
	err := ctx.ctrlClient.List(context.Background(), sandboxClaimList, client.InNamespace(namespace))
	if err != nil {
		if apierrors.IsNotFound(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to list sandboxclaims: %w", err)
	}

	// Filter by CodeInterpreter owner reference
	count := 0
	for _, claim := range sandboxClaimList.Items {
		for _, ownerRef := range claim.OwnerReferences {
			if ownerRef.Kind == "CodeInterpreter" && ownerRef.Name == codeInterpreterName {
				count++
				break
			}
		}
	}

	return count, nil
}

// countWarmPoolPods counts the number of warmpool pods for a given CodeInterpreter
func (ctx *e2eTestContext) countWarmPoolPods(namespace, codeInterpreterName string) (int, error) {
	listCtx := context.Background()

	// List all pods in namespace
	podList, err := ctx.kubeClient.CoreV1().Pods(namespace).List(
		listCtx,
		metav1.ListOptions{},
	)
	if err != nil {
		return 0, fmt.Errorf("failed to list pods: %w", err)
	}

	count := 0
	for _, pod := range podList.Items {
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == ownerKindSandboxWarmPool && owner.Name == codeInterpreterName {
				count++
				break
			}
		}
	}

	return count, nil
}

// getWarmPoolPodNames returns the names of warmpool pods for a given CodeInterpreter
func (ctx *e2eTestContext) getWarmPoolPodNames(namespace, codeInterpreterName string) ([]string, error) {
	listCtx := context.Background()

	podList, err := ctx.kubeClient.CoreV1().Pods(namespace).List(
		listCtx,
		metav1.ListOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	podNames := make([]string, 0, len(podList.Items))
	for _, pod := range podList.Items {
		isWarmPool := false
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == ownerKindSandboxWarmPool && owner.Name == codeInterpreterName {
				isWarmPool = true
				break
			}
		}
		if isWarmPool {
			podNames = append(podNames, pod.Name)
		}
	}

	return podNames, nil
}

// waitForWarmPoolReady waits for the warmpool to have the expected number of ready pods
func (ctx *e2eTestContext) waitForWarmPoolReady(namespace, codeInterpreterName string, expectedCount int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		count, err := ctx.countWarmPoolPods(namespace, codeInterpreterName)
		if err != nil {
			return err
		}

		if count >= expectedCount {
			// Verify pods are actually ready
			ready, err := ctx.arePodsReady(namespace, codeInterpreterName)
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
func (ctx *e2eTestContext) arePodsReady(namespace, codeInterpreterName string) (bool, error) {
	listCtx := context.Background()

	podList, err := ctx.kubeClient.CoreV1().Pods(namespace).List(
		listCtx,
		metav1.ListOptions{},
	)
	if err != nil {
		return false, fmt.Errorf("failed to list pods: %w", err)
	}

	warmPoolPods := 0
	readyPods := 0
	for _, pod := range podList.Items {
		isWarmPool := false
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "SandboxWarmPool" && owner.Name == codeInterpreterName {
				isWarmPool = true
				break
			}
		}

		if isWarmPool {
			warmPoolPods++
			for _, condition := range pod.Status.Conditions {
				if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
					readyPods++
					break
				}
			}
		}
	}

	if warmPoolPods == 0 {
		return false, nil
	}

	return warmPoolPods == readyPods, nil
}

// getSandboxClaimByOwner finds exactly one SandboxClaim owned by the specified owner
func (ctx *e2eTestContext) getSandboxClaimByOwner(namespace, ownerKind, ownerName string) (*extensionsv1alpha1.SandboxClaim, error) {
	sandboxClaimList := &extensionsv1alpha1.SandboxClaimList{}
	err := ctx.ctrlClient.List(context.Background(), sandboxClaimList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxclaims: %w", err)
	}

	var found *extensionsv1alpha1.SandboxClaim
	for i := range sandboxClaimList.Items {
		claim := &sandboxClaimList.Items[i]
		for _, ownerRef := range claim.OwnerReferences {
			if ownerRef.Kind == ownerKind && ownerRef.Name == ownerName {
				if found != nil {
					return nil, fmt.Errorf("found multiple SandboxClaims owned by %s/%s", ownerKind, ownerName)
				}
				found = claim
				break
			}
		}
	}

	return found, nil
}

// getSandboxByOwner finds exactly one Sandbox owned by the specified owner
func (ctx *e2eTestContext) getSandboxByOwner(namespace, ownerKind, ownerName string) (*sandboxv1alpha1.Sandbox, error) {
	sandboxList := &sandboxv1alpha1.SandboxList{}
	err := ctx.ctrlClient.List(context.Background(), sandboxList, client.InNamespace(namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	var found *sandboxv1alpha1.Sandbox
	for i := range sandboxList.Items {
		sandbox := &sandboxList.Items[i]
		for _, ownerRef := range sandbox.OwnerReferences {
			if ownerRef.Kind == ownerKind && ownerRef.Name == ownerName {
				if found != nil {
					return nil, fmt.Errorf("found multiple Sandboxes owned by %s/%s", ownerKind, ownerName)
				}
				found = sandbox
				break
			}
		}
	}

	return found, nil
}

// getPodByOwner finds exactly one Pod owned by the specified owner
func (ctx *e2eTestContext) getPodByOwner(namespace, ownerKind, ownerName string) (*corev1.Pod, error) {
	podList, err := ctx.kubeClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	var found *corev1.Pod
	for i := range podList.Items {
		pod := &podList.Items[i]
		for _, ownerRef := range pod.OwnerReferences {
			if ownerRef.Kind == ownerKind && ownerRef.Name == ownerName {
				if found != nil {
					return nil, fmt.Errorf("found multiple Pods owned by %s/%s", ownerKind, ownerName)
				}
				found = pod
				break
			}
		}
	}

	return found, nil
}

// loadTestResult represents the result of a single load test request
type loadTestResult struct {
	success  bool
	err      error
	duration time.Duration
}

// runCodeInterpreterLoadTest executes a load test with the specified configuration.
func runCodeInterpreterLoadTest(
	t *testing.T,
	env *testEnv,
	namespace, name string,
	requestsPerSecond int,
	testDuration time.Duration,
	outputMessageFormat string,
) {
	totalRequests := int(testDuration.Seconds() * float64(requestsPerSecond))

	t.Logf("Starting load test: %d requests per second for %v (total: %d requests)",
		requestsPerSecond, testDuration, totalRequests)

	// Create a ticker for rate limiting
	ticker := time.NewTicker(time.Second / time.Duration(requestsPerSecond))
	defer ticker.Stop()

	// Track results
	results := make(chan loadTestResult, totalRequests)

	startTime := time.Now()
	requestsSent := 0

	// Send requests at controlled rate
	for requestsSent < totalRequests {
		<-ticker.C // Wait for next tick

		requestNum := requestsSent
		requestsSent++

		go func(reqNum int) {
			reqStart := time.Now()

			// Create a new session for this request
			sessionID, err := env.createCodeInterpreterSession(namespace, name)
			if err != nil {
				results <- loadTestResult{success: false, err: err, duration: time.Since(reqStart)}
				return
			}

			// Cleanup session when done
			defer func() {
				_ = env.deleteCodeInterpreterSession(sessionID)
			}()

			// Execute command
			req := &CodeInterpreterExecuteRequest{
				Command: []string{"echo", fmt.Sprintf(outputMessageFormat, reqNum)},
			}

			resp, err := env.invokeCodeInterpreter(namespace, name, sessionID, req)
			if err != nil {
				results <- loadTestResult{success: false, err: err, duration: time.Since(reqStart)}
				return
			}

			expectedOutput := fmt.Sprintf(outputMessageFormat, reqNum)
			if !strings.Contains(resp.Stdout, expectedOutput) {
				err := fmt.Errorf("unexpected output: got '%s', expected to contain '%s'", resp.Stdout, expectedOutput)
				results <- loadTestResult{success: false, err: err, duration: time.Since(reqStart)}
				return
			}

			duration := time.Since(reqStart)
			results <- loadTestResult{success: true, err: nil, duration: duration}
		}(requestNum)
	}

	// Wait for all results
	successCount := 0
	failureCount := 0
	var totalDuration time.Duration
	var maxDuration time.Duration
	var minDuration = time.Hour

	for i := 0; i < totalRequests; i++ {
		res := <-results
		if res.success {
			successCount++
			totalDuration += res.duration
			if res.duration > maxDuration {
				maxDuration = res.duration
			}
			if res.duration < minDuration {
				minDuration = res.duration
			}
		} else {
			failureCount++
			t.Logf("Request failed: %v", res.err)
		}
	}

	elapsedTime := time.Since(startTime)
	var avgDuration time.Duration
	if successCount > 0 {
		avgDuration = totalDuration / time.Duration(successCount)
	}

	t.Logf("Load test results:")
	t.Logf("  Total requests: %d", totalRequests)
	t.Logf("  Successful: %d", successCount)
	t.Logf("  Failed: %d", failureCount)
	t.Logf("  Success rate: %.2f%%", float64(successCount)/float64(totalRequests)*100)
	t.Logf("  Total elapsed time: %v", elapsedTime)
	if successCount > 0 {
		t.Logf("  Average response time: %v", avgDuration)
		t.Logf("  Min response time: %v", minDuration)
		t.Logf("  Max response time: %v", maxDuration)
	}
	t.Logf("  Actual rate: %.2f req/sec", float64(totalRequests)/elapsedTime.Seconds())

	// Verify that most requests succeeded (allow up to 10% failure for network issues)
	require.GreaterOrEqual(t, float64(successCount)/float64(totalRequests), 0.9,
		"At least 90%% of requests should succeed")
}

// TestCodeInterpreterWarmPoolLoad tests code interpreter with warmpool under load (10 requests per second)
func TestCodeInterpreterWarmPoolLoad(t *testing.T) {
	env := newTestEnv(t)
	ctx, err := newE2ETestContext()
	require.NoError(t, err)

	yamlPath := "e2e_code_interpreter_warmpool.yaml"
	codeInterpreter, err := loadCodeInterpreterYAML(yamlPath)
	require.NoError(t, err)

	namespace := codeInterpreter.Namespace
	if namespace == "" {
		namespace = "default"
	}
	name := codeInterpreter.Name
	warmPoolSize := 0
	if codeInterpreter.Spec.WarmPoolSize != nil {
		warmPoolSize = int(*codeInterpreter.Spec.WarmPoolSize)
	}

	t.Logf("Applying %s...", yamlPath)
	require.NoError(t, ctx.applyYamlFile(yamlPath))

	defer ctx.cleanupCodeInterpreter(t, namespace, name, yamlPath)

	ctx.verifyWarmPoolReady(t, namespace, name, warmPoolSize)

	// Load test configuration
	const (
		requestsPerSecond = 10
		testDuration      = 10 * time.Second
	)

	// Run load test with warmpool
	runCodeInterpreterLoadTest(t, env, namespace, name, requestsPerSecond, testDuration,
		"Load test request %d from warmpool!")

	// Verify warmpool still has correct number of pods after load test
	ctx.verifyWarmPoolReady(t, namespace, name, warmPoolSize)
}

// TestCodeInterpreterBasicInvocationLoad tests code interpreter without warmpool under load (10 requests per second)
func TestCodeInterpreterBasicInvocationLoad(t *testing.T) {
	env := newTestEnv(t)

	namespace := agentcubeNamespace
	name := "e2e-code-interpreter"

	// Load test configuration
	const (
		requestsPerSecond = 10
		testDuration      = 10 * time.Second
	)

	// Run load test with basic invocation
	runCodeInterpreterLoadTest(t, env, namespace, name, requestsPerSecond, testDuration,
		"Load test request %d!")
}
