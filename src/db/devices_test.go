/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"strings"
	"testing"
)

func TestDeviceAttestationTier(t *testing.T) {
	cases := []struct {
		name       string
		secureBoot bool
		attested   bool
		want       string
	}{
		{"no secure boot", false, false, "none"},
		{"secure boot only", true, false, "secure-boot"},
		{"secure boot and attested", true, true, "attested"},
		{"attested without secure boot stays none", false, true, "none"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Device{SecureBootEnabled: tc.secureBoot, Attested: tc.attested}
			if got := d.AttestationTier(); got != tc.want {
				t.Fatalf("AttestationTier() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEnrollmentCodeAlphabetIsUnbiased(t *testing.T) {
	if len(enrollmentCodeAlphabet) != 32 {
		t.Fatalf("enrollment alphabet must be 32 symbols for unbiased selection, got %d", len(enrollmentCodeAlphabet))
	}

	for _, ambiguous := range []rune{'I', 'O', '0', '1'} {
		if strings.ContainsRune(enrollmentCodeAlphabet, ambiguous) {
			t.Fatalf("enrollment alphabet must not contain ambiguous character %q", ambiguous)
		}
	}
}

func TestGenerateEnrollmentCode(t *testing.T) {
	code, err := generateEnrollmentCode()
	if err != nil {
		t.Fatalf("generateEnrollmentCode returned error: %v", err)
	}

	if len(code) != enrollmentCodeLength {
		t.Fatalf("expected code length %d, got %d (%q)", enrollmentCodeLength, len(code), code)
	}

	for _, c := range code {
		if !strings.ContainsRune(enrollmentCodeAlphabet, c) {
			t.Fatalf("code %q contains out-of-alphabet character %q", code, c)
		}
	}
}

func TestGenerateDeviceToken(t *testing.T) {
	raw, prefix, hash, err := generateDeviceToken()
	if err != nil {
		t.Fatalf("generateDeviceToken returned error: %v", err)
	}

	if !strings.HasPrefix(raw, deviceTokenPrefix) {
		t.Fatalf("token %q missing prefix %q", raw, deviceTokenPrefix)
	}

	if len(prefix) != deviceTokenDisplayPrefixSize {
		t.Fatalf("expected display prefix length %d, got %d", deviceTokenDisplayPrefixSize, len(prefix))
	}

	if len(hash) != 32 {
		t.Fatalf("expected sha256 hash length 32, got %d", len(hash))
	}

	if string(hash) == raw {
		t.Fatalf("hash must not equal the raw token")
	}
}

func TestShorten(t *testing.T) {
	if got := shorten("abcdefghij", 8); got != "abcdefgh" {
		t.Fatalf("expected truncation to 8 chars, got %q", got)
	}

	if got := shorten("abc", 8); got != "abc" {
		t.Fatalf("expected short value unchanged, got %q", got)
	}
}
