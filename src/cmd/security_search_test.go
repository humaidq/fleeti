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
