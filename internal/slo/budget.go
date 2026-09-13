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
// the SLI error ratio over each window, and multi-window, multi-burn-rate alerts.
//
// Like internal/builders, everything here is pure: no API calls, no clock.
package slo

import (
	"fmt"
	"math/big"
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
