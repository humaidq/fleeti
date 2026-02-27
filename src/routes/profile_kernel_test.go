/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"encoding/base64"
	"net/url"
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

func TestProfileConfigWithKernelIncludesPatches(t *testing.T) {
	t.Parallel()

	patchContent := "diff --git a/foo b/foo\n"
	updated, err := profileConfigWithKernel(`{"packages":["vim"]}`, ProfileKernelConfig{
		Attr: "linux_6_19",
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: true,
			URL:     "https://github.com/example/linux.git",
			Rev:     "abcd1234abcd1234abcd1234abcd1234abcd1234",
			Patches: []ProfileKernelPatch{
				{
					Name:          "0001-fix.patch",
					SHA256:        "be6f08ca3e84fdbcf9f53c778f1944f24591b31af08c74f81f6ff497f57a5717",
					ContentBase64: base64.StdEncoding.EncodeToString([]byte(patchContent)),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("profileConfigWithKernel returned error: %v", err)
	}

	if !strings.Contains(updated, `"patches":[{"content_base64":"`) {
		t.Fatalf("expected patches to be included in config JSON, got: %s", updated)
	}
}

func TestProfileKernelSourcePatchesFromFormOrdersAndRemoves(t *testing.T) {
	t.Parallel()

	existing := []ProfileKernelPatch{
		{Name: "first.patch", SHA256: "5f1f5f7f3ecf30b8f7f9b9e0ad2a77ba90e6c4d15f30d5f6a12eaf7b7d40f5be", ContentBase64: base64.StdEncoding.EncodeToString([]byte("a"))},
		{Name: "second.patch", SHA256: "0d3aed023148ffd2e82ed44f443ad54333f59fd00f09b03512f2c46d65b57c10", ContentBase64: base64.StdEncoding.EncodeToString([]byte("b"))},
	}

	values := url.Values{}
	values.Set(existingKernelPatchOrderFieldName(0), "2")
	values.Set(existingKernelPatchOrderFieldName(1), "1")
	values.Set(existingKernelPatchRemoveFieldName(0), "1")

	patches, err := profileKernelSourcePatchesFromForm(values, nil, existing)
	if err != nil {
		t.Fatalf("profileKernelSourcePatchesFromForm returned error: %v", err)
	}

	if len(patches) != 1 {
		t.Fatalf("expected one patch after remove, got %d", len(patches))
	}

	if patches[0].Name != "second.patch" {
		t.Fatalf("expected second.patch to remain, got %s", patches[0].Name)
	}
}

func TestValidateProfileKernelConfigRejectsChecksumMismatch(t *testing.T) {
	t.Parallel()

	err := validateProfileKernelConfig(ProfileKernelConfig{
		Attr: "linux_6_19",
		SourceOverride: ProfileKernelSourceOverride{
			Enabled: true,
			URL:     "https://github.com/example/linux.git",
			Rev:     "abcd1234abcd1234abcd1234abcd1234abcd1234",
			Patches: []ProfileKernelPatch{
				{
					Name:          "0001-fix.patch",
					SHA256:        "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					ContentBase64: base64.StdEncoding.EncodeToString([]byte("diff --git a/foo b/foo\n")),
				},
			},
		},
	}, nil)
	if err == nil {
		t.Fatal("expected checksum mismatch validation error, got nil")
	}

	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}
}
