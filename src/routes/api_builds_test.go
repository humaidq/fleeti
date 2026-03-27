/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/db"
)

func TestNewAPIBuildMapsDatabaseBuild(t *testing.T) {
	t.Parallel()

	build := newAPIBuild(db.Build{
		ID:                "build-1",
		ProfileID:         "profile-1",
		ProfileName:       "Production Base",
		FleetID:           "fleet-1",
		FleetName:         "Primary Fleet",
		ProfileRevisionID: "revision-7",
		ProfileRevision:   7,
		Version:           "v1.2.3",
		Status:            db.BuildStatusQueued,
		Artifact:          "updates/build-1",
		InstallerStatus:   "not_requested",
		InstallerArtifact: "",
		CreatedAt:         "2026-03-20 09:00:00",
	})

	if build.ID != "build-1" || build.ProfileID != "profile-1" || build.FleetID != "fleet-1" {
		t.Fatalf("expected ids to be preserved, got %#v", build)
	}

	if build.ProfileRevision != 7 || build.Version != "v1.2.3" || build.Status != db.BuildStatusQueued {
		t.Fatalf("expected build metadata to be preserved, got %#v", build)
	}

	if build.InstallerStatus != "not_requested" || build.CreatedAt != "2026-03-20 09:00:00" {
		t.Fatalf("expected installer status and created time to be preserved, got %#v", build)
	}
}

func TestDecodeAPICreateBuildRequestParsesValidJSON(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("POST", "/api/v1/profiles/profile-1/builds", strings.NewReader(`{"fleet_id":" fleet-1 ","version":" v1.2.3 "}`))
	decoded, err := decodeAPICreateBuildRequest(&flamego.Request{Request: req})
	if err != nil {
		t.Fatalf("decodeAPICreateBuildRequest returned error: %v", err)
	}

	if decoded.FleetID != "fleet-1" || decoded.Version != "v1.2.3" {
		t.Fatalf("expected trimmed request values, got %#v", decoded)
	}
}

func TestDecodeAPICreateBuildRequestRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest("POST", "/api/v1/profiles/profile-1/builds", strings.NewReader(`{"fleet_id":"fleet-1","version":"v1.2.3","extra":true}`))
	_, err := decodeAPICreateBuildRequest(&flamego.Request{Request: req})

	var requestErr *apiRequestError
	if !errors.As(err, &requestErr) {
		t.Fatalf("expected apiRequestError, got %v", err)
	}

	if requestErr.message != "Request body contains invalid fields or values" {
		t.Fatalf("unexpected request error message: %q", requestErr.message)
	}
}
