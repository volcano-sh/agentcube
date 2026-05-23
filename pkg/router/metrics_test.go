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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestRouterMetricsRegistrationAndCollection(t *testing.T) {
	// Reset/initialize counters to know starting points (metrics are global)
	initialSessionCreates := testutil.ToFloat64(routerSessionCreateTotal)
	routerSessionCreateTotal.Inc()
	assert.Equal(t, initialSessionCreates+1, testutil.ToFloat64(routerSessionCreateTotal))

	initialProxyErrors := testutil.ToFloat64(routerProxyErrorsTotal.WithLabelValues("connection_refused"))
	routerProxyErrorsTotal.WithLabelValues("connection_refused").Inc()
	assert.Equal(t, initialProxyErrors+1, testutil.ToFloat64(routerProxyErrorsTotal.WithLabelValues("connection_refused")))

	// Gauge test
	routerConcurrentRequests.Set(5.0)
	assert.Equal(t, float64(5), testutil.ToFloat64(routerConcurrentRequests))
	routerConcurrentRequests.Set(0.0)

	// Histogram test
	routerRequestDuration.WithLabelValues("AgentRuntime").Observe(0.12)
}

func TestRouterMetricsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{config: &Config{MaxConcurrentRequests: 10}}
	server.setupRoutes()
	routerProxyErrorsTotal.WithLabelValues("other").Add(0)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	server.engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "agentcube_router_session_create_total"))
	assert.True(t, strings.Contains(body, "agentcube_router_proxy_errors_total"))
	assert.True(t, strings.Contains(body, "agentcube_router_concurrent_requests"))
}
