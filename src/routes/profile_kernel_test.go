/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"strings"
	"testing"
)

func TestProfileConfigWithKernelMergesIntoConfigJSON(t *testing.T) {
	t.Parallel()

	updated, err := profileConfigWithKernel(`{"packages":["vim"]}`, ProfileKernelConfig{
		Attr: "linux_6_19",
	})
	if err != nil {
		t.Fatalf("profileConfigWithKernel returned error: %v", err)
	}

	if !strings.Contains(updated, `"packages":["vim"]`) {
		t.Fatalf("expected package list to be preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `"kernel":{"attr":"linux_6_19"}`) {
		t.Fatalf("expected kernel selection in config JSON, got: %s", updated)
	}
}

func TestValidateProfileKernelConfigRequiresRevisionWhenSourceOverrideEnabled(t *testing.T) {
	t.Parallel()

	err := validateProfileKernelConfig(ProfileKernelConfig{
		Attr: "linux_6_19",
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: true,
			URL:     "https://github.com/example/linux.git",
			Ref:     "refs/heads/main",
		},
	}, nil)
	if err == nil {
		t.Fatal("expected validation error for missing source override revision, got nil")
	}

	if !strings.Contains(err.Error(), "revision") {
		t.Fatalf("expected revision validation error, got: %v", err)
	}
}

func TestValidateProfileKernelConfigRequiresHTTPSourceURL(t *testing.T) {
	t.Parallel()

	err := validateProfileKernelConfig(ProfileKernelConfig{
		Attr: "linux_6_19",
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: true,
			URL:     "ssh://git@github.com/example/linux.git",
			Rev:     "abcd1234abcd1234abcd1234abcd1234abcd1234",
		},
	}, nil)
	if err == nil {
		t.Fatal("expected validation error for non-http URL, got nil")
	}

	if !strings.Contains(err.Error(), "http or https") {
		t.Fatalf("expected scheme validation error, got: %v", err)
	}
}
