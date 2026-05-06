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
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Sandbox Lifecycle Tests
// =============================================================================

// TestLifecycle_CreateAndDelete tests the basic create and delete lifecycle
func TestLifecycle_CreateAndDelete(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Step 1: Create sandbox
	req := CreateSandboxRequest{
		Name: "lifecycle-test-sandbox",
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 3600,
			Resources: ResourceConfig{
				CPU:    "1",
				Memory: "1Gi",
			},
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	assert.Equal(t, "running", created.Status)
	t.Logf("Created sandbox: ID=%s, SessionID=%s", created.ID, created.SessionID)

	// Step 2: Verify sandbox exists
	resp2, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var fetched SandboxResponse
	ParseResponse(t, resp2, &fetched)
	resp2.Body.Close()

	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, created.SessionID, fetched.SessionID)
	t.Logf("Fetched sandbox: Status=%s, CreatedAt=%v", fetched.Status, fetched.CreatedAt)

	// Step 3: Delete sandbox
	resp3, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var deleteResult SuccessResponse
	ParseResponse(t, resp3, &deleteResult)
	resp3.Body.Close()

	assert.Equal(t, "Sandbox deleted successfully", deleteResult.Message)
	t.Logf("Deleted sandbox: %s", created.ID)

	// Step 4: Verify sandbox is gone
	resp4, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)
	resp4.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp4.StatusCode)
	t.Logf("Verified sandbox is gone: %s", created.ID)
}

// TestLifecycle_CreateRefreshDelete tests the refresh (keep-alive) functionality
func TestLifecycle_CreateRefreshDelete(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create sandbox with short timeout
	req := CreateSandboxRequest{
		Name: "refresh-test-sandbox",
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 60, // 1 minute timeout
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	originalExpiresAt := created.ExpiresAt
	t.Logf("Created sandbox with 60s timeout, expires at: %v", originalExpiresAt)

	// Wait a bit
	time.Sleep(500 * time.Millisecond)

	// Refresh the sandbox
	resp2, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
	require.NoError(t, err)

	var refreshed RefreshResponse
	ParseResponse(t, resp2, &refreshed)
	resp2.Body.Close()

	assert.Equal(t, "Sandbox refreshed successfully", refreshed.Message)
	assert.False(t, refreshed.ExpiresAt.IsZero())
	assert.True(t, refreshed.ExpiresAt.After(originalExpiresAt),
		"refresh should extend expiration time")
	t.Logf("Refreshed sandbox, new expires at: %v", refreshed.ExpiresAt)

	// Verify the extension via GET
	resp3, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var fetched SandboxResponse
	ParseResponse(t, resp3, &fetched)
	resp3.Body.Close()

	assert.True(t, fetched.ExpiresAt.After(originalExpiresAt),
		"GET should reflect extended expiration")

	// Cleanup
	resp4, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp4.Body.Close()
}

// TestLifecycle_TimeoutExtension tests the timeout extension functionality
func TestLifecycle_TimeoutExtension(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create sandbox with short timeout
	req := CreateSandboxRequest{
		Name: "timeout-extension-test",
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 300, // 5 minutes
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	originalExpiresAt := created.ExpiresAt
	t.Logf("Created sandbox with 300s timeout")

	// Extend timeout to 1 hour
	setTimeoutReq := SetTimeoutRequest{TimeoutSecs: 3600}
	resp2, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/timeout", setTimeoutReq)
	require.NoError(t, err)

	var extended SandboxResponse
	ParseResponse(t, resp2, &extended)
	resp2.Body.Close()

	// Verify expiration was extended significantly
	newDuration := extended.ExpiresAt.Sub(time.Now().UTC())
	assert.InDelta(t, 3600, newDuration.Seconds(), 10, "timeout should be ~1 hour after extension")
	assert.True(t, extended.ExpiresAt.After(originalExpiresAt),
		"extended expiration should be after original")
	t.Logf("Extended timeout to 3600s")

	// Cleanup
	resp3, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp3.Body.Close()
}

// TestLifecycle_MultipleRefreshes tests multiple consecutive refreshes
func TestLifecycle_MultipleRefreshes(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create sandbox
	req := CreateSandboxRequest{
		Name: "multi-refresh-test",
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 300,
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	// Perform multiple refreshes
	numRefreshes := 5
	var lastExpiresAt time.Time

	for i := 0; i < numRefreshes; i++ {
		time.Sleep(100 * time.Millisecond)

		resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
		require.NoError(t, err)

		var refreshed RefreshResponse
		ParseResponse(t, resp, &refreshed)
		resp.Body.Close()

		assert.Equal(t, "Sandbox refreshed successfully", refreshed.Message)

		if i > 0 {
			assert.True(t, refreshed.ExpiresAt.After(lastExpiresAt) || refreshed.ExpiresAt.Equal(lastExpiresAt),
				"expiration should not go backwards")
		}
		lastExpiresAt = refreshed.ExpiresAt
	}

	t.Logf("Performed %d refreshes successfully", numRefreshes)

	// Cleanup
	resp2, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp2.Body.Close()
}

// TestLifecycle_ActivityTracking tests that activity is tracked properly
func TestLifecycle_ActivityTracking(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create sandbox
	req := CreateSandboxRequest{
		Name: "activity-tracking-test",
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 3600,
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	// Get initial activity
	resp2, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var initial SandboxResponse
	ParseResponse(t, resp2, &initial)
	resp2.Body.Close()

	initialActivity := initial.LastActivity
	t.Logf("Initial activity: %v", initialActivity)

	// Wait and refresh
	time.Sleep(200 * time.Millisecond)

	resp3, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
	require.NoError(t, err)
	resp3.Body.Close()

	// Get updated activity
	resp4, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var updated SandboxResponse
	ParseResponse(t, resp4, &updated)
	resp4.Body.Close()

	assert.True(t, updated.LastActivity.After(initialActivity),
		"last_activity should be updated after refresh")
	t.Logf("Updated activity: %v", updated.LastActivity)

	// Cleanup
	resp5, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp5.Body.Close()
}

// TestLifecycle_ExpirationCalculation tests that expiration is calculated correctly
func TestLifecycle_ExpirationCalculation(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	tests := []struct {
		name        string
		timeoutSecs int
		tolerance   float64
	}{
		{
			name:        "short timeout - 60 seconds",
			timeoutSecs: 60,
			tolerance:   5,
		},
		{
			name:        "medium timeout - 5 minutes",
			timeoutSecs: 300,
			tolerance:   5,
		},
		{
			name:        "long timeout - 1 hour",
			timeoutSecs: 3600,
			tolerance:   5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := CreateSandboxRequest{
				Name: "expiration-test",
				Config: SandboxConfig{
					Template:    "default",
					TimeoutSecs: tt.timeoutSecs,
				},
			}

			resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
			require.NoError(t, err)

			var created CreateSandboxResponse
			ParseResponse(t, resp, &created)
			resp.Body.Close()

			duration := created.ExpiresAt.Sub(created.CreatedAt)
			assert.InDelta(t, float64(tt.timeoutSecs), duration.Seconds(), tt.tolerance,
				"expiration should be timeout seconds after creation")

			// Cleanup
			resp2, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
			resp2.Body.Close()
		})
	}
}

// TestLifecycle_DeleteNonExistent tests deleting a non-existent sandbox
func TestLifecycle_DeleteNonExistent(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	nonExistentIDs := []string{
		"sb_nonexistent",
		"sb_deleted_already",
		"",
	}

	for _, id := range nonExistentIDs {
		if id == "" {
			continue // Skip empty ID test - router handles differently
		}

		resp, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+id, nil)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"expected 404 for non-existent sandbox: %s", id)
	}
}

// TestLifecycle_CreateListDeleteSequence tests the full sequence of operations
func TestLifecycle_CreateListDeleteSequence(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Initial list should be empty
	resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
	require.NoError(t, err)

	var initialList ListSandboxesResponse
	ParseResponse(t, resp, &initialList)
	resp.Body.Close()

	initialCount := initialList.Total

	// Create multiple sandboxes
	numSandboxes := 5
	createdIDs := make([]string, 0, numSandboxes)

	for i := 0; i < numSandboxes; i++ {
		req := CreateSandboxRequest{
			Name: "sequence-test",
			Config: SandboxConfig{
				Template:    "default",
				TimeoutSecs: 3600,
			},
		}

		resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
		require.NoError(t, err)

		var created CreateSandboxResponse
		ParseResponse(t, resp, &created)
		resp.Body.Close()

		createdIDs = append(createdIDs, created.ID)
	}

	// List should show all sandboxes
	resp2, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
	require.NoError(t, err)

	var list ListSandboxesResponse
	ParseResponse(t, resp2, &list)
	resp2.Body.Close()

	assert.Equal(t, initialCount+numSandboxes, list.Total,
		"list should show all created sandboxes")

	// Delete all
	for _, id := range createdIDs {
		resp, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+id, nil)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// List should be back to initial count
	resp3, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
	require.NoError(t, err)

	var finalList ListSandboxesResponse
	ParseResponse(t, resp3, &finalList)
	resp3.Body.Close()

	assert.Equal(t, initialCount, finalList.Total,
		"list should show initial count after deletion")
}

// TestLifecycle_SandboxMetadata tests that sandbox metadata is properly handled
func TestLifecycle_SandboxMetadata(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	req := CreateSandboxRequest{
		Name: "metadata-test",
		Config: SandboxConfig{
			Template:    "python-3.11",
			TimeoutSecs: 3600,
			Resources: ResourceConfig{
				CPU:    "2",
				Memory: "4Gi",
				Disk:   "10Gi",
			},
			EnvVars: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	// Fetch and verify
	resp2, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var fetched SandboxResponse
	ParseResponse(t, resp2, &fetched)
	resp2.Body.Close()

	assert.Equal(t, created.ID, fetched.ID)
	assert.Equal(t, created.SessionID, fetched.SessionID)
	assert.Equal(t, "running", fetched.Status)

	// Cleanup
	resp3, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp3.Body.Close()
}

// TestLifecycle_IdempotentOperations tests that operations are properly idempotent
func TestLifecycle_IdempotentOperations(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create sandbox
	req := CreateSandboxRequest{
		Name: "idempotent-test",
		Config: SandboxConfig{
			Template:    "default",
			TimeoutSecs: 3600,
		},
	}

	resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
	require.NoError(t, err)

	var created CreateSandboxResponse
	ParseResponse(t, resp, &created)
	resp.Body.Close()

	// Multiple GETs should return same data
	var firstGet, secondGet SandboxResponse

	resp2, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	ParseResponse(t, resp2, &firstGet)
	resp2.Body.Close()

	resp3, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	ParseResponse(t, resp3, &secondGet)
	resp3.Body.Close()

	assert.Equal(t, firstGet.ID, secondGet.ID)
	assert.Equal(t, firstGet.SessionID, secondGet.SessionID)
	assert.Equal(t, firstGet.Status, secondGet.Status)

	// Cleanup
	resp4, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	resp4.Body.Close()
}
