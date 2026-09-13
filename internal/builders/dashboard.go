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

package builders

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

const (
	// DashboardLabel is the label kube-prometheus-stack's Grafana sidecar loads dashboards from,
	// in every namespace.
	DashboardLabel      = "grafana_dashboard"
	dashboardLabelValue = "1"

	// prometheusDatasourceUID is the UID kube-prometheus-stack gives its Prometheus datasource.
	prometheusDatasourceUID = "prometheus"
)

// Panel titles, in dashboard order.
const (
	PanelRequestRate   = "Request rate"
	PanelErrorRatio    = "Error ratio vs objective"
	PanelLatency       = "Latency p50 / p95 / p99"
	PanelBudgetLeft    = "Error budget remaining"
	dashboardTagPaved  = "paved"
	dashboardTimeRange = "now-6h"
)

// BuildDashboard returns a ConfigMap that Grafana's sidecar loads as the claim's dashboard:
// request rate, error ratio against the objective, latency percentiles, and error budget
// remaining over the SLO window.
func BuildDashboard(sc *platformv1alpha1.ServiceClaim) (*corev1.ConfigMap, error) {
	model, err := dashboardModel(sc)
	if err != nil {
		return nil, err
	}
	content, err := json.MarshalIndent(model, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding dashboard: %w", err)
	}

	dashboardLabels := Labels(sc)
	dashboardLabels[DashboardLabel] = dashboardLabelValue

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sc.Name + "-dashboard",
			Namespace: NamespaceName(sc),
			Labels:    dashboardLabels,
		},
		Data: map[string]string{sc.Name + ".json": string(content)},
	}, nil
}

// DashboardUID is the claim's Grafana dashboard UID. It is derived from the claim's namespace
// and name, so it is stable across reconciles and stays within Grafana's 40-character UID
// limit however long the claim name is.
func DashboardUID(sc *platformv1alpha1.ServiceClaim) string {
	sum := sha256.Sum256([]byte(sc.Namespace + "/" + sc.Name))
	return "paved-" + hex.EncodeToString(sum[:8])
}

func dashboardModel(sc *platformv1alpha1.ServiceClaim) (grafanaDashboard, error) {
	namespace := NamespaceName(sc)
	budget, err := slo.ErrorBudget(sc.Spec.SLO.Objective)
	if err != nil {
		return grafanaDashboard{}, err
	}
	budgetText := slo.Decimal(budget)
	windowRatio, err := slo.ErrorRatioExpr(sc.Spec.SLI, namespace, sc.Spec.SLO.Window)
	if err != nil {
		return grafanaDashboard{}, err
	}
	requests := fmt.Sprintf("%s{namespace=%q}", slo.RequestsMetric, namespace)
	buckets := fmt.Sprintf("sum by (le) (rate(%s_bucket{namespace=%q}[$__rate_interval]))", slo.DurationMetric, namespace)
	record := func(window string) string {
		return fmt.Sprintf("%s{service=%q}", slo.RecordName(sc.Spec.SLI.Type, window), sc.Name)
	}

	latencyTargets := []grafanaTarget{
		target("A", fmt.Sprintf("histogram_quantile(0.5, %s)", buckets), "p50"),
		target("B", fmt.Sprintf("histogram_quantile(0.95, %s)", buckets), "p95"),
		target("C", fmt.Sprintf("histogram_quantile(0.99, %s)", buckets), "p99"),
	}
	if sc.Spec.SLI.Type == platformv1alpha1.SLIHTTPLatency {
		threshold, err := time.ParseDuration(sc.Spec.SLI.LatencyThreshold)
		if err != nil {
			return grafanaDashboard{}, fmt.Errorf("latencyThreshold %q is not a duration: %w", sc.Spec.SLI.LatencyThreshold, err)
		}
		latencyTargets = append(latencyTargets,
			target("D", fmt.Sprintf("vector(%g)", threshold.Seconds()), "SLO threshold"))
	}

	return grafanaDashboard{
		UID:     DashboardUID(sc),
		Title:   sc.Name,
		Tags:    []string{dashboardTagPaved, sc.Spec.Tier},
		Time:    grafanaTime{From: dashboardTimeRange, To: "now"},
		Refresh: "30s",
		Panels: []grafanaPanel{
			panel(1, "timeseries", PanelRequestRate, "reqps", gridPos(0, 0),
				"Requests per second, by status code.",
				target("A", fmt.Sprintf("sum by (code) (rate(%s[$__rate_interval]))", requests), "{{code}}")),
			panel(2, "timeseries", PanelErrorRatio, "percentunit", gridPos(12, 0),
				fmt.Sprintf("Share of bad requests. Above the budget line (%s), the service is spending "+
					"its error budget faster than its %s%% objective allows.", budgetText, sc.Spec.SLO.Objective),
				target("A", record("5m"), "error ratio, 5m"),
				target("B", record("1h"), "error ratio, 1h"),
				target("C", fmt.Sprintf("vector(%s)", budgetText), "error budget")),
			panel(3, "timeseries", PanelLatency, "s", gridPos(0, 8),
				"Request latency percentiles.", latencyTargets...),
			panel(4, "stat", PanelBudgetLeft, "percentunit", gridPos(12, 8),
				fmt.Sprintf("Share of the %s error budget still unspent. Prometheus keeps 10 days of data by "+
					"default, so a longer window only counts what it still has.", sc.Spec.SLO.Window),
				target("A", fmt.Sprintf("1 - ((%s) / %s)", windowRatio, budgetText), "remaining")),
		},
	}, nil
}

func panel(id int, kind, title, unit string, pos grafanaGridPos, description string, targets ...grafanaTarget) grafanaPanel {
	return grafanaPanel{
		ID:          id,
		Type:        kind,
		Title:       title,
		Description: description,
		GridPos:     pos,
		Datasource:  prometheusDatasource(),
		FieldConfig: grafanaFieldConfig{Defaults: grafanaFieldDefaults{Unit: unit}, Overrides: []any{}},
		Targets:     targets,
	}
}

func target(refID, expr, legend string) grafanaTarget {
	return grafanaTarget{RefID: refID, Expr: expr, LegendFormat: legend, Datasource: prometheusDatasource()}
}

// gridPos places a half-width, 8-row panel; Grafana's grid is 24 columns wide.
func gridPos(x, y int) grafanaGridPos {
	return grafanaGridPos{H: 8, W: 12, X: x, Y: y}
}

func prometheusDatasource() grafanaDatasource {
	return grafanaDatasource{Type: "prometheus", UID: prometheusDatasourceUID}
}

// The subset of Grafana's dashboard JSON model the platform generates.
type grafanaDashboard struct {
	UID     string         `json:"uid"`
	Title   string         `json:"title"`
	Tags    []string       `json:"tags"`
	Time    grafanaTime    `json:"time"`
	Refresh string         `json:"refresh"`
	Panels  []grafanaPanel `json:"panels"`
}

type grafanaTime struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type grafanaPanel struct {
	ID          int                `json:"id"`
	Type        string             `json:"type"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	GridPos     grafanaGridPos     `json:"gridPos"`
	Datasource  grafanaDatasource  `json:"datasource"`
	FieldConfig grafanaFieldConfig `json:"fieldConfig"`
	Targets     []grafanaTarget    `json:"targets"`
}

type grafanaGridPos struct {
	H int `json:"h"`
	W int `json:"w"`
	X int `json:"x"`
	Y int `json:"y"`
}

type grafanaDatasource struct {
	Type string `json:"type"`
	UID  string `json:"uid"`
}

type grafanaFieldConfig struct {
	Defaults  grafanaFieldDefaults `json:"defaults"`
	Overrides []any                `json:"overrides"`
}

type grafanaFieldDefaults struct {
	Unit string `json:"unit"`
}

type grafanaTarget struct {
	RefID        string            `json:"refId"`
	Expr         string            `json:"expr"`
	LegendFormat string            `json:"legendFormat"`
	Datasource   grafanaDatasource `json:"datasource"`
}
