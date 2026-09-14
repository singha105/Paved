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

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// get sends a GET for path to a new testsvc handler and returns the status code and body.
func get(t *testing.T, path string) (int, string) {
	t.Helper()
	handler, err := newHandler()
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	body, err := io.ReadAll(recorder.Result().Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return recorder.Code, string(body)
}

// TestRequestSeriesExistBeforeAnyRequest is the guardrail for the bug in POSTMORTEM.md: rate() can't
// count the increase that creates a series, so both codes testsvc answers must already be exposed at
// zero when Prometheus first scrapes it.
func TestRequestSeriesExistBeforeAnyRequest(t *testing.T) {
	_, metrics := get(t, "/metrics")
	for _, code := range []string{"200", "500"} {
		for _, series := range []string{
			fmt.Sprintf(`http_requests_total{code=%q,method="get"} 0`, code),
			fmt.Sprintf(`http_request_duration_seconds_count{code=%q,method="get"} 0`, code),
		} {
			if !strings.Contains(metrics, series) {
				t.Errorf("/metrics before any request is missing %s", series)
			}
		}
	}
}

func TestRoutes(t *testing.T) {
	for path, want := range map[string]int{
		"/":        http.StatusOK,
		"/boom":    http.StatusInternalServerError,
		"/healthz": http.StatusOK,
		"/readyz":  http.StatusOK,
	} {
		if got, _ := get(t, path); got != want {
			t.Errorf("GET %s answered %d, want %d", path, got, want)
		}
	}
}
