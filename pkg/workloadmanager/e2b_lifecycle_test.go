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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cubefake "github.com/volcano-sh/agentcube/client-go/clientset/versioned/fake"
	cubeinformers "github.com/volcano-sh/agentcube/client-go/informers/externalversions"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensionsv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

func generateTestRSAKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	require.NoError(t, err)
	pubPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubBytes}))
	return priv, pubPEM
}

func signTestIdentityToken(t *testing.T, priv *rsa.PrivateKey, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": sub,
		"aud": "workloadmanager",
		"exp": time.Now().Add(5 * time.Minute).Unix(),
		"iat": time.Now().Unix(),
	})
	s, err := tok.SignedString(priv)
	require.NoError(t, err)
	return s
}

func TestInitEnvd(t *testing.T) {
	var attempts atomic.Int32
	var receivedBody envdInitRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/init", r.URL.Path)
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&receivedBody))
		w.Header().Set("X-Envd-Version", "0.5.3")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port64, err := strconv.ParseUint(u.Port(), 10, 32)
	require.NoError(t, err)

	version, err := initEnvd(context.Background(), u.Hostname(), uint32(port64), map[string]string{"FOO": "bar"}, "secret-tok")
	require.NoError(t, err)
	assert.Equal(t, "0.5.3", version)
	assert.Equal(t, int32(2), attempts.Load())
	assert.Equal(t, "bar", receivedBody.EnvVars["FOO"])
	assert.Equal(t, "secret-tok", receivedBody.AccessToken)
	assert.NotEmpty(t, receivedBody.Timestamp)
}

func TestInitEnvd_DisablesRedirects(t *testing.T) {
	var redirectTargetHit atomic.Bool
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectTargetHit.Store(true)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer targetSrv.Close()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, targetSrv.URL, http.StatusFound)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	port64, err := strconv.ParseUint(u.Port(), 10, 32)
	require.NoError(t, err)

	_, err = initEnvd(context.Background(), u.Hostname(), uint32(port64), nil, "secret-tok")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "302")
	assert.False(t, redirectTargetHit.Load())
}

func TestApplyCodeInterpreterRequestOptions_SetsMaxCap(t *testing.T) {
	ttl := int64(120)
	req := &types.CreateSandboxRequest{
		Kind:               types.CodeInterpreterKind,
		Name:               "e2b-ci",
		Namespace:          "default",
		TTL:                &ttl,
		Metadata:           map[string]any{"k": "v"},
		EnvVars:            map[string]string{"E": "1"},
		DisableIdleTimeout: true,
		InitEnvd:           true,
		EnvdAccessToken:    "tok",
	}

	now := time.Now()
	sb := &sandboxv1beta1.Sandbox{
		Spec: sandboxv1beta1.SandboxSpec{
			Lifecycle: sandboxv1beta1.Lifecycle{
				ShutdownTime: &metav1.Time{Time: now.Add(2 * time.Minute)},
			},
		},
	}
	entry := &sandboxEntry{
		Kind:               types.SandboxKind,
		SessionID:          "sess-1",
		MaxSessionDuration: 4 * time.Hour,
		ExpiresAt:          now.Add(2 * time.Minute),
	}

	applyCodeInterpreterRequestOptions(sb, nil, entry, req)

	assert.Equal(t, "e2b-ci", entry.TemplateName)
	assert.Equal(t, uint32(defaultEnvdPort), entry.EnvdPort)
	assert.True(t, entry.DisableIdleTimeout)
	assert.Equal(t, "tok", entry.EnvdAccessToken)
	assert.WithinDuration(t, now.Add(4*time.Hour), sb.Spec.Lifecycle.ShutdownTime.Time, 2*time.Second)
	assert.WithinDuration(t, now.Add(2*time.Minute), entry.ExpiresAt, 2*time.Second)
}

func TestCreateSandbox_EnvdInitFailureRollsBack(t *testing.T) {
	s := newFakeServer()
	s.storeClient = &fakeStore{}

	patches := gomonkey.NewPatches()
	defer patches.Reset()

	patches.ApplyFunc(initEnvd, func(_ context.Context, _ string, _ uint32, _ map[string]string, _ string) (string, error) {
		return "", errors.New("envd refused")
	})

	sb := &sandboxv1beta1.Sandbox{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sb-1",
			Namespace: "default",
		},
	}
	entry := &sandboxEntry{
		Kind:      types.SandboxKind,
		SessionID: "sess-1",
		InitEnvd:  true,
		EnvdPort:  49983,
	}

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, sandboxv1beta1.AddToScheme(scheme))
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)

	ch := make(chan SandboxStatusUpdate, 1)
	ready := readySandbox()
	ready.Namespace = "default"
	ready.Name = "sb-1"
	ch <- SandboxStatusUpdate{Sandbox: ready}

	// Patch pod IP lookup and entrypoint probe
	patches.ApplyMethod(s.k8sClient, "GetSandboxPodIP", func(_ *K8sClient, _ context.Context, _, _, _ string) (string, error) {
		return "10.0.0.5", nil
	})
	origDial := sandboxEntrypointDial
	sandboxEntrypointDial = func(_ context.Context, _ string, _ time.Duration) error { return nil }
	defer func() { sandboxEntrypointDial = origDial }()

	_, err := s.createSandbox(context.Background(), dyn, sb, nil, entry, ch)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initialize envd")
	assert.Equal(t, 1, s.storeClient.(*fakeStore).deleteSessionCalls)
}

type sessionMemStore struct {
	fakeStore
	items map[string]*types.SandboxInfo
}

func (m *sessionMemStore) GetSandboxBySessionID(_ context.Context, sessionID string) (*types.SandboxInfo, error) {
	sb, ok := m.items[sessionID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sb
	return &cp, nil
}

func (m *sessionMemStore) ListActiveSandboxes(_ context.Context, ownerID string, namespace string) ([]*types.SandboxInfo, error) {
	now := time.Now()
	var out []*types.SandboxInfo
	for _, sb := range m.items {
		if !sb.ExpiresAt.After(now) {
			continue
		}
		if ownerID != "" && sb.OwnerID != ownerID {
			continue
		}
		if namespace != "" && sb.SandboxNamespace != namespace {
			continue
		}
		cp := *sb
		out = append(out, &cp)
	}
	return out, nil
}

func (m *sessionMemStore) UpdateSandboxExpiry(_ context.Context, sessionID string, expiresAt time.Time, extendOnly bool) (*types.SandboxInfo, error) {
	sb, ok := m.items[sessionID]
	if !ok {
		return nil, store.ErrNotFound
	}
	if !extendOnly || expiresAt.After(sb.ExpiresAt) {
		sb.ExpiresAt = expiresAt
	}
	cp := *sb
	return &cp, nil
}

func (m *sessionMemStore) DeleteSandboxBySessionID(_ context.Context, sessionID string) error {
	m.deleteSessionCalls++
	delete(m.items, sessionID)
	return nil
}

func TestSessionLifecycleHandlers_OwnershipAndTTL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	priv, pubPEM := generateTestRSAKeyPair(t)
	setCachedPublicKeyForTest(t, pubPEM)

	now := time.Now().UTC().Truncate(time.Second)
	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-ci",
			Namespace: "ns-a",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			MaxSessionDuration: &metav1.Duration{Duration: 30 * time.Minute},
			Template:           &runtimev1alpha1.CodeInterpreterSandboxTemplate{Image: "img:latest"},
		},
	}
	fakeCube := cubefake.NewSimpleClientset(ci)
	cubeFactory := cubeinformers.NewSharedInformerFactory(fakeCube, 0)
	ciInformer := cubeFactory.Runtime().V1alpha1().CodeInterpreters()
	require.NoError(t, ciInformer.Informer().GetStore().Add(ci))

	mem := &sessionMemStore{
		items: map[string]*types.SandboxInfo{
			"sess-a": {
				Kind:             types.SandboxKind,
				SessionID:        "sess-a",
				Name:             "sb-a",
				TemplateName:     "e2b-ci",
				SandboxNamespace: "ns-a",
				OwnerID:          "owner-a",
				Status:           "ready",
				EnvdVersion:      "0.5.3",
				CreatedAt:        now,
				ExpiresAt:        now.Add(5 * time.Minute),
			},
			"sess-creating": {
				Kind:             types.SandboxKind,
				SessionID:        "sess-creating",
				Name:             "sb-creating",
				TemplateName:     "e2b-ci",
				SandboxNamespace: "ns-a",
				OwnerID:          "owner-a",
				Status:           "creating",
				CreatedAt:        now,
				ExpiresAt:        now.Add(5 * time.Minute),
			},
			"sess-b": {
				Kind:             types.SandboxKind,
				SessionID:        "sess-b",
				Name:             "sb-b",
				TemplateName:     "e2b-ci",
				SandboxNamespace: "ns-a",
				OwnerID:          "owner-b",
				Status:           "ready",
				CreatedAt:        now,
				ExpiresAt:        now.Add(5 * time.Minute),
			},
		},
	}

	srv := newFakeServer()
	srv.storeClient = mem
	srv.informers = &Informers{CodeInterpreterLister: ciInformer.Lister()}
	srv.setupRoutes()

	tokA := signTestIdentityToken(t, priv, "owner-a")
	tokB := signTestIdentityToken(t, priv, "owner-b")

	// List without verified owner identity returns 401.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/code-interpreter/sessions?namespace=ns-a", nil)
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// List as owner-a sees only owner-a sessions (sess-a + sess-creating).
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/code-interpreter/sessions?namespace=ns-a", nil)
	req.Header.Set(identityJWTHeader, tokA)
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var listed []types.SandboxInfo
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Len(t, listed, 2)

	// TTL update on a non-ready ("creating") session is rejected with 409 Conflict.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/code-interpreter/sessions/sess-creating/ttl", bytes.NewBufferString(`{"ttl":600,"mode":"set"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identityJWTHeader, tokA)
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	// Cross-owner get returns 403.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/code-interpreter/sessions/sess-a", nil)
	req.Header.Set(identityJWTHeader, tokB)
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// TTL extend past MaxSessionDuration is capped to CreatedAt + 30m.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPatch, "/v1/code-interpreter/sessions/sess-a/ttl", bytes.NewBufferString(`{"ttl":7200,"mode":"extend"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identityJWTHeader, tokA)
	srv.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var ttlResp types.UpdateSandboxTTLResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &ttlResp))
	assert.WithinDuration(t, now.Add(30*time.Minute), ttlResp.ExpiresAt, 2*time.Second)

	// Cross-owner delete is rejected with 403, missing identity is also rejected with 403.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/code-interpreter/sessions/sess-a", nil)
	req.Header.Set(identityJWTHeader, tokB)
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/code-interpreter/sessions/sess-a", nil)
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// AgentRuntime delete route must not delete an E2B CodeInterpreter session.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/agent-runtime/sessions/sess-a", nil)
	req.Header.Set(identityJWTHeader, tokA)
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// E2B create with initEnvd=true requires verified owner identity.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/code-interpreter", bytes.NewBufferString(`{"name":"e2b-ci","namespace":"ns-a","initEnvd":true}`))
	req.Header.Set("Content-Type", "application/json")
	srv.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	assert.Equal(t, 0, mem.deleteSessionCalls)
}

func TestE2BAndNativeAuthClientSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	priv, pubPEM := generateTestRSAKeyPair(t)
	setCachedPublicKeyForTest(t, pubPEM)

	ci := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-ci",
			Namespace: "ns-a",
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{Image: "img:latest"},
		},
	}
	fakeCube := cubefake.NewSimpleClientset(ci)
	cubeFactory := cubeinformers.NewSharedInformerFactory(fakeCube, 0)
	ciInformer := cubeFactory.Runtime().V1alpha1().CodeInterpreters()
	require.NoError(t, ciInformer.Informer().GetStore().Add(ci))

	wmDynamic := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme())
	mem := &sessionMemStore{
		items: map[string]*types.SandboxInfo{
			"e2b-sess": {
				Kind:             types.SandboxKind,
				SessionID:        "e2b-sess",
				Name:             "sb-e2b",
				TemplateName:     "e2b-ci",
				SandboxNamespace: "ns-a",
				OwnerID:          "owner-a",
				Status:           "ready",
				EnvdVersion:      "0.5.3",
			},
			"native-oidc-sess": {
				Kind:             types.SandboxKind,
				SessionID:        "native-oidc-sess",
				Name:             "sb-native",
				SandboxNamespace: "ns-a",
				OwnerID:          "oidc-user",
				Status:           "ready",
			},
		},
	}

	srv := newFakeServer()
	srv.config.EnableAuth = true
	srv.k8sClient = &K8sClient{dynamicClient: wmDynamic}
	srv.storeClient = mem
	srv.informers = &Informers{CodeInterpreterLister: ciInformer.Lister()}

	r := gin.New()
	r.POST("/v1/code-interpreter", srv.handleCodeInterpreterCreate)
	r.DELETE("/v1/code-interpreter/sessions/:sessionId", srv.handleDeleteCodeInterpreterSession)
	r.DELETE("/v1/sandboxes/:sessionId", srv.handleDeleteSandbox)

	tokA := signTestIdentityToken(t, priv, "owner-a")

	var capturedClient dynamic.Interface
	patches := gomonkey.NewPatches()
	defer patches.Reset()
	patches.ApplyPrivateMethod(reflect.TypeOf(srv), "createSandbox", func(
		_ *Server,
		_ context.Context,
		client dynamic.Interface,
		_ *sandboxv1beta1.Sandbox,
		_ *extensionsv1beta1.SandboxClaim,
		entry *sandboxEntry,
		_ <-chan SandboxStatusUpdate,
	) (*types.CreateSandboxResponse, error) {
		capturedClient = client
		return &types.CreateSandboxResponse{SessionID: entry.SessionID, SandboxName: "sb-new", EnvdVersion: "0.5.3"}, nil
	})

	// Verified E2B create uses WorkloadManager's dynamicClient even when EnableAuth=true.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/code-interpreter", bytes.NewBufferString(`{"name":"e2b-ci","namespace":"ns-a","initEnvd":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identityJWTHeader, tokA)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Same(t, wmDynamic, capturedClient)

	// Non-E2B CodeInterpreter create (initEnvd=false) still requires extractUserK8sClient under EnableAuth=true.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/code-interpreter", bytes.NewBufferString(`{"name":"e2b-ci","namespace":"ns-a"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(identityJWTHeader, tokA)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// Verified E2B delete uses WorkloadManager's dynamicClient under EnableAuth=true.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/code-interpreter/sessions/e2b-sess", nil)
	req.Header.Set(identityJWTHeader, tokA)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, mem.items, "e2b-sess")

	// Native Router-created session with OwnerID can still be deleted directly without X-AgentCube-Identity;
	// here EnableAuth=true proceeds past authorizeSessionAccess into extractUserK8sClient (401 rather than 403),
	// and succeeds cleanly when EnableAuth=false.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/native-oidc-sess", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	srv.config.EnableAuth = false
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/v1/sandboxes/native-oidc-sess", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, mem.items, "native-oidc-sess")
}
