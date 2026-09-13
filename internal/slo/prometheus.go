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
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
)

const (
	// DefaultPrometheusURL is the in-cluster address of kube-prometheus-stack's Prometheus, as
	// hack/cluster-up.sh installs it.
	DefaultPrometheusURL = "http://kps-kube-prometheus-stack-prometheus.monitoring.svc:9090"

	// QueryTimeout bounds every query, so an unreachable Prometheus can't stall a reconcile.
	QueryTimeout = 5 * time.Second
)

// ErrNoData means Prometheus answered but had no value for the query, usually because the
// service received no requests in the window.
var ErrNoData = errors.New("no data")

// Querier evaluates an instant PromQL query that yields one number.
type Querier interface {
	QueryValue(ctx context.Context, query string) (float64, error)
}

// PrometheusClient queries a Prometheus server through its HTTP API.
type PrometheusClient struct {
	api     promv1.API
	timeout time.Duration
}

// NewPrometheusClient returns a client for the Prometheus server at address, for example
// DefaultPrometheusURL.
func NewPrometheusClient(address string) (*PrometheusClient, error) {
	return newPrometheusClient(address, QueryTimeout)
}

func newPrometheusClient(address string, timeout time.Duration) (*PrometheusClient, error) {
	client, err := promapi.NewClient(promapi.Config{
		Address: address,
		Client:  &http.Client{Timeout: timeout},
	})
	if err != nil {
		return nil, fmt.Errorf("creating Prometheus client for %q: %w", address, err)
	}
	return &PrometheusClient{api: promv1.NewAPI(client), timeout: timeout}, nil
}

// QueryValue evaluates query at the current time and returns the value of its single sample.
// It returns ErrNoData when the result is empty or NaN, and a wrapped error when Prometheus
// can't be reached, doesn't answer within the timeout, or returns anything other than one
// sample.
func (c *PrometheusClient) QueryValue(ctx context.Context, query string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	result, warnings, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("querying Prometheus: %w", err)
	}
	vector, ok := result.(model.Vector)
	if !ok {
		return 0, fmt.Errorf("query returned a %s, want a vector", result.Type())
	}
	switch len(vector) {
	case 0:
		if len(warnings) > 0 {
			return 0, fmt.Errorf("%w (Prometheus warned: %v)", ErrNoData, warnings)
		}
		return 0, ErrNoData
	case 1:
	default:
		return 0, fmt.Errorf("query returned %d series, want 1", len(vector))
	}

	value := float64(vector[0].Value)
	if math.IsNaN(value) {
		return 0, ErrNoData
	}
	return value, nil
}
