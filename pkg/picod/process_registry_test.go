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
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const osWindows = "windows"

func TestProcessRegistry_StartAndGet(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"echo", "hello"}, nil, "", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, mp.ProcessID)
	assert.Equal(t, ProcessStateRunning, mp.State)

	got, err := r.Get(mp.ProcessID)
	require.NoError(t, err)
	assert.Equal(t, mp.ProcessID, got.ProcessID)
}

func TestProcessRegistry_StartEmptyCmd(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	_, err := r.Start(context.Background(), []string{}, nil, "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cmd is required")
}

func TestProcessRegistry_InputAndCloseStdin(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("skipping stdin test on windows")
	}

	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"cat"}, nil, "", 0)
	require.NoError(t, err)

	err = r.Input(mp.ProcessID, "hello world")
	require.NoError(t, err)

	err = r.CloseStdin(mp.ProcessID)
	require.NoError(t, err)

	// Wait for process to exit
	time.Sleep(200 * time.Millisecond)

	got, err := r.Get(mp.ProcessID)
	require.NoError(t, err)
	assert.Equal(t, ProcessStateExited, got.State)
}

func TestProcessRegistry_Signal(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"sleep", "10"}, nil, "", 0)
	require.NoError(t, err)

	err = r.Signal(mp.ProcessID, 15) // SIGTERM
	require.NoError(t, err)

	// Wait for process to exit
	time.Sleep(200 * time.Millisecond)

	got, err := r.Get(mp.ProcessID)
	require.NoError(t, err)
	assert.Equal(t, ProcessStateExited, got.State)
}

func TestProcessRegistry_List(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp1, err := r.Start(context.Background(), []string{"echo", "a"}, nil, "", 0)
	require.NoError(t, err)

	mp2, err := r.Start(context.Background(), []string{"echo", "b"}, nil, "", 0)
	require.NoError(t, err)

	list := r.List()
	require.Len(t, list, 2)

	ids := make(map[string]bool)
	for _, p := range list {
		ids[p.ProcessID] = true
	}
	assert.True(t, ids[mp1.ProcessID])
	assert.True(t, ids[mp2.ProcessID])
}

func TestProcessRegistry_GetNotFound(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	_, err := r.Get("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProcessRegistry_InputNotFound(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	err := r.Input("nonexistent", "data")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProcessRegistry_CloseStdinNotFound(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	err := r.CloseStdin("nonexistent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProcessRegistry_SignalNotFound(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	err := r.Signal("nonexistent", 9)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestProcessRegistry_MaxProcesses(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	// Start maxProcesses processes that sleep for a while
	for i := 0; i < maxProcesses; i++ {
		_, err := r.Start(context.Background(), []string{"sleep", "10"}, nil, "", 0)
		require.NoError(t, err)
	}

	// The next start should fail
	_, err := r.Start(context.Background(), []string{"sleep", "10"}, nil, "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "process limit exceeded")
}

func TestProcessRegistry_SubscribeAndEvents(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"echo", "hello"}, nil, "", 0)
	require.NoError(t, err)

	events, err := r.Subscribe(mp.ProcessID)
	require.NoError(t, err)

	// Collect events with timeout
	var collected []ProcessEvent
	done := make(chan struct{})
	go func() {
		for evt := range events {
			collected = append(collected, evt)
			if evt.Type == ProcessEventTypeExit {
				break
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for events")
	}

	// Should have at least stdout and exit events
	require.NotEmpty(t, collected)
	foundExit := false
	for _, evt := range collected {
		if evt.Type == ProcessEventTypeExit {
			foundExit = true
		}
	}
	assert.True(t, foundExit, "expected exit event")
}

func TestProcessRegistry_ReapExitedProcesses(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"echo", "hello"}, nil, "", 0)
	require.NoError(t, err)

	// Wait for process to exit
	time.Sleep(200 * time.Millisecond)

	// Trigger reap manually by waiting
	time.Sleep(100 * time.Millisecond)

	// Process should still be in registry immediately after exit
	_, err = r.Get(mp.ProcessID)
	require.NoError(t, err)
}

func TestProcessRegistry_StartWithEnv(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"sh", "-c", "echo $TEST_VAR"}, map[string]string{"TEST_VAR": "value"}, "", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, mp.ProcessID)
}

func TestProcessRegistry_StartWithCwd(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"pwd"}, nil, "/tmp", 0)
	require.NoError(t, err)
	assert.NotEmpty(t, mp.ProcessID)
}

func TestProcessRegistry_StartWithTimeout(t *testing.T) {
	r := NewProcessRegistry()
	defer r.Stop()

	mp, err := r.Start(context.Background(), []string{"sleep", "10"}, nil, "", 1)
	require.NoError(t, err)

	// Wait for timeout
	time.Sleep(1500 * time.Millisecond)

	got, err := r.Get(mp.ProcessID)
	require.NoError(t, err)
	assert.Equal(t, ProcessStateExited, got.State)
}

// TestProcessRegistry_StartIgnoresCallerContextCancellation verifies that
// processes spawned via Start() are NOT killed when the caller's context is
// canceled. This is critical because the envd HTTP handler passes the request
// context, which is canceled as soon as the response is written. Subsequent
// Input/Signal/CloseStdin calls happen in different requests; the process must
// survive between them.
func TestProcessRegistry_StartIgnoresCallerContextCancellation(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("skipping on windows")
	}

	r := NewProcessRegistry()
	defer r.Stop()

	callerCtx, cancel := context.WithCancel(context.Background())

	mp, err := r.Start(callerCtx, []string{"sleep", "5"}, nil, "", 0)
	require.NoError(t, err)

	// Cancel the caller's context (simulates HTTP handler returning).
	cancel()

	// Give cancellation a chance to propagate.
	time.Sleep(300 * time.Millisecond)

	got, err := r.Get(mp.ProcessID)
	require.NoError(t, err)
	assert.Equal(t, ProcessStateRunning, got.State, "process must outlive caller context")

	// Process must remain controllable from the registry.
	err = r.Signal(mp.ProcessID, 15)
	require.NoError(t, err)
}

// TestProcessRegistry_InputAfterCallerContextCancellation reproduces the e2e
// failure: spawn a `cat` process, drop the caller context, then send input.
// If process exec is bound to caller context, the cat process exits early and
// stdin closes, which causes Input() to return "stdin is closed".
func TestProcessRegistry_InputAfterCallerContextCancellation(t *testing.T) {
	if runtime.GOOS == osWindows {
		t.Skip("skipping on windows")
	}

	r := NewProcessRegistry()
	defer r.Stop()

	callerCtx, cancel := context.WithCancel(context.Background())

	mp, err := r.Start(callerCtx, []string{"cat"}, nil, "", 0)
	require.NoError(t, err)

	cancel()
	time.Sleep(200 * time.Millisecond)

	err = r.Input(mp.ProcessID, "hello")
	require.NoError(t, err, "Input must succeed after caller context cancellation")

	err = r.CloseStdin(mp.ProcessID)
	require.NoError(t, err)
}
