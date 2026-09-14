package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// maxBodyBytes caps a create request's body.
const maxBodyBytes = 1 << 16

// statuses lists the status codes shortlink answers application requests with, by the method
// label promhttp records. Their series are created at zero before serving: Prometheus's rate()
// can't see the increase that creates a series, so otherwise the first request with each code
// after a start would never count against the SLO.
var statuses = map[string][]string{
	"post": {"201", "400", "500"},
	"get":  {"302", "404"},
}

type createRequest struct {
	URL string `json:"url"`
}

type createResponse struct {
	Code  string `json:"code"`
	Short string `json:"short"`
	URL   string `json:"url"`
}

// newHandler returns shortlink's routes: the application endpoints, instrumented, and the probe
// and metrics endpoints, which are not, so that they don't make the service look healthier.
func newHandler(links *store) (http.Handler, error) {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "HTTP requests served, by status code and method.",
	}, []string{"code", "method"})
	// A bucket at 0.25s matches paved's default http-latency threshold of 250ms.
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
	for method, codes := range statuses {
		for _, code := range codes {
			if _, err := requests.GetMetricWithLabelValues(code, method); err != nil {
				return nil, fmt.Errorf("creating the %s %s request series: %w", method, code, err)
			}
			if _, err := latency.GetMetricWithLabelValues(code, method); err != nil {
				return nil, fmt.Errorf("creating the %s %s latency series: %w", method, code, err)
			}
		}
	}

	instrument := func(handler http.HandlerFunc) http.Handler {
		return promhttp.InstrumentHandlerDuration(latency, promhttp.InstrumentHandlerCounter(requests, handler))
	}
	mux := http.NewServeMux()
	mux.Handle("POST /links", instrument(createLink(links)))
	mux.Handle("GET /{code}", instrument(followLink(links)))
	mux.HandleFunc("GET /healthz", ok)
	mux.HandleFunc("GET /readyz", ok)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	return mux, nil
}

// createLink stores a short code for an absolute http or https URL and answers 201 with it.
func createLink(links *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
			http.Error(w, `the body must be JSON like {"url": "https://example.com"}`, http.StatusBadRequest)
			return
		}
		target, err := url.Parse(req.URL)
		if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" {
			http.Error(w, "url must be an absolute http or https URL", http.StatusBadRequest)
			return
		}

		code, err := links.add(target.String())
		if err != nil {
			log.Printf("Failed to create a link: %v", err)
			http.Error(w, "could not create a link", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/"+code)
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(createResponse{Code: code, Short: "/" + code, URL: target.String()}); err != nil {
			log.Printf("Failed to write the response: %v", err)
		}
	}
}

// followLink redirects to the URL stored for the code in the path, or answers 404.
func followLink(links *store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		target, found := links.get(r.PathValue("code"))
		if !found {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// ok answers a probe with 200.
func ok(w http.ResponseWriter, _ *http.Request) {
	if _, err := fmt.Fprintln(w, "ok"); err != nil {
		log.Printf("Failed to write the response: %v", err)
	}
}
