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

// Package e2e contains end-to-end tests for AgentCube session and sandbox lifecycle.
//
// This file covers the net-new lifecycle scenarios from GitHub issue #103 that
// are not already addressed by the existing e2e_test.go:
//
//   - A1: AgentRuntime session auto-creation and reuse via x-agentcube-session-id header
//   - B1: CodeInterpreter session auto-creation via the Router (header round-trip)
//   - B2: Stateful CodeInterpreter session (variable persists across multiple calls)
//
// Scenarios A2, A3, and B3 are covered by existing tests:
//   - A2 -> TestAgentRuntimeErrorHandling           (e2e_test.go)
//   - A3 -> TestAgentRuntimeSessionTTL              (e2e_test.go)
//   - B3 -> TestCodeInterpreterFileOperations / "download file" (e2e_test.go)
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// ============================================================
// A1 – AgentRuntime: session auto-creation and reuse
// ============================================================

// TestAgentRuntimeSessionCreationAndReuse verifies that:
//  1. A POST without x-agentcube-session-id returns HTTP 200 AND a non-empty
//     x-agentcube-session-id header (auto-create path).
//  2. A second POST with that session ID returns HTTP 200 (session reuse path)
//     and the same session ID echoed back.
func TestAgentRuntimeSessionCreationAndReuse(t *testing.T) {
	env := newTestEnv(t)

	namespace := agentcubeNamespace
	runtimeName := "echo-agent"

	// --- Step 1: First call – no session ID provided ---
	req1 := &AgentInvokeRequest{
		Input: "hello from A1 step 1",
		Metadata: map[string]interface{}{
			"test": "session_creation",
		},
	}

	resp1, sessionID, err := env.invokeAgentRuntime(namespace, runtimeName, "", req1)
	if err != nil {
		t.Fatalf("A1 step 1: unexpected error on first invoke: %v", err)
	}
	if resp1 == nil {
		t.Fatal("A1 step 1: response is nil")
	}
	if sessionID == "" {
		t.Fatal("A1 step 1: expected x-agentcube-session-id header in response, got empty string")
	}
	t.Logf("A1 step 1: session auto-created, session_id=%s", sessionID)

	// The echo agent prefixes output with "echo: "
	expectedOutput1 := "echo: hello from A1 step 1"
	if resp1.Output != expectedOutput1 {
		t.Errorf("A1 step 1: expected output %q, got %q", expectedOutput1, resp1.Output)
	}

	// --- Step 2: Second call – reuse the session ID ---
	req2 := &AgentInvokeRequest{
		Input: "hello from A1 step 2",
		Metadata: map[string]interface{}{
			"test": "session_reuse",
		},
	}

	resp2, sessionID2, err := env.invokeAgentRuntime(namespace, runtimeName, sessionID, req2)
	if err != nil {
		t.Fatalf("A1 step 2: unexpected error on second invoke with session_id=%s: %v", sessionID, err)
	}
	if resp2 == nil {
		t.Fatal("A1 step 2: response is nil")
	}

	// The returned session ID must be the same (or at least non-empty).
	if sessionID2 == "" {
		t.Error("A1 step 2: expected x-agentcube-session-id header in response, got empty string")
	}
	if sessionID2 != sessionID {
		t.Errorf("A1 step 2: expected session ID to remain %q, got %q", sessionID, sessionID2)
	}

	expectedOutput2 := "echo: hello from A1 step 2"
	if resp2.Output != expectedOutput2 {
		t.Errorf("A1 step 2: expected output %q, got %q", expectedOutput2, resp2.Output)
	}

	t.Logf("A1: session reuse verified – session_id=%s, output=%q", sessionID, resp2.Output)
}

// ============================================================
// B1 – CodeInterpreter: session auto-creation via Router
// ============================================================

// TestCodeInterpreterSessionAutoCreation verifies that a POST to the Router
// code-interpreter endpoint *without* an x-agentcube-session-id header:
//  1. Returns HTTP 200.
//  2. Produces the expected stdout (e.g. "2\n" for print(1+1)).
//  3. Sets the x-agentcube-session-id response header.
func TestCodeInterpreterSessionAutoCreation(t *testing.T) {
	env := newTestEnv(t)

	namespace := agentcubeNamespace
	name := e2eCodeInterpreterName

	// Run a trivial Python expression through the interpreter.
	req := &CodeInterpreterExecuteRequest{
		Command: []string{"python3", "-c", "print(1+1)"},
	}

	resp, sessionID, err := env.invokeCodeInterpreterWithHeader(namespace, name, "", req)
	if err != nil {
		t.Fatalf("B1: unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("B1: response is nil")
	}

	// Assert: correct stdout.
	expectedStdout := "2\n"
	if resp.Stdout != expectedStdout {
		t.Errorf("B1: expected stdout %q, got %q", expectedStdout, resp.Stdout)
	}
	if resp.ExitCode != 0 {
		t.Errorf("B1: expected exit code 0, got %d (stderr: %s)", resp.ExitCode, resp.Stderr)
	}

	// Assert: session ID header is present.
	if sessionID == "" {
		t.Error("B1: expected x-agentcube-session-id header in response, got empty string")
	} else {
		t.Logf("B1: session auto-created by Router, session_id=%s", sessionID)
	}
}

// ============================================================
// B2 – CodeInterpreter: stateful multi-step session
// ============================================================

// TestCodeInterpreterStatefulSession verifies that the CodeInterpreter preserves
// state across multiple calls within the same session.
//
// Because each command runs in a fresh sub-process, we write state to a file in
// the shared workspace on step 1 and read it back on step 2.
func TestCodeInterpreterStatefulSession(t *testing.T) {
	env := newTestEnv(t)

	namespace := agentcubeNamespace
	name := e2eCodeInterpreterName

	// Create a session explicitly so we control the session ID.
	sessionID, err := env.createCodeInterpreterSession(namespace, name)
	if err != nil {
		t.Fatalf("B2: failed to create code interpreter session: %v", err)
	}
	t.Cleanup(func() {
		_ = env.deleteCodeInterpreterSession(sessionID)
	})
	t.Logf("B2: session created, session_id=%s", sessionID)

	// Step 1: Write state to a file in the workspace.
	step1 := &CodeInterpreterExecuteRequest{
		Command: []string{"python3", "-c", "open('_state.py','w').write('x = 10\\n')"},
	}
	resp1, err := env.invokeCodeInterpreter(namespace, name, sessionID, step1)
	if err != nil {
		t.Fatalf("B2 step 1 (write state): unexpected error: %v", err)
	}
	if resp1.ExitCode != 0 {
		t.Fatalf("B2 step 1 (write state): expected exit code 0, got %d (stderr: %s)",
			resp1.ExitCode, resp1.Stderr)
	}
	t.Log("B2 step 1: state written to _state.py")

	// Step 2: Read the file and print x.
	step2 := &CodeInterpreterExecuteRequest{
		Command: []string{"python3", "-c",
			"exec(open('_state.py').read()); print(x)"},
	}
	resp2, err := env.invokeCodeInterpreter(namespace, name, sessionID, step2)
	if err != nil {
		t.Fatalf("B2 step 2 (read state): unexpected error: %v", err)
	}
	if resp2.ExitCode != 0 {
		t.Fatalf("B2 step 2 (read state): expected exit code 0, got %d (stderr: %s)",
			resp2.ExitCode, resp2.Stderr)
	}

	// Assert: printed value is "10".
	expectedOutput := "10\n"
	if resp2.Stdout != expectedOutput {
		t.Errorf("B2 step 2: expected stdout %q, got %q (state persisted via file in shared workspace)",
			expectedOutput, resp2.Stdout)
	}
	t.Logf("B2: stateful session verified – printed x=%q", strings.TrimSpace(resp2.Stdout))
}

// ============================================================
// Helpers used only in this file
// ============================================================

// invokeCodeInterpreterWithHeader is like invokeCodeInterpreter but also returns
// the x-agentcube-session-id response header so B1 can assert on it.
func (e *testEnv) invokeCodeInterpreterWithHeader(
	namespace, name, sessionID string,
	req *CodeInterpreterExecuteRequest,
) (*CodeInterpreterExecuteResponse, string, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to marshal request: %w", err)
	}

	rawURL := fmt.Sprintf("%s/v1/namespaces/%s/code-interpreters/%s/invocations/api/execute",
		e.routerURL, namespace, name)

	httpReq, err := http.NewRequest(http.MethodPost, rawURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+e.authToken)
	}
	if sessionID != "" {
		httpReq.Header.Set("x-agentcube-session-id", sessionID)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, "", fmt.Errorf("failed to send request: %w", err)
	}
	defer httpResp.Body.Close()

	respSessionID := httpResp.Header.Get("x-agentcube-session-id")

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, respSessionID, fmt.Errorf("failed to read response body: %w", err)
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, respSessionID, fmt.Errorf("request failed with status %d: %s",
			httpResp.StatusCode, string(body))
	}

	var resp CodeInterpreterExecuteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, respSessionID, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &resp, respSessionID, nil
}
