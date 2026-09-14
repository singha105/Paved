package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve sends one request to handler and returns the response.
func serve(t *testing.T, handler http.Handler, method, target, body string) *http.Response {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(method, target, strings.NewReader(body)))
	return recorder.Result()
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	handler, err := newHandler(newStore())
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return handler
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading the body: %v", err)
	}
	return string(body)
}

// TestEveryStatusSeriesExistsBeforeAnyRequest is the guardrail for the bug in POSTMORTEM.md: a
// series created by its first request loses that request from rate(), so every status code
// shortlink can answer must already be exposed at zero.
func TestEveryStatusSeriesExistsBeforeAnyRequest(t *testing.T) {
	metrics := readBody(t, serve(t, newTestHandler(t), http.MethodGet, "/metrics", ""))
	for method, codes := range statuses {
		for _, code := range codes {
			for _, series := range []string{
				fmt.Sprintf(`http_requests_total{code=%q,method=%q} 0`, code, method),
				fmt.Sprintf(`http_request_duration_seconds_count{code=%q,method=%q} 0`, code, method),
			} {
				if !strings.Contains(metrics, series) {
					t.Errorf("/metrics before any request is missing %s", series)
				}
			}
		}
	}
}

func TestCreateAndFollowALink(t *testing.T) {
	handler := newTestHandler(t)

	created := serve(t, handler, http.MethodPost, "/links", `{"url": "https://example.com/docs?page=2"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("POST /links answered %d, want 201: %s", created.StatusCode, readBody(t, created))
	}
	var link createResponse
	if err := json.NewDecoder(created.Body).Decode(&link); err != nil {
		t.Fatalf("decoding the create response: %v", err)
	}
	if len(link.Code) != codeLength || link.Short != "/"+link.Code || created.Header.Get("Location") != link.Short {
		t.Errorf("created link %+v with Location %q, want a %d-character code at /<code>",
			link, created.Header.Get("Location"), codeLength)
	}

	followed := serve(t, handler, http.MethodGet, link.Short, "")
	if followed.StatusCode != http.StatusFound || followed.Header.Get("Location") != "https://example.com/docs?page=2" {
		t.Errorf("GET %s answered %d to %q, want 302 to the stored URL",
			link.Short, followed.StatusCode, followed.Header.Get("Location"))
	}

	metrics := readBody(t, serve(t, handler, http.MethodGet, "/metrics", ""))
	for _, series := range []string{
		`http_requests_total{code="201",method="post"} 1`,
		`http_requests_total{code="302",method="get"} 1`,
	} {
		if !strings.Contains(metrics, series) {
			t.Errorf("/metrics is missing %s", series)
		}
	}
}

func TestRejectsWhatIsNotAnAbsoluteWebURL(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":           `url=https://example.com`,
		"no URL":             `{}`,
		"a relative URL":     `{"url": "/docs"}`,
		"another scheme":     `{"url": "ftp://example.com/file"}`,
		"a URL with no host": `{"url": "https://"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := serve(t, newTestHandler(t), http.MethodPost, "/links", body).StatusCode; got != http.StatusBadRequest {
				t.Errorf("POST /links with %s answered %d, want 400", body, got)
			}
		})
	}
}

func TestUnknownCodeIsNotFound(t *testing.T) {
	if got := serve(t, newTestHandler(t), http.MethodGet, "/nosuchcode", "").StatusCode; got != http.StatusNotFound {
		t.Errorf("GET /nosuchcode answered %d, want 404", got)
	}
}

func TestProbesAnswer(t *testing.T) {
	handler := newTestHandler(t)
	for _, probe := range []string{"/healthz", "/readyz"} {
		if got := serve(t, handler, http.MethodGet, probe, "").StatusCode; got != http.StatusOK {
			t.Errorf("GET %s answered %d, want 200", probe, got)
		}
	}
}
