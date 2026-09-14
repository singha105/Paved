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

// Command testsvc is a small HTTP service that meets the paved service contract: it serves
// /healthz and /readyz for the platform's probes and /metrics for Prometheus, runs as a
// non-root user and writes nothing to disk. GET /boom answers 500, so the SLO rules and
// burn-rate alerts can be exercised on demand. The example ServiceClaims run it
// (DECISIONS.md, ADR-012).
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

// broken is set at build time, with -ldflags "-X main.broken=true", to make a bad release whose
// every request to / fails. The probes and /metrics still answer, so its pods become ready and
// take traffic: the canary rollback demo needs a version that deploys cleanly and then fails.
var broken = "false"

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	if err := run(*addr); err != nil {
		log.Fatal(err)
	}
}

func run(addr string) error {
	handler, err := newHandler()
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Listening on %s (broken=%s)", addr, broken)
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

// newHandler returns testsvc's routes: / and /boom, instrumented, and the probes and /metrics,
// which are not.
func newHandler() (http.Handler, error) {
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
			return nil, fmt.Errorf("registering metrics: %w", err)
		}
	}
	// Create every series the SLI reads at zero before serving. rate() can't see the increase
	// that creates a series, so otherwise the first failures after each start would go uncounted.
	for _, code := range []string{"200", "500"} {
		if _, err := requests.GetMetricWithLabelValues(code, "get"); err != nil {
			return nil, fmt.Errorf("creating the code=%s request series: %w", code, err)
		}
		if _, err := latency.GetMetricWithLabelValues(code, "get"); err != nil {
			return nil, fmt.Errorf("creating the code=%s latency series: %w", code, err)
		}
	}

	// Only application requests are counted: probes and scrapes would dilute the SLI.
	instrument := func(handler http.HandlerFunc) http.Handler {
		return promhttp.InstrumentHandlerDuration(latency, promhttp.InstrumentHandlerCounter(requests, handler))
	}
	mux := http.NewServeMux()
	root := ok
	if broken == "true" {
		root = boom
	}
	mux.Handle("/", instrument(root))
	mux.Handle("/boom", instrument(boom))
	mux.HandleFunc("/healthz", ok)
	mux.HandleFunc("/readyz", ok)
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	return mux, nil
}

// ok answers any request with 200 and a short body.
func ok(w http.ResponseWriter, _ *http.Request) {
	if _, err := fmt.Fprintln(w, "ok"); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

// boom answers 500, to spend error budget on purpose.
func boom(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "boom", http.StatusInternalServerError)
}
