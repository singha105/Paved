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

package slo

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakePrometheus serves a fixed body for /api/v1/query and records the query it was asked.
func fakePrometheus(t *testing.T, status int, body string) (*httptest.Server, *string) {
	t.Helper()
	var asked string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/v1/query") {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parsing query form: %v", err)
		}
		asked = r.Form.Get("query")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := fmt.Fprint(w, body); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	return server, &asked
}

func vectorBody(values ...string) string {
	samples := make([]string, 0, len(values))
	for _, v := range values {
		samples = append(samples, fmt.Sprintf(`{"metric":{"service":"url-shortener"},"value":[1789277146.331,%q]}`, v))
	}
	return `{"status":"success","data":{"resultType":"vector","result":[` + strings.Join(samples, ",") + `]}}`
}

func TestQueryValue(t *testing.T) {
	server, asked := fakePrometheus(t, http.StatusOK, vectorBody("0.625"))
	client, err := NewPrometheusClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	value, err := client.QueryValue(t.Context(), `sli:http_availability:error_ratio_rate1h{service="url-shortener"}`)
	if err != nil {
		t.Fatalf("QueryValue returned error: %v", err)
	}
	if value != 0.625 {
		t.Errorf("value = %v, want 0.625", value)
	}
	if *asked != `sli:http_availability:error_ratio_rate1h{service="url-shortener"}` {
		t.Errorf("Prometheus was asked %q", *asked)
	}
}

func TestQueryValueWithoutData(t *testing.T) {
	for name, body := range map[string]string{
		"empty result": vectorBody(),
		"NaN, from 0/0 when there were no requests": vectorBody("NaN"),
	} {
		t.Run(name, func(t *testing.T) {
			server, _ := fakePrometheus(t, http.StatusOK, body)
			client, err := NewPrometheusClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.QueryValue(t.Context(), "up"); !errors.Is(err, ErrNoData) {
				t.Errorf("QueryValue error = %v, want ErrNoData", err)
			}
		})
	}
}

func TestQueryValueFailures(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
	}{
		"more than one series": {http.StatusOK, vectorBody("0.1", "0.2")},
		"scalar result":        {http.StatusOK, `{"status":"success","data":{"resultType":"scalar","result":[1789277146.331,"1"]}}`},
		"bad query":            {http.StatusBadRequest, `{"status":"error","errorType":"bad_data","error":"parse error"}`},
		"server error":         {http.StatusInternalServerError, `{"status":"error","errorType":"internal","error":"boom"}`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			server, _ := fakePrometheus(t, tt.status, tt.body)
			client, err := NewPrometheusClient(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.QueryValue(t.Context(), "up")
			if err == nil || errors.Is(err, ErrNoData) {
				t.Errorf("QueryValue error = %v, want a failure that isn't ErrNoData", err)
			}
		})
	}
}

func TestQueryValueUnreachable(t *testing.T) {
	// Reserve a port, then close it, so nothing is listening there.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := "http://" + listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	client, err := NewPrometheusClient(address)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.QueryValue(t.Context(), "up")
	if err == nil || errors.Is(err, ErrNoData) || !strings.Contains(err.Error(), "querying Prometheus") {
		t.Errorf("QueryValue error = %v, want a wrapped connection failure", err)
	}
}

func TestQueryValueTimesOut(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	client, err := newPrometheusClient(server.URL, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = client.QueryValue(t.Context(), "up")
	if err == nil {
		t.Fatal("QueryValue returned no error from a server that never answers")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("QueryValue took %v, want it to give up after about 100ms", elapsed)
	}
}

func TestNewPrometheusClientRejectsABadAddress(t *testing.T) {
	if _, err := NewPrometheusClient("://not a url"); err == nil {
		t.Error("NewPrometheusClient accepted an invalid address")
	}
}
