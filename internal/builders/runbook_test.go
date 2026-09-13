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
	"maps"
	"strings"
	"testing"

	platformv1alpha1 "github.com/singha105/paved/api/v1alpha1"
	"github.com/singha105/paved/internal/slo"
)

func TestBuildRunbook(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	cm, err := BuildRunbook(sc)
	if err != nil {
		t.Fatal(err)
	}

	if cm.Name != "url-shortener-runbook" || cm.Namespace != testNamespace {
		t.Errorf("ConfigMap = %s/%s, want %s/url-shortener-runbook", cm.Namespace, cm.Name, testNamespace)
	}
	if !maps.Equal(cm.Labels, Labels(sc)) {
		t.Errorf("labels = %v, want %v", cm.Labels, Labels(sc))
	}
	text, ok := cm.Data[RunbookKey]
	if !ok {
		t.Fatalf("data has no %q key", RunbookKey)
	}

	for _, want := range []string{
		"# Runbook: url-shortener",
		"## What this service is",
		"url-shortener is a public service",
		"not one of 200, 201, 204, 301, 302, 304, 400, 404.",
		"99.5% of requests are good over 28d, so the error budget is 0.005 (0.5% of requests).",
		"## Who owns it",
		"**team-links** owns this service",
		"ServiceClaim `platform-claims/url-shortener`",
		"## What each alert means",
		"| page | 1h and 5m | 0.072 (14.4x) | about 47 hours |",
		"| page | 6h and 30m | 0.03 (6x) | about 4.7 days |",
		"| ticket | 1d and 2h | 0.015 (3x) | about 9.3 days |",
		"| ticket | 3d and 6h | 0.005 (1x) | about 28 days |",
		"## First three debugging steps",
		"1. **Check whether a deploy is involved.**",
		"2. **Find when the burn started**",
		"3. **Read recent logs:** `kubectl logs -n svc-url-shortener -l app.kubernetes.io/name=url-shortener",
		slo.RunbookURL,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("runbook is missing %q\n--- runbook ---\n%s", want, text)
		}
	}
}

func TestBuildRunbookDescribesLatencySLIs(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierInternal)
	sc.Spec.SLI.Type = platformv1alpha1.SLIHTTPLatency
	cm, err := BuildRunbook(sc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cm.Data[RunbookKey], "A request is bad when it takes longer than 250ms.") {
		t.Errorf("latency runbook doesn't define a bad request:\n%s", cm.Data[RunbookKey])
	}
}

func TestBuildRunbookRejectsInvalidSLOs(t *testing.T) {
	sc := newClaim(platformv1alpha1.TierPublic)
	sc.Spec.SLO.Window = "fortnight"
	if _, err := BuildRunbook(sc); err == nil {
		t.Error("BuildRunbook returned no error for window \"fortnight\"")
	}
}
