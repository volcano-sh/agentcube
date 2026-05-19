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
	"github.com/prometheus/client_golang/prometheus"
)

var (
	routerRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentcube_router_request_duration_seconds",
			Help:    "Duration of proxy requests through the router in seconds.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
		},
		[]string{"kind"},
	)

	routerProxyErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentcube_router_proxy_errors_total",
			Help: "Total number of proxy errors encountered by the router.",
		},
		[]string{"error_category"},
	)

	routerConcurrentRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "agentcube_router_concurrent_requests",
			Help: "Current number of concurrent requests being processed by the router.",
		},
	)

	routerSessionCreateTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "agentcube_router_session_create_total",
			Help: "Total number of sandbox sessions implicitly created via the router.",
		},
	)
)

func init() {
	prometheus.MustRegister(routerRequestDuration)
	prometheus.MustRegister(routerProxyErrorsTotal)
	prometheus.MustRegister(routerConcurrentRequests)
	prometheus.MustRegister(routerSessionCreateTotal)
}
