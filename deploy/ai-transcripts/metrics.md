PROMPT: ChatGPT

I have an existing Go API with main.go, handle.go, handle_test.go, and store.go. Currently, the /metrics endpoint only tracks basic task metrics such as task_api_tasks_total and task_api_tasks_done.

Objective:

Update the codebase to expose comprehensive Prometheus metrics for request volume, HTTP failure rates, request latency, with the ability to calculate P50, P95, and P99, and ongoing task state tracking.

Requirements:

- HTTP Metrics: Add middleware to automatically track API requests, including request count, HTTP status codes, HTTP methods, and latency. Do not track the /metrics request itself.

- Latency Histograms: Track request duration using a Prometheus Histogram with sensible bucket ranges so Prometheus can calculate P50, P95, and P99.

- Clean Labels: Use clean, consistent route, method, and status code labels. Avoid putting dynamic task IDs or query strings into the metrics.

- Task State: Update the existing task metrics so they accurately show total, pending, and completed tasks based on the current application state.

- Unit Tests: Please update handle_test.go to test that the new metrics are working correctly, including requests, status codes, HTTP methods, routes, excluding /metrics, task counts, and request latency. The tests should be reliable and not depend on waiting or having a separate Prometheus server running.

- Main.go: Please also update main.go wherever necessary so the new metrics work for all API requests. Keep the existing API and handlers working as they do now.

- Dependencies: If the Prometheus client dependency is missing, include the commands needed to add it and tidy the Go modules.

Please provide the complete, ready-to-test code for:
1. main.go
2. handle.go
3. handle_test.go

Also briefly explain what was changed and how I can run the tests.

---


Yes. Given your existing Go API structure, I would make the Prometheus instrumentation **middleware-driven** and keep the `/metrics` handler responsible only for exposing the metrics.

The important design is:

```text
HTTP request
   │
   ▼
instrumentHandler
   │
   ├── method
   ├── normalized route
   ├── status code
   └── duration
   │
   ▼
Prometheus CounterVec + HistogramVec
   │
   ▼
GET /metrics
   │
   ▼
Prometheus scrape
```

For task state, the gauges are updated from `store.Stats()` when `/metrics` is requested, so they represent the **current state**, rather than counting task events.


Absolutely. Using that prompt, here is the complete implementation.

### 1. Dependency

First make sure the Prometheus client is installed:

```bash
go get github.com/prometheus/client_golang
go mod tidy
```

---

## `main.go`

The important change here is that the metrics middleware is applied to the **entire API**, so you don't have to add metrics logic to every individual handler.

```go
package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	port := getEnv("PORT", "8080")
	store := NewMemoryStore()

	mux := http.NewServeMux()

	// Health check.
	mux.HandleFunc("GET /healthz", HealthHandler)

	// Prometheus metrics endpoint.
	mux.HandleFunc("GET /metrics", MetricsHandler(store))

	// Task API.
	mux.HandleFunc("GET /tasks", ListTasksHandler(store))
	mux.HandleFunc("POST /tasks", CreateTaskHandler(store))
	mux.HandleFunc("GET /tasks/{id}", GetTaskHandler(store))
	mux.HandleFunc("PUT /tasks/{id}", UpdateTaskHandler(store))
	mux.HandleFunc("DELETE /tasks/{id}", DeleteTaskHandler(store))

	// Apply metrics to all requests handled by the API.
	// The middleware itself excludes /metrics from being recorded.
	handler := instrumentHandler(mux.ServeHTTP)

	addr := ":" + port
	log.Printf("task-api starting on %s", addr)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
```

---

# 2. `handle.go`

```go
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
```

---

# 3. `handle_test.go`

This version avoids `time.Sleep()` and correctly checks histogram data using `metricsRegistry.Gather()`.

```go
package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

func resetMetrics() {
	httpRequestsTotal.Reset()
	httpRequestDuration.Reset()

	taskTotal.Set(0)
	taskDone.Set(0)
	taskPending.Set(0)
}

func executeRequest(
	method string,
	routePattern string,
	target string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)

	// In the real application ServeMux sets Pattern.
	// The test sets it manually because we are testing the middleware directly.
	req.Pattern = routePattern

	recorder := httptest.NewRecorder()

	instrumentHandler(handler)(recorder, req)

	return recorder
}

func getMetricFamily(name string) *prometheus.MetricFamily {
	families, err := metricsRegistry.Gather()
	if err != nil {
		return nil
	}

	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}

	return nil
}

// -----------------------------------------------------------------------------
// HTTP request metrics
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsRequest(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /tasks",
		"/tasks",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks",
			"200",
		),
	)

	if value != 1 {
		t.Fatalf("expected request count 1, got %v", value)
	}
}

func TestInstrumentHandlerRecordsErrorStatus(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /tasks",
		"/tasks",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks",
			"404",
		),
	)

	if value != 1 {
		t.Fatalf("expected 404 count 1, got %v", value)
	}
}

func TestInstrumentHandlerRecordsHTTPMethod(t *testing.T) {
	resetMetrics()

	executeRequest(
		"POST",
		"POST /tasks",
		"/tasks",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusCreated)
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"POST",
			"/tasks",
			"201",
		),
	)

	if value != 1 {
		t.Fatalf("expected POST count 1, got %v", value)
	}
}

func TestInstrumentHandlerUsesNormalizedRoute(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /tasks/{id}",
		"/tasks/123",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/{id}",
			"200",
		),
	)

	if value != 1 {
		t.Fatalf("expected normalized route count 1, got %v", value)
	}

	// Make sure the dynamic ID did not become a label.
	dynamicValue := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/123",
			"200",
		),
	)

	if dynamicValue != 0 {
		t.Fatalf("dynamic task ID should not be used as a route label")
	}
}

func TestInstrumentHandlerIgnoresQueryString(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /tasks",
		"/tasks?status=done&page=1",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks",
			"200",
		),
	)

	if value != 1 {
		t.Fatalf("expected route /tasks count 1, got %v", value)
	}
}

func TestInstrumentHandlerDoesNotRecordMetricsRoute(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /metrics",
		"/metrics",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/metrics",
			"200",
		),
	)

	if value != 0 {
		t.Fatalf("/metrics should not be recorded, got %v", value)
	}
}

func TestInstrumentHandlerRecordsImplicitOK(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /healthz",
		"/healthz",
		func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("ok"))
		},
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/healthz",
			"200",
		),
	)

	if value != 1 {
		t.Fatalf("expected implicit 200 count 1, got %v", value)
	}
}

// -----------------------------------------------------------------------------
// Latency histogram
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsLatency(t *testing.T) {
	resetMetrics()

	executeRequest(
		"GET",
		"GET /tasks",
		"/tasks",
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	)

	family := getMetricFamily("task_api_http_request_duration_seconds")

	if family == nil {
		t.Fatal("latency histogram was not found")
	}

	if len(family.GetMetric()) != 1 {
		t.Fatalf(
			"expected 1 histogram metric, got %d",
			len(family.GetMetric()),
		)
	}

	histogram := family.GetMetric()[0].GetHistogram()

	if histogram.GetSampleCount() != 1 {
		t.Fatalf(
			"expected histogram sample count 1, got %d",
			histogram.GetSampleCount(),
		)
	}

	if histogram.GetSampleSum() <= 0 {
		t.Fatalf(
			"expected histogram sample sum > 0, got %v",
			histogram.GetSampleSum(),
		)
}

func TestLatencyHistogramUsesExpectedBuckets(t *testing.T) {
	resetMetrics()

	// Use a fixed duration rather than time.Sleep() so this test
	// remains deterministic.
	recordHTTPMetrics(
		"GET",
		"/tasks",
		"200",
		20*time.Millisecond,
	)

	family := getMetricFamily("task_api_http_request_duration_seconds")

	if family == nil {
		t.Fatal("latency histogram was not found")
	}

	histogram := family.GetMetric()[0].GetHistogram()

	if histogram.GetSampleCount() != 1 {
		t.Fatalf(
			"expected sample count 1, got %d",
			histogram.GetSampleCount(),
		)
	}

	if histogram.GetSampleSum() != 0.020 {
		t.Fatalf(
			"expected sample sum 0.020, got %v",
			histogram.GetSampleSum(),
		)
	}

	// 20ms should be included in the 25ms bucket.
	found25msBucket := false

	for _, bucket := range histogram.GetBucket() {
		if bucket.GetUpperBound() == 0.025 {
			found25msBucket = true

			if bucket.GetCumulativeCount() != 1 {
				t.Fatalf(
					"expected 25ms bucket count 1, got %d",
					bucket.GetCumulativeCount(),
				)
			}
		}
	}

	if !found25msBucket {
		t.Fatal("expected 25ms histogram bucket")
	}
}

// -----------------------------------------------------------------------------
// Status codes
// -----------------------------------------------------------------------------

func TestHTTPStatusCodesAreSeparated(t *testing.T) {
	resetMetrics()

	statusCodes := []int{
		http.StatusOK,
		http.StatusCreated,
		http.StatusNoContent,
		http.StatusBadRequest,
		http.StatusNotFound,
		http.StatusInternalServerError,
	}

	for _, statusCode := range statusCodes {
		status := statusCode

		executeRequest(
			"GET",
			"GET /tasks",
			"/tasks",
			func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			},
		)
	}

	for _, statusCode := range statusCodes {
		value := testutil.ToFloat64(
			httpRequestsTotal.WithLabelValues(
				"GET",
				"/tasks",
				strconv.Itoa(statusCode),
			),
		)

		if value != 1 {
			t.Fatalf(
				"expected status %d count 1, got %v",
				statusCode,
				value,
			)
		}
	}
}

// -----------------------------------------------------------------------------
// Metrics endpoint
// -----------------------------------------------------------------------------

func TestMetricsEndpointExposesPrometheusMetrics(t *testing.T) {
	resetMetrics()

	store := NewMemoryStore()
	store.Create("task one")

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(
		"GET",
		"/metrics",
		nil,
	)

	MetricsHandler(store)(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected status 200, got %d",
			recorder.Code,
		)
	}

	body := recorder.Body.String()

	expectedMetrics := []string{
		"task_api_http_requests_total",
		"task_api_http_request_duration_seconds",
		"task_api_tasks_total",
		"task_api_tasks_done",
		"task_api_tasks_pending",
	}

	for _, metric := range expectedMetrics {
		if !strings.Contains(body, metric) {
			t.Fatalf(
				"expected metrics output to contain %q",
				metric,
			)
		}
	}
}

// -----------------------------------------------------------------------------
// Task state gauges
// -----------------------------------------------------------------------------

func TestTaskStateGauges(t *testing.T) {
	resetMetrics()

	store := NewMemoryStore()

	store.Create("pending task")
	store.Create("completed task")

	done := true

	_, ok := store.Update(
		2,
		nil,
		&done,
	)

	if !ok {
		t.Fatal("expected task update to succeed")
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(
		"GET",
		"/metrics",
		nil,
	)

	MetricsHandler(store)(recorder, req)

	total := testutil.ToFloat64(taskTotal)
	completed := testutil.ToFloat64(taskDone)
	pending := testutil.ToFloat64(taskPending)

	if total != 2 {
		t.Fatalf("expected total tasks 2, got %v", total)
	}

	if completed != 1 {
		t.Fatalf("expected completed tasks 1, got %v", completed)
	}

	if pending != 1 {
		t.Fatalf("expected pending tasks 1, got %v", pending)
	}
}

func TestTaskStateGaugesUpdateAfterDelete(t *testing.T) {
	resetMetrics()

	store := NewMemoryStore()

	store.Create("task one")
	store.Create("task two")

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(
		"GET",
		"/metrics",
		nil,
	)

	MetricsHandler(store)(recorder, req)

	if value := testutil.ToFloat64(taskTotal); value != 2 {
		t.Fatalf("expected total 2, got %v", value)
	}

	store.Delete(1)

	recorder = httptest.NewRecorder()

	MetricsHandler(store)(recorder, req)

	if value := testutil.ToFloat64(taskTotal); value != 1 {
		t.Fatalf("expected total 1 after delete, got %v", value)
	}

	if value := testutil.ToFloat64(taskPending); value != 1 {
		t.Fatalf("expected pending 1 after delete, got %v", value)
	}
}
```

### One small correction

The test code above uses `prometheus.MetricFamily`, so `handle_test.go` also needs the Prometheus package import. Use this import section:

```go
import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)
```

### Run everything

```bash
go get github.com/prometheus/client_golang
go mod tidy
go test ./...
```

### What you now get

Your `/metrics` endpoint will expose:

```text
task_api_http_requests_total
task_api_http_request_duration_seconds
task_api_tasks_total
task_api_tasks_done
task_api_tasks_pending
```

For example, P95 can be calculated in Prometheus with:

```promql
histogram_quantile(
  0.95,
  sum by (le, route, method) (
    rate(task_api_http_request_duration_seconds_bucket[5m])
  )
)
```

And the HTTP failure rate can be calculated with:

```promql
sum(rate(task_api_http_requests_total{status_code=~"4..|5.."}[5m]))
/
sum(rate(task_api_http_requests_total[5m]))
```

The important architectural change is that **`main.go` now puts the middleware around the whole mux**, while `handle.go` owns the metrics definitions and instrumentation. The tests can inspect the dedicated registry directly, so there is no external Prometheus server involved.
