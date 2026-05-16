/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package cmd

import (
	"testing"
	"time"

	"github.com/humaidq/fleeti/v2/routes"
)

func TestSlugifyArtifactSegment(t *testing.T) {
	t.Parallel()

	if got := slugifyArtifactSegment("  Web Server / SSH + HTTPS  "); got != "web-server-ssh-https" {
		t.Fatalf("unexpected slug: %q", got)
	}
}

func TestBuildSecuritySearchRunArtifactMarksPartialRun(t *testing.T) {
	t.Parallel()

	startedAt := time.Date(2026, time.April, 27, 12, 0, 0, 0, time.UTC)
	artifact := buildSecuritySearchRunArtifact(
		startedAt,
		1500*time.Millisecond,
		routes.ProfileSecuritySearchBaseProfile{ID: "profile-a", Name: "Web Profile", Revision: 7, ConfigSchemaVersion: 1},
		routes.DefaultProfileSecuritySearchRunOptions("web server"),
		routes.ProfileSecuritySearchRunResult{
			Goal:                 "web server",
			TargetCandidateCount: 24,
			UniqueCandidateCount: 12,
		},
		nil,
		"pilot",
	)

	if artifact.Status != "partial" {
		t.Fatalf("expected partial status, got %q", artifact.Status)
	}

	if artifact.RunID != "20260427T120000Z-pilot" {
		t.Fatalf("unexpected run id: %q", artifact.RunID)
	}
	if artifact.DurationMS != 1500 {
		t.Fatalf("unexpected duration: %d", artifact.DurationMS)
	}
}

func TestNextSecuritySearchProgressMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		lastPrinted    string
		progress       string
		expectedOutput string
	}{
		{name: "preparing milestone", progress: "Preparing security search", expectedOutput: "Preparing security search"},
		{name: "generation milestone", progress: "Generating candidate batch 1 of 3", expectedOutput: "Generating candidate batch 1 of 3"},
		{name: "evaluation milestone", progress: "Evaluated 8 of 24 unique candidates", expectedOutput: "Evaluated 8 of 24 unique candidates"},
		{name: "skip duplicate noise", progress: "Skipped duplicate candidate in batch 1", expectedOutput: ""},
		{name: "skip repeated milestone", lastPrinted: "Preparing security search", progress: "Preparing security search", expectedOutput: ""},
		{name: "skip empty message", progress: "   ", expectedOutput: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := nextSecuritySearchProgressMessage(tt.lastPrinted, tt.progress); got != tt.expectedOutput {
				t.Fatalf("unexpected output: got %q want %q", got, tt.expectedOutput)
			}
		})
	}
}
