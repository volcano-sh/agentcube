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
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

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
			Buckets: prometheus.DefBuckets,
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
		m.HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		m.HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}
