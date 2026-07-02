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

package picod

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	metricHTTPRequestsTotal = "picod_http_requests_total"
	labelPath               = "path"
	labelMethod             = "method"
	executeAPIPath          = "/api/execute"
)

func TestMetrics_Exposition(t *testing.T) {
	routerPriv, routerPubStr := generateRSAKeys(t)
	server, ts, tmpDir := setupTestServer(t, routerPubStr)
	defer os.RemoveAll(tmpDir)
	defer ts.Close()
	defer os.Unsetenv(PublicKeyEnvVar)

	client := ts.Client()

	// 1. Check /metrics endpoint is reachable and returns HTTP 200
	resp, err := client.Get(ts.URL + "/metrics")
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 2. Perform an execution request
	execReq := ExecuteRequest{
		Command: []string{"echo", "test-metrics"},
	}
	bodyBytes, err := json.Marshal(execReq)
	require.NoError(t, err)

	claims := jwt.MapClaims{
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour * 6).Unix(),
	}
	token := createToken(t, routerPriv, claims)

	req, err := http.NewRequest(http.MethodPost, ts.URL+executeAPIPath, bytes.NewBuffer(bodyBytes))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// 3. Programmatically verify the metrics from the registry
	metricFamilies, err := server.metrics.Registry.Gather()
	require.NoError(t, err)

	var foundActiveExecutions, foundExecuteRequests, foundHTTPRequests, foundHTTPRequestDuration bool

	for _, mf := range metricFamilies {
		switch *mf.Name {
		case "picod_active_executions":
			foundActiveExecutions = true
			verifyActiveExecutions(t, mf)

		case "picod_execute_requests_total":
			foundExecuteRequests = true
			verifyExecuteRequests(t, mf)

		case metricHTTPRequestsTotal:
			foundHTTPRequests = true
			verifyHTTPRequests(t, mf)

		case "picod_http_request_duration_seconds":
			foundHTTPRequestDuration = true
			verifyHTTPRequestDuration(t, mf)
		}
	}

	assert.True(t, foundActiveExecutions, "picod_active_executions should be registered")
	assert.True(t, foundExecuteRequests, "picod_execute_requests_total should be registered")
	assert.True(t, foundHTTPRequests, "picod_http_requests_total should be registered")
	assert.True(t, foundHTTPRequestDuration, "picod_http_request_duration_seconds should be registered")
}

func verifyActiveExecutions(t *testing.T, mf *dto.MetricFamily) {
	require.Len(t, mf.Metric, 1)
	assert.Equal(t, 0.0, *mf.Metric[0].Gauge.Value)
}

func verifyExecuteRequests(t *testing.T, mf *dto.MetricFamily) {
	require.Len(t, mf.Metric, 1)
	assert.Equal(t, 1.0, *mf.Metric[0].Counter.Value)
	require.Len(t, mf.Metric[0].Label, 1)
	assert.Equal(t, "status", *mf.Metric[0].Label[0].Name)
	assert.Equal(t, "success", *mf.Metric[0].Label[0].Value)
}

func verifyHTTPRequests(t *testing.T, mf *dto.MetricFamily) {
	var foundExecute bool
	for _, m := range mf.Metric {
		var path, method, status string
		for _, label := range m.Label {
			switch *label.Name {
			case labelPath:
				path = *label.Value
			case labelMethod:
				method = *label.Value
			case "status_code":
				status = *label.Value
			}
		}
		if path == executeAPIPath && method == http.MethodPost && status == "200" {
			foundExecute = true
			assert.Equal(t, 1.0, *m.Counter.Value)
		}
	}
	assert.True(t, foundExecute, "should record POST /api/execute 200 metric")
}

func verifyHTTPRequestDuration(t *testing.T, mf *dto.MetricFamily) {
	var foundExecute bool
	for _, m := range mf.Metric {
		var path, method string
		for _, label := range m.Label {
			switch *label.Name {
			case labelPath:
				path = *label.Value
			case labelMethod:
				method = *label.Value
			}
		}
		if path == executeAPIPath && method == http.MethodPost {
			foundExecute = true
			assert.Greater(t, *m.Histogram.SampleCount, uint64(0))
			assert.Greater(t, *m.Histogram.SampleSum, 0.0)
		}
	}
	assert.True(t, foundExecute, "should record duration for POST /api/execute")
}

func TestMetrics_OversizedRequest(t *testing.T) {
	_, routerPubStr := generateRSAKeys(t)
	server, ts, tmpDir := setupTestServer(t, routerPubStr)
	defer os.RemoveAll(tmpDir)
	defer ts.Close()
	defer os.Unsetenv(PublicKeyEnvVar)

	// Use ServeHTTP directly to set ContentLength without HTTP client validation.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, executeAPIPath, nil)
	r.ContentLength = int64(MaxBodySize) + 1
	r.Header.Set("Content-Type", "application/json")
	server.engine.ServeHTTP(w, r)
	assert.Equal(t, http.StatusRequestEntityTooLarge, w.Code)

	metricFamilies, err := server.metrics.Registry.Gather()
	require.NoError(t, err)

	var found413 bool
	for _, mf := range metricFamilies {
		if *mf.Name != metricHTTPRequestsTotal {
			continue
		}
		for _, m := range mf.Metric {
			var path, method, status string
			for _, label := range m.Label {
				switch *label.Name {
				case labelPath:
					path = *label.Value
				case labelMethod:
					method = *label.Value
				case "status_code":
					status = *label.Value
				}
			}
			if path == executeAPIPath && method == http.MethodPost && status == "413" {
				found413 = true
				assert.Equal(t, 1.0, *m.Counter.Value)
			}
		}
	}
	assert.True(t, found413, "oversized request should be recorded with POST /api/execute status_code=413")
}

func TestMetrics_UnmatchedRoute(t *testing.T) {
	_, routerPubStr := generateRSAKeys(t)
	server, ts, tmpDir := setupTestServer(t, routerPubStr)
	defer os.RemoveAll(tmpDir)
	defer ts.Close()
	defer os.Unsetenv(PublicKeyEnvVar)

	client := ts.Client()

	resp, err := client.Get(ts.URL + "/does-not-exist")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()

	metricFamilies, err := server.metrics.Registry.Gather()
	require.NoError(t, err)

	var foundUnmatched bool
	for _, mf := range metricFamilies {
		if *mf.Name != metricHTTPRequestsTotal {
			continue
		}
		for _, m := range mf.Metric {
			for _, label := range m.Label {
				if *label.Name == labelPath && *label.Value == "unmatched" {
					foundUnmatched = true
				}
			}
		}
	}
	assert.True(t, foundUnmatched, "unmatched routes must use path label 'unmatched', not the raw URL path")
}
