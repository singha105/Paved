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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
)

const (
	// DashboardLabel is the label kube-prometheus-stack's Grafana sidecar loads dashboards from,
	// in every namespace.
	DashboardLabel      = "grafana_dashboard"
	dashboardLabelValue = "1"
)

// BuildDashboard returns a ConfigMap that Grafana's sidecar loads as the claim's dashboard. It
// has no panels yet.
func BuildDashboard(sc *platformv1alpha1.ServiceClaim) *corev1.ConfigMap {
	dashboardLabels := Labels(sc)
	dashboardLabels[DashboardLabel] = dashboardLabelValue

	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{APIVersion: corev1.SchemeGroupVersion.String(), Kind: "ConfigMap"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      sc.Name + "-dashboard",
			Namespace: NamespaceName(sc),
			Labels:    dashboardLabels,
		},
		Data: map[string]string{sc.Name + ".json": dashboardJSON(sc)},
	}
}

// DashboardUID is the claim's Grafana dashboard UID. It is derived from the claim's namespace
// and name, so it is stable across reconciles and stays within Grafana's 40-character UID
// limit however long the claim name is.
func DashboardUID(sc *platformv1alpha1.ServiceClaim) string {
	sum := sha256.Sum256([]byte(sc.Namespace + "/" + sc.Name))
	return "paved-" + hex.EncodeToString(sum[:8])
}

// dashboardJSON is the dashboard model. %q is safe here: the UID is hex, and claim names are
// Kubernetes object names (lowercase letters, digits, '-' and '.'), which need no escaping.
func dashboardJSON(sc *platformv1alpha1.ServiceClaim) string {
	return fmt.Sprintf(`{"uid":%q,"title":%q,"tags":["paved"],"panels":[]}`, DashboardUID(sc), sc.Name)
}
