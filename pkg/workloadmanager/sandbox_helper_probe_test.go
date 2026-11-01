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
	"errors"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

const probeTestPodIP = "10.0.0.1"

type dialCall struct {
	endpoint string
	timeout  time.Duration
	deadline time.Time
}

// stubEntrypointDial replaces sandboxEntrypointDial for one test. fn receives the
// zero-based call index. The returned slice pointer records every call.
func stubEntrypointDial(t *testing.T, fn func(call int, endpoint string) error) *[]dialCall {
	t.Helper()
	calls := &[]dialCall{}
	orig := sandboxEntrypointDial
	sandboxEntrypointDial = func(ctx context.Context, endpoint string, timeout time.Duration) error {
		idx := len(*calls)
		deadline, _ := ctx.Deadline()
		*calls = append(*calls, dialCall{endpoint: endpoint, timeout: timeout, deadline: deadline})
		return fn(idx, endpoint)
	}
	t.Cleanup(func() { sandboxEntrypointDial = orig })
	return calls
}

func entrypointPorts(ports ...uint32) []runtimev1alpha1.TargetPort {
	out := make([]runtimev1alpha1.TargetPort, 0, len(ports))
	for _, p := range ports {
		out = append(out, runtimev1alpha1.TargetPort{Port: p})
	}
	return out
}

func endpointsOf(calls []dialCall) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.endpoint)
	}
	return out
}

func TestProbeSandboxEntryPoints_Endpoints(t *testing.T) {
	tests := []struct {
		name     string
		podIP    string
		ports    []runtimev1alpha1.TargetPort
		wantDial []string
	}{
		{name: "no ports", podIP: probeTestPodIP, ports: nil, wantDial: []string{}},
		{
			name:     "all ports are dialed in order",
			podIP:    probeTestPodIP,
			ports:    entrypointPorts(8080, 9090),
			wantDial: []string{"10.0.0.1:8080", "10.0.0.1:9090"},
		},
		{
			name:     "IPv6 pod IP is bracketed",
			podIP:    "fd00::1",
			ports:    entrypointPorts(8080),
			wantDial: []string{"[fd00::1]:8080"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := stubEntrypointDial(t, func(int, string) error { return nil })

			err := probeSandboxEntryPoints(context.Background(), tt.podIP, tt.ports, time.Second)

			require.NoError(t, err)
			assert.Equal(t, tt.wantDial, endpointsOf(*calls))
		})
	}
}

func TestProbeSandboxEntryPoints_StopsAtFirstFailure(t *testing.T) {
	errDial := errors.New("connection refused")
	calls := stubEntrypointDial(t, func(int, string) error { return errDial })

	err := probeSandboxEntryPoints(context.Background(), probeTestPodIP, entrypointPorts(8080, 9090), time.Second)

	require.Error(t, err)
	assert.ErrorIs(t, err, errDial)
	assert.Contains(t, err.Error(), "10.0.0.1:8080")
	assert.Equal(t, []string{"10.0.0.1:8080"}, endpointsOf(*calls))
}

func TestProbeSandboxEntryPoints_DialTimeout(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     time.Duration
	}{
		{name: "zero interval uses the dial cap", interval: 0, want: defaultSandboxReadyDialTimeout},
		{name: "negative interval uses the dial cap", interval: -time.Second, want: defaultSandboxReadyDialTimeout},
		{name: "short interval is used as is", interval: 200 * time.Millisecond, want: 200 * time.Millisecond},
		{name: "long interval is capped", interval: 5 * time.Second, want: defaultSandboxReadyDialTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := stubEntrypointDial(t, func(int, string) error { return nil })

			err := probeSandboxEntryPoints(context.Background(), probeTestPodIP, entrypointPorts(8080), tt.interval)

			require.NoError(t, err)
			require.Len(t, *calls, 1)
			assert.Equal(t, tt.want, (*calls)[0].timeout)
		})
	}
}

func TestWaitForSandboxEntryPointsReady_NothingToProbe(t *testing.T) {
	tests := []struct {
		name  string
		entry *sandboxEntry
	}{
		{name: "nil entry", entry: nil},
		{name: "entry without ports", entry: &sandboxEntry{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := stubEntrypointDial(t, func(int, string) error { return errors.New("must not be dialed") })
			s := &Server{}

			err := s.waitForSandboxEntryPointsReady(context.Background(), probeTestPodIP, tt.entry)

			require.NoError(t, err)
			assert.Empty(t, *calls)
		})
	}
}

func TestWaitForSandboxEntryPointsReady_Retries(t *testing.T) {
	tests := []struct {
		name         string
		failFirst    int
		wantAttempts int
	}{
		{name: "ready on the first attempt", failFirst: 0, wantAttempts: 1},
		{name: "ready after two failed attempts", failFirst: 2, wantAttempts: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := stubEntrypointDial(t, func(call int, _ string) error {
				if call < tt.failFirst {
					return errors.New("connection refused")
				}
				return nil
			})
			s := &Server{config: &Config{
				SandboxReadyProbeTimeout:  5 * time.Second,
				SandboxReadyProbeInterval: 10 * time.Millisecond,
			}}
			entry := &sandboxEntry{Ports: entrypointPorts(8080)}

			err := s.waitForSandboxEntryPointsReady(context.Background(), probeTestPodIP, entry)

			require.NoError(t, err)
			assert.Len(t, *calls, tt.wantAttempts)
			for _, c := range *calls {
				assert.Equal(t, 10*time.Millisecond, c.timeout, "dial timeout follows the configured interval")
				remaining := time.Until(c.deadline)
				assert.Greater(t, remaining, 0*time.Second)
				assert.LessOrEqual(t, remaining, 5*time.Second, "probe deadline comes from the configured timeout, not the 15s default")
			}
		})
	}
}

func TestWaitForSandboxEntryPointsReady_TimesOut(t *testing.T) {
	errDial := errors.New("connection refused")
	calls := stubEntrypointDial(t, func(int, string) error { return errDial })
	s := &Server{config: &Config{
		SandboxReadyProbeTimeout:  300 * time.Millisecond,
		SandboxReadyProbeInterval: 10 * time.Millisecond,
	}}
	entry := &sandboxEntry{Ports: entrypointPorts(8080)}

	start := time.Now()
	err := s.waitForSandboxEntryPointsReady(context.Background(), probeTestPodIP, entry)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox entrypoints not ready before timeout")
	assert.ErrorIs(t, err, errDial)
	assert.Greater(t, len(*calls), 1, "it keeps retrying until the timeout")
	assert.Less(t, elapsed, 5*time.Second, "the configured timeout is used, not the 15s default")
}

func TestWaitForSandboxEntryPointsReady_DefaultsWhenNotConfigured(t *testing.T) {
	tests := []struct {
		name   string
		server *Server
	}{
		{name: "nil server", server: nil},
		{name: "nil config", server: &Server{}},
		{name: "zero config values", server: &Server{config: &Config{}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errDial := errors.New("connection refused")
			calls := stubEntrypointDial(t, func(int, string) error { return errDial })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			entry := &sandboxEntry{Ports: entrypointPorts(8080)}

			start := time.Now()
			err := tt.server.waitForSandboxEntryPointsReady(ctx, probeTestPodIP, entry)

			require.Error(t, err)
			assert.ErrorIs(t, err, errDial)
			assert.Len(t, *calls, 1, "one attempt is made before the canceled context ends the wait")
			assert.Less(t, time.Since(start), 5*time.Second)
			assert.Equal(t, defaultSandboxReadyDialTimeout, (*calls)[0].timeout, "default 1s interval gives the 1s dial timeout")
			remaining := time.Until((*calls)[0].deadline)
			assert.Greater(t, remaining, 10*time.Second, "probe deadline comes from the 15s default")
			assert.LessOrEqual(t, remaining, 15*time.Second)
		})
	}
}

func TestWaitForSandboxEntryPointsReady_ParentContextCanceled(t *testing.T) {
	errDial := errors.New("connection refused")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := stubEntrypointDial(t, func(call int, _ string) error {
		if call == 1 {
			cancel()
		}
		return errDial
	})
	s := &Server{config: &Config{
		SandboxReadyProbeTimeout:  time.Minute,
		SandboxReadyProbeInterval: 50 * time.Millisecond,
	}}
	entry := &sandboxEntry{Ports: entrypointPorts(8080)}

	start := time.Now()
	err := s.waitForSandboxEntryPointsReady(ctx, probeTestPodIP, entry)

	require.Error(t, err)
	assert.ErrorIs(t, err, errDial)
	assert.Len(t, *calls, 2, "it stops right after the cancellation, long before the one minute timeout")
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestWaitForSandboxEntryPointsReady_AllPortsMustBeReady(t *testing.T) {
	calls := stubEntrypointDial(t, func(call int, endpoint string) error {
		if endpoint == "10.0.0.1:9090" && call < 5 {
			return errors.New("connection refused")
		}
		return nil
	})
	s := &Server{config: &Config{
		SandboxReadyProbeTimeout:  5 * time.Second,
		SandboxReadyProbeInterval: 10 * time.Millisecond,
	}}
	entry := &sandboxEntry{Ports: entrypointPorts(8080, 9090)}

	err := s.waitForSandboxEntryPointsReady(context.Background(), probeTestPodIP, entry)

	require.NoError(t, err)
	assert.Equal(t, []string{
		"10.0.0.1:8080", "10.0.0.1:9090",
		"10.0.0.1:8080", "10.0.0.1:9090",
		"10.0.0.1:8080", "10.0.0.1:9090",
	}, endpointsOf(*calls), "every pass re-dials the ports that were already up, ready only when all are")
}

func TestWaitForSandboxEntryPointsReady_TimeoutShorterThanInterval(t *testing.T) {
	calls := stubEntrypointDial(t, func(int, string) error { return errors.New("connection refused") })
	s := &Server{config: &Config{
		SandboxReadyProbeTimeout:  50 * time.Millisecond,
		SandboxReadyProbeInterval: time.Minute,
	}}
	entry := &sandboxEntry{Ports: entrypointPorts(8080)}

	start := time.Now()
	err := s.waitForSandboxEntryPointsReady(context.Background(), probeTestPodIP, entry)

	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "it gives up at the timeout and does not sleep a whole interval")
	assert.Len(t, *calls, 1)
}

func TestSandboxEntrypointDial_RealDialer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	t.Run("reachable endpoint", func(t *testing.T) {
		err := sandboxEntrypointDial(context.Background(), listener.Addr().String(), time.Second)
		assert.NoError(t, err)
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := sandboxEntrypointDial(ctx, listener.Addr().String(), time.Second)
		assert.ErrorIs(t, err, context.Canceled)
	})
}
