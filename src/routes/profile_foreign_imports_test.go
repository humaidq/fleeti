/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

const testForeignRev = "abcd1234abcd1234abcd1234abcd1234abcd1234"

func TestPinnedForeignFlakeRef(t *testing.T) {
	cases := []struct {
		ref  string
		want string
	}{
		{"github:acme/mods", "github:acme/mods/" + testForeignRev},
		{"github:acme/mods/main", "github:acme/mods/" + testForeignRev},
		{"github:org/mono?dir=nixos", "github:org/mono/" + testForeignRev + "?dir=nixos"},
		{"github:org/mono/main?dir=nixos", "github:org/mono/" + testForeignRev + "?dir=nixos"},
		{"gitlab:acme/mods", "gitlab:acme/mods/" + testForeignRev},
		{"git+https://git.example.com/team/mods", "git+https://git.example.com/team/mods?rev=" + testForeignRev},
		{"git+https://git.example.com/team/mods?ref=main", "git+https://git.example.com/team/mods?ref=main&rev=" + testForeignRev},
	}

	for _, tc := range cases {
		got, err := pinnedForeignFlakeRef(tc.ref, testForeignRev)
		if err != nil {
			t.Fatalf("pinnedForeignFlakeRef(%q) error: %v", tc.ref, err)
		}
		if got != tc.want {
			t.Fatalf("pinnedForeignFlakeRef(%q) = %q, want %q", tc.ref, got, tc.want)
		}
	}

	if _, err := pinnedForeignFlakeRef("github:acme/mods", "not-a-rev"); err == nil {
		t.Fatal("expected error for unresolved rev")
	}
}

func TestForeignFlakeAuthTarget(t *testing.T) {
	cases := []struct {
		ref      string
		wantHost string
		wantType string
	}{
		{"github:acme/mods", "github.com", db.ForeignImportAuthGitHubToken},
		{"gitlab:acme/mods", "gitlab.com", db.ForeignImportAuthGitHubToken},
		{"git+https://user@git.example.com/team/mods", "git.example.com", db.ForeignImportAuthNetrcPassword},
	}

	for _, tc := range cases {
		host, authType, err := foreignFlakeAuthTarget(tc.ref)
		if err != nil {
			t.Fatalf("foreignFlakeAuthTarget(%q) error: %v", tc.ref, err)
		}
		if host != tc.wantHost || authType != tc.wantType {
			t.Fatalf("foreignFlakeAuthTarget(%q) = (%q,%q), want (%q,%q)", tc.ref, host, authType, tc.wantHost, tc.wantType)
		}
	}
}

func TestNixAuthArgsGitHubToken(t *testing.T) {
	imports := []db.ForeignImport{{
		FlakeRef: "github:acme/mods",
		Rev:      testForeignRev,
		Modules:  []string{"foo"},
		Auth:     &db.ForeignImportAuth{Type: db.ForeignImportAuthGitHubToken, Host: "github.com", Token: "secret"},
	}}

	args, cleanup, err := nixAuthArgs(imports, t.TempDir())
	if err != nil {
		t.Fatalf("nixAuthArgs error: %v", err)
	}
	defer cleanup()

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--option access-tokens") || !strings.Contains(joined, "github.com=secret") {
		t.Fatalf("expected access-tokens option, got %v", args)
	}
	if strings.Contains(joined, "--netrc-file") {
		t.Fatalf("did not expect netrc for github token, got %v", args)
	}
}

func TestNixAuthArgsNetrc(t *testing.T) {
	scratch := t.TempDir()
	imports := []db.ForeignImport{{
		FlakeRef: "git+https://git.example.com/team/mods",
		Rev:      testForeignRev,
		Modules:  []string{"foo"},
		Auth:     &db.ForeignImportAuth{Type: db.ForeignImportAuthNetrcPassword, Host: "git.example.com", Username: "ci", Token: "secret"},
	}}

	args, cleanup, err := nixAuthArgs(imports, scratch)
	if err != nil {
		t.Fatalf("nixAuthArgs error: %v", err)
	}

	var netrcPath string
	for i := range args {
		if args[i] == "--netrc-file" && i+1 < len(args) {
			netrcPath = args[i+1]
		}
	}
	if netrcPath == "" {
		t.Fatalf("expected --netrc-file in args, got %v", args)
	}

	info, err := os.Stat(netrcPath)
	if err != nil {
		t.Fatalf("netrc file missing: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected netrc mode 0600, got %o", info.Mode().Perm())
	}

	content, err := os.ReadFile(netrcPath)
	if err != nil {
		t.Fatalf("read netrc: %v", err)
	}
	if !strings.Contains(string(content), "machine git.example.com login ci password secret") {
		t.Fatalf("unexpected netrc content: %q", string(content))
	}

	cleanup()
	if _, err := os.Stat(netrcPath); !os.IsNotExist(err) {
		t.Fatalf("expected netrc file removed by cleanup, stat err = %v", err)
	}
}

func TestWriteProfileForeignImportsModuleEmptyStub(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatalf("mkdir modules: %v", err)
	}

	if err := writeProfileForeignImportsModule(root, nil); err != nil {
		t.Fatalf("writeProfileForeignImportsModule error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "modules", "profile-foreign-imports.nix"))
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	if strings.TrimSpace(string(content)) != "_: { }" {
		t.Fatalf("expected stub module, got %q", string(content))
	}
}

func TestWriteProfileForeignImportsModuleGeneratesImports(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "modules"), 0o755); err != nil {
		t.Fatalf("mkdir modules: %v", err)
	}

	imports := []db.ForeignImport{
		{FlakeRef: "github:acme/mods", Rev: testForeignRev, Modules: []string{"fooService", "barHardening"}},
		{FlakeRef: "git+https://git.example.com/team/mods", Rev: testForeignRev, Modules: []string{"vpn"}},
	}

	if err := writeProfileForeignImportsModule(root, imports); err != nil {
		t.Fatalf("writeProfileForeignImportsModule error: %v", err)
	}

	content, err := os.ReadFile(filepath.Join(root, "modules", "profile-foreign-imports.nix"))
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	generated := string(content)

	wantSnippets := []string{
		`builtins.getFlake "github:acme/mods/` + testForeignRev + `"`,
		`builtins.getFlake "git+https://git.example.com/team/mods?rev=` + testForeignRev + `"`,
		`.nixosModules."fooService"`,
		`.nixosModules."barHardening"`,
		`.nixosModules."vpn"`,
	}
	for _, snippet := range wantSnippets {
		if !strings.Contains(generated, snippet) {
			t.Fatalf("generated module missing %q:\n%s", snippet, generated)
		}
	}
}
