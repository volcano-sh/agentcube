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

package picod

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childSleepSeconds is how long the child processes in these tests run. It is
// much longer than the time the handler is allowed to take, so a handler that
// waits for the child is caught by the elapsed-time assertions.
const childSleepSeconds = 30

func runExecuteRequest(t *testing.T, server *Server, req ExecuteRequest) (ExecuteResponse, time.Duration) {
	t.Helper()
	body, err := json.Marshal(req)
	require.NoError(t, err)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/api/execute", bytes.NewBuffer(body))
	c.Request.Header.Set("Content-Type", "application/json")

	start := time.Now()
	server.ExecuteHandler(c)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, w.Code)
	var resp ExecuteResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp, elapsed
}

// childPID reads the PID that the test command printed on its first stdout line.
func childPID(t *testing.T, stdout string) int {
	t.Helper()
	firstLine, _, _ := strings.Cut(stdout, "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(firstLine))
	require.NoError(t, err, "expected child PID on first stdout line, got %q", stdout)
	return pid
}

// processExited reports whether pid has exited. A zombie counts as exited:
// it is dead and only waiting for its new parent to reap it.
func processExited(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return os.IsNotExist(err)
	}
	// The state field follows the parenthesized command name.
	if i := bytes.LastIndexByte(stat, ')'); i >= 0 && i+2 < len(stat) {
		return stat[i+2] == 'Z'
	}
	return false
}

func TestExecuteHandler_TimeoutKillsChildProcesses(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// The background sleep inherits stdout, so it keeps the output pipe open
	// after the shell is killed.
	script := fmt.Sprintf("sleep %d & echo $!; sleep %d", childSleepSeconds, childSleepSeconds)
	resp, elapsed := runExecuteRequest(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", script},
		Timeout: "1s",
	})

	pid := childPID(t, resp.Stdout)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	assert.Equal(t, TimeoutExitCode, resp.ExitCode)
	assert.Contains(t, resp.Stderr, "Command timed out")
	assert.Less(t, elapsed, 2*time.Second, "handler used the SDK's response margin after the timeout")
	assert.Eventually(t, func() bool { return processExited(pid) }, 5*time.Second, 50*time.Millisecond,
		"child process %d is still running after the timeout", pid)
}

func TestExecuteHandler_BackgroundChildDoesNotBlockResponse(t *testing.T) {
	server, tmpDir := setupExecuteTestServer(t)
	defer os.RemoveAll(tmpDir)
	defer os.Unsetenv(PublicKeyEnvVar)

	// The shell exits right away, but the background sleep keeps stdout open.
	script := fmt.Sprintf("sleep %d & echo $!; echo done", childSleepSeconds)
	resp, elapsed := runExecuteRequest(t, server, ExecuteRequest{
		Command: []string{"sh", "-c", script},
		Timeout: "20s",
	})

	pid := childPID(t, resp.Stdout)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	assert.Equal(t, 0, resp.ExitCode)
	assert.Contains(t, resp.Stdout, "done")
	assert.Empty(t, resp.Stderr)
	assert.Less(t, elapsed, 10*time.Second, "handler waited for a background child after the command exited")
}
