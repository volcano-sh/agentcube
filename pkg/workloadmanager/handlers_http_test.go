/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workloadmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

type httpFakeStore struct {
	store.Store
	sandbox *types.SandboxInfo
}

func (f *httpFakeStore) GetSandboxBySessionID(_ context.Context, _ string) (*types.SandboxInfo, error) {
	if f.sandbox == nil {
		return nil, store.ErrNotFound
	}
	return f.sandbox, nil
}
func (f *httpFakeStore) StoreSandbox(_ context.Context, _ *types.SandboxInfo) error { return nil }
func (f *httpFakeStore) UpdateSandbox(_ context.Context, _ *types.SandboxInfo) error { return nil }
func (f *httpFakeStore) DeleteSandboxBySessionID(_ context.Context, _ string) error { return nil }

func TestHandlers_HTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	
	s := &Server{
		config:            &Config{EnableAuth: false},
		k8sClient:         &K8sClient{},
		sandboxController: &SandboxReconciler{},
		storeClient:       &httpFakeStore{},
		informers:         &Informers{},
	}
	s.setupRoutes()

	t.Run("handleHealth", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/health", nil)
		s.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "healthy")
	})

	t.Run("handleDeleteSandbox", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		
		patches.ApplyFunc(extractUserInfo, func(_ *gin.Context) (string, string, string, string) {
			return "token", "ns", "sa", "sa-name"
		})

		patches.ApplyMethod(reflect.TypeOf(s.k8sClient), "GetOrCreateUserK8sClient", func(_ *K8sClient, _, _, _ string) (*UserK8sClient, error) {
			return &UserK8sClient{dynamicClient: nil}, nil
		})
		
		patches.ApplyFunc(deleteSandbox, func(_ context.Context, _ dynamic.Interface, _, _ string) error {
			return nil
		})
		patches.ApplyFunc(deleteSandboxClaim, func(_ context.Context, _ dynamic.Interface, _, _ string) error {
			return nil
		})

		// Mock store to return a sandbox
		s.storeClient = &httpFakeStore{
			sandbox: &types.SandboxInfo{
				Kind:             types.AgentRuntimeKind,
				SessionID:        "sess-123",
				SandboxNamespace: "default",
				Name:             "test-sb",
			},
		}

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("DELETE", "/v1/agent-runtime/sessions/sess-123", nil)
		s.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("handleAgentRuntimeCreate", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		
		patches.ApplyFunc(extractUserInfo, func(_ *gin.Context) (string, string, string, string) {
			return "token", "ns", "sa", "sa-name"
		})
		
		patches.ApplyMethod(reflect.TypeOf(s.k8sClient), "GetOrCreateUserK8sClient", func(_ *K8sClient, _, _, _ string) (*UserK8sClient, error) {
			return &UserK8sClient{dynamicClient: nil}, nil
		})

		patches.ApplyFunc(buildSandboxByAgentRuntime, func(_, _ string, _ *Informers) (*sandboxv1alpha1.Sandbox, *sandboxEntry, error) {
			return &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default", UID: "uid-123"},
			}, &sandboxEntry{SessionID: "sess-123", Ports: []runtimev1alpha1.TargetPort{{Port: 8080}}}, nil
		})
		
		// Mock WatchSandboxOnce
		resultChan := make(chan SandboxStatusUpdate, 1)
		resultChan <- SandboxStatusUpdate{Sandbox: &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default", UID: "uid-123"},
		}}
		patches.ApplyMethod(reflect.TypeOf(s.sandboxController), "WatchSandboxOnce", func(_ *SandboxReconciler, _ context.Context, _, _ string) <-chan SandboxStatusUpdate {
			return resultChan
		})
		patches.ApplyMethod(reflect.TypeOf(s.sandboxController), "UnWatchSandbox", func(_ *SandboxReconciler, _, _ string) {})

		// Mock internal createSandbox call chain
		patches.ApplyFunc(createSandbox, func(_ context.Context, _ dynamic.Interface, _ *sandboxv1alpha1.Sandbox) (*SandboxInfo, error) {
			return &SandboxInfo{Name: "test-sb", Namespace: "default"}, nil
		})
		
		patches.ApplyMethod(reflect.TypeOf(s.k8sClient), "GetSandboxPodIP", func(_ *K8sClient, _ context.Context, _, _, _ string) (string, error) {
			return "10.0.0.1", nil
		})

		body, _ := json.Marshal(types.CreateSandboxRequest{
			Namespace: "default",
			Name:      "test-agent",
		})
		
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/agent-runtime", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		s.router.ServeHTTP(w, req)
		
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "sess-123")
	})

	t.Run("handleCodeInterpreterCreate", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		
		patches.ApplyFunc(extractUserInfo, func(_ *gin.Context) (string, string, string, string) {
			return "token", "ns", "sa", "sa-name"
		})
		
		patches.ApplyMethod(reflect.TypeOf(s.k8sClient), "GetOrCreateUserK8sClient", func(_ *K8sClient, _, _, _ string) (*UserK8sClient, error) {
			return &UserK8sClient{dynamicClient: nil}, nil
		})

		patches.ApplyFunc(buildSandboxByCodeInterpreter, func(_, _ string, _ *Informers) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxEntry, error) {
			return &sandboxv1alpha1.Sandbox{
				ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default", UID: "uid-123"},
			}, nil, &sandboxEntry{SessionID: "sess-123", Ports: []runtimev1alpha1.TargetPort{{Port: 8080}}}, nil
		})
		
		// Mock WatchSandboxOnce
		resultChan := make(chan SandboxStatusUpdate, 1)
		resultChan <- SandboxStatusUpdate{Sandbox: &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{Name: "test-sb", Namespace: "default", UID: "uid-123"},
		}}
		patches.ApplyMethod(reflect.TypeOf(s.sandboxController), "WatchSandboxOnce", func(_ *SandboxReconciler, _ context.Context, _, _ string) <-chan SandboxStatusUpdate {
			return resultChan
		})
		patches.ApplyMethod(reflect.TypeOf(s.sandboxController), "UnWatchSandbox", func(_ *SandboxReconciler, _, _ string) {})

		// Mock internal createSandbox call chain
		patches.ApplyFunc(createSandbox, func(_ context.Context, _ dynamic.Interface, _ *sandboxv1alpha1.Sandbox) (*SandboxInfo, error) {
			return &SandboxInfo{Name: "test-sb", Namespace: "default"}, nil
		})
		
		patches.ApplyMethod(reflect.TypeOf(s.k8sClient), "GetSandboxPodIP", func(_ *K8sClient, _ context.Context, _, _, _ string) (string, error) {
			return "10.0.0.1", nil
		})

		body, _ := json.Marshal(types.CreateSandboxRequest{
			Namespace: "default",
			Name:      "test-ci",
		})
		
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/code-interpreter", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		s.router.ServeHTTP(w, req)
		
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "sess-123")
	})

	t.Run("handleDeleteSandbox_NotFound", func(t *testing.T) {
		patches := gomonkey.NewPatches()
		defer patches.Reset()
		
		// Mock store to return not found
		s.storeClient = &httpFakeStore{sandbox: nil}

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("DELETE", "/v1/agent-runtime/sessions/sess-missing", nil)
		s.router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
