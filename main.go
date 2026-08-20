package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		os.Exit(healthcheck(getEnv("PORT", "8080")))
	}

	port := getEnv("PORT", "8080")
	store := NewMemoryStore()

	mux := http.NewServeMux()

	// Health check — must respond 200 for liveness probes
	mux.HandleFunc("GET /healthz", HealthHandler)

	// Metrics — Prometheus scrape endpoint
	registry := NewMetricsRegistry(store)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	// Task CRUD
	mux.HandleFunc("GET /tasks", ListTasksHandler(store))
	mux.HandleFunc("POST /tasks", CreateTaskHandler(store))
	mux.HandleFunc("GET /tasks/{id}", GetTaskHandler(store))
	mux.HandleFunc("PUT /tasks/{id}", UpdateTaskHandler(store))
	mux.HandleFunc("DELETE /tasks/{id}", DeleteTaskHandler(store))

	addr := ":" + port
	log.Printf("task-api starting on %s", addr)
	if err := http.ListenAndServe(addr, instrumentHTTP(mux)); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// healthcheck lets the Dockerfile's HEALTHCHECK exec this binary instead of
// shelling out to curl/wget, which the distroless runtime image doesn't have.
func healthcheck(port string) int {
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
