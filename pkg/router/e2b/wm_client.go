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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

// #nosec G101 -- well-known Kubernetes service account token path, not a credential.
const serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

const identityHeaderName = "X-AgentCube-User-Identity"

// WorkloadManagerClient defines the control-plane operations E2B invokes on WorkloadManager.
type WorkloadManagerClient interface {
	CreateCodeInterpreter(ctx context.Context, ownerID string, req *types.CreateSandboxRequest) (*types.CreateSandboxResponse, error)
	ListCodeInterpreterSessions(ctx context.Context, ownerID, namespace string) ([]*types.SandboxInfo, error)
	GetCodeInterpreterSession(ctx context.Context, ownerID, namespace, sessionID string) (*types.SandboxInfo, error)
	UpdateCodeInterpreterSessionTTL(ctx context.Context, ownerID, namespace, sessionID string, req *types.UpdateSandboxTTLRequest) (*types.UpdateSandboxTTLResponse, error)
	DeleteCodeInterpreterSession(ctx context.Context, ownerID, namespace, sessionID string) error
}

// WMStatusError represents a non-2xx HTTP response returned by WorkloadManager.
type WMStatusError struct {
	StatusCode int
	Message    string
}

func (e *WMStatusError) Error() string {
	return fmt.Sprintf("workloadmanager returned status %d: %s", e.StatusCode, e.Message)
}

type httpWMClient struct {
	baseURL    string
	httpClient *http.Client
	signer     IdentitySigner
}

// NewHTTPWorkloadManagerClient wraps Router's existing HTTP/2+mTLS client and IdentitySigner.
func NewHTTPWorkloadManagerClient(baseURL string, httpClient *http.Client, signer IdentitySigner) (WorkloadManagerClient, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("WORKLOAD_MANAGER_URL cannot be empty")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("httpClient cannot be nil")
	}
	return &httpWMClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		signer:     signer,
		httpClient: httpClient,
	}, nil
}

func (c *httpWMClient) CreateCodeInterpreter(ctx context.Context, ownerID string, req *types.CreateSandboxRequest) (*types.CreateSandboxResponse, error) {
	var resp types.CreateSandboxResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/code-interpreter", ownerID, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *httpWMClient) ListCodeInterpreterSessions(ctx context.Context, ownerID, namespace string) ([]*types.SandboxInfo, error) {
	endpoint := "/v1/code-interpreter/sessions"
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}
	var resp []*types.SandboxInfo
	if err := c.doJSON(ctx, http.MethodGet, endpoint, ownerID, nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *httpWMClient) GetCodeInterpreterSession(ctx context.Context, ownerID, namespace, sessionID string) (*types.SandboxInfo, error) {
	endpoint := "/v1/code-interpreter/sessions/" + url.PathEscape(sessionID)
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}
	var resp types.SandboxInfo
	if err := c.doJSON(ctx, http.MethodGet, endpoint, ownerID, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *httpWMClient) UpdateCodeInterpreterSessionTTL(ctx context.Context, ownerID, namespace, sessionID string, req *types.UpdateSandboxTTLRequest) (*types.UpdateSandboxTTLResponse, error) {
	endpoint := "/v1/code-interpreter/sessions/" + url.PathEscape(sessionID) + "/ttl"
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}
	var resp types.UpdateSandboxTTLResponse
	if err := c.doJSON(ctx, http.MethodPatch, endpoint, ownerID, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (c *httpWMClient) DeleteCodeInterpreterSession(ctx context.Context, ownerID, namespace, sessionID string) error {
	endpoint := "/v1/code-interpreter/sessions/" + url.PathEscape(sessionID)
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}
	return c.doJSON(ctx, http.MethodDelete, endpoint, ownerID, nil, nil)
}

func (c *httpWMClient) doJSON(ctx context.Context, method, path, ownerID string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token := loadServiceAccountToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if err := c.attachIdentityHeader(req, ownerID); err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s failed: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("read response from %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBytes))
		var apiErr struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(respBytes, &apiErr) == nil {
			if apiErr.Error != "" {
				msg = apiErr.Error
			} else if apiErr.Message != "" {
				msg = apiErr.Message
			}
		}
		return &WMStatusError{StatusCode: resp.StatusCode, Message: msg}
	}
	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response from %s %s: %w", method, path, err)
		}
	}
	return nil
}

func (c *httpWMClient) attachIdentityHeader(req *http.Request, ownerID string) error {
	if ownerID == "" || c.signer == nil {
		return nil
	}
	token, err := c.signer.GenerateToken(map[string]interface{}{
		"sub": ownerID,
		"aud": "workloadmanager",
		"exp": time.Now().Add(60 * time.Second).Unix(),
		"iat": time.Now().Unix(),
	})
	if err != nil {
		return fmt.Errorf("sign caller identity token: %w", err)
	}
	req.Header.Set(identityHeaderName, token)
	return nil
}

func loadServiceAccountToken() string {
	data, err := os.ReadFile(serviceAccountTokenPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			klog.V(4).Infof("Failed to read service account token: %v", err)
		}
		return ""
	}
	return strings.TrimSpace(string(data))
}
