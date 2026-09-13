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

// Package slo turns a ServiceClaim's SLI and SLO into Prometheus rules: recording rules for
// the SLI error ratio over each window, and multi-window, multi-burn-rate alerts. It also
// computes how much error budget a service has left.
//
// Apart from the Prometheus client, everything here is pure: no API calls, no clock.
package slo

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
)

// ErrorBudget returns the fraction of requests a claim may fail, 1 - objective/100, as an
// exact fraction: objective "99.5" gives 1/200. It accepts what the CRD accepts: a plain
// decimal number, at least 90 and less than 100.
func ErrorBudget(objective string) (*big.Rat, error) {
	if !isDecimal(objective) {
		return nil, fmt.Errorf("objective %q is not a plain decimal number", objective)
	}
	percent, ok := new(big.Rat).SetString(objective)
	if !ok {
		return nil, fmt.Errorf("objective %q is not a plain decimal number", objective)
	}
	if percent.Cmp(big.NewRat(90, 1)) < 0 || percent.Cmp(big.NewRat(100, 1)) >= 0 {
		return nil, fmt.Errorf("objective %q must be at least 90 and less than 100", objective)
	}
	target := new(big.Rat).Quo(percent, big.NewRat(100, 1))
	return new(big.Rat).Sub(big.NewRat(1, 1), target), nil
}

// BudgetUse is how much of its error budget a service has spent over a window.
type BudgetUse struct {
	// Consumed is the share of the budget spent: 0 for a service with no errors, 1 when the
	// error ratio exactly equals the budget, and above 1 when the budget is overspent.
	Consumed float64
	// Remaining is the share of the budget left, never below 0 or above 1.
	Remaining float64
}

// SpendBudget works out how much error budget an error ratio has used:
//
//	consumed  = errorRatio / (1 - objective)
//	remaining = clamp(1 - consumed, 0, 1)
//
// budget is 1 - objective, as ErrorBudget returns it. A budget of zero, which an objective of
// 100 would produce, returns an error rather than dividing by zero. A NaN or infinite error
// ratio returns an error too. A slightly negative ratio, which floating-point rounding can
// produce in "1 - (good / all)", counts as 0.
func SpendBudget(errorRatio float64, budget *big.Rat) (BudgetUse, error) {
	rate, err := BurnRate(errorRatio, budget)
	if err != nil {
		return BudgetUse{}, err
	}
	// Spending budget at burn rate r over a whole window consumes r budgets.
	return BudgetUse{Consumed: rate, Remaining: min(max(1-rate, 0), 1)}, nil
}

// BurnRate is how many times faster than sustainable a service is spending its budget:
// errorRatio / (1 - objective). A burn rate of 1 spends exactly the whole budget over the SLO
// window. It returns an error for a zero budget and for a NaN or infinite error ratio.
func BurnRate(errorRatio float64, budget *big.Rat) (float64, error) {
	if budget == nil || budget.Sign() <= 0 {
		return 0, fmt.Errorf("error budget must be greater than zero; an objective of 100 leaves no budget to spend")
	}
	if math.IsNaN(errorRatio) || math.IsInf(errorRatio, 0) {
		return 0, fmt.Errorf("error ratio %v is not a number", errorRatio)
	}
	b, _ := budget.Float64()
	return max(errorRatio, 0) / b, nil
}

// FormatPercent formats a fraction of a budget for status, with one decimal place: 0.625
// gives "62.5%".
func FormatPercent(fraction float64) string {
	return strconv.FormatFloat(fraction*100, 'f', 1, 64) + "%"
}

// FormatBurnRate formats a burn rate for status, with two decimal places: 14.4 gives "14.40".
func FormatBurnRate(rate float64) string {
	return strconv.FormatFloat(rate, 'f', 2, 64)
}

// Decimal formats r without floating-point rounding: 1/200 gives "0.005". Budgets and
// thresholds derived from a decimal objective always have a finite decimal expansion.
func Decimal(r *big.Rat) string {
	digits, exact := r.FloatPrec()
	if !exact {
		digits = 12
	}
	return r.FloatString(digits)
}

// isDecimal reports whether s is digits, optionally followed by a dot and more digits.
func isDecimal(s string) bool {
	seenDigit, seenDot, digitAfterDot := false, false, false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			seenDigit = true
			if seenDot {
				digitAfterDot = true
			}
		case r == '.' && seenDigit && !seenDot:
			seenDot = true
		default:
			return false
		}
	}
	return seenDigit && (!seenDot || digitAfterDot)
}
