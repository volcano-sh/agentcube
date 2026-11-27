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

package router

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

func TestWaitForUpstreamReachableWithoutRetriesDoesNotDial(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = listener.Close() }()

	var accepted atomic.Int32
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepted.Add(1)
			_ = conn.Close()
		}
	}()

	targetURL, err := buildURL("HTTP", listener.Addr().String())
	require.NoError(t, err)

	s := &Server{config: &Config{InitialConnectRetryCount: 0, InitialConnectRetryInterval: 100 * time.Millisecond}}
	require.NoError(t, s.waitForUpstreamReachable(context.Background(), targetURL))

	time.Sleep(50 * time.Millisecond)
	assert.Zero(t, accepted.Load(), "preflight should not open a connection when no retries are configured")
}

func TestForwardToSandboxRefusedUpstreamReturnsBadGateway(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext}
	defer transport.CloseIdleConnections()
	s := &Server{
		config:        &Config{MaxConcurrentRequests: 10, InitialConnectRetryCount: 0, InitialConnectRetryInterval: 200 * time.Millisecond},
		httpTransport: transport,
		sessionManager: &mockSessionManager{sandbox: &types.SandboxInfo{
			Kind:        types.CodeInterpreterKind,
			SessionID:   "s1",
			EntryPoints: []types.SandboxEntryPoint{{Path: "/", Protocol: "HTTP", Endpoint: address}},
		}},
		storeClient: &fakeStoreClient{},
	}
	s.setupRoutes()

	routerServer := httptest.NewServer(s.engine)
	defer routerServer.Close()

	req, err := http.NewRequest(http.MethodGet, routerServer.URL+"/v1/namespaces/default/code-interpreters/ci/invocations/x", nil)
	require.NoError(t, err)
	req.Header.Set("x-agentcube-session-id", "s1")
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
}
