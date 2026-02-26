/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import "testing"

func TestIsSemanticVersion(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		value  string
		expect bool
	}{
		{name: "simple", value: "v1.1.0", expect: true},
		{name: "pre-release", value: "v2.0.0-rc.1", expect: true},
		{name: "build metadata", value: "v2.0.0+build.5", expect: true},
		{name: "missing prefix", value: "1.1.0", expect: false},
		{name: "missing patch", value: "v1.1", expect: false},
		{name: "invalid characters", value: "v1.1.0?", expect: false},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			actual := isSemanticVersion(tc.value)
			if actual != tc.expect {
				t.Fatalf("expected %v, got %v for %q", tc.expect, actual, tc.value)
			}
		})
	}
}
