package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scrape drives real HTTP traffic through the wired router and returns the /metrics body.
//
// These tests deliberately go through httptest.NewServer and newRouter rather than calling
// handlers directly: the pre-existing handler tests bypass the mux with SetPathValue, so they
// would stay green even if the middleware were never installed. Only a request that actually
// passes through the mux proves the route label and status code are recorded.
func scrape(t *testing.T, do func(base string)) string {
	t.Helper()

	handler, _ := newRouter(NewMemoryStore())
	srv := httptest.NewServer(handler)
	defer srv.Close()

	do(srv.URL)

	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("scrape /metrics: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	return string(body)
}

func TestRouteLabelUsesTemplateNotRawPath(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Post(base+"/tasks", "application/json", strings.NewReader(`{"title":"a"}`))
		http.Get(base + "/tasks/0")
	})

	if !strings.Contains(body, `route="GET /tasks/{id}"`) {
		t.Fatalf("expected templated route label, got:\n%s", body)
	}
	// The whole point of the template: an unbounded ID space must not become unbounded series.
	if strings.Contains(body, `route="/tasks/0"`) || strings.Contains(body, `route="GET /tasks/0"`) {
		t.Fatalf("raw path leaked into the route label, cardinality is unbounded:\n%s", body)
	}
}

func TestStatusCodeLabelIsRecorded(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Post(base+"/tasks", "application/json", strings.NewReader(`{"title":"a"}`))
	})

	if !strings.Contains(body, `code="201"`) {
		t.Fatalf("expected code=\"201\" for a create, got:\n%s", body)
	}
}

func TestUnmatchedRouteIsBucketed(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Get(base + "/nope/definitely-not-a-route")
	})

	if !strings.Contains(body, `route="unmatched"`) {
		t.Fatalf("expected unmatched requests to collapse to one series, got:\n%s", body)
	}
	if strings.Contains(body, "definitely-not-a-route") {
		t.Fatalf("unmatched path leaked into labels:\n%s", body)
	}
}

func TestNotFoundOnKnownRouteIsCounted(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Get(base + "/tasks/999")
	})

	// A 404 from a matched route keeps its template, so error rate stays attributable per route.
	if !strings.Contains(body, `http_requests_total{code="404",method="GET",route="GET /tasks/{id}"}`) {
		t.Fatalf("expected a 404 counted against the route template, got:\n%s", body)
	}
}

func TestTaskStateGaugeTracksStore(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Post(base+"/tasks", "application/json", strings.NewReader(`{"title":"a"}`))
		http.Post(base+"/tasks", "application/json", strings.NewReader(`{"title":"b"}`))

		req, _ := http.NewRequest(http.MethodPut, base+"/tasks/0", strings.NewReader(`{"done":true}`))
		req.Header.Set("Content-Type", "application/json")
		http.DefaultClient.Do(req)
	})

	for _, want := range []string{
		`task_api_tasks_by_state{state="done"} 1`,
		`task_api_tasks_by_state{state="pending"} 1`,
		"task_api_tasks_total 2",
		"task_api_tasks_done 1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in metrics output, got:\n%s", want, body)
		}
	}
}

func TestLatencyHistogramObservesRequests(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Get(base + "/tasks")
	})

	if !strings.Contains(body, `http_request_duration_seconds_count{method="GET",route="GET /tasks"}`) {
		t.Fatalf("expected a latency observation for GET /tasks, got:\n%s", body)
	}
}

func TestInFlightSettlesBackToZero(t *testing.T) {
	body := scrape(t, func(base string) {
		http.Get(base + "/tasks")
	})

	// The scrape itself is in flight while /metrics renders, so the gauge reads 1, not 0.
	if !strings.Contains(body, "http_requests_in_flight 1") {
		t.Fatalf("expected only the in-progress scrape to be in flight, got:\n%s", body)
	}
}

func TestBuildInfoIsExposed(t *testing.T) {
	body := scrape(t, func(base string) {})

	if !strings.Contains(body, "task_api_build_info{") {
		t.Fatalf("expected build info for commit traceability, got:\n%s", body)
	}
}
