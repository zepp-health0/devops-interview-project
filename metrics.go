package main

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Build metadata, injected at link time with -ldflags "-X main.version=... -X main.revision=...".
// revision is the git SHA the image was built from, which is what makes a running container
// traceable back to a commit.
var (
	version  = "dev"
	revision = "unknown"
)

// latencyBuckets defines the histogram boundaries for request duration.
//
// These are NOT prometheus.DefBuckets. The defaults start at 5ms, and this service answers from
// an in-memory map: a measured run put all 55 observations of GET /tasks/{id} into that single
// first bucket, so histogram_quantile() could only interpolate inside [0, 0.005] and reported
// p99 = 4.95ms against a true mean of 0.176ms -- roughly 28x too high, and an artefact of the
// bucket layout rather than a property of the service.
//
// The boundaries below were picked from that measurement: business routes mean 0.026ms to
// 0.21ms, so the resolution starts at 25us. The range still extends to 1s so that if the store
// is ever replaced by a network-backed one, the histogram loses resolution gracefully instead of
// saturating into +Inf. See deploy/NOTES.md section 3.
var latencyBuckets = []float64{
	0.000025, // 25µs
	0.00005,
	0.0001,
	0.00025,
	0.0005,
	0.001, // 1ms
	0.0025,
	0.005,
	0.01,
	0.025,
	0.05,
	0.1,
	0.25,
	0.5,
	1,
}

// Metrics owns the Prometheus registry and every collector this service exposes.
//
// The registry is deliberately per-instance rather than prometheus.DefaultRegisterer: the tests
// construct metrics repeatedly, and a package-global registry would panic with duplicate
// registration on the second construction.
type Metrics struct {
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	inFlight prometheus.Gauge
}

// NewMetrics builds a registry describing the given store.
func NewMetrics(store Store) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),

		requests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "http_requests_total",
				Help: "Total HTTP requests, by method, matched route template and status code.",
			},
			// route is the ServeMux pattern ("GET /tasks/{id}"), never the raw path, so task IDs
			// cannot turn into unbounded label cardinality.
			[]string{"method", "route", "code"},
		),

		duration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "http_request_duration_seconds",
				Help:    "HTTP request latency in seconds, by method and matched route template.",
				Buckets: latencyBuckets,
			},
			[]string{"method", "route"},
		),

		inFlight: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "http_requests_in_flight",
				Help: "Number of HTTP requests currently being served.",
			},
		),
	}

	buildInfo := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name:        "task_api_build_info",
			Help:        "Build metadata of the running binary. Always 1; read the labels.",
			ConstLabels: prometheus.Labels{"version": version, "revision": revision},
		},
		func() float64 { return 1 },
	)

	// The two legacy gauges below keep the exact names the hand-rolled /metrics handler used.
	// task_api_tasks_total is a gauge whose name ends in _total, which violates the Prometheus
	// naming convention (that suffix is reserved for counters) -- but handler_test.go asserts
	// these exact strings and any existing dashboard or alert would reference them, so the
	// convention is knowingly sacrificed for a stable contract. See deploy/NOTES.md section 4.
	tasksTotal := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "task_api_tasks_total",
			Help: "Total number of tasks.",
		},
		func() float64 {
			total, _ := store.Stats()
			return float64(total)
		},
	)

	tasksDone := prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "task_api_tasks_done",
			Help: "Number of completed tasks.",
		},
		func() float64 {
			_, done := store.Stats()
			return float64(done)
		},
	)

	// Same information as the two gauges above, but as a labelled series so a dashboard can plot
	// done vs pending without a recording rule doing the subtraction.
	tasksByState := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "task_api_tasks_by_state",
			Help: "Current number of tasks by completion state.",
		},
		[]string{"state"},
	)

	m.registry.MustRegister(
		m.requests,
		m.duration,
		m.inFlight,
		buildInfo,
		tasksTotal,
		tasksDone,
		newStateCollector(store, tasksByState),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	return m
}

// stateCollector refreshes tasksByState from the store at scrape time.
//
// A plain gauge updated from the handlers would drift the moment any code path forgot to update
// it; reading the store on collect makes the metric structurally incapable of disagreeing with
// the data it describes.
type stateCollector struct {
	store Store
	gauge *prometheus.GaugeVec
}

func newStateCollector(store Store, gauge *prometheus.GaugeVec) *stateCollector {
	return &stateCollector{store: store, gauge: gauge}
}

func (c *stateCollector) Describe(ch chan<- *prometheus.Desc) {
	c.gauge.Describe(ch)
}

func (c *stateCollector) Collect(ch chan<- prometheus.Metric) {
	total, done := c.store.Stats()
	c.gauge.WithLabelValues("done").Set(float64(done))
	c.gauge.WithLabelValues("pending").Set(float64(total - done))
	c.gauge.Collect(ch)
}

// Handler serves the Prometheus exposition format for this registry.
func (m *Metrics) Handler() http.HandlerFunc {
	h := promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		ErrorHandling: promhttp.ContinueOnError,
	})
	return h.ServeHTTP
}

// Middleware wraps a ServeMux so every request is counted and timed.
//
// It takes the concrete *http.ServeMux rather than an http.Handler because the route label has to
// be the pattern the mux matched. ServeMux.ServeHTTP assigns Request.Pattern on the request it is
// given, so reading r.Pattern *after* the inner call yields the template ("GET /tasks/{id}")
// without matching the route a second time.
func (m *Metrics) Middleware(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		m.inFlight.Inc()
		mux.ServeHTTP(rec, r)
		m.inFlight.Dec()

		// Empty for a 404 or a 405, where no pattern matched. Bucketing those under a single
		// label keeps a scanner or a typo from creating one series per URL.
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}

		elapsed := time.Since(start).Seconds()
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(elapsed)
	})
}

// statusRecorder captures the response status code for the metrics labels.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader implicitly sends 200.
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer for flush/deadline support.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// MetricsHandler preserves the signature the original hand-rolled endpoint exposed, so existing
// callers and handler_test.go keep working. It builds a self-contained registry for the store.
//
// main() does NOT use this: it constructs one Metrics and shares it between the middleware and
// the endpoint, because HTTP counters have to live in the same registry that gets scraped.
func MetricsHandler(store Store) http.HandlerFunc {
	return NewMetrics(store).Handler()
}
