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

package e2b

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// POST /sandboxes - Create Sandbox Tests
// =============================================================================

func TestCreateSandbox_Success(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	tests := []struct {
		name       string
		req        CreateSandboxRequest
		wantStatus int
		wantInResp []string
	}{
		{
			name: "create with minimal config",
			req: CreateSandboxRequest{
				Name: "test-sandbox-1",
				Config: SandboxConfig{
					Template:    "default",
					TimeoutSecs: 3600,
				},
			},
			wantStatus: http.StatusCreated,
			wantInResp: []string{"id", "session_id", "status", "created_at"},
		},
		{
			name: "create with full config",
			req: CreateSandboxRequest{
				Name: "test-sandbox-2",
				Config: SandboxConfig{
					Template:    "python-3.11",
					TimeoutSecs: 7200,
					Resources: ResourceConfig{
						CPU:    "2",
						Memory: "4Gi",
						Disk:   "10Gi",
					},
					EnvVars: map[string]string{
						"KEY1": "value1",
						"KEY2": "value2",
					},
				},
			},
			wantStatus: http.StatusCreated,
			wantInResp: []string{"id", "session_id", "status", "created_at"},
		},
		{
			name: "create with default timeout",
			req: CreateSandboxRequest{
				Name: "test-sandbox-3",
				Config: SandboxConfig{
					Template: "default",
					// TimeoutSecs not specified, should use default
				},
			},
			wantStatus: http.StatusCreated,
			wantInResp: []string{"id", "session_id", "expires_at"},
		},
		{
			name: "create with long timeout",
			req: CreateSandboxRequest{
				Name: "test-sandbox-4",
				Config: SandboxConfig{
					Template:    "default",
					TimeoutSecs: 86400, // 24 hours
				},
			},
			wantStatus: http.StatusCreated,
			wantInResp: []string{"id", "session_id", "expires_at"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", tt.req)
			require.NoError(t, err)
			defer resp.Body.Close()

			AssertStatus(t, resp, tt.wantStatus)

			var result CreateSandboxResponse
			ParseResponse(t, resp, &result)

			assert.NotEmpty(t, result.ID, "expected sandbox ID")
			assert.NotEmpty(t, result.SessionID, "expected session ID")
			assert.Equal(t, "running", result.Status)
			assert.False(t, result.CreatedAt.IsZero(), "expected created_at")
			assert.False(t, result.ExpiresAt.IsZero(), "expected expires_at")

			// Verify timeout is properly calculated
			expectedTimeout := tt.req.Config.TimeoutSecs
			if expectedTimeout <= 0 {
				expectedTimeout = 3600 // Default 1 hour
			}
			duration := result.ExpiresAt.Sub(result.CreatedAt)
			assert.InDelta(t, float64(expectedTimeout), duration.Seconds(), 5, "timeout mismatch")
		})
	}
}

func TestCreateSandbox_ValidationErrors(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "invalid JSON",
			body:       `{invalid json}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_request",
		},
		{
			name:       "empty body",
			body:       `{}`,
			wantStatus: http.StatusCreated, // Should succeed with defaults
		},
		{
			name:       "negative timeout",
			body:       `{"name":"test","config":{"timeout_secs":-1}}`,
			wantStatus: http.StatusCreated, // Server handles negative timeout
		},
		{
			name:       "nested invalid JSON",
			body:       `{"config":{"env_vars":{"KEY":"value"`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "invalid_request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", nil)
			require.NoError(t, err)
			defer resp.Body.Close()

			// For raw body tests, we need to use raw request
			req, _ := http.NewRequest(http.MethodPost, server.Server.URL+server.Config.BasePath+"/sandboxes", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err = http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			AssertStatus(t, resp, tt.wantStatus)

			if tt.wantErr != "" {
				var errResp ErrorResponse
				ParseResponse(t, resp, &errResp)
				assert.Contains(t, errResp.Error, tt.wantErr)
			}
		})
	}
}

func TestCreateSandbox_Concurrent(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Test concurrent sandbox creation
	numRequests := 10
	done := make(chan *CreateSandboxResponse, numRequests)
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(_ int) {
			req := CreateSandboxRequest{
				Name: "concurrent-sandbox",
				Config: SandboxConfig{
					Template:    "default",
					TimeoutSecs: 3600,
				},
			}
			resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
			if err != nil {
				errors <- err
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusCreated {
				errors <- assert.AnError
				return
			}

			var result CreateSandboxResponse
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				errors <- err
				return
			}
			done <- &result
		}(i)
	}

	// Collect results
	var results []*CreateSandboxResponse
	var errs []error
	for i := 0; i < numRequests; i++ {
		select {
		case result := <-done:
			results = append(results, result)
		case err := <-errors:
			errs = append(errs, err)
		case <-time.After(10 * time.Second):
			t.Fatal("timeout waiting for concurrent requests")
		}
	}

	// All requests should succeed
	assert.Empty(t, errs, "expected no errors")
	assert.Len(t, results, numRequests, "expected all requests to succeed")

	// All IDs should be unique
	ids := make(map[string]bool)
	for _, r := range results {
		assert.False(t, ids[r.ID], "expected unique sandbox IDs")
		ids[r.ID] = true
	}
}

// =============================================================================
// GET /sandboxes - List Sandboxes Tests
// =============================================================================

func TestListSandboxes_Success(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create some sandboxes first
	for i := 0; i < 3; i++ {
		server.CreateTestSandbox(t, "list-test-sandbox")
	}

	resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusOK)

	var result ListSandboxesResponse
	ParseResponse(t, resp, &result)

	assert.GreaterOrEqual(t, result.Total, 3, "expected at least 3 sandboxes")
	assert.GreaterOrEqual(t, len(result.Sandboxes), 3, "expected at least 3 sandbox entries")

	// Verify sandbox structure
	for _, sb := range result.Sandboxes {
		assert.NotEmpty(t, sb.ID, "expected sandbox ID")
		assert.NotEmpty(t, sb.SessionID, "expected session ID")
		assert.NotEmpty(t, sb.Status, "expected status")
		assert.False(t, sb.CreatedAt.IsZero(), "expected created_at")
	}
}

func TestListSandboxes_Empty(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusOK)

	var result ListSandboxesResponse
	ParseResponse(t, resp, &result)

	assert.Equal(t, 0, result.Total, "expected 0 sandboxes")
	assert.Empty(t, result.Sandboxes, "expected empty sandbox list")
}

// =============================================================================
// GET /sandboxes/{id} - Get Sandbox Tests
// =============================================================================

func TestGetSandbox_Success(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a sandbox
	created := server.CreateTestSandbox(t, "get-test-sandbox")

	resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusOK)

	var result SandboxResponse
	ParseResponse(t, resp, &result)

	assert.Equal(t, created.ID, result.ID)
	assert.Equal(t, created.SessionID, result.SessionID)
	assert.Equal(t, "get-test-sandbox", result.Name)
	assert.Equal(t, "running", result.Status)
	assert.False(t, result.CreatedAt.IsZero())
	assert.NotNil(t, result.ExpiresAt)
	assert.False(t, result.LastActivity.IsZero())
}

func TestGetSandbox_NotFound(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	tests := []struct {
		name string
		id   string
	}{
		{
			name: "non-existent ID",
			id:   "sb_nonexistent123456",
		},
		{
			name: "malformed ID",
			id:   "!!!invalid!!!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := server.Config.BasePath + "/sandboxes/" + tt.id
			if tt.id == "" {
				path = server.Config.BasePath + "/sandboxes/"
			}

			resp, err := server.MakeRequest(http.MethodGet, path, nil)
			require.NoError(t, err)
			defer resp.Body.Close()

			if tt.id == "" {
				// Empty ID returns 404 from router
				assert.Equal(t, http.StatusMovedPermanently, resp.StatusCode)
			} else {
				AssertStatus(t, resp, http.StatusNotFound)

				var errResp ErrorResponse
				ParseResponse(t, resp, &errResp)
				assert.Equal(t, "not_found", errResp.Error)
				assert.Equal(t, "SANDBOX_NOT_FOUND", errResp.Code)
			}
		})
	}
}

// =============================================================================
// DELETE /sandboxes/{id} - Delete Sandbox Tests
// =============================================================================

func TestDeleteSandbox_Success(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a sandbox
	created := server.CreateTestSandbox(t, "delete-test-sandbox")

	// Delete the sandbox
	resp, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusOK)

	var result SuccessResponse
	ParseResponse(t, resp, &result)
	assert.Equal(t, "Sandbox deleted successfully", result.Message)

	// Verify it's gone
	resp2, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)
	defer resp2.Body.Close()

	AssertStatus(t, resp2, http.StatusNotFound)
}

func TestDeleteSandbox_NotFound(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	resp, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/sb_nonexistent", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusNotFound)

	var errResp ErrorResponse
	ParseResponse(t, resp, &errResp)
	assert.Equal(t, "not_found", errResp.Error)
}

func TestDeleteSandbox_AlreadyDeleted(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create and delete
	created := server.CreateTestSandbox(t, "delete-test-sandbox")
	resp, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp.Body.Close()

	// Try to delete again
	resp2, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)
	defer resp2.Body.Close()

	AssertStatus(t, resp2, http.StatusNotFound)
}

// =============================================================================
// POST /sandboxes/{id}/timeout - Set Timeout Tests
// =============================================================================

func TestSetTimeout_Success(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a sandbox
	created := server.CreateTestSandbox(t, "timeout-test-sandbox")

	// Get original expiration
	resp1, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	var original SandboxResponse
	ParseResponse(t, resp1, &original)
	resp1.Body.Close()

	// Set new timeout
	req := SetTimeoutRequest{TimeoutSecs: 7200} // 2 hours
	resp2, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/timeout", req)
	require.NoError(t, err)
	defer resp2.Body.Close()

	AssertStatus(t, resp2, http.StatusOK)

	var result SandboxResponse
	ParseResponse(t, resp2, &result)

	assert.Equal(t, created.ID, result.ID)
	assert.NotNil(t, result.ExpiresAt)

	// Verify expiration was extended
	newDuration := result.ExpiresAt.Sub(time.Now().UTC())
	assert.InDelta(t, 7200, newDuration.Seconds(), 10, "timeout should be ~2 hours")
}

func TestSetTimeout_ValidationErrors(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	created := server.CreateTestSandbox(t, "timeout-test-sandbox")

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantErr    string
	}{
		{
			name:       "zero timeout",
			body:       `{"timeout_secs":0}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "timeout_secs must be greater than 0",
		},
		{
			name:       "negative timeout",
			body:       `{"timeout_secs":-1}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "timeout_secs must be greater than 0",
		},
		{
			name:       "invalid JSON",
			body:       `{"timeout_secs":}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "Invalid request body",
		},
		{
			name:       "missing timeout field",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
			wantErr:    "timeout_secs must be greater than 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, server.Server.URL+server.Config.BasePath+"/sandboxes/"+created.ID+"/timeout", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			AssertStatus(t, resp, tt.wantStatus)

			var errResp ErrorResponse
			ParseResponse(t, resp, &errResp)
			assert.Contains(t, errResp.Message, tt.wantErr)
		})
	}
}

func TestSetTimeout_NotFound(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	req := SetTimeoutRequest{TimeoutSecs: 3600}
	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/sb_nonexistent/timeout", req)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusNotFound)

	var errResp ErrorResponse
	ParseResponse(t, resp, &errResp)
	assert.Equal(t, "not_found", errResp.Error)
	assert.Equal(t, "SANDBOX_NOT_FOUND", errResp.Code)
}

// =============================================================================
// POST /sandboxes/{id}/refreshes - Refresh Sandbox Tests
// =============================================================================

func TestRefreshSandbox_Success(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a sandbox with a timeout
	created := server.CreateTestSandbox(t, "refresh-test-sandbox")

	// Wait a bit to ensure timestamp difference
	time.Sleep(100 * time.Millisecond)

	// Refresh the sandbox
	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusOK)

	var result RefreshResponse
	ParseResponse(t, resp, &result)

	assert.Equal(t, "Sandbox refreshed successfully", result.Message)
	assert.False(t, result.LastActivity.IsZero())
	assert.False(t, result.ExpiresAt.IsZero())
}

func TestRefreshSandbox_NotFound(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/sb_nonexistent/refreshes", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	AssertStatus(t, resp, http.StatusNotFound)

	var errResp ErrorResponse
	ParseResponse(t, resp, &errResp)
	assert.Equal(t, "not_found", errResp.Error)
}

func TestRefreshSandbox_UpdatesActivity(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a sandbox
	created := server.CreateTestSandbox(t, "refresh-activity-test")

	// Get initial state
	resp1, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	var initial SandboxResponse
	ParseResponse(t, resp1, &initial)
	resp1.Body.Close()

	// Wait a bit
	time.Sleep(200 * time.Millisecond)

	// Refresh
	resp2, _ := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
	resp2.Body.Close()

	// Get updated state
	resp3, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	var updated SandboxResponse
	ParseResponse(t, resp3, &updated)
	resp3.Body.Close()

	// Activity should be updated
	assert.True(t, updated.LastActivity.After(initial.LastActivity),
		"last_activity should be updated after refresh")
}
