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
