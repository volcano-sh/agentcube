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
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecuteEnvChild inspects only requested names, so a regression never dumps
// unrelated credentials from the environment of the process running the tests.
func TestExecuteEnvChild(_ *testing.T) {
	marker := slices.Index(os.Args, "--picod-env-child")
	if marker < 0 {
		return
	}
	values := make(map[string]string)
	for _, name := range os.Args[marker+1:] {
		if value, present := os.LookupEnv(name); present {
			values[name] = value
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(values); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestBuildCommandEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name      string
		parent    []string
		overrides map[string]string
		expected  []string
	}{
		{name: "nil inputs", expected: []string{}},
		{name: "all parent names filtered", parent: []string{"SECRET=dummy", "PYTHON_API_KEY=dummy", "HTTPS_PROXY=dummy", "SSL_CERT_FILE=dummy", "REQUESTS_CA_BUNDLE=dummy", "TMPDIR=dummy", "malformed"}, expected: []string{}},
		{name: "inherited empty values", parent: []string{"HOME=", "PYTHONPATH=", "TERM="}, expected: []string{"HOME=", "PYTHONPATH=", "TERM="}},
		{name: "explicit values", overrides: map[string]string{"HOME": "", "SECRET": "request=value"}, expected: []string{"HOME=", "SECRET=request=value"}},
		{name: "embedded equals", parent: []string{"PYTHONPATH=one=two"}, expected: []string{"PYTHONPATH=one=two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := buildCommandEnvironment(tc.parent, tc.overrides)
			require.NotNil(t, env)
			assert.ElementsMatch(t, tc.expected, env)
		})
	}
}

func TestBuildCommandEnvironmentDoesNotMutateInputs(t *testing.T) {
	parent := make([]string, 2, 6)
	parent[0], parent[1] = "HOME=parent", "SECRET=dummy"
	backing := parent[:cap(parent)]
	for i := len(parent); i < len(backing); i++ {
		backing[i] = "untouched"
	}
	original := slices.Clone(backing)
	overrides := map[string]string{"HOME": "request", "REQUEST_ONLY": "value"}
	env := buildCommandEnvironment(parent, overrides)
	assert.Equal(t, original, backing)
	assert.Equal(t, map[string]string{"HOME": "request", "REQUEST_ONLY": "value"}, overrides)
	env[0] = "HOME=modified-result"
	assert.Equal(t, original, backing)
}

func newExecuteEnvHTTPServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	privateKey, publicKey := generateRSAKeys(t)
	t.Setenv(PublicKeyEnvVar, publicKey)
	server := NewServer(Config{Workspace: t.TempDir()})
	ts := httptest.NewServer(server.engine)
	t.Cleanup(ts.Close)
	token := createToken(t, privateKey, jwt.MapClaims{
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Minute).Unix(),
	})
	return ts, token
}

func executeEnvHTTP(ts *httptest.Server, token string, body []byte) (ExecuteResponse, error) {
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/execute", bytes.NewReader(body))
	if err != nil {
		return ExecuteResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		return ExecuteResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ExecuteResponse{}, fmt.Errorf("execute status: %d", resp.StatusCode)
	}
	var result ExecuteResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	return result, err
}

func executeEnvCommand(t *testing.T, names ...string) []string {
	t.Helper()
	executable, err := os.Executable()
	require.NoError(t, err)
	return append([]string{executable, "-test.run=^TestExecuteEnvChild$", "--", "--picod-env-child"}, names...)
}

func readExecuteEnv(t *testing.T, ts *httptest.Server, token, envJSON string, names ...string) map[string]string {
	t.Helper()
	command, err := json.Marshal(executeEnvCommand(t, names...))
	require.NoError(t, err)
	body := []byte(fmt.Sprintf(`{"command":%s%s}`, command, envJSON))
	result, err := executeEnvHTTP(ts, token, body)
	require.NoError(t, err)
	require.Zero(t, result.ExitCode, "%s", result.Stderr)
	var values map[string]string
	require.NoError(t, json.Unmarshal([]byte(result.Stdout), &values))
	return values
}

func TestExecuteHandler_EnvironmentSanitization(t *testing.T) {
	ts, token := newExecuteEnvHTTPServer(t)
	allowed := map[string]string{
		"PATH":                    "inherited-path",
		"HOME":                    "inherited-home",
		"LANG":                    "C.UTF-8",
		"LC_ALL":                  "C.UTF-8",
		"LC_CTYPE":                "C.UTF-8",
		"TERM":                    "",
		"PYTHONHOME":              "inherited-python-home",
		"PYTHONPATH":              "inherited=python-path",
		"PYTHONUSERBASE":          "",
		"PYTHONNOUSERSITE":        "1",
		"PYTHONUNBUFFERED":        "1",
		"PYTHONDONTWRITEBYTECODE": "1",
		"PYTHONIOENCODING":        "utf-8",
		"PYTHONUTF8":              "1",
	}
	var names []string
	for name, value := range allowed {
		t.Setenv(name, value)
		names = append(names, name)
	}
	blocked := []string{"AWS_SECRET_ACCESS_KEY", "PICOD_TEST_PARENT_SECRET", "PYTHON_API_KEY", "KUBERNETES_SERVICE_HOST", PublicKeyEnvVar}
	for _, name := range blocked {
		if name != PublicKeyEnvVar {
			t.Setenv(name, "dummy-parent-value")
		}
		names = append(names, name)
	}
	for _, tc := range []struct{ name, envJSON string }{
		{"omitted", ""},
		{"null", `,"env":null`},
		{"empty", `,"env":{}`},
		{"populated", `,"env":{"PICOD_TEST_REQUEST_VAR":"request-value"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := readExecuteEnv(t, ts, token, tc.envJSON, names...)
			for name, expected := range allowed {
				value, present := values[name]
				assert.True(t, present, "allowed name missing: %s", name)
				assert.Equal(t, expected, value, "allowed name: %s", name)
			}
			for _, name := range blocked {
				_, present := values[name]
				assert.False(t, present, "unexpected inherited name: %s", name)
			}
		})
	}
}

func TestExecuteHandler_EnvironmentOverridesDoNotPersist(t *testing.T) {
	ts, token := newExecuteEnvHTTPServer(t)
	t.Setenv("HOME", "parent-home")
	t.Setenv("TERM", "parent-term")
	t.Setenv("PICOD_TEST_REQUEST_VAR", "parent-secret")
	values := readExecuteEnv(t, ts, token,
		`,"env":{"HOME":"request=home","TERM":"","PICOD_TEST_REQUEST_VAR":"request-secret","PICOD_TEST_EMPTY":""}`,
		"HOME", "TERM", "PICOD_TEST_REQUEST_VAR", "PICOD_TEST_EMPTY")
	assert.Equal(t, map[string]string{
		"HOME": "request=home", "TERM": "", "PICOD_TEST_REQUEST_VAR": "request-secret", "PICOD_TEST_EMPTY": "",
	}, values)
	assert.Equal(t, "parent-home", os.Getenv("HOME"))
	assert.Equal(t, "parent-term", os.Getenv("TERM"))
	assert.Equal(t, "parent-secret", os.Getenv("PICOD_TEST_REQUEST_VAR"))

	values = readExecuteEnv(t, ts, token, "", "HOME", "TERM", "PICOD_TEST_REQUEST_VAR", "PICOD_TEST_EMPTY")
	assert.Equal(t, map[string]string{"HOME": "parent-home", "TERM": "parent-term"}, values)
}

func TestExecuteHandler_ConcurrentEnvironmentOverrides(t *testing.T) {
	ts, token := newExecuteEnvHTTPServer(t)
	t.Setenv("HOME", "parent-home")
	t.Setenv("PICOD_TEST_REQUEST_VAR", "parent-secret")
	command := executeEnvCommand(t, "HOME", "PICOD_TEST_REQUEST_VAR")
	type outcome struct {
		expected string
		response ExecuteResponse
		err      error
	}
	const requests = 8
	results := make(chan outcome, requests)
	start := make(chan struct{})
	for i := range requests {
		expected := fmt.Sprintf("request-%d", i)
		body, err := json.Marshal(ExecuteRequest{
			Command: command,
			Env:     map[string]string{"HOME": expected, "PICOD_TEST_REQUEST_VAR": expected},
		})
		require.NoError(t, err)
		go func() {
			<-start
			response, err := executeEnvHTTP(ts, token, body)
			results <- outcome{expected: expected, response: response, err: err}
		}()
	}
	close(start)
	for range requests {
		result := <-results
		require.NoError(t, result.err)
		require.Zero(t, result.response.ExitCode, "%s", result.response.Stderr)
		var values map[string]string
		require.NoError(t, json.Unmarshal([]byte(result.response.Stdout), &values))
		assert.Equal(t, map[string]string{"HOME": result.expected, "PICOD_TEST_REQUEST_VAR": result.expected}, values)
	}
	assert.Equal(t, "parent-home", os.Getenv("HOME"))
	assert.Equal(t, "parent-secret", os.Getenv("PICOD_TEST_REQUEST_VAR"))
	values := readExecuteEnv(t, ts, token, "", "HOME", "PICOD_TEST_REQUEST_VAR")
	assert.Equal(t, map[string]string{"HOME": "parent-home"}, values)
}

func TestExecuteHandler_EnvironmentPATHLookup(t *testing.T) {
	ts, token := newExecuteEnvHTTPServer(t)
	parentDir, childDir := t.TempDir(), t.TempDir()
	for dir, label := range map[string]string{parentDir: "parent", childDir: "child"} {
		script := fmt.Sprintf("#!/bin/sh\nprintf '%s\\n%%s\\n' \"$PATH\"\n", label)
		//nolint:gosec // This private test script must be executable for PATH lookup.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "picod-env-lookup"), []byte(script), 0700))
	}
	t.Setenv("PATH", parentDir)
	body, err := json.Marshal(ExecuteRequest{
		Command: []string{"picod-env-lookup"},
		Env:     map[string]string{"PATH": childDir},
	})
	require.NoError(t, err)
	result, err := executeEnvHTTP(ts, token, body)
	require.NoError(t, err)
	require.Zero(t, result.ExitCode, "%s", result.Stderr)
	assert.Equal(t, "parent\n"+childDir+"\n", result.Stdout)
	assert.Equal(t, parentDir, os.Getenv("PATH"))
}
