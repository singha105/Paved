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
	"math/big"
	"testing"
)

func TestErrorBudget(t *testing.T) {
	tests := []struct {
		objective string
		want      string
	}{
		{"99.5", "0.005"},
		{"99.9", "0.001"},
		{"99.99", "0.0001"},
		{"99", "0.01"},
		{"90", "0.1"},
		{"95.25", "0.0475"},
	}
	for _, tt := range tests {
		t.Run(tt.objective, func(t *testing.T) {
			budget, err := ErrorBudget(tt.objective)
			if err != nil {
				t.Fatalf("ErrorBudget(%q) returned error: %v", tt.objective, err)
			}
			if got := Decimal(budget); got != tt.want {
				t.Errorf("ErrorBudget(%q) = %s, want %s", tt.objective, got, tt.want)
			}
		})
	}
}

func TestErrorBudgetRejectsWhatTheCRDRejects(t *testing.T) {
	for _, objective := range []string{"", "abc", "100", "100.0", "89.9", "1/2", "9.95e1", "-99", ".5", "99.", "99.5.1"} {
		if _, err := ErrorBudget(objective); err == nil {
			t.Errorf("ErrorBudget(%q) returned no error", objective)
		}
	}
}

func TestDecimalIsExact(t *testing.T) {
	// 14.4 * 0.005 is 0.07200000000000001 in float64; the threshold must read 0.072.
	budget, err := ErrorBudget("99.5")
	if err != nil {
		t.Fatal(err)
	}
	burnRate, _ := new(big.Rat).SetString("14.4")
	if got := Decimal(new(big.Rat).Mul(burnRate, budget)); got != "0.072" {
		t.Errorf("14.4 x budget = %s, want 0.072", got)
	}
	if got := Decimal(big.NewRat(3, 1)); got != "3" {
		t.Errorf("Decimal(3) = %s, want 3", got)
	}
}
