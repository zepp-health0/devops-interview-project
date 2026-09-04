package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Task struct {
	ID        int       `json:"id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

// -----------------------------------------------------------------------------
// Prometheus metrics
// -----------------------------------------------------------------------------

var (
	// Tracks the total number of HTTP requests.
	//
	// Labels:
	//   method      - HTTP method such as GET or POST
	//   route       - normalized API route
	//   status_code - HTTP response status
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "task_api_http_requests_total",
			Help: "Total number of HTTP requests received.",
		},
		[]string{"method", "route", "status_code"},
	)

	// Tracks HTTP request duration.
	//
	// Prometheus can use the histogram buckets to calculate
	// P50, P95, and P99 latency.
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name: "task_api_http_request_duration_seconds",
			Help: "HTTP request latency distribution in seconds.",
			Buckets: []float64{
				0.005, // 5ms
				0.010, // 10ms
				0.025, // 25ms
				0.050, // 50ms
				0.100, // 100ms
				0.250, // 250ms
				0.500, // 500ms
				1.0,   // 1s
				2.5,   // 2.5s
				5.0,   // 5s
				10.0,  // 10s
			},
		},
		[]string{"method", "route", "status_code"},
	)

	// Current total number of tasks.
	taskTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "task_api_tasks_total",
			Help: "Current total number of tasks.",
		},
	)

	// Current number of completed tasks.
	taskDone = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "task_api_tasks_done",
			Help: "Current number of completed tasks.",
		},
	)

	// Current number of pending tasks.
	taskPending = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "task_api_tasks_pending",
			Help: "Current number of pending tasks.",
		},
	)

	// Dedicated registry keeps application metrics isolated and
	// makes testing easier.
	metricsRegistry = prometheus.NewRegistry()
)

func init() {
	metricsRegistry.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		taskTotal,
		taskDone,
		taskPending,
	)
}

// -----------------------------------------------------------------------------
// Health
// -----------------------------------------------------------------------------

func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
	})
}

// -----------------------------------------------------------------------------
// Prometheus /metrics endpoint
// -----------------------------------------------------------------------------

func MetricsHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Read the current state from the store so the gauges always
		// represent the actual application state.
		total, done := store.Stats()
		pending := total - done

		taskTotal.Set(float64(total))
		taskDone.Set(float64(done))
		taskPending.Set(float64(pending))

		w.Header().Set(
			"Content-Type",
			"text/plain; version=0.0.4; charset=utf-8",
		)

		promhttp.HandlerFor(
			metricsRegistry,
			promhttp.HandlerOpts{},
		).ServeHTTP(w, r)
	}
}

// -----------------------------------------------------------------------------
// HTTP metrics
// -----------------------------------------------------------------------------

// recordHTTPMetrics records request count and request duration.
//
// Keeping this separate from the middleware also makes it possible
// to test histogram observations with a fixed duration.
func recordHTTPMetrics(
	method string,
	route string,
	status string,
	duration time.Duration,
) {
	httpRequestsTotal.WithLabelValues(
		method,
		route,
		status,
	).Inc()

	httpRequestDuration.WithLabelValues(
		method,
		route,
		status,
	).Observe(duration.Seconds())
}

// instrumentHandler records HTTP metrics for the API.
func instrumentHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		recorder := &statusRecorder{
			ResponseWriter: w,
		}

		next(recorder, r)

		status := recorder.statusCode

		// A handler that does not explicitly write a response is treated
		// as HTTP 200 by net/http.
		if status == 0 {
			status = http.StatusOK
		}

		route := normalizedRoute(r)

		// Do not record Prometheus scrape requests.
		if route == "/metrics" {
			return
		}

		recordHTTPMetrics(
			r.Method,
			route,
			strconv.Itoa(status),
			time.Since(start),
		)
	}
}

// normalizedRoute returns a safe route label.
//
// The registered ServeMux pattern is preferred over the raw URL so
// dynamic IDs do not create a new Prometheus time series for every task.
func normalizedRoute(r *http.Request) string {
	route := r.Pattern

	if route == "" {
		return "unknown"
	}

	// ServeMux patterns can contain the HTTP method, for example:
	//
	// GET /tasks/{id}
	//
	// Only keep:
	//
	// /tasks/{id}
	if index := strings.Index(route, " "); index >= 0 {
		route = route[index+1:]
	}

	if route == "" {
		return "unknown"
	}

	return route
}

// -----------------------------------------------------------------------------
// Response status recorder
// -----------------------------------------------------------------------------

type statusRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

// WriteHeader records the first status code written by the handler.
func (sr *statusRecorder) WriteHeader(statusCode int) {
	if sr.wroteHeader {
		return
	}

	sr.wroteHeader = true
	sr.statusCode = statusCode

	sr.ResponseWriter.WriteHeader(statusCode)
}

// Write handles implicit HTTP 200 responses.
func (sr *statusRecorder) Write(b []byte) (int, error) {
	if !sr.wroteHeader {
		sr.WriteHeader(http.StatusOK)
	}

	return sr.ResponseWriter.Write(b)
}

// -----------------------------------------------------------------------------
// Task handlers
// -----------------------------------------------------------------------------

func ListTasksHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tasks := store.List()
		writeJSON(w, http.StatusOK, tasks)
	}
}

func CreateTaskHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Title string `json:"title"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil ||
			input.Title == "" {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]string{
					"error": "title is required",
				},
			)
			return
		}

		task := store.Create(input.Title)

		log.Printf(
			"task created: id=%d title=%q",
			task.ID,
			task.Title,
		)

		writeJSON(w, http.StatusCreated, task)
	}
}

func GetTaskHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]string{
					"error": "invalid id",
				},
			)
			return
		}

		task, ok := store.Get(id)
		if !ok {
			writeJSON(
				w,
				http.StatusNotFound,
				map[string]string{
					"error": "task not found",
				},
			)
			return
		}

		writeJSON(w, http.StatusOK, task)
	}
}

func UpdateTaskHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]string{
					"error": "invalid id",
				},
			)
			return
		}

		var input struct {
			Title *string `json:"title"`
			Done  *bool   `json:"done"`
		}

		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]string{
					"error": "invalid body",
				},
			)
			return
		}

		task, ok := store.Update(id, input.Title, input.Done)
		if !ok {
			writeJSON(
				w,
				http.StatusNotFound,
				map[string]string{
					"error": "task not found",
				},
			)
			return
		}

		writeJSON(w, http.StatusOK, task)
	}
}

func DeleteTaskHandler(store Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			writeJSON(
				w,
				http.StatusBadRequest,
				map[string]string{
					"error": "invalid id",
				},
			)
			return
		}

		if !store.Delete(id) {
			writeJSON(
				w,
				http.StatusNotFound,
				map[string]string{
					"error": "task not found",
				},
			)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// -----------------------------------------------------------------------------
// JSON helper
// -----------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(v)
}
