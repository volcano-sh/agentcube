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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

type fakeStore struct {
	store.Store
	storeErr           error
	updateErr          error
	storeCalls         int
	updateCalls        int
	deleteSessionCalls int
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
	f.deleteSessionCalls++
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
			Annotations:       map[string]string{sandboxv1alpha1.SandboxPodNameAnnotation: "pod-1"},
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
		Kind:      types.SandboxKind,
		SessionID: "sess-1",
		Ports: []runtimev1alpha1.TargetPort{
			{Port: 8080, Protocol: runtimev1alpha1.ProtocolTypeHTTP, PathPrefix: "/api"},
		},
	}
}

type recordingStore struct {
	fakeStore
	lastUpdated *types.SandboxInfo
}

func (f *recordingStore) UpdateSandbox(ctx context.Context, sandbox *types.SandboxInfo) error {
	if err := f.fakeStore.UpdateSandbox(ctx, sandbox); err != nil {
		return err
	}
	copied := *sandbox
	copied.EntryPoints = append([]types.SandboxEntryPoint(nil), sandbox.EntryPoints...)
	f.lastUpdated = &copied
	return nil
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
		expectStoreDeleteCalls int
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
			name:                   "sandbox creation fails triggers rollback",
			createSandboxErr:       errors.New("create sandbox failed"),
			expectErr:              true,
			expectCreateCalls:      1,
			expectDeleteCalls:      1,
			expectStoreDeleteCalls: 1,
		},
		{
			name:                   "sandbox claim creation fails triggers rollback",
			sandboxClaim:           true,
			createClaimErr:         errors.New("create claim failed"),
			expectErr:              true,
			expectClaimCalls:       1,
			expectDeleteCalls:      1,
			expectStoreDeleteCalls: 1,
		},
		{
			name:                   "pod ip lookup fails triggers rollback",
			podIPErr:               errors.New("pod ip missing"),
			sendResult:             true,
			expectErr:              true,
			expectCreateCalls:      1,
			expectDeleteCalls:      1,
			expectStoreDeleteCalls: 1,
		},
		{
			name:                   "entrypoint readiness failure triggers rollback",
			readyErr:               errors.New("connection refused"),
			sendResult:             true,
			expectErr:              true,
			expectCreateCalls:      1,
			expectDeleteCalls:      1,
			expectStoreDeleteCalls: 1,
		},
		{
			name:                   "update store fails triggers rollback",
			updateErr:              errors.New("update failed"),
			sendResult:             true,
			expectErr:              true,
			expectCreateCalls:      1,
			expectUpdateCalls:      1,
			expectDeleteCalls:      1,
			expectStoreDeleteCalls: 1,
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
	patches.ApplyPrivateMethod(reflect.TypeOf(server), "waitForCreatedSandbox", func(_ *Server, _ context.Context, _ dynamic.Interface, sandbox *sandboxv1alpha1.Sandbox, _ *extensionsv1alpha1.SandboxClaim, resultChan <-chan SandboxStatusUpdate) (*sandboxv1alpha1.Sandbox, error) {
		if resultChan == nil {
			return readySandbox(), nil
		}
		select {
		case result := <-resultChan:
			return result.Sandbox, nil
		default:
			return sandbox, nil
		}
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

			entry := makeEntry()
			if tt.sandboxClaim {
				entry.Kind = types.SandboxClaimsKind
			}
			resp, err := server.createSandbox(context.Background(), nil, sb, claim, entry, resultChan)

			require.Equal(t, tt.expectCreateCalls, createCalls, "createSandbox call count")
			require.Equal(t, tt.expectClaimCalls, claimCalls, "createSandboxClaim call count")
			require.Equal(t, tt.expectDeleteCalls, deleteCalls, "delete call count")
			require.Equal(t, 1, fakeStoreInst.storeCalls, "StoreSandbox call count")
			require.Equal(t, tt.expectUpdateCalls, fakeStoreInst.updateCalls, "UpdateSandbox call count")
			require.Equal(t, tt.expectStoreDeleteCalls, fakeStoreInst.deleteSessionCalls, "DeleteSandboxBySessionID call count")

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
			expectedKind := types.SandboxKind
			if tt.sandboxClaim {
				expectedKind = types.SandboxClaimsKind
			}
			require.Equal(t, expectedKind, resp.Kind)
			require.Len(t, resp.EntryPoints, 1)
			require.Equal(t, "/api", resp.EntryPoints[0].Path)
			require.Equal(t, "10.0.0.9:8080", resp.EntryPoints[0].Endpoint)
		})
	}
}

func TestServerCreateSandboxClaimUsesAdoptedSandboxButStoresClaimName(t *testing.T) {
	claim := &extensionsv1alpha1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions.agents.x-k8s.io/v1alpha1",
			Kind:       types.SandboxClaimsKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ci-claim",
			Namespace: "ns-1",
		},
		Status: extensionsv1alpha1.SandboxClaimStatus{
			SandboxStatus: extensionsv1alpha1.SandboxStatus{
				Name: "warm-pool-sandbox-abc",
			},
		},
	}
	claimObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(claim)
	require.NoError(t, err)
	claimUnstructured := &unstructured.Unstructured{Object: claimObj}

	adoptedSandboxUnstructured := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "agents.x-k8s.io/v1alpha1",
			"kind":       types.SandboxKind,
			"metadata": map[string]interface{}{
				"name":              "warm-pool-sandbox-abc",
				"namespace":         "ns-1",
				"uid":               "adopted-uid",
				"creationTimestamp": metav1.Now().Format(time.RFC3339),
				"annotations": map[string]interface{}{
					sandboxv1alpha1.SandboxPodNameAnnotation: "warm-pool-pod-abc",
				},
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   string(sandboxv1alpha1.SandboxConditionReady),
						"status": string(metav1.ConditionTrue),
					},
				},
			},
		},
	}

	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("get", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		getAction, ok := action.(k8stesting.GetAction)
		require.True(t, ok)
		switch {
		case action.GetResource() == SandboxClaimGVR && getAction.GetName() == "ci-claim":
			return true, claimUnstructured.DeepCopy(), nil
		case action.GetResource() == SandboxGVR && getAction.GetName() == "warm-pool-sandbox-abc":
			return true, adoptedSandboxUnstructured.DeepCopy(), nil
		default:
			return false, nil, nil
		}
	})

	storeInst := &recordingStore{}
	server := &Server{k8sClient: &K8sClient{}, storeClient: storeInst}

	var gotNamespace, gotSandboxName, gotPodName string
	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(createSandboxClaim, func(_ context.Context, _ dynamic.Interface, _ *extensionsv1alpha1.SandboxClaim) error {
		return nil
	})
	patches.ApplyFunc(deleteSandboxClaim, func(_ context.Context, _ dynamic.Interface, _, _ string) error {
		return nil
	})
	patches.ApplyMethod(reflect.TypeOf((*K8sClient)(nil)), "GetSandboxPodIP", func(_ *K8sClient, _ context.Context, namespace, sandboxName, podName string) (string, error) {
		gotNamespace = namespace
		gotSandboxName = sandboxName
		gotPodName = podName
		return "10.0.0.10", nil
	})
	patches.ApplyPrivateMethod(reflect.TypeOf(server), "waitForSandboxEntryPointsReady", func(_ *Server, _ context.Context, _ string, _ *sandboxEntry) error {
		return nil
	})

	templateSandbox := &sandboxv1alpha1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claim.Name,
			Namespace: claim.Namespace,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	entry := makeEntry()
	entry.Kind = types.SandboxClaimsKind
	resp, err := server.createSandbox(ctx, dynamicClient, templateSandbox, claim, entry, nil)

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "ci-claim", resp.SandboxName)
	require.Equal(t, "adopted-uid", resp.SandboxID)
	require.Equal(t, "ns-1", gotNamespace)
	require.Equal(t, "warm-pool-sandbox-abc", gotSandboxName)
	require.Equal(t, "warm-pool-pod-abc", gotPodName)

	require.NotNil(t, storeInst.lastUpdated)
	require.Equal(t, types.SandboxClaimsKind, storeInst.lastUpdated.Kind)
	require.Equal(t, "ci-claim", storeInst.lastUpdated.Name)
	require.Equal(t, "ns-1", storeInst.lastUpdated.SandboxNamespace)
	require.Equal(t, "adopted-uid", storeInst.lastUpdated.SandboxID)
	require.Equal(t, "10.0.0.10:8080", storeInst.lastUpdated.EntryPoints[0].Endpoint)
}

func TestWaitForDirectSandboxReadyWatcherFailures(t *testing.T) {
	server := &Server{}
	sandbox := readySandbox()

	createdSandbox, err := server.waitForDirectSandboxReady(context.Background(), sandbox, nil)
	require.Nil(t, createdSandbox)
	require.ErrorIs(t, err, errSandboxReadyWatcherNotRegistered)

	closedResultChan := make(chan SandboxStatusUpdate)
	close(closedResultChan)
	createdSandbox, err = server.waitForDirectSandboxReady(context.Background(), sandbox, closedResultChan)
	require.Nil(t, createdSandbox)
	require.ErrorIs(t, err, errSandboxReadyWatcherClosed)

	emptyResultChan := make(chan SandboxStatusUpdate, 1)
	emptyResultChan <- SandboxStatusUpdate{}
	createdSandbox, err = server.waitForDirectSandboxReady(context.Background(), sandbox, emptyResultChan)
	require.Nil(t, createdSandbox)
	require.ErrorIs(t, err, errSandboxReadyWatcherMissingSandbox)
}

func TestWaitForClaimSandboxReadyReturnsForbidden(t *testing.T) {
	claim := &extensionsv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
	}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	getCalls := 0
	dynamicClient.PrependReactor("get", "sandboxclaims", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		getCalls++
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: SandboxClaimGVR.Group, Resource: SandboxClaimGVR.Resource},
			claim.Name,
			errors.New("access denied"),
		)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	createdSandbox, err := (&Server{}).waitForClaimSandboxReady(ctx, dynamicClient, claim)

	require.Nil(t, createdSandbox)
	require.Error(t, err)
	require.True(t, apierrors.IsInternalError(err), "expected permanent read error to fail immediately, got %v", err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, getCalls)
}

func TestWaitForClaimSandboxReadyReturnsSandboxForbidden(t *testing.T) {
	claim := &extensionsv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
		Status: extensionsv1alpha1.SandboxClaimStatus{
			SandboxStatus: extensionsv1alpha1.SandboxStatus{Name: "adopted-sandbox"},
		},
	}
	claimObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(claim)
	require.NoError(t, err)

	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	claimGetCalls := 0
	sandboxGetCalls := 0
	dynamicClient.PrependReactor("get", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		switch action.GetResource() {
		case SandboxClaimGVR:
			claimGetCalls++
			return true, &unstructured.Unstructured{Object: claimObject}, nil
		case SandboxGVR:
			sandboxGetCalls++
			return true, nil, apierrors.NewForbidden(
				schema.GroupResource{Group: SandboxGVR.Group, Resource: SandboxGVR.Resource},
				"adopted-sandbox",
				errors.New("access denied"),
			)
		default:
			return false, nil, nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	createdSandbox, err := (&Server{}).waitForClaimSandboxReady(ctx, dynamicClient, claim)

	require.Nil(t, createdSandbox)
	require.True(t, apierrors.IsInternalError(err), "expected permanent read error to fail immediately, got %v", err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, claimGetCalls)
	require.Equal(t, 1, sandboxGetCalls)
}

func TestWaitForClaimSandboxReadyReturnsConversionError(t *testing.T) {
	claim := &extensionsv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
	}
	malformedClaim := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "extensions.agents.x-k8s.io/v1alpha1",
		"kind":       "SandboxClaim",
		"metadata": map[string]interface{}{
			"name":      claim.Name,
			"namespace": claim.Namespace,
		},
		"status": map[string]interface{}{
			"sandbox": map[string]interface{}{"name": int64(1)},
		},
	}}
	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	getCalls := 0
	dynamicClient.PrependReactor("get", "sandboxclaims", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		getCalls++
		return true, malformedClaim.DeepCopy(), nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	createdSandbox, err := (&Server{}).waitForClaimSandboxReady(ctx, dynamicClient, claim)

	require.Nil(t, createdSandbox)
	require.True(t, apierrors.IsInternalError(err), "expected conversion error to fail immediately, got %v", err)
	require.NotErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, 1, getCalls)
}

func TestWaitForClaimSandboxReadyCancelsInFlightGet(t *testing.T) {
	tests := []struct {
		name          string
		parentTimeout time.Duration
		waitTimeout   time.Duration
		blockSandbox  bool
		cancelParent  bool
		wantErr       error
	}{
		{
			name:          "internal readiness deadline",
			parentTimeout: 2 * time.Second,
			waitTimeout:   250 * time.Millisecond,
			wantErr:       errSandboxCreationTimeout,
		},
		{
			name:          "internal readiness deadline during sandbox read",
			parentTimeout: 2 * time.Second,
			waitTimeout:   250 * time.Millisecond,
			blockSandbox:  true,
			wantErr:       errSandboxCreationTimeout,
		},
		{
			name:          "parent deadline",
			parentTimeout: 250 * time.Millisecond,
			waitTimeout:   2 * time.Second,
			wantErr:       context.DeadlineExceeded,
		},
		{
			name:          "parent cancellation",
			parentTimeout: 2 * time.Second,
			waitTimeout:   2 * time.Second,
			cancelParent:  true,
			wantErr:       context.Canceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestCanceled := make(chan struct{})
			ctx, cancel := context.WithTimeout(context.Background(), tt.parentTimeout)
			defer cancel()
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.blockSandbox && strings.Contains(r.URL.Path, "/sandboxclaims/") {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(&extensionsv1alpha1.SandboxClaim{
						TypeMeta: metav1.TypeMeta{
							APIVersion: SandboxClaimGVR.Group + "/" + SandboxClaimGVR.Version,
							Kind:       "SandboxClaim",
						},
						ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
						Status: extensionsv1alpha1.SandboxClaimStatus{
							SandboxStatus: extensionsv1alpha1.SandboxStatus{Name: "sandbox-1"},
						},
					})
					return
				}
				if tt.cancelParent {
					cancel()
				}
				<-r.Context().Done()
				close(requestCanceled)
			}))
			defer apiServer.Close()

			dynamicClient, err := dynamic.NewForConfig(&rest.Config{Host: apiServer.URL})
			require.NoError(t, err)

			claim := &extensionsv1alpha1.SandboxClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
			}

			createdSandbox, err := (&Server{}).waitForClaimSandboxReadyWithTimeout(ctx, dynamicClient, claim, tt.waitTimeout)

			require.Nil(t, createdSandbox)
			require.ErrorIs(t, err, tt.wantErr)
			if errors.Is(tt.wantErr, errSandboxCreationTimeout) {
				require.NoError(t, ctx.Err(), "internal deadline must expire before the parent context")
				require.NotErrorIs(t, err, context.DeadlineExceeded)
			} else {
				require.NotErrorIs(t, err, errSandboxCreationTimeout)
			}
			select {
			case <-requestCanceled:
			case <-time.After(time.Second):
				t.Fatal("Kubernetes GET was not canceled")
			}
		})
	}
}

func TestWaitForClaimSandboxReadyRejectsLateReadySandbox(t *testing.T) {
	claim := &extensionsv1alpha1.SandboxClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "ci-claim", Namespace: "ns-1"},
		Status: extensionsv1alpha1.SandboxClaimStatus{
			SandboxStatus: extensionsv1alpha1.SandboxStatus{Name: "sandbox-1"},
		},
	}
	claimObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(claim)
	require.NoError(t, err)
	sandboxObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(readySandbox())
	require.NoError(t, err)

	dynamicClient := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	dynamicClient.PrependReactor("get", "*", func(action k8stesting.Action) (bool, runtime.Object, error) {
		switch action.GetResource() {
		case SandboxClaimGVR:
			return true, &unstructured.Unstructured{Object: claimObject}, nil
		case SandboxGVR:
			// The fake deliberately ignores cancellation to verify the post-GET deadline check.
			time.Sleep(100 * time.Millisecond)
			return true, &unstructured.Unstructured{Object: sandboxObject}, nil
		default:
			return false, nil, nil
		}
	})

	createdSandbox, err := (&Server{}).waitForClaimSandboxReadyWithTimeout(
		context.Background(), dynamicClient, claim, 20*time.Millisecond,
	)

	require.Nil(t, createdSandbox)
	require.ErrorIs(t, err, errSandboxCreationTimeout)
}

func TestIsRetryableSandboxReadError(t *testing.T) {
	resource := schema.GroupResource{Group: SandboxClaimGVR.Group, Resource: SandboxClaimGVR.Resource}
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{name: "not found", err: apierrors.NewNotFound(resource, "claim"), retryable: true},
		{name: "wrapped not found", err: fmt.Errorf("get claim: %w", apierrors.NewNotFound(resource, "claim")), retryable: true},
		{name: "timeout", err: apierrors.NewTimeoutError("timeout", 1), retryable: true},
		{name: "server timeout", err: apierrors.NewServerTimeout(resource, "get", 1), retryable: true},
		{name: "too many requests", err: apierrors.NewTooManyRequests("busy", 1), retryable: true},
		{name: "service unavailable", err: apierrors.NewServiceUnavailable("unavailable"), retryable: true},
		{name: "internal server error", err: apierrors.NewInternalError(errors.New("server failed")), retryable: true},
		{name: "wrapped unexpected EOF", err: fmt.Errorf("get claim: %w", fmt.Errorf("read response: %w", io.ErrUnexpectedEOF)), retryable: true},
		{name: "forbidden", err: apierrors.NewForbidden(resource, "claim", errors.New("denied")), retryable: false},
		{name: "wrapped forbidden", err: fmt.Errorf("get claim: %w", apierrors.NewForbidden(resource, "claim", errors.New("denied"))), retryable: false},
		{name: "unauthorized", err: apierrors.NewUnauthorized("unauthorized"), retryable: false},
		{name: "conversion error", err: errors.New("cannot convert object"), retryable: false},
		{name: "context deadline", err: context.DeadlineExceeded, retryable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.retryable, isRetryableSandboxReadError(tt.err))
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

			patches.ApplyFunc(buildSandboxByAgentRuntime, func(_, _, _ string, _ *Informers) (*sandboxv1alpha1.Sandbox, *sandboxEntry, error) {
				if tc.kind != types.AgentRuntimeKind {
					return nil, nil, errors.New("unexpected kind")
				}
				if tc.buildErr != nil {
					return nil, nil, tc.buildErr
				}
				return sb, entry, nil
			})

			patches.ApplyFunc(buildSandboxByCodeInterpreter, func(_, _, _ string, _ *Informers) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxEntry, error) {
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

// This test verifies that the deleteSandbox handler correctly handles scenarios where the client disconnects (Context Cancellation) before the deletion operation completes.
//
// Key Points:
//
// The handler creates a new context for the K8s deletion operation using context.WithTimeout(ctx, deletionTimeout).
// This derived context remains valid even if the parent context (c.Request.Context()) is canceled.
// The test simulates a client disconnect by canceling the request context immediately after calling the deleteSandbox function.
// It verifies that the store deletion (the final cleanup step) still occurs by checking that the store's DeleteSandboxBySessionID method was called with a valid, non-canceled context.
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

func TestHandleSandboxCreate_IdentityErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name          string
		extractErr    error
		expectStatus  int
		expectMessage string
	}{
		{
			name:          "public key not cached returns 503",
			extractErr:    ErrPublicKeyNotCached,
			expectStatus:  http.StatusServiceUnavailable,
			expectMessage: "identity verifier not ready",
		},
		{
			name:          "invalid identity token returns 401",
			extractErr:    ErrVerificationFailed,
			expectStatus:  http.StatusUnauthorized,
			expectMessage: "invalid identity token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeServer := newFakeServer()
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(`{"name":"workload","namespace":"ns"}`))
			req.Header.Set("Content-Type", "application/json")
			c.Request = req

			patches := gomonkey.NewPatches()
			defer patches.Reset()

			patches.ApplyFunc(extractOwnerID, func(_ *http.Request) (string, error) {
				return "", tt.extractErr
			})

			fakeServer.handleSandboxCreate(c, types.AgentRuntimeKind)

			require.Equal(t, tt.expectStatus, w.Code)
			var errResp ErrorResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
			require.Equal(t, tt.expectMessage, errResp.Message)
		})
	}
}
