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

package workloadmanager

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/volcano-sh/agentcube/pkg/store"
)

// TestGarbageCollectorNativeStore runs against an isolated server started by the
// test. Run once per provider in separate go test processes because Storage is a
// process-wide singleton. Miniredis uses offsets for ZSCAN and cannot validate
// the native traversal guarantees when GC deletes members between pages.
func TestGarbageCollectorNativeStore(t *testing.T) {
	binary := os.Getenv("AGENTCUBE_GC_TEST_SERVER")
	if binary == "" {
		t.Skip("set AGENTCUBE_GC_TEST_SERVER to a redis-server or valkey-server executable")
	}
	provider := os.Getenv("AGENTCUBE_GC_TEST_PROVIDER")
	require.Contains(t, []string{"redis", "valkey"}, provider)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	_, port, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.NoError(t, listener.Close())
	dir := t.TempDir()
	logFile, err := os.Create(filepath.Join(dir, "server.log"))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, logFile.Close()) })
	// The binary is supplied explicitly for this opt-in integration test.
	// #nosec G204
	command := exec.Command(binary, "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", dir)
	command.Stdout, command.Stderr = logFile, logFile
	require.NoError(t, command.Start())
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
		if t.Failed() {
			contents, readErr := os.ReadFile(logFile.Name())
			if readErr == nil {
				t.Log(string(contents))
			}
		}
	})
	require.Eventually(t, func() bool {
		conn, dialErr := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if dialErr != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, 5*time.Second, 10*time.Millisecond)

	t.Setenv("STORE_TYPE", provider)
	t.Setenv("REDIS_ADDR", addr)
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_PASSWORD_REQUIRED", "false")
	t.Setenv("VALKEY_ADDR", addr)
	t.Setenv("VALKEY_PASSWORD", "")
	t.Setenv("VALKEY_PASSWORD_REQUIRED", "false")
	t.Setenv("VALKEY_DISABLE_CACHE", "true")
	t.Setenv("VALKEY_FORCE_SINGLE", "true")
	client := store.Storage()
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	now := time.Now()

	t.Run("long_timeouts_before_eligible_session", func(t *testing.T) {
		for i := 0; i < 100; i++ {
			sandbox := gcIdleSandbox(fmt.Sprintf("long-%03d", i), now)
			sandbox.ExpiresAt = now.Add(48 * time.Hour)
			sandbox.IdleTimeout.Duration = 24 * time.Hour
			require.NoError(t, client.StoreSandbox(ctx, sandbox))
			require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, now.Add(-2*time.Hour)))
		}
		eligible := gcIdleSandbox("eligible", now)
		eligible.ExpiresAt = now.Add(time.Hour)
		require.NoError(t, client.StoreSandbox(ctx, eligible))
		require.NoError(t, client.UpdateSessionLastActivity(ctx, eligible.SessionID, now.Add(-time.Hour)))
		// Verify that the old first-page query cannot see the eligible session.
		oldPage, err := client.ListInactiveSandboxes(ctx, now.Add(-time.Minute), 100)
		require.NoError(t, err)
		require.Len(t, oldPage, 100)
		for _, sandbox := range oldPage {
			require.NotEqual(t, eligible.SessionID, sandbox.SessionID)
		}

		gc := newTestGC(client)
		gc.collect(ctx, now)
		_, err = client.GetSandboxBySessionID(ctx, eligible.SessionID)
		require.ErrorIs(t, err, store.ErrNotFound)
		require.Len(t, gcTestDynamicClient(t, gc).Actions(), 1)
		for i := 0; i < 100; i++ {
			id := fmt.Sprintf("long-%03d", i)
			_, err = client.GetSandboxBySessionID(ctx, id)
			require.NoError(t, err, "long-timeout session must survive")
			require.NoError(t, client.DeleteSandboxBySessionID(ctx, id))
		}
	})

	t.Run("delete_across_native_scan_pages_with_equal_scores", func(t *testing.T) {
		// Exceed the servers' default compact-zset threshold so ZSCAN traverses
		// a hash table while deletion changes the collection between requests.
		for i := 0; i < 512; i++ {
			sandbox := gcIdleSandbox(fmt.Sprintf("tied-%03d", i), now)
			sandbox.ExpiresAt = now.Add(time.Hour)
			require.NoError(t, client.StoreSandbox(ctx, sandbox))
			require.NoError(t, client.UpdateSessionLastActivity(ctx, sandbox.SessionID, now.Add(-time.Hour)))
		}
		gc := newTestGC(client)
		gc.collect(ctx, now)
		for i := 0; i < 512; i++ {
			_, err := client.GetSandboxBySessionID(ctx, fmt.Sprintf("tied-%03d", i))
			require.ErrorIs(t, err, store.ErrNotFound, "scan skipped an eligible session")
		}
		require.Len(t, gcTestDynamicClient(t, gc).Actions(), 512)
		require.Empty(t, gc.inactivePending)
		require.Zero(t, gc.inactiveCursor)
		require.NoError(t, ctx.Err())
	})
}
