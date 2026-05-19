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
	"github.com/prometheus/client_golang/prometheus"
)

var (
	sandboxCreateDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "agentcube_sandbox_create_duration_seconds",
			Help:    "Duration of sandbox creation in seconds.",
			Buckets: []float64{1, 2, 5, 10, 20, 30, 45, 60, 90, 120, 150, 180},
		},
		[]string{"kind", "status"},
	)

	sandboxCreateTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentcube_sandbox_create_total",
			Help: "Total number of sandboxes created.",
		},
		[]string{"kind", "status"},
	)

	sandboxDeleteTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentcube_sandbox_delete_total",
			Help: "Total number of sandboxes deleted.",
		},
		[]string{"kind"},
	)

	sandboxRollbackTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "agentcube_sandbox_rollback_total",
			Help: "Total number of sandbox creation rollbacks.",
		},
	)

	gcCycleDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "agentcube_gc_cycle_duration_seconds",
			Help:    "Duration of a GC cycle in seconds.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30, 60},
		},
	)

	gcSandboxesReclaimedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "agentcube_gc_sandboxes_reclaimed_total",
			Help: "Total number of sandboxes reclaimed by the garbage collector.",
		},
		[]string{"reason"},
	)

	gcErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "agentcube_gc_errors_total",
			Help: "Total number of garbage collection errors.",
		},
	)
)

func init() {
	prometheus.MustRegister(sandboxCreateDuration)
	prometheus.MustRegister(sandboxCreateTotal)
	prometheus.MustRegister(sandboxDeleteTotal)
	prometheus.MustRegister(sandboxRollbackTotal)
	prometheus.MustRegister(gcCycleDuration)
	prometheus.MustRegister(gcSandboxesReclaimedTotal)
	prometheus.MustRegister(gcErrorsTotal)
}
