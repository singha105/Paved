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
	"math"
	"math/big"
	"testing"
)

func TestErrorBudget(t *testing.T) {
	tests := []struct {
		objective string
		want      string
	}{
		{testObjective, "0.005"},
		{tighterObjective, "0.001"},
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
	budget, err := ErrorBudget(testObjective)
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

// mustBudget returns the error budget for objective, failing the test if it is invalid.
func mustBudget(t *testing.T, objective string) *big.Rat {
	t.Helper()
	budget, err := ErrorBudget(objective)
	if err != nil {
		t.Fatal(err)
	}
	return budget
}

func TestSpendBudget(t *testing.T) {
	const tolerance = 1e-9
	tests := []struct {
		name          string
		errorRatio    float64
		objective     string
		wantConsumed  float64
		wantRemaining float64
	}{
		{"perfect service", 0, testObjective, 0, 1},
		{"exactly at the objective", 0.005, testObjective, 1, 0},
		{"half the budget spent", 0.0025, testObjective, 0.5, 0.5},
		{"2x over the objective", 0.01, testObjective, 2, 0},
		{"every request failing", 1, testObjective, 200, 0},
		{"a tighter objective spends faster", 0.001, tighterObjective, 1, 0},
		{"rounding below zero counts as zero", -1e-17, testObjective, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			use, err := SpendBudget(tt.errorRatio, mustBudget(t, tt.objective))
			if err != nil {
				t.Fatalf("SpendBudget returned error: %v", err)
			}
			if math.Abs(use.Consumed-tt.wantConsumed) > tolerance || math.Abs(use.Remaining-tt.wantRemaining) > tolerance {
				t.Errorf("SpendBudget(%v, %s) = consumed %v remaining %v, want consumed %v remaining %v",
					tt.errorRatio, tt.objective, use.Consumed, use.Remaining, tt.wantConsumed, tt.wantRemaining)
			}
		})
	}
}

func TestSpendBudgetGuardsAgainstAZeroBudget(t *testing.T) {
	// An objective of 100 is rejected before any arithmetic happens...
	if _, err := ErrorBudget("100"); err == nil {
		t.Fatal(`ErrorBudget("100") returned no error`)
	}
	// ...and a zero budget reaching SpendBudget returns an error instead of dividing by zero.
	for name, budget := range map[string]*big.Rat{"zero": big.NewRat(0, 1), "negative": big.NewRat(-1, 200), "nil": nil} {
		if use, err := SpendBudget(0.01, budget); err == nil {
			t.Errorf("%s budget: SpendBudget returned %+v and no error", name, use)
		}
	}
}

func TestSpendBudgetRejectsNonNumbers(t *testing.T) {
	for _, ratio := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := SpendBudget(ratio, mustBudget(t, testObjective)); err == nil {
			t.Errorf("SpendBudget(%v) returned no error", ratio)
		}
	}
}

func TestBurnRate(t *testing.T) {
	rate, err := BurnRate(0.072, mustBudget(t, testObjective))
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatBurnRate(rate); got != "14.40" {
		t.Errorf("burn rate for a 0.072 error ratio at 99.5 = %s, want 14.40", got)
	}
}

func TestFormatting(t *testing.T) {
	percents := map[float64]string{1: "100.0%", 0.625: "62.5%", 0: "0.0%", 0.9996: "100.0%", 0.0004: "0.0%"}
	for fraction, want := range percents {
		if got := FormatPercent(fraction); got != want {
			t.Errorf("FormatPercent(%v) = %q, want %q", fraction, got, want)
		}
	}
	rates := map[float64]string{0: "0.00", 1.25: "1.25", 200: "200.00"}
	for rate, want := range rates {
		if got := FormatBurnRate(rate); got != want {
			t.Errorf("FormatBurnRate(%v) = %q, want %q", rate, got, want)
		}
	}
}
