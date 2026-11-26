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
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	headerEnvdAccessToken      = "X-Access-Token" //nolint:gosec // Header name, not a secret.
	sandboxStatusReady         = "ready"
	defaultCodeInterpreterPort = 49999
)

type sandboxTarget struct {
	sandboxID string
	port      int
}

func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func isAPIHost(host, domain string) bool {
	domainOnly := strings.ToLower(stripHostPort(strings.TrimSpace(domain)))
	if domainOnly == "" {
		return false
	}
	return strings.ToLower(stripHostPort(strings.TrimSpace(host))) == "api."+domainOnly
}

// extractHostTarget parses "<port>-<sandboxID>.<domain>" when Host matches the configured E2B domain.
func extractHostTarget(host, domain string) (sandboxTarget, bool, error) {
	domainOnly := strings.ToLower(stripHostPort(strings.TrimSpace(domain)))
	if domainOnly == "" {
		return sandboxTarget{}, false, nil
	}
	hostOnly := strings.ToLower(stripHostPort(strings.TrimSpace(host)))
	suffix := "." + domainOnly
	if !strings.HasSuffix(hostOnly, suffix) {
		return sandboxTarget{}, false, nil
	}
	label := strings.TrimSuffix(hostOnly, suffix)
	if label == "" || label == "api" || strings.Contains(label, ".") {
		return sandboxTarget{}, false, nil
	}
	portStr, sandboxID, hasDash := strings.Cut(label, "-")
	if !hasDash || portStr == "" || sandboxID == "" {
		return sandboxTarget{}, true, fmt.Errorf("invalid sandbox host %q", host)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return sandboxTarget{}, true, fmt.Errorf("invalid sandbox port %q", portStr)
	}
	return sandboxTarget{sandboxID: sandboxID, port: port}, true, nil
}

func isEnvdInitPath(reqPath string) bool {
	cleaned := path.Clean("/" + reqPath)
	return cleaned == "/init" || strings.HasPrefix(cleaned, "/init/")
}

func writeProxyJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Code: code, Message: msg})
}

func (h *Handler) handleSandboxProxy(w http.ResponseWriter, r *http.Request, target sandboxTarget) {
	if target.port == defaultEnvdPort && isEnvdInitPath(r.URL.Path) {
		writeProxyJSONError(w, http.StatusForbidden, "/init endpoint is restricted")
		return
	}

	sandbox, err := h.storeClient.GetSandboxBySessionID(r.Context(), target.sandboxID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeProxyJSONError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		klog.Errorf("E2B proxy store lookup failed for %s: %v", target.sandboxID, err)
		writeProxyJSONError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if sandbox.Status == "deleting" || !sandbox.ExpiresAt.After(time.Now()) {
		writeProxyJSONError(w, http.StatusNotFound, "sandbox expired")
		return
	}
	if sandbox.Status != sandboxStatusReady {
		writeProxyJSONError(w, http.StatusServiceUnavailable, "sandbox is not ready")
		return
	}
	if sandbox.PodIP == "" {
		writeProxyJSONError(w, http.StatusServiceUnavailable, "sandbox endpoint unavailable")
		return
	}
	// Only proxy sessions initialized as E2B sandboxes; never expose native AgentCube sessions.
	if sandbox.EnvdVersion == "" {
		writeProxyJSONError(w, http.StatusNotFound, "sandbox not found")
		return
	}

	if target.port == defaultCodeInterpreterPort && sandbox.EnvdAccessToken != "" {
		provided := r.Header.Get(headerEnvdAccessToken)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(sandbox.EnvdAccessToken)) != 1 {
			writeProxyJSONError(w, http.StatusUnauthorized, "invalid access token")
			return
		}
	}

	upstreamURL := &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(sandbox.PodIP, strconv.Itoa(target.port)),
	}
	proxy := &httputil.ReverseProxy{
		Transport:     h.proxyTransport,
		FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(upstreamURL)
			pr.Out.URL.Path = pr.In.URL.Path
			pr.Out.URL.RawPath = pr.In.URL.RawPath
			pr.Out.URL.RawQuery = pr.In.URL.RawQuery
			pr.SetXForwarded()
		},
		ErrorHandler: func(rw http.ResponseWriter, _ *http.Request, proxyErr error) {
			klog.Errorf("E2B proxy error for sandbox %s: %v", sandbox.SessionID, proxyErr)
			writeProxyJSONError(rw, http.StatusBadGateway, "sandbox unreachable")
		},
	}
	proxy.ServeHTTP(w, r)
}
