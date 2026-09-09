package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"
)

func main() {
	// The final image is FROM scratch: no shell, no curl, no wget. A conventional
	// HEALTHCHECK CMD curl ... would therefore never succeed, so the binary health-checks
	// itself and the Dockerfile invokes it in exec form.
	healthcheck := flag.Bool("healthcheck", false, "probe the local /healthz endpoint and exit")
	showVersion := flag.Bool("version", false, "print build metadata and exit")
	flag.Parse()

	port := getEnv("PORT", "8080")

	switch {
	case *showVersion:
		fmt.Printf("task-api version=%s revision=%s\n", version, revision)
		return
	case *healthcheck:
		if err := probeHealth(port); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	store := NewMemoryStore()
	handler, _ := newRouter(store)

	addr := ":" + port
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
		// Without these a single idle connection can hold a worker open indefinitely.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("task-api starting on %s (version=%s revision=%s)", addr, version, revision)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// newRouter wires every route and returns the instrumented handler plus the Metrics that observe
// it. Tests call this instead of rebuilding the mux themselves, so instrumentation can never be
// exercised in tests while being absent from the binary that actually ships.
func newRouter(store Store) (http.Handler, *Metrics) {
	metrics := NewMetrics(store)

	mux := http.NewServeMux()

	// Health check — must respond 200 for liveness probes
	mux.HandleFunc("GET /healthz", HealthHandler)

	// Metrics — Prometheus scrape endpoint
	mux.Handle("GET /metrics", metrics.Handler())

	// Task CRUD
	mux.HandleFunc("GET /tasks", ListTasksHandler(store))
	mux.HandleFunc("POST /tasks", CreateTaskHandler(store))
	mux.HandleFunc("GET /tasks/{id}", GetTaskHandler(store))
	mux.HandleFunc("PUT /tasks/{id}", UpdateTaskHandler(store))
	mux.HandleFunc("DELETE /tasks/{id}", DeleteTaskHandler(store))

	// Instrumentation wraps the mux rather than each handler, so a route added later is
	// measured automatically instead of being silently invisible.
	return metrics.Middleware(mux), metrics
}

// probeHealth is the container HEALTHCHECK. It returns an error unless /healthz answers 200.
func probeHealth(port string) error {
	client := &http.Client{Timeout: 2 * time.Second}

	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz")
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
