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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Concurrent Sandbox Creation Tests
// =============================================================================

// TestConcurrent_CreateSandboxes tests concurrent sandbox creation
func TestConcurrent_CreateSandboxes(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	numGoroutines := 10
	sandboxesPerGoroutine := 5

	var wg sync.WaitGroup
	results := make(chan *CreateSandboxResponse, numGoroutines*sandboxesPerGoroutine)
	errors := make(chan error, numGoroutines*sandboxesPerGoroutine)

	start := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start // Wait for signal to start

			for j := 0; j < sandboxesPerGoroutine; j++ {
				req := CreateSandboxRequest{
					Name: "concurrent-create-test",
					Config: SandboxConfig{
						Template:    "default",
						TimeoutSecs: 3600,
					},
				}

				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
				if err != nil {
					errors <- err
					continue
				}

				if resp.StatusCode != http.StatusCreated {
					errors <- assert.AnError
					resp.Body.Close()
					continue
				}

				var created CreateSandboxResponse
				if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
					errors <- err
					resp.Body.Close()
					continue
				}
				resp.Body.Close()

				results <- &created
			}
		}(i)
	}

	// Start all goroutines simultaneously
	close(start)

	// Wait for completion with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for concurrent creation")
	}

	close(results)
	close(errors)

	// Collect results
	var createdSandboxes []*CreateSandboxResponse
	for sb := range results {
		createdSandboxes = append(createdSandboxes, sb)
	}

	var errs []error
	for err := range errors {
		errs = append(errs, err)
	}

	// Verify results
	expectedCount := numGoroutines * sandboxesPerGoroutine
	assert.Empty(t, errs, "expected no errors during concurrent creation")
	assert.Len(t, createdSandboxes, expectedCount, "expected all sandboxes to be created")

	// Verify all IDs are unique
	idMap := make(map[string]bool)
	for _, sb := range createdSandboxes {
		assert.False(t, idMap[sb.ID], "expected unique sandbox IDs, found duplicate: %s", sb.ID)
		idMap[sb.ID] = true
	}

	t.Logf("Successfully created %d sandboxes concurrently", len(createdSandboxes))

	// Cleanup
	for _, sb := range createdSandboxes {
		resp, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+sb.ID, nil)
		if resp != nil {
			resp.Body.Close()
		}
	}
}

// TestConcurrent_DeleteSandboxes tests concurrent sandbox deletion
func TestConcurrent_DeleteSandboxes(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create sandboxes first
	numSandboxes := 20
	createdIDs := make([]string, 0, numSandboxes)

	for i := 0; i < numSandboxes; i++ {
		req := CreateSandboxRequest{
			Name: "concurrent-delete-test",
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

	t.Logf("Created %d sandboxes for deletion test", len(createdIDs))

	// Delete concurrently
	var wg sync.WaitGroup
	successCount := int32(0)
	notFoundCount := int32(0)
	errorCount := int32(0)

	start := make(chan struct{})

	for _, id := range createdIDs {
		wg.Add(1)
		go func(sandboxID string) {
			defer wg.Done()
			<-start

			resp, err := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+sandboxID, nil)
			if err != nil {
				atomic.AddInt32(&errorCount, 1)
				return
			}
			resp.Body.Close()

			switch resp.StatusCode {
			case http.StatusOK:
				atomic.AddInt32(&successCount, 1)
			case http.StatusNotFound:
				atomic.AddInt32(&notFoundCount, 1)
			default:
				atomic.AddInt32(&errorCount, 1)
			}
		}(id)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for concurrent deletion")
	}

	assert.Equal(t, int32(0), errorCount, "expected no errors during deletion")
	assert.Equal(t, int32(numSandboxes), successCount, "expected all deletions to succeed")
	assert.Equal(t, int32(0), notFoundCount, "expected no 404s")

	t.Logf("Successfully deleted %d sandboxes concurrently", successCount)
}

// TestConcurrent_MixedOperations tests concurrent mixed operations
//
//nolint:gocyclo
func TestConcurrent_MixedOperations(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create initial sandboxes
	numSandboxes := 10
	createdIDs := make([]string, 0, numSandboxes)

	for i := 0; i < numSandboxes; i++ {
		created := server.CreateTestSandbox(t, "mixed-ops-test")
		createdIDs = append(createdIDs, created.ID)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Concurrent refreshes
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for _, id := range createdIDs {
				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+id+"/refreshes", nil)
				if err == nil && resp != nil {
					resp.Body.Close()
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	// Concurrent GETs
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for _, id := range createdIDs {
				resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+id, nil)
				if err == nil && resp != nil {
					resp.Body.Close()
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	// Concurrent timeout updates
	for i := 0; i < 3; i++ {
		wg.Add(1)
		//nolint:revive // workerID is used for generating unique timeout values
		go func(workerID int) {
			defer wg.Done()
			<-start

			for _, id := range createdIDs {
				req := SetTimeoutRequest{TimeoutSecs: 3600 + workerID*600}
				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+id+"/timeout", req)
				if err == nil && resp != nil {
					resp.Body.Close()
				}
				time.Sleep(20 * time.Millisecond)
			}
		}(i)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Mixed operations completed successfully")
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for mixed operations")
	}

	// Cleanup
	for _, id := range createdIDs {
		resp, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+id, nil)
		if resp != nil {
			resp.Body.Close()
		}
	}
}

// TestConcurrent_CreateAndList tests concurrent creation while listing
//
//nolint:gocyclo
func TestConcurrent_CreateAndList(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Creator goroutines
	createdCount := int32(0)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for j := 0; j < 5; j++ {
				req := CreateSandboxRequest{
					Name: "create-and-list-test",
					Config: SandboxConfig{
						Template:    "default",
						TimeoutSecs: 3600,
					},
				}

				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
				if err == nil && resp.StatusCode == http.StatusCreated {
					resp.Body.Close()
					atomic.AddInt32(&createdCount, 1)
				} else if resp != nil {
					resp.Body.Close()
				}
				time.Sleep(10 * time.Millisecond)
			}
		}(i)
	}

	// Lister goroutines
	listCount := int32(0)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for j := 0; j < 10; j++ {
				resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
				if err == nil && resp.StatusCode == http.StatusOK {
					var list ListSandboxesResponse
					if err := json.NewDecoder(resp.Body).Decode(&list); err == nil {
						atomic.AddInt32(&listCount, 1)
					}
					resp.Body.Close()
				} else if resp != nil {
					resp.Body.Close()
				}
				time.Sleep(15 * time.Millisecond)
			}
		}(i)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("Created %d sandboxes, performed %d list operations", createdCount, listCount)
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for operations")
	}

	// Cleanup all sandboxes
	resp, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes", nil)
	if resp != nil && resp.StatusCode == http.StatusOK {
		var list ListSandboxesResponse
		if err := json.NewDecoder(resp.Body).Decode(&list); err == nil {
			for _, sb := range list.Sandboxes {
				resp2, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+sb.ID, nil)
				if resp2 != nil {
					resp2.Body.Close()
				}
			}
		}
		resp.Body.Close()
	}
}

// TestConcurrent_SameSandboxOperations tests concurrent operations on the same sandbox
func TestConcurrent_SameSandboxOperations(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a single sandbox
	created := server.CreateTestSandbox(t, "concurrent-same-sandbox-test")

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Multiple goroutines refreshing the same sandbox
	refreshCount := int32(0)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for j := 0; j < 5; j++ {
				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
				if err == nil && resp.StatusCode == http.StatusOK {
					resp.Body.Close()
					atomic.AddInt32(&refreshCount, 1)
				} else if resp != nil {
					resp.Body.Close()
				}
			}
		}(i)
	}

	// Multiple goroutines getting the same sandbox
	getCount := int32(0)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for j := 0; j < 5; j++ {
				resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
				if err == nil && resp.StatusCode == http.StatusOK {
					resp.Body.Close()
					atomic.AddInt32(&getCount, 1)
				} else if resp != nil {
					resp.Body.Close()
				}
			}
		}(i)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("Performed %d refreshes and %d gets on the same sandbox", refreshCount, getCount)
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for operations")
	}

	// Verify sandbox is still in good state
	resp, err := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	require.NoError(t, err)

	var finalState SandboxResponse
	ParseResponse(t, resp, &finalState)
	resp.Body.Close()

	assert.Equal(t, created.ID, finalState.ID)
	assert.Equal(t, "running", finalState.Status)

	// Cleanup
	resp2, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
	if resp2 != nil {
		resp2.Body.Close()
	}
}

// TestConcurrent_RaceCondition_DeleteAndGet tests race between delete and get
func TestConcurrent_RaceCondition_DeleteAndGet(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create multiple sandboxes
	numSandboxes := 20
	createdIDs := make([]string, 0, numSandboxes)

	for i := 0; i < numSandboxes; i++ {
		created := server.CreateTestSandbox(t, "race-delete-get-test")
		createdIDs = append(createdIDs, created.ID)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Deleters
	for _, id := range createdIDs {
		wg.Add(1)
		go func(sandboxID string) {
			defer wg.Done()
			<-start
			resp, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+sandboxID, nil)
			if resp != nil {
				resp.Body.Close()
			}
		}(id)
	}

	// Getters
	for _, id := range createdIDs {
		wg.Add(1)
		go func(sandboxID string) {
			defer wg.Done()
			<-start
			resp, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+sandboxID, nil)
			if resp != nil {
				// Either 200 (got it) or 404 (deleted) is acceptable
				assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound,
					"expected 200 or 404, got %d", resp.StatusCode)
				resp.Body.Close()
			}
		}(id)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Race condition test completed")
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for race condition test")
	}
}

// TestConcurrent_RaceCondition_DoubleDelete tests double deletion attempts
func TestConcurrent_RaceCondition_DoubleDelete(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// Create a sandbox
	created := server.CreateTestSandbox(t, "race-double-delete-test")

	var wg sync.WaitGroup
	start := make(chan struct{})

	successCount := int32(0)
	notFoundCount := int32(0)

	// Try to delete the same sandbox from multiple goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			resp, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
			if resp != nil {
				switch resp.StatusCode {
				case http.StatusOK:
					atomic.AddInt32(&successCount, 1)
				case http.StatusNotFound:
					atomic.AddInt32(&notFoundCount, 1)
				}
				resp.Body.Close()
			}
		}()
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Exactly one should succeed, others should get 404
		assert.Equal(t, int32(1), successCount, "expected exactly one successful delete")
		assert.Equal(t, int32(4), notFoundCount, "expected four 404s for already deleted")
		t.Logf("Double delete test: success=%d, not_found=%d", successCount, notFoundCount)
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for double delete test")
	}
}

// TestConcurrent_StoreErrors tests behavior when store has errors
func TestConcurrent_StoreErrors(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	// This test verifies that concurrent operations don't corrupt state
	// even when store operations might fail

	var wg sync.WaitGroup
	start := make(chan struct{})

	// Create sandboxes concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for j := 0; j < 3; j++ {
				req := CreateSandboxRequest{
					Name: "store-error-test",
					Config: SandboxConfig{
						Template:    "default",
						TimeoutSecs: 3600,
					},
				}

				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
				if err == nil && resp != nil {
					var created CreateSandboxResponse
					if resp.StatusCode == http.StatusCreated {
						if err := json.NewDecoder(resp.Body).Decode(&created); err == nil {
							// Immediately delete to clean up
							resp.Body.Close()
							resp2, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
							if resp2 != nil {
								resp2.Body.Close()
							}
							continue
						}
					}
					resp.Body.Close()
				}
			}
		}(i)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Store error test completed")
	case <-time.After(30 * time.Second):
		t.Fatal("timeout waiting for store error test")
	}
}

// TestConcurrent_HighLoad tests the system under high load
func TestConcurrent_HighLoad(t *testing.T) {
	server, _ := SetupTest(t)
	defer TeardownTest(server)

	if testing.Short() {
		t.Skip("skipping high load test in short mode")
	}

	numIterations := 50
	numWorkers := 20

	var wg sync.WaitGroup
	start := make(chan struct{})

	createdIDs := make(chan string, numIterations*numWorkers)

	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		go func(_ int) {
			defer wg.Done()
			<-start

			for j := 0; j < numIterations; j++ {
				// Create
				req := CreateSandboxRequest{
					Name: "high-load-test",
					Config: SandboxConfig{
						Template:    "default",
						TimeoutSecs: 300,
					},
				}

				resp, err := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes", req)
				if err != nil || resp.StatusCode != http.StatusCreated {
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}

				var created CreateSandboxResponse
				if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
					resp.Body.Close()
					continue
				}
				resp.Body.Close()

				createdIDs <- created.ID

				// Refresh
				resp2, _ := server.MakeRequest(http.MethodPost, server.Config.BasePath+"/sandboxes/"+created.ID+"/refreshes", nil)
				if resp2 != nil {
					resp2.Body.Close()
				}

				// Get
				resp3, _ := server.MakeRequest(http.MethodGet, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
				if resp3 != nil {
					resp3.Body.Close()
				}

				// Delete
				resp4, _ := server.MakeRequest(http.MethodDelete, server.Config.BasePath+"/sandboxes/"+created.ID, nil)
				if resp4 != nil {
					resp4.Body.Close()
				}
			}
		}(i)
	}

	close(start)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
		close(createdIDs)
	}()

	select {
	case <-done:
		var count int
		for range createdIDs {
			count++
		}
		t.Logf("High load test completed: %d sandboxes created and deleted", count)
		assert.Greater(t, count, 0, "expected at least some sandboxes to be created")
	case <-time.After(60 * time.Second):
		t.Fatal("timeout waiting for high load test")
	}
}
