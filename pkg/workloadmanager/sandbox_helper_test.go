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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

// newTestBootstrapAuth creates an in-memory BootstrapAuthManager without K8s,
// suitable for unit tests.
func newTestBootstrapAuth(t *testing.T) *BootstrapAuthManager {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privKey.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubKeyBytes})

	return &BootstrapAuthManager{
		privateKey:   privKey,
		publicKeyPEM: string(pubPEM),
		namespace:    "test",
	}
}

func TestInitializePicoD_SessionKeyOnlySetOnSuccess(t *testing.T) {
	// Arrange: mock PicoD server that returns 200 OK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/init", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		var body map[string]string
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.NotEmpty(t, body["token"])
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	entry := &sandboxEntry{
		SessionID: "sess-1",
		AuthMode:  runtimev1alpha1.AuthModePicoD,
		Ports: []runtimev1alpha1.TargetPort{
			{Name: "picod", Port: tsPort(t, ts.URL)},
		},
	}
	s := &Server{
		httpClient:    ts.Client(),
		bootstrapAuth: newTestBootstrapAuth(t),
	}

	err := s.initializePicoD(context.Background(), tsHost(t, ts.URL), entry)
	require.NoError(t, err)
	assert.NotEmpty(t, entry.SessionPrivateKey, "key must be set after 200 OK")
}

func TestGetSandboxStatus_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		sandbox  *sandboxv1alpha1.Sandbox
		expected string
	}{
		{
			name: "ready condition true",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: "ready",
		},
		{
			name: "ready condition false without reason",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expected: "not-ready",
		},
		{
			name: "ready condition false with reason is not-ready",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:    string(sandboxv1alpha1.SandboxConditionReady),
							Status:  metav1.ConditionFalse,
							Reason:  "ErrImagePull",
							Message: "Back-off pulling image",
						},
					},
				},
			},
			expected: "not-ready",
		},
		{
			name: "ready condition unknown",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionUnknown,
						},
					},
				},
			},
			expected: "not-ready",
		},
		{
			name: "no conditions",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{},
				},
			},
			expected: "not-ready",
		},
		{
			name: "nil conditions",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: nil,
				},
			},
			expected: "not-ready",
		},
		{
			name: "other condition type",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "OtherCondition",
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: "not-ready",
		},
		{
			name: "multiple conditions with ready true",
			sandbox: &sandboxv1alpha1.Sandbox{
				Status: sandboxv1alpha1.SandboxStatus{
					Conditions: []metav1.Condition{
						{
							Type:   "OtherCondition",
							Status: metav1.ConditionFalse,
						},
						{
							Type:   string(sandboxv1alpha1.SandboxConditionReady),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: "ready",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getSandboxStatus(tt.sandbox)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInitializePicoD_SessionKeyNotSetOnFailure(t *testing.T) {
	// Arrange: mock PicoD server that returns 500
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	entry := &sandboxEntry{
		SessionID: "sess-2",
		AuthMode:  runtimev1alpha1.AuthModePicoD,
		Ports: []runtimev1alpha1.TargetPort{
			{Name: "picod", Port: tsPort(t, ts.URL)},
		},
	}
	s := &Server{
		httpClient:    ts.Client(),
		bootstrapAuth: newTestBootstrapAuth(t),
	}

	err := s.initializePicoD(context.Background(), "127.0.0.1", entry)
	require.Error(t, err)
	assert.Empty(t, entry.SessionPrivateKey,
		"key must NOT be set when /init returns a non-200 status")
}

func TestInitializePicoD_SkipsNonPicoDMode(t *testing.T) {
	entry := &sandboxEntry{
		SessionID: "sess-3",
		AuthMode:  runtimev1alpha1.AuthModeNone,
	}
	s := &Server{bootstrapAuth: newTestBootstrapAuth(t)}
	err := s.initializePicoD(context.Background(), "127.0.0.1", entry)
	require.NoError(t, err)
	assert.Empty(t, entry.SessionPrivateKey)
}

func TestFindPicoDInitPort_NamedPort(t *testing.T) {
	ports := []runtimev1alpha1.TargetPort{
		{Name: "http", Port: 9000},
		{Name: "picod", Port: 8080},
	}
	assert.Equal(t, uint32(8080), findPicoDInitPort(ports))
}

func TestFindPicoDInitPort_Fallback(t *testing.T) {
	ports := []runtimev1alpha1.TargetPort{
		{Name: "http", Port: 9000},
	}
	assert.Equal(t, uint32(8080), findPicoDInitPort(ports))
}

// tsPort extracts the port number from a httptest.Server URL string.
func tsPort(t *testing.T, rawURL string) uint32 {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	portStr := u.Port()
	var port uint32
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	return port
}

// tsHost extracts the host IP/name from a httptest.Server URL string.
func tsHost(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	return u.Hostname()
}
