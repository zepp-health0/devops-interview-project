package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// resetMetrics clears the in-memory Prometheus metrics between tests.
//
// Prometheus counters and histograms are cumulative, so without resetting
// them one test could affect another test.
func resetMetrics() {
	httpRequestsTotal.Reset()
	httpRequestDuration.Reset()

	taskTotal.Set(0)
	taskDone.Set(0)
	taskPending.Set(0)
}

// executeRequest runs a handler through the instrumentation middleware.
//
// routePattern simulates the route pattern that Go's ServeMux normally
// populates in r.Pattern.
func executeRequest(
	method string,
	routePattern string,
	target string,
	handler http.HandlerFunc,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		method,
		target,
		nil,
	)

	req.Pattern = routePattern

	recorder := httptest.NewRecorder()

	instrumentHandler(handler)(
		recorder,
		req,
	)

	return recorder
}

// assertHistogramObservation verifies that a particular histogram label
// combination contains the expected number of observations.
func assertHistogramObservation(
	t *testing.T,
	method string,
	route string,
	statusCode string,
	expectedCount uint64,
) {
	t.Helper()

	metricFamilies, err := metricsRegistry.Gather()
	if err != nil {
		t.Fatalf(
			"failed to gather metrics: %v",
			err,
		)
	}

	for _, family := range metricFamilies {
		if family.GetName() != "task_api_http_request_duration_seconds" {
			continue
		}

		for _, metric := range family.GetMetric() {
			histogram := metric.GetHistogram()

			if histogram == nil {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"method",
				method,
			) {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"route",
				route,
			) {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"status_code",
				statusCode,
			) {
				continue
			}

			if histogram.GetSampleCount() != expectedCount {
				t.Fatalf(
					"expected histogram sample count %d, got %d",
					expectedCount,
					histogram.GetSampleCount(),
				)
			}

			return
		}
	}

	t.Fatalf(
		"histogram metric not found for method=%q route=%q status_code=%q",
		method,
		route,
		statusCode,
	)
}

// hasLabelValue checks whether a Prometheus label has a specific value.
func hasLabelValue(
	labels []*dto.LabelPair,
	name string,
	value string,
) bool {
	for _, label := range labels {
		if label.Name == nil || label.Value == nil {
			continue
		}

		if *label.Name == name && *label.Value == value {
			return true
		}
	}

	return false
}

// -----------------------------------------------------------------------------
// HTTP request counter tests
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsRequest(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
	}

	recorder := executeRequest(
		http.MethodGet,
		"GET /tasks",
		"/tasks",
		handler,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected status 200, got %d",
			recorder.Code,
		)
	}

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks",
			"200",
		),
	)

	if value != 1 {
		t.Fatalf(
			"expected request count 1, got %v",
			value,
		)
	}
}

// -----------------------------------------------------------------------------
// HTTP status code tests
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsErrorStatus(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusNotFound)
	}

	executeRequest(
		http.MethodGet,
		"GET /tasks/{id}",
		"/tasks/123",
		handler,
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/{id}",
			"404",
		),
	)

	if value != 1 {
		t.Fatalf(
			"expected 404 request count 1, got %v",
			value,
		)
	}

	// Make sure the request wasn't incorrectly recorded as 200.
	successValue := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/{id}",
			"200",
		),
	)

	if successValue != 0 {
		t.Fatalf(
			"expected 200 request count 0, got %v",
			successValue,
		)
	}
}

// -----------------------------------------------------------------------------
// HTTP method tests
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsHTTPMethod(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusCreated)
	}

	executeRequest(
		http.MethodPost,
		"POST /tasks",
		"/tasks",
		handler,
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"POST",
			"/tasks",
			"201",
		),
	)

	if value != 1 {
		t.Fatalf(
			"expected POST request count 1, got %v",
			value,
		)
	}
}

// -----------------------------------------------------------------------------
// Route normalization tests
// -----------------------------------------------------------------------------

func TestInstrumentHandlerUsesNormalizedRoute(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
	}

	// The actual URL contains a dynamic task ID.
	//
	// We expect the metric label to contain:
	//
	//   /tasks/{id}
	//
	// rather than:
	//
	//   /tasks/12345
	executeRequest(
		http.MethodGet,
		"GET /tasks/{id}",
		"/tasks/12345",
		handler,
	)

	normalized := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/{id}",
			"200",
		),
	)

	if normalized != 1 {
		t.Fatalf(
			"expected normalized route count 1, got %v",
			normalized,
		)
	}

	// Make sure the dynamic URL wasn't used as a metric label.
	dynamic := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/12345",
			"200",
		),
	)

	if dynamic != 0 {
		t.Fatalf(
			"expected dynamic route count 0, got %v",
			dynamic,
		)
	}
}

// -----------------------------------------------------------------------------
// Unknown route cardinality test
// -----------------------------------------------------------------------------

func TestInstrumentHandlerDoesNotUseRawPathWhenRouteIsUnknown(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusNotFound)
	}

	executeRequest(
		http.MethodGet,
		"",
		"/some/random/path/12345",
		handler,
	)

	unknown := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"unknown",
			"404",
		),
	)

	if unknown != 1 {
		t.Fatalf(
			"expected unknown route count 1, got %v",
			unknown,
		)
	}

	rawPath := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/some/random/path/12345",
			"404",
		),
	)

	if rawPath != 0 {
		t.Fatalf(
			"expected raw path count 0, got %v",
			rawPath,
		)
	}
}

// -----------------------------------------------------------------------------
// /metrics exclusion test
// -----------------------------------------------------------------------------

func TestInstrumentHandlerDoesNotRecordMetricsRoute(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
	}

	executeRequest(
		http.MethodGet,
		"GET /metrics",
		"/metrics",
		handler,
	)

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/metrics",
			"200",
		),
	)

	if value != 0 {
		t.Fatalf(
			"expected /metrics request not to be recorded, got %v",
			value,
		)
	}
}

// -----------------------------------------------------------------------------
// Implicit 200 test
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsImplicitOK(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		// No explicit WriteHeader(200).
		//
		// net/http automatically treats Write() as HTTP 200.
		_, _ = w.Write([]byte("hello"))
	}

	recorder := executeRequest(
		http.MethodGet,
		"GET /healthz",
		"/healthz",
		handler,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected response status 200, got %d",
			recorder.Code,
		)
	}

	value := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/healthz",
			"200",
		),
	)

	if value != 1 {
		t.Fatalf(
			"expected implicit 200 request count 1, got %v",
			value,
		)
	}
}

// -----------------------------------------------------------------------------
// Response status recorder tests
// -----------------------------------------------------------------------------

func TestStatusRecorderKeepsFirstStatusCode(t *testing.T) {
	recorder := httptest.NewRecorder()

	status := &statusRecorder{
		ResponseWriter: recorder,
		statusCode:     http.StatusOK,
	}

	status.WriteHeader(http.StatusNotFound)
	status.WriteHeader(http.StatusInternalServerError)

	if status.statusCode != http.StatusNotFound {
		t.Fatalf(
			"expected first status 404, got %d",
			status.statusCode,
		)
	}

	if recorder.Code != http.StatusNotFound {
		t.Fatalf(
			"expected response status 404, got %d",
			recorder.Code,
		)
	}
}

// -----------------------------------------------------------------------------
// Latency histogram tests
// -----------------------------------------------------------------------------

func TestInstrumentHandlerRecordsLatency(t *testing.T) {
	resetMetrics()

	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
	}

	executeRequest(
		http.MethodGet,
		"GET /tasks",
		"/tasks",
		handler,
	)

	// A Histogram cannot be tested with testutil.ToFloat64().
	//
	// Instead, inspect the histogram through Gather().
	assertHistogramObservation(
		t,
		"GET",
		"/tasks",
		"200",
		1,
	)
}

// TestRecordHTTPMetricsDeterministic verifies a known duration.
//
// We deliberately do not use time.Sleep() here because sleeping for
// 20ms is not deterministic. The operating system could schedule the
// test for 21ms, 30ms, etc.
//
// Instead, we directly provide a fixed duration to recordHTTPMetrics().
func TestRecordHTTPMetricsDeterministic(t *testing.T) {
	resetMetrics()

	recordHTTPMetrics(
		"GET",
		"/tasks",
		"200",
		50*time.Millisecond,
	)

	metricFamilies, err := metricsRegistry.Gather()
	if err != nil {
		t.Fatalf(
			"failed to gather metrics: %v",
			err,
		)
	}

	for _, family := range metricFamilies {
		if family.GetName() != "task_api_http_request_duration_seconds" {
			continue
		}

		for _, metric := range family.GetMetric() {
			histogram := metric.GetHistogram()

			if histogram == nil {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"method",
				"GET",
			) {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"route",
				"/tasks",
			) {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"status_code",
				"200",
			) {
				continue
			}

			if histogram.GetSampleCount() != 1 {
				t.Fatalf(
					"expected sample count 1, got %d",
					histogram.GetSampleCount(),
				)
			}

			if histogram.GetSampleSum() != 0.05 {
				t.Fatalf(
					"expected histogram sum 0.05, got %v",
					histogram.GetSampleSum(),
				)
			}

			return
		}
	}

	t.Fatal(
		"expected task_api_http_request_duration_seconds histogram",
	)
}

// -----------------------------------------------------------------------------
// Histogram bucket test
// -----------------------------------------------------------------------------

func TestLatencyHistogramUsesExpectedBuckets(t *testing.T) {
	resetMetrics()

	// Directly record exactly 20ms.
	//
	// 20ms should be:
	//
	//   > 10ms
	//   <= 25ms
	//
	// Therefore it should be included in the 25ms cumulative bucket.
	recordHTTPMetrics(
		"GET",
		"/tasks",
		"200",
		20*time.Millisecond,
	)

	metricFamilies, err := metricsRegistry.Gather()
	if err != nil {
		t.Fatalf(
			"failed to gather metrics: %v",
			err,
		)
	}

	for _, family := range metricFamilies {
		if family.GetName() != "task_api_http_request_duration_seconds" {
			continue
		}

		for _, metric := range family.GetMetric() {
			if !hasLabelValue(
				metric.GetLabel(),
				"method",
				"GET",
			) {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"route",
				"/tasks",
			) {
				continue
			}

			if !hasLabelValue(
				metric.GetLabel(),
				"status_code",
				"200",
			) {
				continue
			}

			histogram := metric.GetHistogram()

			if histogram == nil {
				continue
			}

			if histogram.GetSampleCount() != 1 {
				t.Fatalf(
					"expected sample count 1, got %d",
					histogram.GetSampleCount(),
				)
			}

			found25ms := false

			for _, bucket := range histogram.GetBucket() {
				if bucket.GetUpperBound() == 0.025 {
					found25ms = true

					if bucket.GetCumulativeCount() != 1 {
						t.Fatalf(
							"expected 25ms bucket count 1, got %d",
							bucket.GetCumulativeCount(),
						)
					}
				}
			}

			if !found25ms {
				t.Fatal(
					"expected 25ms histogram bucket was not found",
				)
			}

			return
		}
	}

	t.Fatal(
		"expected task_api_http_request_duration_seconds histogram",
	)
}

// -----------------------------------------------------------------------------
// Prometheus exposition format test
// -----------------------------------------------------------------------------

func TestMetricsEndpointExposesPrometheusMetrics(t *testing.T) {
	resetMetrics()

	// Generate one HTTP request so the request metrics have data.
	handler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusOK)
	}

	executeRequest(
		http.MethodGet,
		"GET /tasks",
		"/tasks",
		handler,
	)

	store := NewMemoryStore()

	req := httptest.NewRequest(
		http.MethodGet,
		"/metrics",
		nil,
	)

	req.Pattern = "GET /metrics"

	recorder := httptest.NewRecorder()

	MetricsHandler(store)(
		recorder,
		req,
	)

	if recorder.Code != http.StatusOK {
		t.Fatalf(
			"expected metrics endpoint status 200, got %d",
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

	for _, metricName := range expectedMetrics {
		if !strings.Contains(body, metricName) {
			t.Fatalf(
				"expected /metrics output to contain %q",
				metricName,
			)
		}
	}
}

// -----------------------------------------------------------------------------
// Task gauge tests
// -----------------------------------------------------------------------------

func TestTaskStateGauges(t *testing.T) {
	resetMetrics()

	store := NewMemoryStore()

	// Create three tasks.
	store.Create("task one")
	store.Create("task two")
	store.Create("task three")

	// Mark task 1 as completed.
	done := true

	_, ok := store.Update(
		1,
		nil,
		&done,
	)

	if !ok {
		t.Fatal("expected task 1 to exist")
	}

	req := httptest.NewRequest(
		http.MethodGet,
		"/metrics",
		nil,
	)

	req.Pattern = "GET /metrics"

	recorder := httptest.NewRecorder()

	MetricsHandler(store)(
		recorder,
		req,
	)

	total := testutil.ToFloat64(taskTotal)
	completed := testutil.ToFloat64(taskDone)
	pending := testutil.ToFloat64(taskPending)

	if total != 3 {
		t.Fatalf(
			"expected total tasks 3, got %v",
			total,
		)
	}

	if completed != 1 {
		t.Fatalf(
			"expected completed tasks 1, got %v",
			completed,
		)
	}

	if pending != 2 {
		t.Fatalf(
			"expected pending tasks 2, got %v",
			pending,
		)
	}
}

// -----------------------------------------------------------------------------
// Live task gauge test
// -----------------------------------------------------------------------------

func TestTaskStateGaugesReflectLiveState(t *testing.T) {
	resetMetrics()

	store := NewMemoryStore()

	// Initially there is one pending task.
	store.Create("task one")

	req := httptest.NewRequest(
		http.MethodGet,
		"/metrics",
		nil,
	)

	req.Pattern = "GET /metrics"

	recorder := httptest.NewRecorder()

	MetricsHandler(store)(
		recorder,
		req,
	)

	if total := testutil.ToFloat64(taskTotal); total != 1 {
		t.Fatalf(
			"expected total 1, got %v",
			total,
		)
	}

	if done := testutil.ToFloat64(taskDone); done != 0 {
		t.Fatalf(
			"expected done 0, got %v",
			done,
		)
	}

	if pending := testutil.ToFloat64(taskPending); pending != 1 {
		t.Fatalf(
			"expected pending 1, got %v",
			pending,
		)
	}

	// Add another task and mark it completed.
	task2 := store.Create("task two")

	done := true

	_, ok := store.Update(
		task2.ID,
		nil,
		&done,
	)

	if !ok {
		t.Fatal("expected task two to be updated")
	}

	// Scrape again.
	recorder = httptest.NewRecorder()

	MetricsHandler(store)(
		recorder,
		req,
	)

	total := testutil.ToFloat64(taskTotal)
	completed := testutil.ToFloat64(taskDone)
	pending := testutil.ToFloat64(taskPending)

	if total != 2 {
		t.Fatalf(
			"expected total 2, got %v",
			total,
		)
	}

	if completed != 1 {
		t.Fatalf(
			"expected completed 1, got %v",
			completed,
		)
	}

	if pending != 1 {
		t.Fatalf(
			"expected pending 1, got %v",
			pending,
		)
	}
}

// -----------------------------------------------------------------------------
// HTTP failure-rate metric test
// -----------------------------------------------------------------------------

func TestHTTPFailureStatusCodesAreSeparated(t *testing.T) {
	resetMetrics()

	notFoundHandler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusNotFound)
	}

	badRequestHandler := func(
		w http.ResponseWriter,
		r *http.Request,
	) {
		w.WriteHeader(http.StatusBadRequest)
	}

	executeRequest(
		http.MethodGet,
		"GET /tasks/{id}",
		"/tasks/999",
		notFoundHandler,
	)

	executeRequest(
		http.MethodPost,
		"POST /tasks",
		"/tasks",
		badRequestHandler,
	)

	notFound := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"GET",
			"/tasks/{id}",
			"404",
		),
	)

	badRequest := testutil.ToFloat64(
		httpRequestsTotal.WithLabelValues(
			"POST",
			"/tasks",
			"400",
		),
	)

	if notFound != 1 {
		t.Fatalf(
			"expected one 404 request, got %v",
			notFound,
		)
	}

	if badRequest != 1 {
		t.Fatalf(
			"expected one 400 request, got %v",
			badRequest,
		)
	}
}
