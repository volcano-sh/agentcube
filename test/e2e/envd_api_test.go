//go:build e2e
// +build e2e

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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEnvdAPIHealth tests the unauthenticated health endpoint via Router proxy.
func TestEnvdAPIHealth(t *testing.T) {
	env := newTestEnv(t)
	namespace := agentcubeNamespace
	name := e2eCodeInterpreterName

	sessionID, err := env.createCodeInterpreterSession(namespace, name)
	require.NoError(t, err, "Failed to create code interpreter session")
	t.Cleanup(func() {
		_ = env.deleteCodeInterpreterSession(sessionID)
	})

	resp, err := env.envdGet(namespace, name, sessionID, "health")
	require.NoError(t, err, "Failed to call /envd/health")
	require.Equal(t, http.StatusNoContent, resp.StatusCode, "Health endpoint should return 204")
}

// TestEnvdAPIEnv tests the environment endpoint.
func TestEnvdAPIEnv(t *testing.T) {
	env := newTestEnv(t)
	namespace := agentcubeNamespace
	name := e2eCodeInterpreterName

	sessionID, err := env.createCodeInterpreterSession(namespace, name)
	require.NoError(t, err, "Failed to create code interpreter session")
	t.Cleanup(func() {
		_ = env.deleteCodeInterpreterSession(sessionID)
	})

	body, err := env.envdGetJSON(namespace, name, sessionID, "env")
	require.NoError(t, err, "Failed to call /envd/env")

	var envVars map[string]string
	err = json.Unmarshal(body, &envVars)
	require.NoError(t, err, "Response should be a JSON object of env vars")
	require.NotEmpty(t, envVars, "Environment should not be empty")
}

// TestEnvdAPIFilesystem tests filesystem operations via Envd API.
func TestEnvdAPIFilesystem(t *testing.T) {
	env := newTestEnv(t)
	namespace := agentcubeNamespace
	name := e2eCodeInterpreterName

	sessionID, err := env.createCodeInterpreterSession(namespace, name)
	require.NoError(t, err, "Failed to create code interpreter session")
	t.Cleanup(func() {
		_ = env.deleteCodeInterpreterSession(sessionID)
	})

	t.Run("upload and download", func(t *testing.T) {
		testContent := "hello from envd filesystem api"
		uploadReq := map[string]interface{}{
			"path":    "envd_test.txt",
			"content": base64.StdEncoding.EncodeToString([]byte(testContent)),
		}
		body, err := env.envdPostJSON(namespace, name, sessionID, "filesystem/upload", uploadReq)
		require.NoError(t, err, "Failed to upload file")

		var info map[string]interface{}
		err = json.Unmarshal(body, &info)
		require.NoError(t, err)
		require.Equal(t, "envd_test.txt", info["name"])
		require.Equal(t, "file", info["type"])

		// Download
		downloadURL := fmt.Sprintf("filesystem/download?path=%s", url.QueryEscape("envd_test.txt"))
		resp, err := env.envdGet(namespace, name, sessionID, downloadURL)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)

		downloaded, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.Equal(t, testContent, string(downloaded))
	})

	t.Run("mkdir and list", func(t *testing.T) {
		mkdirReq := map[string]interface{}{
			"path":    "envd_test_dir",
			"parents": false,
		}
		body, err := env.envdPostJSON(namespace, name, sessionID, "filesystem/mkdir", mkdirReq)
		require.NoError(t, err, "Failed to create directory")

		var info map[string]interface{}
		err = json.Unmarshal(body, &info)
		require.NoError(t, err)
		require.Equal(t, "envd_test_dir", info["name"])
		require.Equal(t, "directory", info["type"])

		// List root directory
		listBody, err := env.envdGetJSON(namespace, name, sessionID, "filesystem/list?path=.")
		require.NoError(t, err)

		var listResp struct {
			Entries []map[string]interface{} `json:"entries"`
		}
		err = json.Unmarshal(listBody, &listResp)
		require.NoError(t, err)

		found := false
		for _, entry := range listResp.Entries {
			if entry["name"] == "envd_test_dir" {
				found = true
				require.Equal(t, "directory", entry["type"])
				break
			}
		}
		require.True(t, found, "Created directory should appear in listing")
	})

	t.Run("stat and move and remove", func(t *testing.T) {
		// Upload a file for stat/move/remove tests
		testContent := "stat me"
		uploadReq := map[string]interface{}{
			"path":    "stat_source.txt",
			"content": base64.StdEncoding.EncodeToString([]byte(testContent)),
		}
		_, err := env.envdPostJSON(namespace, name, sessionID, "filesystem/upload", uploadReq)
		require.NoError(t, err)

		// Stat
		statBody, err := env.envdGetJSON(namespace, name, sessionID, "filesystem/stat?path=stat_source.txt")
		require.NoError(t, err)

		var statInfo map[string]interface{}
		err = json.Unmarshal(statBody, &statInfo)
		require.NoError(t, err)
		require.Equal(t, "stat_source.txt", statInfo["name"])
		require.Equal(t, "file", statInfo["type"])

		// Move
		moveReq := map[string]interface{}{
			"source_path": "stat_source.txt",
			"target_path": "stat_moved.txt",
		}
		_, err = env.envdPostJSON(namespace, name, sessionID, "filesystem/move", moveReq)
		require.NoError(t, err)

		// Verify old path is gone
		resp, err := env.envdGet(namespace, name, sessionID, "filesystem/stat?path=stat_source.txt")
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()

		// Verify new path exists
		statBody, err = env.envdGetJSON(namespace, name, sessionID, "filesystem/stat?path=stat_moved.txt")
		require.NoError(t, err)
		err = json.Unmarshal(statBody, &statInfo)
		require.NoError(t, err)
		require.Equal(t, "stat_moved.txt", statInfo["name"])

		// Remove
		removeReq := map[string]interface{}{
			"path": "stat_moved.txt",
		}
		_, err = env.envdDeleteJSON(namespace, name, sessionID, "filesystem/remove", removeReq)
		require.NoError(t, err)

		// Verify removed
		resp, err = env.envdGet(namespace, name, sessionID, "filesystem/stat?path=stat_moved.txt")
		require.NoError(t, err)
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
		resp.Body.Close()
	})
}

// TestEnvdAPIProcess tests process management via Envd API.
func TestEnvdAPIProcess(t *testing.T) {
	env := newTestEnv(t)
	namespace := agentcubeNamespace
	name := e2eCodeInterpreterName

	sessionID, err := env.createCodeInterpreterSession(namespace, name)
	require.NoError(t, err, "Failed to create code interpreter session")
	t.Cleanup(func() {
		_ = env.deleteCodeInterpreterSession(sessionID)
	})

	t.Run("start and list", func(t *testing.T) {
		startReq := map[string]interface{}{
			"cmd": []string{"echo", "hello envd"},
		}
		body, err := env.envdPostJSON(namespace, name, sessionID, "process/start", startReq)
		require.NoError(t, err)

		var proc map[string]interface{}
		err = json.Unmarshal(body, &proc)
		require.NoError(t, err)
		require.NotEmpty(t, proc["process_id"])
		require.Equal(t, "running", proc["state"])

		// List processes
		listBody, err := env.envdGetJSON(namespace, name, sessionID, "process/list")
		require.NoError(t, err)

		var listResp struct {
			Processes []map[string]interface{} `json:"processes"`
		}
		err = json.Unmarshal(listBody, &listResp)
		require.NoError(t, err)
		require.NotEmpty(t, listResp.Processes)
	})

	t.Run("start with input and close stdin", func(t *testing.T) {
		// Start a cat process
		startReq := map[string]interface{}{
			"cmd": []string{"cat"},
		}
		body, err := env.envdPostJSON(namespace, name, sessionID, "process/start", startReq)
		require.NoError(t, err)

		var proc map[string]interface{}
		err = json.Unmarshal(body, &proc)
		require.NoError(t, err)
		processID := proc["process_id"].(string)
		require.NotEmpty(t, processID)

		// Send input
		inputReq := map[string]interface{}{
			"process_id": processID,
			"data":       "hello from stdin",
		}
		_, err = env.envdPostJSON(namespace, name, sessionID, "process/input", inputReq)
		require.NoError(t, err)

		// Close stdin
		closeReq := map[string]interface{}{
			"process_id": processID,
		}
		_, err = env.envdPostJSON(namespace, name, sessionID, "process/close-stdin", closeReq)
		require.NoError(t, err)

		// Wait for process to exit
		time.Sleep(500 * time.Millisecond)

		// Verify process exited
		listBody, err := env.envdGetJSON(namespace, name, sessionID, "process/list")
		require.NoError(t, err)

		var listResp struct {
			Processes []map[string]interface{} `json:"processes"`
		}
		err = json.Unmarshal(listBody, &listResp)
		require.NoError(t, err)

		found := false
		for _, p := range listResp.Processes {
			if p["process_id"] == processID {
				found = true
				require.Equal(t, "exited", p["state"])
				break
			}
		}
		require.True(t, found, "Process should be in list with exited state")
	})

	t.Run("start and signal", func(t *testing.T) {
		// Start a sleep process
		startReq := map[string]interface{}{
			"cmd": []string{"sleep", "30"},
		}
		body, err := env.envdPostJSON(namespace, name, sessionID, "process/start", startReq)
		require.NoError(t, err)

		var proc map[string]interface{}
		err = json.Unmarshal(body, &proc)
		require.NoError(t, err)
		processID := proc["process_id"].(string)
		require.NotEmpty(t, processID)

		// Send SIGTERM
		signalReq := map[string]interface{}{
			"process_id": processID,
			"signal":     15,
		}
		_, err = env.envdPostJSON(namespace, name, sessionID, "process/signal", signalReq)
		require.NoError(t, err)

		// Wait for process to exit
		time.Sleep(500 * time.Millisecond)

		// Verify process exited
		listBody, err := env.envdGetJSON(namespace, name, sessionID, "process/list")
		require.NoError(t, err)

		var listResp struct {
			Processes []map[string]interface{} `json:"processes"`
		}
		err = json.Unmarshal(listBody, &listResp)
		require.NoError(t, err)

		found := false
		for _, p := range listResp.Processes {
			if p["process_id"] == processID {
				found = true
				require.Equal(t, "exited", p["state"])
				break
			}
		}
		require.True(t, found, "Process should be in list with exited state")
	})
}

// envdURL builds the Router proxy URL for an envd endpoint.
func (e *testEnv) envdURL(namespace, name, path string) string {
	return fmt.Sprintf("%s/v1/namespaces/%s/code-interpreters/%s/invocations/envd/%s",
		e.routerURL, namespace, name, path)
}

// envdGet performs a GET request to an envd endpoint via Router proxy.
func (e *testEnv) envdGet(namespace, name, sessionID, path string) (*http.Response, error) {
	reqURL := e.envdURL(namespace, name, path)
	httpReq, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}
	if sessionID != "" {
		httpReq.Header.Set("x-agentcube-session-id", sessionID)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	return client.Do(httpReq)
}

// envdGetJSON performs a GET request and returns the response body.
func (e *testEnv) envdGetJSON(namespace, name, sessionID, path string) ([]byte, error) {
	resp, err := e.envdGet(namespace, name, sessionID, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}
	return body, nil
}

// envdPostJSON performs a POST request with a JSON body to an envd endpoint via Router proxy.
func (e *testEnv) envdPostJSON(namespace, name, sessionID, path string, payload interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	reqURL := e.envdURL(namespace, name, path)
	httpReq, err := http.NewRequest("POST", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}
	if sessionID != "" {
		httpReq.Header.Set("x-agentcube-session-id", sessionID)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// envdDeleteJSON performs a DELETE request with a JSON body to an envd endpoint via Router proxy.
func (e *testEnv) envdDeleteJSON(namespace, name, sessionID, path string, payload interface{}) ([]byte, error) {
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	reqURL := e.envdURL(namespace, name, path)
	httpReq, err := http.NewRequest("DELETE", reqURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if e.authToken != "" {
		httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", e.authToken))
	}
	if sessionID != "" {
		httpReq.Header.Set("x-agentcube-session-id", sessionID)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}
