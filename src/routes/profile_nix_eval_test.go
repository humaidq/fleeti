/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import "testing"

func TestNormalizeNixOSOptionPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "simple", input: "services.openssh.enable", want: "services.openssh.enable"},
		{name: "trimmed", input: " services.openssh.settings ", want: "services.openssh.settings"},
		{name: "hyphenated", input: "services.openclaw-gateway", want: "services.openclaw-gateway"},
		{name: "empty segment", input: "services..enable", wantErr: true},
		{name: "unsupported chars", input: "services.openssh[0]", wantErr: true},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeNixOSOptionPath(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("normalizeNixOSOptionPath returned error: %v", err)
			}

			if got != test.want {
				t.Fatalf("expected %q, got %q", test.want, got)
			}
		})
	}
}
