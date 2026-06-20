/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"strings"
	"testing"
)

const validForeignRev = "abcd1234abcd1234abcd1234abcd1234abcd1234"

func TestCalculateProfileConfigHashEmptyForeignImportsUnchanged(t *testing.T) {
	// Profiles without foreign imports must keep the exact hash material they had
	// before the feature existed (the reserved literal "[]"). Passing an empty
	// canonical form must equal passing the literal "[]".
	withEmpty := calculateProfileConfigHash(1, `{"packages":[]}`, "", "")
	withLiteral := calculateProfileConfigHash(1, `{"packages":[]}`, "", "[]")

	if withEmpty != withLiteral {
		t.Fatalf("empty foreign imports changed the hash material: %s != %s", withEmpty, withLiteral)
	}
}

func TestCalculateProfileConfigHashTokenChangesHash(t *testing.T) {
	base := []ForeignImport{{
		FlakeRef: "github:acme/mods",
		Rev:      validForeignRev,
		Modules:  []string{"foo"},
		Auth:     &ForeignImportAuth{Type: ForeignImportAuthGitHubToken, Host: "github.com", Token: "token-a"},
	}}
	rotated := []ForeignImport{{
		FlakeRef: "github:acme/mods",
		Rev:      validForeignRev,
		Modules:  []string{"foo"},
		Auth:     &ForeignImportAuth{Type: ForeignImportAuthGitHubToken, Host: "github.com", Token: "token-b"},
	}}

	baseCanonical, err := canonicalizeForeignImports(base)
	if err != nil {
		t.Fatalf("canonicalize base: %v", err)
	}
	rotatedCanonical, err := canonicalizeForeignImports(rotated)
	if err != nil {
		t.Fatalf("canonicalize rotated: %v", err)
	}

	baseHash := calculateProfileConfigHash(1, "{}", "", baseCanonical)
	rotatedHash := calculateProfileConfigHash(1, "{}", "", rotatedCanonical)

	if baseHash == rotatedHash {
		t.Fatal("rotating the access token did not change the config hash")
	}
}

func TestCanonicalizeForeignImportsEmptyIsBracket(t *testing.T) {
	got, err := canonicalizeForeignImports(nil)
	if err != nil {
		t.Fatalf("canonicalize nil: %v", err)
	}
	if got != "[]" {
		t.Fatalf("expected [] for empty imports, got %q", got)
	}
}

func TestNormalizeForeignImportsIsDeterministic(t *testing.T) {
	input := []ForeignImport{
		{FlakeRef: "github:b/repo", Rev: "ABCD1234ABCD1234ABCD1234ABCD1234ABCD1234", Modules: []string{"z", "a", "a"}},
		{FlakeRef: " github:a/repo ", Rev: validForeignRev, Modules: []string{"m"}},
	}

	first := NormalizeForeignImports(input)
	second := NormalizeForeignImports(input)

	firstJSON, _ := canonicalizeForeignImports(first)
	secondJSON, _ := canonicalizeForeignImports(second)
	if firstJSON != secondJSON {
		t.Fatalf("normalization is not deterministic: %s != %s", firstJSON, secondJSON)
	}

	if first[0].FlakeRef != "github:a/repo" {
		t.Fatalf("expected refs sorted, got %q first", first[0].FlakeRef)
	}

	if first[1].Rev != strings.ToLower("ABCD1234ABCD1234ABCD1234ABCD1234ABCD1234") {
		t.Fatalf("expected rev lowercased, got %q", first[1].Rev)
	}

	// Duplicate module "a" must be collapsed and modules sorted.
	if len(first[1].Modules) != 2 || first[1].Modules[0] != "a" || first[1].Modules[1] != "z" {
		t.Fatalf("expected deduped sorted modules, got %v", first[1].Modules)
	}
}

func TestValidateForeignImports(t *testing.T) {
	valid := []ForeignImport{{FlakeRef: "github:acme/mods", Rev: validForeignRev, Modules: []string{"foo"}}}
	if err := ValidateForeignImports(valid); err != nil {
		t.Fatalf("expected valid imports to pass, got %v", err)
	}

	cases := map[string][]ForeignImport{
		"path ref":         {{FlakeRef: "path:/etc", Rev: validForeignRev, Modules: []string{"foo"}}},
		"file ref":         {{FlakeRef: "file:///etc/passwd", Rev: validForeignRev, Modules: []string{"foo"}}},
		"parent traversal": {{FlakeRef: "git+https://h/../x", Rev: validForeignRev, Modules: []string{"foo"}}},
		"unpinned rev":     {{FlakeRef: "github:acme/mods", Rev: "main", Modules: []string{"foo"}}},
		"no modules":       {{FlakeRef: "github:acme/mods", Rev: validForeignRev, Modules: nil}},
		"bad module name":  {{FlakeRef: "github:acme/mods", Rev: validForeignRev, Modules: []string{"foo; bar"}}},
	}

	for name, imports := range cases {
		if err := ValidateForeignImports(NormalizeForeignImports(imports)); err == nil {
			t.Fatalf("expected %s to be rejected", name)
		}
	}
}

func TestValidateForeignImportsTooMany(t *testing.T) {
	imports := make([]ForeignImport, 0, MaxForeignFlakes+1)
	for i := 0; i <= MaxForeignFlakes; i++ {
		imports = append(imports, ForeignImport{
			FlakeRef: "github:acme/mods" + string(rune('a'+i)),
			Rev:      validForeignRev,
			Modules:  []string{"foo"},
		})
	}

	if err := ValidateForeignImports(imports); err == nil {
		t.Fatal("expected too many flakes to be rejected")
	}
}
