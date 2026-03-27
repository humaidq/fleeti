/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/db"
)

const maxAPIBuildCreateBodyBytes = 64 * 1024

type apiBuild struct {
	ID                string `json:"id"`
	ProfileID         string `json:"profile_id"`
	ProfileName       string `json:"profile_name,omitempty"`
	FleetID           string `json:"fleet_id,omitempty"`
	FleetName         string `json:"fleet_name,omitempty"`
	ProfileRevisionID string `json:"profile_revision_id"`
	ProfileRevision   int    `json:"profile_revision"`
	Version           string `json:"version"`
	Status            string `json:"status"`
	Artifact          string `json:"artifact,omitempty"`
	InstallerStatus   string `json:"installer_status"`
	InstallerArtifact string `json:"installer_artifact,omitempty"`
	CreatedAt         string `json:"created_at"`
}

type apiBuildsResponse struct {
	Builds []apiBuild `json:"builds"`
}

type apiBuildResponse struct {
	Build apiBuild `json:"build"`
}

type apiCreateBuildRequest struct {
	FleetID string `json:"fleet_id"`
	Version string `json:"version"`
}

// APIProfileBuild returns a specific build for a visible profile.
func APIProfileBuild(c flamego.Context, user *db.User) {
	var err error
	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	profile, _, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		writeAPIProfileLookupError(c, profileID, user.ID.String(), err)

		return
	}

	buildID := strings.TrimSpace(c.Param("buildId"))
	if buildID == "" {
		writeJSONError(c, http.StatusNotFound, "Build not found")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err != nil {
		writeAPIBuildLookupError(c, buildID, profile.ID, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		writeJSONError(c, http.StatusNotFound, "Build not found")

		return
	}

	writeJSON(c, apiBuildResponse{Build: newAPIBuild(build)})
}

// APIProfileBuildLogs returns incremental build log content for polling clients.
func APIProfileBuildLogs(c flamego.Context, user *db.User) {
	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	profile, _, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		writeAPIProfileLookupError(c, profileID, user.ID.String(), err)

		return
	}

	buildID := strings.TrimSpace(c.Param("buildId"))
	if buildID == "" {
		writeJSONError(c, http.StatusNotFound, "Build not found")

		return
	}

	afterID, err := parseAfterID(c.Request().URL.Query().Get("after"))
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "Invalid after value")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err != nil {
		writeAPIBuildLookupError(c, buildID, profile.ID, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		writeJSONError(c, http.StatusNotFound, "Build not found")

		return
	}

	chunks, err := db.ListBuildLogChunksSince(c.Request().Context(), buildID, afterID, buildLogBatchLimit+1)
	if err != nil {
		logger.Error("failed to list api build log chunks", "build_id", buildID, "after", afterID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load build logs")

		return
	}

	writeJSON(c, buildLogPayload(build.Status, afterID, chunks, isTerminalBuildStatus(build.Status)))
}

// APIProfileBuilds returns builds for a visible profile.
func APIProfileBuilds(c flamego.Context, user *db.User) {
	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	profile, _, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		writeAPIProfileLookupError(c, profileID, user.ID.String(), err)

		return
	}

	builds, err := db.ListBuilds(c.Request().Context())
	if err != nil {
		logger.Error("failed to list api builds", "profile_id", profileID, "user_id", user.ID.String(), "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load builds")

		return
	}

	filtered := filterBuildsByProfileID(builds, profile.ID)
	response := apiBuildsResponse{Builds: make([]apiBuild, 0, len(filtered))}
	for _, build := range filtered {
		response.Builds = append(response.Builds, newAPIBuild(build))
	}

	writeJSON(c, response)
}

// APICreateProfileBuild queues a new build for a manageable profile.
func APICreateProfileBuild(c flamego.Context, user *db.User) {
	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		writeAPIProfileLookupError(c, profileID, user.ID.String(), err)

		return
	}

	if !canManage {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	request, err := decodeAPICreateBuildRequest(c.Request())
	if err != nil {
		writeAPIBuildMutationError(c, err)

		return
	}

	if err := ensureUserCanManageFleetIDs(c.Request().Context(), user, []string{request.FleetID}); err != nil {
		writeAPIBuildMutationError(c, err)

		return
	}

	buildID, err := db.CreateBuild(c.Request().Context(), db.CreateBuildInput{
		ProfileID: profile.ID,
		FleetID:   request.FleetID,
		Version:   request.Version,
	})
	if err != nil {
		writeAPIBuildMutationError(c, err)

		return
	}

	queueBuildExecution(buildID, strings.TrimSpace(request.Version))

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err != nil {
		logger.Error("failed to reload created api build", "build_id", buildID, "profile_id", profile.ID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load build")

		return
	}

	writeJSONStatus(c, http.StatusCreated, apiBuildResponse{Build: newAPIBuild(build)})
}

func newAPIBuild(build db.Build) apiBuild {
	return apiBuild{
		ID:                build.ID,
		ProfileID:         build.ProfileID,
		ProfileName:       build.ProfileName,
		FleetID:           build.FleetID,
		FleetName:         build.FleetName,
		ProfileRevisionID: build.ProfileRevisionID,
		ProfileRevision:   build.ProfileRevision,
		Version:           build.Version,
		Status:            build.Status,
		Artifact:          build.Artifact,
		InstallerStatus:   build.InstallerStatus,
		InstallerArtifact: build.InstallerArtifact,
		CreatedAt:         build.CreatedAt,
	}
}

func decodeAPICreateBuildRequest(r *flamego.Request) (apiCreateBuildRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body().ReadCloser(), maxAPIBuildCreateBodyBytes+1))
	if err != nil {
		return apiCreateBuildRequest{}, &apiRequestError{message: "Failed to read request body"}
	}

	if len(body) == 0 {
		return apiCreateBuildRequest{}, &apiRequestError{message: "Request body is required"}
	}

	if len(body) > maxAPIBuildCreateBodyBytes {
		return apiCreateBuildRequest{}, &apiRequestError{message: "Request body is too large"}
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	var request apiCreateBuildRequest
	if err := decoder.Decode(&request); err != nil {
		return apiCreateBuildRequest{}, &apiRequestError{message: "Request body contains invalid fields or values"}
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return apiCreateBuildRequest{}, &apiRequestError{message: "Request body must contain a single JSON object"}
	}

	request.FleetID = strings.TrimSpace(request.FleetID)
	request.Version = strings.TrimSpace(request.Version)

	return request, nil
}

func writeAPIProfileLookupError(c flamego.Context, profileID string, userID string, err error) {
	switch {
	case errors.Is(err, db.ErrProfileNotFound), errors.Is(err, db.ErrAccessDenied):
		writeJSONError(c, http.StatusNotFound, "Profile not found")
	default:
		logger.Error("failed to load api profile", "profile_id", profileID, "user_id", userID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load profile")
	}
}

func writeAPIBuildMutationError(c flamego.Context, err error) {
	var requestErr *apiRequestError
	if errors.As(err, &requestErr) {
		writeJSONError(c, http.StatusBadRequest, requestErr.message)

		return
	}

	switch {
	case errors.Is(err, db.ErrProfileNotFound):
		writeJSONError(c, http.StatusNotFound, "Profile not found")
	case errors.Is(err, db.ErrAccessDenied):
		writeJSONError(c, http.StatusForbidden, "Access restricted")
	case errors.Is(err, db.ErrFleetNotFound),
		errors.Is(err, db.ErrFleetRequired),
		errors.Is(err, db.ErrVersionRequired),
		errors.Is(err, db.ErrVersionMustBeSemver),
		errors.Is(err, db.ErrProfileRequired),
		errors.Is(err, db.ErrProfileNotAssignedToFleet),
		errors.Is(err, db.ErrProfileHasNoRevisions),
		errors.Is(err, db.ErrBuildVersionAlreadyExists):
		writeJSONError(c, http.StatusBadRequest, mutationErrorMessage(err))
	default:
		logger.Error("api build mutation failed", "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to create build")
	}
}

func writeAPIBuildLookupError(c flamego.Context, buildID string, profileID string, err error) {
	switch {
	case errors.Is(err, db.ErrBuildNotFound):
		writeJSONError(c, http.StatusNotFound, "Build not found")
	default:
		logger.Error("failed to load api build", "build_id", buildID, "profile_id", profileID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load build")
	}
}

func writeJSONStatus(c flamego.Context, status int, payload any) {
	c.ResponseWriter().Header().Set("Content-Type", "application/json")
	c.ResponseWriter().WriteHeader(status)

	if err := json.NewEncoder(c.ResponseWriter()).Encode(payload); err != nil {
		logger.Error("error encoding api response", "error", err)
	}
}
