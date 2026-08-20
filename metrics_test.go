package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestStoreCollectorReportsTaskState(t *testing.T) {
	store := setup()
	store.Create("a")
	b := store.Create("b")
	done := true
	store.Update(b.ID, nil, &done)

	reg := NewMetricsRegistry(store)
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()
	if !strings.Contains(body, `task_api_tasks_state{state="done"} 1`) {
		t.Fatalf("expected 1 done task in metrics, got:\n%s", body)
	}
	if !strings.Contains(body, `task_api_tasks_state{state="pending"} 1`) {
		t.Fatalf("expected 1 pending task in metrics, got:\n%s", body)
	}
}

func TestInstrumentHTTPRecordsRequestMetrics(t *testing.T) {
	store := setup()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler)
	mux.HandleFunc("GET /tasks/{id}", GetTaskHandler(store))

	handler := instrumentHTTP(mux)

	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/healthz", nil))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/tasks/123", nil))

	reg := NewMetricsRegistry(store)
	rec := httptest.NewRecorder()
	promhttp.HandlerFor(reg, promhttp.HandlerOpts{}).ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if !strings.Contains(body, `task_api_http_requests_total{method="GET",route="/healthz",status="200"} 1`) {
		t.Fatalf("expected /healthz request to be counted, got:\n%s", body)
	}
	// Route label must be the mux pattern, not the raw path with the id baked in,
	// so per-task requests don't create unbounded label cardinality.
	if !strings.Contains(body, `route="/tasks/{id}",status="404"`) {
		t.Fatalf("expected /tasks/123 to be recorded under the /tasks/{id} pattern, got:\n%s", body)
	}
	if strings.Contains(body, `route="/tasks/123"`) {
		t.Fatalf("raw path leaked into route label, got:\n%s", body)
	}
}
