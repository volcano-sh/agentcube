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

package e2b

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	cubefake "github.com/volcano-sh/agentcube/client-go/clientset/versioned/fake"
	cubeinformers "github.com/volcano-sh/agentcube/client-go/informers/externalversions"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

type memStore struct {
	items map[string]*types.SandboxInfo
}

func newMemStore() *memStore {
	return &memStore{items: make(map[string]*types.SandboxInfo)}
}

func (m *memStore) Ping(_ context.Context) error { return nil }
func (m *memStore) GetSandboxBySessionID(_ context.Context, sessionID string) (*types.SandboxInfo, error) {
	sb, ok := m.items[sessionID]
	if !ok {
		return nil, store.ErrNotFound
	}
	cp := *sb
	return &cp, nil
}
func (m *memStore) StoreSandbox(_ context.Context, sb *types.SandboxInfo) error {
	cp := *sb
	m.items[sb.SessionID] = &cp
	return nil
}
func (m *memStore) UpdateSandbox(_ context.Context, sb *types.SandboxInfo) error {
	cp := *sb
	m.items[sb.SessionID] = &cp
	return nil
}
func (m *memStore) UpdateSandboxExpiry(_ context.Context, sessionID string, expiresAt time.Time, extendOnly bool) (*types.SandboxInfo, error) {
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
func (m *memStore) ClaimExpiredSandbox(_ context.Context, sessionID string, now time.Time) (bool, error) {
	sb, ok := m.items[sessionID]
	if !ok || sb.Status == "deleting" || sb.ExpiresAt.After(now) {
		return false, nil
	}
	sb.Status = "deleting"
	return true, nil
}
func (m *memStore) DeleteSandboxBySessionID(_ context.Context, sessionID string) error {
	delete(m.items, sessionID)
	return nil
}
func (m *memStore) ListActiveSandboxes(_ context.Context, ownerID, namespace string) ([]*types.SandboxInfo, error) {
	now := time.Now()
	var out []*types.SandboxInfo
	for _, sb := range m.items {
		if !sb.ExpiresAt.After(now) || (ownerID != "" && sb.OwnerID != ownerID) || (namespace != "" && sb.SandboxNamespace != namespace) {
			continue
		}
		cp := *sb
		out = append(out, &cp)
	}
	return out, nil
}
func (m *memStore) ListExpiredSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (m *memStore) ListInactiveSandboxes(_ context.Context, _ time.Time, _ int64) ([]*types.SandboxInfo, error) {
	return nil, nil
}
func (m *memStore) UpdateSessionLastActivity(_ context.Context, _ string, _ time.Time) error {
	return nil
}
func (m *memStore) Close() error { return nil }

type mockWMClient struct {
	store         *memStore
	seq           int
	lastCreateReq *types.CreateSandboxRequest
}

func (m *mockWMClient) CreateCodeInterpreter(_ context.Context, ownerID string, req *types.CreateSandboxRequest) (*types.CreateSandboxResponse, error) {
	m.seq++
	m.lastCreateReq = req
	sid := "550e8400-e29b-41d4-a716-44665544000" + strconv.Itoa(m.seq)
	now := time.Now().UTC()
	ttl := time.Duration(300) * time.Second
	if req.TTL != nil && *req.TTL > 0 {
		ttl = time.Duration(*req.TTL) * time.Second
	}
	_ = m.store.StoreSandbox(context.Background(), &types.SandboxInfo{
		Kind:             types.SandboxKind,
		SessionID:        sid,
		OwnerID:          ownerID,
		TemplateName:     req.Name,
		Name:             "sb-" + sid,
		SandboxNamespace: req.Namespace,
		Metadata:         types.NormalizeMetadata(req.Metadata),
		PodIP:            "127.0.0.1",
		Status:           sandboxStatusReady,
		CreatedAt:        now.Add(time.Duration(m.seq) * time.Second),
		ExpiresAt:        now.Add(ttl),
		EnvdVersion:      "0.5.3",
		EnvdAccessToken:  req.EnvdAccessToken,
	})
	return &types.CreateSandboxResponse{SessionID: sid, SandboxName: "sb-" + sid, EnvdVersion: "0.5.3"}, nil
}

func (m *mockWMClient) ListCodeInterpreterSessions(ctx context.Context, ownerID, namespace string) ([]*types.SandboxInfo, error) {
	return m.store.ListActiveSandboxes(ctx, ownerID, namespace)
}

func (m *mockWMClient) GetCodeInterpreterSession(ctx context.Context, ownerID, namespace, sessionID string) (*types.SandboxInfo, error) {
	sb, err := m.store.GetSandboxBySessionID(ctx, sessionID)
	if err != nil {
		return nil, &WMStatusError{StatusCode: http.StatusNotFound, Message: "not found"}
	}
	if sb.OwnerID != ownerID || (namespace != "" && sb.SandboxNamespace != namespace) {
		return nil, &WMStatusError{StatusCode: http.StatusForbidden, Message: "forbidden"}
	}
	return sb, nil
}

func (m *mockWMClient) UpdateCodeInterpreterSessionTTL(ctx context.Context, ownerID, namespace, sessionID string, req *types.UpdateSandboxTTLRequest) (*types.UpdateSandboxTTLResponse, error) {
	sb, err := m.GetCodeInterpreterSession(ctx, ownerID, namespace, sessionID)
	if err != nil {
		return nil, err
	}
	updated, err := m.store.UpdateSandboxExpiry(ctx, sb.SessionID, time.Now().UTC().Add(req.RequestedTTL()), req.Mode == ttlModeExtend)
	if err != nil {
		return nil, &WMStatusError{StatusCode: http.StatusNotFound, Message: "not found"}
	}
	return &types.UpdateSandboxTTLResponse{SessionID: updated.SessionID, ExpiresAt: updated.ExpiresAt}, nil
}

func (m *mockWMClient) DeleteCodeInterpreterSession(ctx context.Context, ownerID, namespace, sessionID string) error {
	if _, err := m.GetCodeInterpreterSession(ctx, ownerID, namespace, sessionID); err != nil {
		return err
	}
	return m.store.DeleteSandboxBySessionID(ctx, sessionID)
}

func setupTestHandler(t *testing.T, domain string) (*Handler, *memStore, *mockWMClient, http.Handler) {
	t.Helper()
	st := newMemStore()
	wm := &mockWMClient{store: st}

	ciEnabled := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "e2b-ci",
			Namespace: "ns-a",
			UID:       "uid-ci-1",
			Labels:    map[string]string{labelE2BEnabled: "true"},
			Annotations: map[string]string{
				annotationAliases:     "code-interpreter-v1,py",
				annotationDescription: "Test Python CI",
			},
		},
		Spec: runtimev1alpha1.CodeInterpreterSpec{
			Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{Image: "e2b-ci:latest"},
		},
	}
	ciDisabled := &runtimev1alpha1.CodeInterpreter{
		ObjectMeta: metav1.ObjectMeta{Name: "picod-internal", Namespace: "ns-a"},
		Spec:       runtimev1alpha1.CodeInterpreterSpec{Template: &runtimev1alpha1.CodeInterpreterSandboxTemplate{Image: "picod:latest"}},
	}

	fakeCube := cubefake.NewSimpleClientset(ciEnabled, ciDisabled)
	ciInf := cubeinformers.NewSharedInformerFactory(fakeCube, 0).Runtime().V1alpha1().CodeInterpreters()
	require.NoError(t, ciInf.Informer().GetStore().Add(ciEnabled))
	require.NoError(t, ciInf.Informer().GetStore().Add(ciDisabled))

	auth := NewAuthenticator("ns-a")
	keySecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "e2b-api-keys", Namespace: "agentcube"},
		Data: map[string][]byte{
			HashAPIKey("key-tenant-a"): []byte("owner-a:ns-a"),
			HashAPIKey("key-tenant-b"): []byte("owner-b:ns-a"),
		},
	}
	require.NoError(t, auth.LoadFromSecret(context.Background(), fake.NewSimpleClientset(keySecret), "agentcube", "e2b-api-keys"))

	cfg := &Config{Domain: domain, DefaultNamespace: "ns-a", DefaultTTL: 300}
	h, err := NewWithDeps(cfg, st, wm, auth, NewTemplateResolver(ciInf.Lister(), "0.5.3"), nil, nil)
	require.NoError(t, err)

	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("native-router-ok"))
	})
	return h, st, wm, h.WrapHandler(fallback)
}

func TestAuthenticator_SecretFormat(t *testing.T) {
	hashA := HashAPIKey("secret-key-a")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "e2b-api-keys", Namespace: "agentcube"},
		Data:       map[string][]byte{hashA: []byte("owner-1:ns-1")},
	}
	cs := fake.NewSimpleClientset(secret)
	auth := NewAuthenticator("default")
	require.NoError(t, auth.LoadFromSecret(context.Background(), cs, "agentcube", "e2b-api-keys"))

	idA, ok := auth.Validate("secret-key-a")
	require.True(t, ok)
	assert.Equal(t, "owner-1", idA.OwnerID)
	assert.Equal(t, "ns-1", idA.Namespace)

	_, ok = auth.Validate("wrong-key")
	assert.False(t, ok)
}

func TestHandler_LifecycleAndFailClosed(t *testing.T) {
	h, _, wm, router := setupTestHandler(t, "127.0.0.1.nip.io:8081")
	_ = h

	// Non-E2B Host falls through to native Router.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
	req.Host = "agentcube-router.agentcube.svc:8080"
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "native-router-ok", w.Body.String())

	// Empty Domain fails through without intercepting api.<domain>.
	emptyCfgHandler, err := NewWithDeps(&Config{DefaultNamespace: "ns-a"}, newMemStore(), wm, NewAuthenticator("ns-a"), nil, nil, nil)
	require.NoError(t, err)
	emptyWrapped := emptyCfgHandler.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("native-only"))
	}))
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/templates", nil)
	req.Host = "api.anything.test"
	emptyWrapped.ServeHTTP(w, req)
	assert.Equal(t, "native-only", w.Body.String())

	// List templates only exposes opt-in e2b-ci.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/templates", nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var templates []Template
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &templates))
	require.Len(t, templates, 1)
	assert.Equal(t, "e2b-ci", templates[0].TemplateID)

	// Unsupported network restrictions return 501.
	for _, body := range []string{
		`{"templateID":"code-interpreter-v1","allowInternetAccess":false}`,
		`{"templateID":"code-interpreter-v1","allow_internet_access":false}`,
		`{"templateID":"code-interpreter-v1","network":{"denyOut":["10.0.0.0/8"]}}`,
		`{"templateID":"code-interpreter-v1","autoPause":true}`,
	} {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/v2/sandboxes", bytes.NewBufferString(body))
		req.Host = "api.127.0.0.1.nip.io:8081"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(apiKeyHeader, "key-tenant-a")
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotImplemented, w.Code)
	}

	// Create 3 sandboxes to test metadata filter + cursor deletion resumption.
	var createdIDs []string
	for i, team := range []string{"alpha", "beta", "gamma"} {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodPost, "/v2/sandboxes", bytes.NewBufferString(`{"templateID":"code-interpreter-v1","timeout":120,"metadata":{"team":"`+team+`"},"envVars":{"FOO":"bar"}}`))
		req.Host = "api.127.0.0.1.nip.io:8081"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(apiKeyHeader, "key-tenant-a")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)
		var created Sandbox
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
		createdIDs = append(createdIDs, created.SandboxID)
		if i == 0 {
			assert.Equal(t, "e2b-ci", created.TemplateID)
			assert.Equal(t, "bar", wm.lastCreateReq.EnvVars["FOO"])
			assert.True(t, wm.lastCreateReq.InitEnvd)
			assert.True(t, wm.lastCreateReq.DisableIdleTimeout)
		}
	}

	// Metadata filter returns matching sandbox.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v2/sandboxes?metadata=team%3Dalpha", nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var metaFiltered []ListedSandbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &metaFiltered))
	require.Len(t, metaFiltered, 1)
	assert.Equal(t, createdIDs[0], metaFiltered[0].SandboxID)

	// Cursor pagination resumes cleanly after deleting the cursor item.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v2/sandboxes?limit=1", nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var p1 []ListedSandbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p1))
	require.Len(t, p1, 1)
	cursorTok := w.Header().Get(nextTokenHeader)
	require.NotEmpty(t, cursorTok)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/sandboxes/"+p1[0].SandboxID, nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v2/sandboxes?limit=1&nextToken="+url.QueryEscape(cursorTok), nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var p2 []ListedSandbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p2))
	require.Len(t, p2, 1)
	assert.Equal(t, createdIDs[1], p2[0].SandboxID)

	// Connect with E2b-Sandbox-Id header routes to Platform API (returns 201), and cross-tenant returns 404.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v2/sandboxes/"+createdIDs[0]+"/connect", bytes.NewBufferString(`{"timeout":600}`))
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	req.Header.Set("E2b-Sandbox-Id", createdIDs[0])
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/sandboxes/"+createdIDs[0], nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-b")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_ScopesToE2BSessionsAndEscapedMetadataFilter(t *testing.T) {
	_, st, _, router := setupTestHandler(t, "127.0.0.1.nip.io:8081")

	now := time.Now().UTC()
	nativeID := "550e8400-e29b-41d4-a716-446655440077"
	st.items[nativeID] = &types.SandboxInfo{
		Kind:             types.AgentRuntimeKind,
		SessionID:        nativeID,
		OwnerID:          "owner-a",
		Name:             "native-agent",
		SandboxNamespace: "ns-a",
		Status:           sandboxStatusReady,
		CreatedAt:        now,
		ExpiresAt:        now.Add(10 * time.Minute),
	}
	e2bID := "550e8400-e29b-41d4-a716-446655440078"
	st.items[e2bID] = &types.SandboxInfo{
		Kind:             types.SandboxClaimsKind,
		SessionID:        e2bID,
		OwnerID:          "owner-a",
		TemplateName:     "e2b-ci",
		Name:             "sc-1",
		SandboxNamespace: "ns-a",
		Metadata:         map[string]string{"env": "a&b+c"},
		Status:           sandboxStatusReady,
		CreatedAt:        now,
		ExpiresAt:        now.Add(10 * time.Minute),
		EnvdVersion:      "0.5.3",
	}

	// List returns only E2B session and omits native session for same owner/namespace.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v2/sandboxes", nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var listed []ListedSandbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
	require.Len(t, listed, 1)
	assert.Equal(t, e2bID, listed[0].SandboxID)

	// Escaped inner characters (&, +) in metadata query filter match properly without double unescape.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v2/sandboxes?metadata=env%3Da%2526b%252Bc", nil)
	req.Host = "api.127.0.0.1.nip.io:8081"
	req.Header.Set(apiKeyHeader, "key-tenant-a")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var metaListed []ListedSandbox
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &metaListed))
	require.Len(t, metaListed, 1)
	assert.Equal(t, e2bID, metaListed[0].SandboxID)

	// Get, timeout, connect, and delete all reject native session with 404 without deleting it.
	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/sandboxes/" + nativeID, ""},
		{http.MethodPost, "/sandboxes/" + nativeID + "/timeout", `{"timeout":60}`},
		{http.MethodPost, "/sandboxes/" + nativeID + "/connect", `{"timeout":60}`},
		{http.MethodDelete, "/sandboxes/" + nativeID, ""},
	} {
		w = httptest.NewRecorder()
		req = httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
		req.Host = "api.127.0.0.1.nip.io:8081"
		req.Header.Set(apiKeyHeader, "key-tenant-a")
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, "%s %s", tc.method, tc.path)
	}
	_, stillExists := st.items[nativeID]
	assert.True(t, stillExists, "native session must not be deleted via E2B API")
}

func TestProxy_SecurityBoundariesAndHeaderPreservation(t *testing.T) {
	_, st, _, router := setupTestHandler(t, "127.0.0.1.nip.io:8081")

	var gotAuth, gotAccessToken, gotMethod, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccessToken = r.Header.Get("X-Access-Token")
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("proxied-ok"))
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	upstreamPort, err := strconv.Atoi(u.Port())
	require.NoError(t, err)

	sandboxID := "550e8400-e29b-41d4-a716-446655440099"
	st.items[sandboxID] = &types.SandboxInfo{
		SessionID:       sandboxID,
		PodIP:           u.Hostname(),
		Status:          sandboxStatusReady,
		ExpiresAt:       time.Now().Add(10 * time.Minute),
		EnvdVersion:     "0.5.3",
		EnvdAccessToken: "envd-secret",
	}

	// Native session without EnvdVersion is rejected (404).
	nativeID := "550e8400-e29b-41d4-a716-446655440088"
	st.items[nativeID] = &types.SandboxInfo{
		SessionID: nativeID,
		PodIP:     u.Hostname(),
		Status:    sandboxStatusReady,
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Host = strconv.Itoa(upstreamPort) + "-" + nativeID + ".127.0.0.1.nip.io:8081"
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)

	// /init on port 49983 is blocked with 403.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/init", nil)
	req.Host = "49983-" + sandboxID + ".127.0.0.1.nip.io:8081"
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)

	// Port 49999 enforces X-Access-Token.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/execute", nil)
	req.Host = "49999-" + sandboxID + ".127.0.0.1.nip.io:8081"
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/execute", nil)
	req.Host = "49999-" + sandboxID + ".127.0.0.1.nip.io:8081"
	req.Header.Set(headerEnvdAccessToken, "wrong")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/execute", nil)
	req.Host = "49999-" + sandboxID + ".127.0.0.1.nip.io:8081"
	req.Header.Set(headerEnvdAccessToken, "envd-secret")
	router.ServeHTTP(w, req)
	assert.NotEqual(t, http.StatusUnauthorized, w.Code)

	// X-Access-Token and Basic Authorization are preserved end-to-end.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodDelete, "/contexts/ctx-1", nil)
	req.Host = strconv.Itoa(upstreamPort) + "-" + sandboxID + ".127.0.0.1.nip.io:8081"
	req.Header.Set("X-Access-Token", "envd-secret")
	req.Header.Set("Authorization", "Basic dXNlcjo=")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "proxied-ok", w.Body.String())
	assert.Equal(t, http.MethodDelete, gotMethod)
	assert.Equal(t, "/contexts/ctx-1", gotPath)
	assert.Equal(t, "envd-secret", gotAccessToken)
	assert.Equal(t, "Basic dXNlcjo=", gotAuth)
}
