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
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// closeNotifyRecorder adds http.CloseNotifier, which httputil.ReverseProxy
// asks for and httptest.ResponseRecorder does not implement.
type closeNotifyRecorder struct {
	*httptest.ResponseRecorder
}

func (closeNotifyRecorder) CloseNotify() <-chan bool { return make(chan bool) }

// newInvokeBenchServer builds a Server whose invoke path is the real one
// (handleInvoke -> forwardToSandbox) with an in-process upstream sandbox.
func newInvokeBenchServer(b *testing.B, kind string) (*Server, string) {
	b.Helper()
	gin.DefaultWriter = io.Discard
	klog.LogToStderr(false)
	klog.SetOutput(io.Discard)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	b.Cleanup(upstream.Close)

	jwtMgr, err := NewJWTManager()
	if err != nil {
		b.Fatal(err)
	}
	transport := &http.Transport{}
	b.Cleanup(transport.CloseIdleConnections)

	sandbox := &types.SandboxInfo{
		Kind:      kind,
		SessionID: "session-1",
		Name:      "sb",
		EntryPoints: []types.SandboxEntryPoint{{
			Path:     "/",
			Protocol: "HTTP",
			Endpoint: upstream.Listener.Addr().String(),
		}},
	}
	s := &Server{
		config:         &Config{MaxConcurrentRequests: 1 << 20},
		sessionManager: &mockSessionManager{sandbox: sandbox},
		storeClient:    &fakeStoreClient{},
		httpTransport:  transport,
		jwtManager:     jwtMgr,
	}
	s.setupRoutes()
	return s, "/v1/namespaces/default/code-interpreters/ci/invocations/api/ping"
}

func BenchmarkInvoke(b *testing.B) {
	for _, kind := range []string{types.SandboxClaimsKind, types.CodeInterpreterKind} {
		b.Run(kind, func(b *testing.B) {
			s, path := newInvokeBenchServer(b, kind)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				req.Header.Set("x-agentcube-session-id", "session-1")
				w := closeNotifyRecorder{httptest.NewRecorder()}
				s.engine.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					b.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
			}
		})
	}
}
