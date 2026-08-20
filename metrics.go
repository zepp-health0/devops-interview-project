package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "task_api_http_requests_total",
			Help: "Total HTTP requests, labeled by method, route and status code.",
		},
		[]string{"method", "route", "status"},
	)

	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "task_api_http_request_duration_seconds",
			Help:    "HTTP request duration in seconds, labeled by method and route.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "route"},
	)

	taskStateDesc = prometheus.NewDesc(
		"task_api_tasks_state",
		"Current number of tasks by state (done, pending).",
		[]string{"state"}, nil,
	)
)

// storeCollector reports live task counts on every scrape instead of being
// updated from each handler, so it can never drift from the store.
type storeCollector struct {
	store Store
}

func (c *storeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- taskStateDesc
}

func (c *storeCollector) Collect(ch chan<- prometheus.Metric) {
	total, done := c.store.Stats()
	ch <- prometheus.MustNewConstMetric(taskStateDesc, prometheus.GaugeValue, float64(done), "done")
	ch <- prometheus.MustNewConstMetric(taskStateDesc, prometheus.GaugeValue, float64(total-done), "pending")
}

// NewMetricsRegistry builds a registry scoped to this app's metrics only,
// so /metrics doesn't also carry the client_golang default Go-runtime set.
func NewMetricsRegistry(store Store) *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(httpRequestsTotal, httpRequestDuration, &storeCollector{store: store})
	return reg
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// instrumentHTTP wraps every request with request-count and latency
// observations. It uses mux.Handler(r) to resolve the registered route
// pattern (e.g. "/tasks/{id}") rather than the raw path, so per-task IDs
// don't blow up label cardinality.
func instrumentHTTP(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, route := mux.Handler(r)
		// ServeMux patterns are registered as "METHOD /path"; drop the verb
		// since it's already captured by the method label.
		if _, path, ok := strings.Cut(route, " "); ok {
			route = path
		}
		if route == "" {
			route = "unmatched"
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		mux.ServeHTTP(rec, r)
		duration := time.Since(start).Seconds()

		httpRequestsTotal.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, route).Observe(duration)
	})
}
