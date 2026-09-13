/*
Copyright 2026 Arnab Singh.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Command demo-app is a minimal HTTP service that meets the paved workload contract: it
// serves /healthz and /readyz for the platform's probes and /metrics for Prometheus, runs
// as a non-root user and writes nothing to disk. The example ServiceClaims run it until
// real services replace it (DECISIONS.md, ADR-009).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	if err := run(*addr); err != nil {
		log.Fatal(err)
	}
}

func run(addr string) error {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests served, by status code and method.",
	}, []string{"code", "method"})
	// The 0.25s bucket matches the default http-latency threshold of 250ms.
	latency := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "HTTP request latency, by status code and method.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5},
	}, []string{"code", "method"})
	for _, collector := range []prometheus.Collector{requests, latency} {
		if err := registry.Register(collector); err != nil {
			return fmt.Errorf("registering metrics: %w", err)
		}
	}

	// Probe and scrape requests are not counted, so they never dilute the SLI.
	app := promhttp.InstrumentHandlerDuration(latency,
		promhttp.InstrumentHandlerCounter(requests, http.HandlerFunc(ok)))
	mux := http.NewServeMux()
	mux.Handle("/", app)
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Listening on %s", addr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serving: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serving: %w", err)
	}
	return nil
}

// ok answers any request with 200 and a short body.
func ok(w http.ResponseWriter, _ *http.Request) {
	if _, err := fmt.Fprintln(w, "ok"); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}
