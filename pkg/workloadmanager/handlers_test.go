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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/volcano-sh/agentcube/pkg/api"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	"sigs.k8s.io/agent-sandbox/controllers"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

type fakeStore struct {
	store.Store
	storeErr         error
	updateErr        error
	storeCalls       int
	updateCalls      int
	deleteStoreCalls int32
}

func (f *fakeStore) Ping(_ context.Context) error { return nil }
func (f *fakeStore) GetSandboxBySessionID(_ context.Context, _ string) (*types.SandboxInfo, error) {
	return nil, store.ErrNotFound
}
func (f *fakeStore) StoreSandbox(_ context.Context, _ *types.SandboxInfo) error {
	f.storeCalls++
	return f.storeErr
}
func (f *fakeStore) UpdateSandbox(_ context.Context, _ *types.SandboxInfo) error {
	f.updateCalls++
	return f.updateErr
}
func (f *fakeStore) DeleteSandboxBySessionID(_ context.Context, _ string) error {
	atomic.AddInt32(&f.deleteStoreCalls, 1)
	return nil
}
func (f *fakeStore) ListExpiredSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (f *fakeStore) ListInactiveSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (f *fakeStore) UpdateSessionLastActivity(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (f *fakeStore) Close() error { return nil }

func readySandbox() *sandboxv1alpha1.Sandbox {
	return &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "sandbox-1",
			Namespace:         "ns-1",
			UID:               "uid-123",
			Annotations:       map[string]string{controllers.SandboxPodNameAnnotation: "pod-1"},
			CreationTimestamp: metav1.Now(),
		},
		Status: sandboxv1alpha1.SandboxStatus{Conditions: []metav1.Condition{{
			Type:   string(sandboxv1alpha1.SandboxConditionReady),
			Status: metav1.ConditionTrue,
		}}},
	}
}

func makeEntry() *sandboxEntry {
	return &sandboxEntry{
		Kind:      types.AgentRuntimeKind,
		SessionID: "sess-1",
		Ports: []runtimev1alpha1.TargetPort{
			{Port: 8080, Protocol: runtimev1alpha1.ProtocolTypeHTTP, PathPrefix: "/api"},
		},
	}
}

func TestServerCreateSandbox(t *testing.T) {
	type testCase struct {
		name                   string
		sandboxClaim           bool
		storeErr               error
		createSandboxErr       error
		createClaimErr         error
		podIPErr               error
		readyErr               error
		updateErr              error
		sendResult             bool
		expectErr              bool
		expectCreateCalls      int
		expectClaimCalls       int
		expectDeleteCalls      int
		expectDeleteStoreCalls int
		expectUpdateCalls      int
	}
	tests := []testCase{
		{
			name:              "creates sandbox successfully",
			sendResult:        true,
			expectCreateCalls: 1,
			expectUpdateCalls: 1,
		},
		{
			name:              "creates sandbox claim successfully",
			sandboxClaim:      true,
			sendResult:        true,
			expectClaimCalls:  1,
			expectUpdateCalls: 1,
		},
		{
			name:      "store placeholder fails",
			storeErr:  errors.New("store failed"),
			expectErr: true,
		},
		{
			name:                   "sandbox creation fails",
			createSandboxErr:       errors.New("create sandbox failed"),
			expectErr:              true,
			expectCreateCalls:      1,
			expectDeleteCalls:      1,
			expectDeleteStoreCalls: 1,
		},
		{
			name:                   "sandbox claim creation fails",
			sandboxClaim:           true,
			createClaimErr:         errors.New("create claim failed"),
			expectErr:              true,
			expectClaimCalls:       1,
			expectDeleteCalls:      1,
			expectDeleteStoreCalls: 1,
		},
		{
			name:                   "pod ip lookup fails triggers rollback",
			podIPErr:               errors.New("pod ip missing"),
			sendResult:             true,
			expectErr:              true,
			expectCreateCalls:      1,
			expectDeleteCalls:      1,
			expectDeleteStoreCalls: 1,
		},
		{
			name:                   "entrypoint readiness failure triggers rollback",
			readyErr:               errors.New("connection refused"),
			sendResult:             true,
			expectErr:              true,
			expectCreateCalls:      1,
			expectDeleteCalls:      1,
			expectDeleteStoreCalls: 1,
		},
		{
			name:                   "update store fails cleans up placeholder without K8s rollback",
			updateErr:              errors.New("update failed"),
			sendResult:             true,
			expectErr:              true,
			expectCreateCalls:      1,
			expectUpdateCalls:      1,
			expectDeleteCalls:      0,
			expectDeleteStoreCalls: 1,
		},
	}

	// Apply all patches ONCE at the outer level. Re-patching the same function
	// per-subtest on arm64 causes gomonkey to silently fail on the second apply
	// because PC-relative branch instructions don't recalculate correctly after
	// a reset. A single patch whose closure reads from a shared *testCase pointer
	// avoids the repeated patch-reset-repatch cycle entirely.
	var cur *testCase
	var createCalls, claimCalls, deleteCalls int

	server := &Server{k8sClient: &K8sClient{}}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(createSandbox, func(_ context.Context, _ dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox) (*SandboxInfo, error) {
		createCalls++
		if cur.createSandboxErr != nil {
			return nil, cur.createSandboxErr
		}
		return &SandboxInfo{Name: sandbox.Name, Namespace: sandbox.Namespace}, nil
	})
	patches.ApplyFunc(createSandboxClaim, func(_ context.Context, _ dynamic.Interface, _ *extensionsv1alpha1.SandboxClaim) error {
		claimCalls++
		return cur.createClaimErr
	})
	patches.ApplyFunc(deleteSandbox, func(_ context.Context, _ dynamic.Interface, _, _ string) error {
		deleteCalls++
		return nil
	})
	patches.ApplyFunc(deleteSandboxClaim, func(_ context.Context, _ dynamic.Interface, _, _ string) error {
		deleteCalls++
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf((*K8sClient)(nil)), "GetSandboxPodIP", func(_ *K8sClient, _ context.Context, _, _, _ string) (string, error) {
		if cur.podIPErr != nil {
			return "", cur.podIPErr
		}
		return "10.0.0.9", nil
	})
	patches.ApplyPrivateMethod(reflect.TypeOf(server), "waitForSandboxEntryPointsReady", func(_ *Server, _ context.Context, _ string, _ *sandboxEntry) error {
		return cur.readyErr
	})

	for i := range tests {
		tt := &tests[i]
		t.Run(tt.name, func(t *testing.T) {
			cur = tt
			createCalls = 0
			claimCalls = 0
			deleteCalls = 0

			fakeStoreInst := &fakeStore{storeErr: tt.storeErr, updateErr: tt.updateErr}
			server.storeClient = fakeStoreInst

			resultChan := make(chan SandboxStatusUpdate, 1)
			sb := readySandbox()
			if tt.sendResult {
				resultChan <- SandboxStatusUpdate{Sandbox: sb.DeepCopy()}
			}

			claim := (*extensionsv1alpha1.SandboxClaim)(nil)
			if tt.sandboxClaim {
				claim = &extensionsv1alpha1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: sb.Name, Namespace: sb.Namespace}}
			}

			resp, err := server.createSandbox(context.Background(), nil, sb, claim, makeEntry(), resultChan)

			require.Equal(t, tt.expectCreateCalls, createCalls, "createSandbox call count")
			require.Equal(t, tt.expectClaimCalls, claimCalls, "createSandboxClaim call count")
			require.Equal(t, tt.expectDeleteCalls, deleteCalls, "delete call count")
			require.Equal(t, 1, fakeStoreInst.storeCalls, "StoreSandbox call count")
			require.Equal(t, tt.expectUpdateCalls, fakeStoreInst.updateCalls, "UpdateSandbox call count")

			// Wait for async store cleanup goroutine safely
			if tt.expectDeleteStoreCalls > 0 {
				require.Eventually(t, func() bool {
					return int(atomic.LoadInt32(&fakeStoreInst.deleteStoreCalls)) == tt.expectDeleteStoreCalls
				}, 2*time.Second, 10*time.Millisecond, "DeleteSandboxBySessionID call count")
			} else {
				require.Equal(t, 0, int(atomic.LoadInt32(&fakeStoreInst.deleteStoreCalls)), "DeleteSandboxBySessionID call count")
			}

			if tt.expectErr {
				require.Error(t, err)
				if tt.storeErr != nil {
					require.True(t, apierrors.IsInternalError(err))
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Equal(t, "sess-1", resp.SessionID)
			require.Equal(t, sb.Name, resp.SandboxName)
			require.Equal(t, string(sb.UID), resp.SandboxID)
			require.Len(t, resp.EntryPoints, 1)
			require.Equal(t, "/api", resp.EntryPoints[0].Path)
			require.Equal(t, "10.0.0.9:8080", resp.EntryPoints[0].Endpoint)
		})
	}
}

func newFakeServer() *Server {
	return &Server{
		config:            &Config{SandboxReadyProbeTimeout: 5 * time.Millisecond, SandboxReadyProbeInterval: time.Millisecond},
		k8sClient:         &K8sClient{},
		sandboxController: &SandboxReconciler{},
		storeClient:       &fakeStore{},
	}
}

func makeSandbox(kind, ns, name string) (*sandboxv1alpha1.Sandbox, *sandboxEntry) {
	entry := &sandboxEntry{
		Kind:      kind,
		SessionID: "sess-1",
		Ports: []runtimev1alpha1.TargetPort{
			{Port: 8080, Protocol: runtimev1alpha1.ProtocolTypeHTTP, PathPrefix: "/api"},
		},
	}
	return &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status: sandboxv1alpha1.SandboxStatus{Conditions: []metav1.Condition{{
			Type:   string(sandboxv1alpha1.SandboxConditionReady),
			Status: metav1.ConditionTrue,
		}}},
	}, entry
}

func TestHandleSandboxCreate(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name              string
		kind              string
		body              string
		buildErr          error
		buildNotFound     bool
		createErr         error
		createResp        *types.CreateSandboxResponse
		expectStatus      int
		expectMessage     string
		expectCreateCalls int
	}{
		{
			name:          "invalid json",
			kind:          types.AgentRuntimeKind,
			body:          "{invalid",
			expectStatus:  http.StatusBadRequest,
			expectMessage: "Invalid request body",
		},
		{
			name:          "validation error missing namespace",
			kind:          types.AgentRuntimeKind,
			body:          `{"name":"workload"}`,
			expectStatus:  http.StatusBadRequest,
			expectMessage: "namespace is required",
		},
		{
			name:          "agent runtime not found",
			kind:          types.AgentRuntimeKind,
			body:          `{"name":"workload","namespace":"ns"}`,
			buildErr:      api.ErrAgentRuntimeNotFound,
			buildNotFound: true,
			expectStatus:  http.StatusNotFound,
			expectMessage: api.ErrAgentRuntimeNotFound.Error(),
		},
		{
			name:          "build sandbox internal error",
			kind:          types.AgentRuntimeKind,
			body:          `{"name":"workload","namespace":"ns"}`,
			buildErr:      errors.New("boom"),
			expectStatus:  http.StatusInternalServerError,
			expectMessage: "internal server error",
		},
		{
			name:              "create sandbox error exposes message for non-internal errors",
			kind:              types.AgentRuntimeKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createErr:         errors.New("sandbox ns/name failed: ErrImagePull"),
			expectStatus:      http.StatusInternalServerError,
			expectMessage:     "sandbox ns/name failed: ErrImagePull",
			expectCreateCalls: 1,
		},
		{
			name:              "create sandbox internal error is sanitized",
			kind:              types.AgentRuntimeKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createErr:         api.NewInternalError(errors.New("store connection refused")),
			expectStatus:      http.StatusInternalServerError,
			expectMessage:     "internal server error",
			expectCreateCalls: 1,
		},
		{
			name:              "context canceled returns 499",
			kind:              types.AgentRuntimeKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createErr:         context.Canceled,
			expectStatus:      499,
			expectCreateCalls: 1,
		},
		{
			name:              "context deadline exceeded returns 504",
			kind:              types.AgentRuntimeKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createErr:         context.DeadlineExceeded,
			expectStatus:      http.StatusGatewayTimeout,
			expectMessage:     "request timed out",
			expectCreateCalls: 1,
		},
		{
			name:              "sandbox creation timeout returns 504",
			kind:              types.AgentRuntimeKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createErr:         errSandboxCreationTimeout,
			expectStatus:      http.StatusGatewayTimeout,
			expectMessage:     "sandbox creation timed out",
			expectCreateCalls: 1,
		},
		{
			name:              "create sandbox success agent runtime",
			kind:              types.AgentRuntimeKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createResp:        &types.CreateSandboxResponse{SessionID: "sess-1", SandboxID: "id-1", SandboxName: "sandbox-1"},
			expectStatus:      http.StatusOK,
			expectCreateCalls: 1,
		},
		{
			name:              "create sandbox success code interpreter",
			kind:              types.CodeInterpreterKind,
			body:              `{"name":"workload","namespace":"ns"}`,
			createResp:        &types.CreateSandboxResponse{SessionID: "sess-1", SandboxID: "id-2", SandboxName: "sandbox-2"},
			expectStatus:      http.StatusOK,
			expectCreateCalls: 1,
		},
	}

	for _, tt := range tests {
		tc := tt
		t.Run(tc.name, func(t *testing.T) {
			fakeServer := newFakeServer()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req

			sb, entry := makeSandbox(tc.kind, "ns", "sandbox-1")
			claim := &extensionsv1alpha1.SandboxClaim{ObjectMeta: metav1.ObjectMeta{Name: sb.Name, Namespace: sb.Namespace}}

			patches := gomonkey.NewPatches()
			defer patches.Reset()

			patches.ApplyFunc(buildSandboxByAgentRuntime, func(_, _ string, _ *Informers) (*sandboxv1alpha1.Sandbox, *sandboxEntry, error) {
				if tc.kind != types.AgentRuntimeKind {
					return nil, nil, errors.New("unexpected kind")
				}
				if tc.buildErr != nil {
					return nil, nil, tc.buildErr
				}
				return sb, entry, nil
			})

			patches.ApplyFunc(buildSandboxByCodeInterpreter, func(_, _ string, _ *Informers) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxEntry, error) {
				if tc.kind != types.CodeInterpreterKind {
					return nil, nil, nil, errors.New("unexpected kind")
				}
				if tc.buildErr != nil {
					return nil, nil, nil, tc.buildErr
				}
				return sb, claim, entry, nil
			})

			createCalls := 0
			patches.ApplyPrivateMethod(reflect.TypeOf(fakeServer), "createSandbox", func(_ *Server, _ context.Context, _ dynamic.Interface, _ *sandboxv1alpha1.Sandbox, _ *extensionsv1alpha1.SandboxClaim, _ *sandboxEntry, _ <-chan SandboxStatusUpdate) (*types.CreateSandboxResponse, error) {
				createCalls++
				if tc.createErr != nil {
					return nil, tc.createErr
				}
				if tc.createResp != nil {
					return tc.createResp, nil
				}
				return nil, nil
			})

			fakeServer.handleSandboxCreate(c, tc.kind)

			require.Equal(t, tc.expectCreateCalls, createCalls, "createSandbox call count")
			require.Equal(t, tc.expectStatus, w.Code)

			if tc.expectStatus != http.StatusOK {
				if tc.expectMessage != "" {
					var errResp ErrorResponse
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
					require.Equal(t, tc.expectMessage, errResp.Message)
				}
				return
			}

			var resp types.CreateSandboxResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			if tc.createResp != nil {
				require.Equal(t, *tc.createResp, resp)
			}
		})
	}
}

/*
This test verifies that the deleteSandbox handler correctly handles scenarios where the client disconnects (Context Cancellation) before the deletion operation completes.

Key Points:

The handler creates a new context for the K8s deletion operation using context.WithTimeout(ctx, deletionTimeout).
This derived context remains valid even if the parent context (c.Request.Context()) is canceled.
The test simulates a client disconnect by canceling the request context immediately after calling the deleteSandbox function.
It verifies that the store deletion (the final cleanup step) still occurs by checking that the store's DeleteSandboxBySessionID method was called with a valid, non-canceled context.
*/
func TestHandleDeleteSandbox_DetachedContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fakeServer := newFakeServer()

	fakeStoreInst := &fakeStore{}
	fakeServer.storeClient = fakeStoreInst

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyMethod(reflect.TypeOf((*fakeStore)(nil)), "GetSandboxBySessionID", func(_ *fakeStore, _ context.Context, _ string) (*types.SandboxInfo, error) {
		return &types.SandboxInfo{
			Kind:             types.AgentRuntimeKind,
			SandboxNamespace: "ns-1",
			Name:             "sandbox-1",
			SessionID:        "sess-1",
		}, nil
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodDelete, "/sandboxes/sess-1", nil)

	reqCtx, cancelReq := context.WithCancel(context.Background())
	req = req.WithContext(reqCtx)
	c.Request = req
	c.Params = gin.Params{{Key: "sessionId", Value: "sess-1"}}

	patches.ApplyFunc(deleteSandbox, func(_ context.Context, _ dynamic.Interface, _, _ string) error {
		cancelReq()
		return nil
	})

	storeDeleteCalled := false
	patches.ApplyMethod(reflect.TypeOf((*fakeStore)(nil)), "DeleteSandboxBySessionID", func(_ *fakeStore, ctx context.Context, _ string) error {
		require.NoError(t, ctx.Err(), "Store deletion context MUST NOT be canceled despite client disconnect")
		storeDeleteCalled = true
		return nil
	})

	fakeServer.handleDeleteSandbox(c)

	require.True(t, storeDeleteCalled, "DeleteSandboxBySessionID should be called even if the request context is canceled")
	require.Equal(t, http.StatusOK, w.Code)
}