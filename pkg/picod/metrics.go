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
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const methodUnknown = "unknown"

// requestDurationBuckets covers PicoD's full latency range: sub-millisecond
// for fast file and health-style paths through the 60-second default command
// timeout used by ExecuteHandler.
var requestDurationBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5,
	1, 2.5, 5, 10, 20, 30, 45, 60,
}

// normalizeMethod maps non-standard HTTP methods to "unknown" to prevent
// unbounded label cardinality in Prometheus time series.
func normalizeMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete,
		http.MethodPatch, http.MethodHead, http.MethodOptions,
		http.MethodConnect, http.MethodTrace:
		return method
	default:
		return methodUnknown
	}
}

// Metrics holds the Prometheus collectors for PicoD.
type Metrics struct {
	Registry             *prometheus.Registry
	ActiveExecutions     prometheus.Gauge
	ExecuteRequestsTotal *prometheus.CounterVec
	HTTPRequestsTotal    *prometheus.CounterVec
	HTTPRequestDuration  *prometheus.HistogramVec
}

// NewMetrics creates and registers metrics collectors with a private registry.
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	activeExecutions := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "picod_active_executions",
		Help: "Number of execute handler invocations currently in flight (including validation).",
	})

	executeRequestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "picod_execute_requests_total",
			Help: "Total number of execute requests, partitioned by status (success, error, timeout, invalid).",
		},
		[]string{"status"},
	)

	httpRequestsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "picod_http_requests_total",
			Help: "Total number of HTTP requests processed, partitioned by method, path, and status code.",
		},
		[]string{"method", "path", "status_code"},
	)

	httpRequestDuration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "picod_http_request_duration_seconds",
			Help:    "Latency of HTTP requests, partitioned by method and path.",
			Buckets: requestDurationBuckets,
		},
		[]string{"method", "path"},
	)

	reg.MustRegister(activeExecutions)
	reg.MustRegister(executeRequestsTotal)
	reg.MustRegister(httpRequestsTotal)
	reg.MustRegister(httpRequestDuration)

	return &Metrics{
		Registry:             reg,
		ActiveExecutions:     activeExecutions,
		ExecuteRequestsTotal: executeRequestsTotal,
		HTTPRequestsTotal:    httpRequestsTotal,
		HTTPRequestDuration:  httpRequestDuration,
	}
}

// Handler returns an HTTP handler for the metrics registry.
func (m *Metrics) Handler() gin.HandlerFunc {
	h := promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
	return func(c *gin.Context) {
		h.ServeHTTP(c.Writer, c.Request)
	}
}

// Middleware returns a Gin middleware that records HTTP request metrics.
func (m *Metrics) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.FullPath()
		if path == "" {
			path = "unmatched"
		}
		// Do not record requests to /metrics or /health to avoid noise
		if path == "/metrics" || path == "/health" {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		duration := time.Since(start).Seconds()

		status := strconv.Itoa(c.Writer.Status())
		method := normalizeMethod(c.Request.Method)
		m.HTTPRequestsTotal.WithLabelValues(method, path, status).Inc()
		m.HTTPRequestDuration.WithLabelValues(method, path).Observe(duration)
	}
}
