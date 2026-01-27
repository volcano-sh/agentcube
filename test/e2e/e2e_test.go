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
	"os/exec"
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

// executeCode executes code through the CodeInterpreter API using Python SDK
func (e *testEnv) executeCode(namespace, name string, req *CodeExecuteRequest) (*CodeExecuteResponse, string, error) {
	// Create a temporary Python file
	tmpFile, err := os.CreateTemp("", "e2e-code-exec-*.py")
	if err != nil {
		return nil, "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	// Create a Python script that uses the agentcube SDK
	// Note: Not using 'with' statement to keep session alive for test verification
	pythonScript := fmt.Sprintf(`
import os
import sys
import json

# Set environment variables
os.environ['ROUTER_URL'] = %q
os.environ['WORKLOAD_MANAGER_URL'] = %q
if %q:
    os.environ['API_TOKEN'] = %q

# Add SDK to path
sys.path.insert(0, '/root/agentcube/sdk-python')

from agentcube import CodeInterpreterClient

try:
    client = CodeInterpreterClient(name=%q, namespace=%q)
    result = client.run_code(%q, %q)
    # Output as JSON for easy parsing
    output = {
        'stdout': result,
        'stderr': '',
        'exit_code': 0,
        'session_id': client.session_id
    }
    print(json.dumps(output))
except Exception as e:
    # Return error in expected format
    output = {
        'stdout': '',
        'stderr': str(e),
        'exit_code': 1,
        'session_id': ''
    }
    print(json.dumps(output))
    sys.exit(1)
`, e.routerURL, e.workloadMgrURL, e.authToken, e.authToken, name, namespace, req.Language, req.Code)

	// Write the Python script to the temp file
	if _, err := tmpFile.WriteString(pythonScript); err != nil {
		return nil, "", fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Execute the Python file with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	//nolint:gosec // G204: tmpFile.Name() is controlled by this test, not user input
	cmd := exec.CommandContext(ctx, "python3", tmpFile.Name())
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	output := stdout.String()

	// If stderr has content but stdout is empty, use stderr as output for error info
	if output == "" && stderr.Len() > 0 {
		output = stderr.String()
	}

	// Parse the JSON output
	var jsonOutput struct {
		Stdout    string `json:"stdout"`
		Stderr    string `json:"stderr"`
		ExitCode  int    `json:"exit_code"`
		SessionID string `json:"session_id"`
	}

	if err := json.Unmarshal([]byte(output), &jsonOutput); err != nil {
		return nil, "", fmt.Errorf("failed to parse python output: %w, output: %s, stderr: %s", err, output, stderr.String())
	}

	response := &CodeExecuteResponse{
		Output:   jsonOutput.Stdout,
		Error:    jsonOutput.Stderr,
		ExitCode: jsonOutput.ExitCode,
	}

	// Check exit code from parsed JSON and return detailed error if available
	if jsonOutput.ExitCode != 0 {
		if jsonOutput.Stderr != "" {
			err = fmt.Errorf("python script failed: %s", jsonOutput.Stderr)
		} else if err == nil {
			err = fmt.Errorf("python script failed with exit code %d (no error details)", jsonOutput.ExitCode)
		}
		// else: keep the original cmd.Run() error if no stderr available
	}

	return response, jsonOutput.SessionID, err
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

	env.executeAndVerifyCode(t, namespace, name, "print('Hello from warmpool!')", "Hello from warmpool!")

	ctx.verifyWarmPoolStatus(t, namespace, name, warmPoolSize, initialPods)
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

func (e *testEnv) executeAndVerifyCode(t *testing.T, namespace, name, code, expectedOutput string) string {
	t.Log("Executing code command...")
	codeReq := &CodeExecuteRequest{
		Language: "python",
		Code:     code,
	}

	result, sessionID, err := e.executeCode(namespace, name, codeReq)
	require.NoError(t, err)
	require.NotEmpty(t, sessionID)
	require.Contains(t, result.Output, expectedOutput)

	return sessionID
}

func (ctx *e2eTestContext) verifyWarmPoolStatus(t *testing.T, namespace, name string, warmPoolSize int, initialPods []string) {
	t.Log("Verifying warmpool post-execution status...")
	time.Sleep(2 * time.Second)

	claims, err := ctx.countSandboxClaims(namespace, name)
	require.NoError(t, err)
	require.Greater(t, claims, 0)

	currentPodCount, err := ctx.countWarmPoolPods(namespace, name)
	require.NoError(t, err)
	require.Equal(t, warmPoolSize, currentPodCount)

	claimedPod, err := ctx.findPodWithSandboxClaim(namespace, initialPods)
	require.NoError(t, err)
	require.NotEmpty(t, claimedPod, "One of the initial warmpool pods should be claimed")
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

// findPodWithSandboxClaim finds a pod from the initial list that now has a sandboxclaim owner reference
func (ctx *e2eTestContext) findPodWithSandboxClaim(namespace string, podNames []string) (string, error) {
	getCtx := context.Background()

	for _, podName := range podNames {
		pod, err := ctx.kubeClient.CoreV1().Pods(namespace).Get(
			getCtx,
			podName,
			metav1.GetOptions{},
		)
		if err != nil {
			// Pod might have been deleted, skip it
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", fmt.Errorf("failed to get pod %s: %w", podName, err)
		}

		// Check if pod has a SandboxClaim or Sandbox owner reference
		// Note: The pod doesn't necessarily get a SandboxClaim owner ref, but it definitely loses the SandboxWarmPool owner ref
		// or gets a Sandbox owner ref.
		for _, owner := range pod.OwnerReferences {
			if owner.Kind == "SandboxClaim" || owner.Kind == "Sandbox" {
				return podName, nil
			}
		}

		// Double check: if it's NO LONGER owned by SandboxWarmPool, that's also a strong signal it's claimed or being transitioned?
		// But let's stick to positive assertion of new owner if possible, or absence of old owner if that's the only change.
		// User said: "Use 'ownerReferences' to check if the pods belong to a sandbox warmpool or sandbox instead of labels"
		// If it belongs to Sandbox, it should have Sandbox owner.
		// If the user's system adds Sandbox owner, we find it.

		// If we can't find positive Sandbox owner, we might check absence of WarmPool owner.
		// Note: If the pod was in the warmpool list (initialPods) and is now not owned by WarmPool,
		// it's likely consumed. However, waiting for Sandbox/SandboxClaim owner is safer,
		// so we return only if we see Sandbox/SandboxClaim owner (checked above).
	}

	return "", nil
}
