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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
)

func TestMetricsRegistrationAndCollection(t *testing.T) {
	// Reset/initialize counters to know starting points (metrics are global)
	initialRollbacks := testutil.ToFloat64(sandboxRollbackTotal)
	sandboxRollbackTotal.Inc()
	assert.Equal(t, initialRollbacks+1, testutil.ToFloat64(sandboxRollbackTotal))

	initialCreates := testutil.ToFloat64(sandboxCreateTotal.WithLabelValues("AgentRuntime", "success"))
	sandboxCreateTotal.WithLabelValues("AgentRuntime", "success").Inc()
	assert.Equal(t, initialCreates+1, testutil.ToFloat64(sandboxCreateTotal.WithLabelValues("AgentRuntime", "success")))

	initialDeletes := testutil.ToFloat64(sandboxDeleteTotal.WithLabelValues("CodeInterpreter"))
	sandboxDeleteTotal.WithLabelValues("CodeInterpreter").Inc()
	assert.Equal(t, initialDeletes+1, testutil.ToFloat64(sandboxDeleteTotal.WithLabelValues("CodeInterpreter")))

	// Histogram test
	sandboxCreateDuration.WithLabelValues("AgentRuntime", "success").Observe(1.5)

	// Garbage Collection metrics
	gcCycleDuration.Observe(0.5)

	initialReclaimed := testutil.ToFloat64(gcSandboxesReclaimedTotal.WithLabelValues("expired"))
	gcSandboxesReclaimedTotal.WithLabelValues("expired").Inc()
	assert.Equal(t, initialReclaimed+1, testutil.ToFloat64(gcSandboxesReclaimedTotal.WithLabelValues("expired")))

	initialGcErrors := testutil.ToFloat64(gcErrorsTotal)
	gcErrorsTotal.Inc()
	assert.Equal(t, initialGcErrors+1, testutil.ToFloat64(gcErrorsTotal))
}

func TestMetricsRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := &Server{}
	server.setupRoutes()
	sandboxCreateTotal.WithLabelValues("AgentRuntime", "success").Add(0)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/metrics", nil)
	server.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.True(t, strings.Contains(body, "agentcube_sandbox_rollback_total"))
	assert.True(t, strings.Contains(body, "agentcube_sandbox_create_total"))
	assert.True(t, strings.Contains(body, "agentcube_gc_errors_total"))
}
