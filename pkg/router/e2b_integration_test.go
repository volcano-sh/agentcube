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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

type stubSessionManager struct {
	sandbox *types.SandboxInfo
}

func (s *stubSessionManager) GetSandboxBySession(_ context.Context, _, _, _, _ string) (*types.SandboxInfo, error) {
	return s.sandbox, nil
}

func (s *stubSessionManager) Close() error { return nil }

func TestHandleInvoke_RejectsE2BSessionOnNativePath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	srv := &Server{
		config: &Config{MaxConcurrentRequests: 10},
		sessionManager: &stubSessionManager{
			sandbox: &types.SandboxInfo{
				SessionID:   "550e8400-e29b-41d4-a716-446655440001",
				Name:        "e2b-ci-pod",
				PodIP:       "10.0.0.9",
				Status:      "ready",
				EnvdVersion: "0.11.0",
				ExpiresAt:   time.Now().Add(5 * time.Minute),
			},
		},
		storeClient: &fakeStoreClient{},
	}
	srv.setupRoutes()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/namespaces/agentcube/code-interpreters/e2b-ci/invocations/init", nil)
	req.Header.Set("x-agentcube-session-id", "550e8400-e29b-41d4-a716-446655440001")
	srv.engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "SESSION_NOT_FOUND")
}
