/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package templates

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
	"time"

	flamegoTemplate "github.com/flamego/template"
)

func TestAllTemplatesParse(t *testing.T) {
	if _, err := flamegoTemplate.EmbedFS(Templates, ".", []string{".html"}); err != nil {
		t.Fatalf("templates failed to parse: %v", err)
	}
}

func TestUsersTemplateRenders(t *testing.T) {
	tmpl, err := template.New("").ParseFS(Templates, "*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	ts := time.Date(2026, 6, 10, 14, 30, 0, 0, time.UTC)

	data := map[string]any{
		"PageTitle":  "Users",
		"IsUsers":    true,
		"csrf_token": "tok",
		"Users": []map[string]any{
			{"ID": "u1", "DisplayName": "Alice", "IsAdmin": true, "CreatedAt": ts, "LastLoginAt": &ts, "PasskeyCount": 2, "IsSelf": true, "CanDelete": false},
			{"ID": "u2", "DisplayName": "Bob", "IsAdmin": false, "CreatedAt": ts, "LastLoginAt": (*time.Time)(nil), "PasskeyCount": 1, "CanDelete": true},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "users.html", data); err != nil {
		t.Fatalf("failed to render users: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Alice", "Bob", "Last login Jun 10, 2026 14:30", "Never signed in"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered users missing %q", want)
		}
	}
}

func TestDevicesTemplateRenders(t *testing.T) {
	tmpl, err := template.New("").ParseFS(Templates, "*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	device := func(host, serial, fleet, state, cur, desired, attest, seen string) map[string]any {
		return map[string]any{
			"ID": host, "Hostname": host, "SerialNumber": serial, "FleetName": fleet,
			"UpdateState": state, "CurrentReleaseVersion": cur, "DesiredReleaseVersion": desired,
			"AttestationLevel": attest, "LastSeenAt": seen,
		}
	}

	data := map[string]any{
		"PageTitle":  "Devices",
		"IsDevices":  true,
		"csrf_token": "tok",
		"Devices": []map[string]any{
			device("edge-node-01", "SN-7741-AA", "Production", "healthy", "v2.4.1", "v2.4.1", "measured-boot", "2026-06-05 08:12"),
			device("edge-node-02", "SN-7741-AB", "Production", "downloading", "v2.4.0", "v2.4.1", "unknown", "2026-06-05 08:11"),
			device("warehouse-gw-1", "SN-9001-WG", "Warehouse", "failed", "v2.3.8", "v2.4.1", "tpm-backed", "2026-06-05 06:32"),
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "devices.html", data); err != nil {
		t.Fatalf("failed to render devices: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"+ Pair Device", "pair-code-input", "Device Inventory", "3 devices",
		"device-card device-tone-ok", "device-card device-tone-warn", "device-card device-tone-err",
		"edge-node-01", "SN-7741-AA", "status-badge status-downloading",
		"ver-from", "ver-to", "device-attest-warn",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered devices missing %q", want)
		}
	}

	if strings.Contains(out, "contacts-list") {
		t.Errorf("rendered devices still contains the old inventory table")
	}
}

func TestDashboardTemplateRenders(t *testing.T) {
	tmpl, err := template.New("").ParseFS(Templates, "*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	data := map[string]any{
		"PageTitle":       "Dashboard",
		"IsDashboard":     true,
		"IsAdmin":         true,
		"UserDisplayName": "Humaid Alqasimi",
		"csrf_token":      "tok",
		"HealthPct":       95,
		"Counts": map[string]any{
			"Fleets": 4, "Profiles": 7, "Builds": 23, "Releases": 9,
			"Devices": 128, "HealthyDevices": 121, "Rollouts": 5,
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "dashboard.html", data); err != nil {
		t.Fatalf("failed to render dashboard: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"Fleet Control Plane", "Welcome back, Humaid Alqasimi",
		"Fleet Health", "95%", "121 of 128 healthy",
		"23 builds · 9 releases", "Manage", "Account &amp; Administration",
		`href="/users"`, "fa-users",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered dashboard missing %q", want)
		}
	}

	for _, unwanted := range []string{"Guided Deployment", "stats-compact", "welcome-container"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered dashboard still contains removed element %q", unwanted)
		}
	}
}

func TestProfileDeploymentsTemplateRenders(t *testing.T) {
	tmpl, err := template.New("").ParseFS(Templates, "*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	build := func(id, version, status string) map[string]any {
		return map[string]any{
			"ID": id, "Version": version, "FleetID": "f1", "FleetName": "fleet-a",
			"ProfileRevision": 3, "Status": status, "InstallerStatus": "not_requested",
			"CreatedAt": "2026-06-17 00:00:00",
		}
	}
	release := func(id, version, status string) map[string]any {
		return map[string]any{
			"ID": id, "Version": version, "Channel": "stable", "Status": status,
			"PublishedAt": "2026-06-17 00:00:00", "FleetID": "f1", "FleetName": "fleet-a",
		}
	}
	rollout := map[string]any{
		"ID": "ro1", "FleetName": "fleet-a", "Strategy": "all-at-once",
		"StagePercent": 100, "Status": "completed", "StartedAt": "2026-06-17 00:00:00",
	}

	data := map[string]any{
		"Profile":          map[string]any{"ID": "p1", "Name": "demo", "LatestRevision": 3, "ConfigHash": "abc", "CreatedAt": "2026-06-17"},
		"CanManageProfile": true,
		"csrf_token":       "tok",
		"DeploymentFleets": []map[string]any{{"ID": "f1", "Name": "fleet-a"}},
		"HasDeployments":   true,
		"DeploymentChains": []map[string]any{
			// Fully shipped: build -> release -> rollout.
			{"Build": build("b1", "v1.0.0", "succeeded"), "Releases": []map[string]any{
				{"Release": release("r1", "v1.0.1", "active"), "Rollouts": []map[string]any{rollout}},
			}},
			// Released but not rolled out (shows Roll out button).
			{"Build": build("b2", "v1.1.0", "succeeded"), "Releases": []map[string]any{
				{"Release": release("r2", "v1.1.0", "active"), "Rollouts": []map[string]any{}},
			}},
			// Succeeded build, no release yet (shows Create release form).
			{"Build": build("b3", "v1.2.0", "succeeded"), "Releases": []map[string]any{}},
			// Build still running, no release possible yet.
			{"Build": build("b4", "v1.3.0", "running"), "Releases": []map[string]any{}},
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "profile_deployments.html", data); err != nil {
		t.Fatalf("failed to render profile_deployments: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"Build v1.0.0", "Release v1.0.1", "Roll out", "+ Create release", "Available once the build succeeds", "+ New deployment"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q", want)
		}
	}
}

func TestProfileViewTemplateRenders(t *testing.T) {
	tmpl, err := template.New("").ParseFS(Templates, "*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}

	data := map[string]any{
		"Profile": map[string]any{
			"ID": "p1", "Name": "edge-kiosk", "LatestRevision": 12,
			"ConfigHash": "a1b2c3d4", "CreatedAt": "2026-06-17 00:00:00",
			"Description": "Locked-down kiosk image.",
		},
		"CanManageProfile":       true,
		"csrf_token":             "tok",
		"PackageCount":           3,
		"SecuritySummary":        "Hardened baseline",
		"KernelSummary":          "Default from pinned nixpkgs",
		"OpenClawMicroVMSummary": "Disabled",
		"HasRawNix":              false,
		"ProfileNavActive":       "summary",
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "profile_view.html", data); err != nil {
		t.Fatalf("failed to render profile_view: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"prof-hero", "prof-hero-title", "edge-kiosk", "Locked-down kiosk image.",
		"prof-meta", "prof-rev", "r12", "a1b2c3d4",
		"prof-tabs", "prof-tab-active", "fa-rocket",
		"prof-config-grid", "3 packages configured", "AI Wizard",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered profile_view missing %q", want)
		}
	}

	// Summary tab no longer carries the old page header or the Selected Packages table.
	for _, unwanted := range []string{"Profile Summary", "Selected Packages", "Configuration Overview", "profile-section-link"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered profile_view still contains removed element %q", unwanted)
		}
	}
}
