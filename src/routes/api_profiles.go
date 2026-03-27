/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/db"
)

const maxAPIProfileMutationBodyBytes = 1024 * 1024

type apiProfile struct {
	ID             string   `json:"id"`
	FleetID        string   `json:"fleet_id,omitempty"`
	FleetIDs       []string `json:"fleet_ids"`
	FleetName      string   `json:"fleet_name,omitempty"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	LatestRevision int      `json:"latest_revision"`
	ConfigHash     string   `json:"config_hash,omitempty"`
	CreatedAt      string   `json:"created_at"`
}

type apiProfilesResponse struct {
	Profiles []apiProfile `json:"profiles"`
}

type apiProfileDetailResponse struct {
	Profile apiProfileDetail `json:"profile"`
}

type apiProfileMutationResponse struct {
	Profile            apiProfileDetail `json:"profile"`
	CreatedNewRevision bool             `json:"created_new_revision"`
}

type apiProfileDetail struct {
	ID                     string                 `json:"id"`
	FleetID                string                 `json:"fleet_id,omitempty"`
	FleetIDs               []string               `json:"fleet_ids"`
	FleetName              string                 `json:"fleet_name,omitempty"`
	Name                   string                 `json:"name"`
	Description            string                 `json:"description,omitempty"`
	LatestRevision         int                    `json:"latest_revision"`
	ConfigHash             string                 `json:"config_hash,omitempty"`
	ConfigSchemaVersion    int                    `json:"config_schema_version"`
	CreatedAt              string                 `json:"created_at"`
	Config                 map[string]any         `json:"config"`
	Packages               []string               `json:"packages"`
	Kernel                 apiProfileKernelConfig `json:"kernel"`
	OpenClawMicroVMEnabled bool                   `json:"openclaw_microvm_enabled"`
	RawNix                 string                 `json:"raw_nix,omitempty"`
}

type apiProfileKernelConfig struct {
	Attr           string                         `json:"attr,omitempty"`
	SourceOverride apiProfileKernelSourceOverride `json:"source_override"`
}

type apiProfileKernelSourceOverride struct {
	Enabled bool                    `json:"enabled"`
	URL     string                  `json:"url,omitempty"`
	Ref     string                  `json:"ref,omitempty"`
	Rev     string                  `json:"rev,omitempty"`
	Patches []apiProfileKernelPatch `json:"patches"`
}

type apiProfileKernelPatch struct {
	Name          string `json:"name"`
	SHA256        string `json:"sha256"`
	ContentBase64 string `json:"content_base64"`
}

type apiProfileMutationRequest struct {
	Config                 map[string]any               `json:"config"`
	Packages               *[]string                    `json:"packages"`
	Kernel                 *apiProfileKernelConfigInput `json:"kernel"`
	OpenClawMicroVMEnabled *bool                        `json:"openclaw_microvm_enabled"`
	RawNix                 *string                      `json:"raw_nix"`
	ConfigSchemaVersion    *int                         `json:"config_schema_version"`
}

type apiProfileKernelConfigInput struct {
	Attr           string                               `json:"attr"`
	SourceOverride *apiProfileKernelSourceOverrideInput `json:"source_override"`
}

type apiProfileKernelSourceOverrideInput struct {
	Enabled bool                         `json:"enabled"`
	URL     string                       `json:"url"`
	Ref     string                       `json:"ref"`
	Rev     string                       `json:"rev"`
	Patches []apiProfileKernelPatchInput `json:"patches"`
}

type apiProfileKernelPatchInput struct {
	Name          string `json:"name"`
	SHA256        string `json:"sha256"`
	ContentBase64 string `json:"content_base64"`
}

type apiRequestError struct {
	message string
}

func (e *apiRequestError) Error() string {
	return e.message
}

// APIProfiles returns the list of profiles visible to the authenticated API user.
func APIProfiles(c flamego.Context, user *db.User) {
	profiles, err := db.ListProfilesForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list api profiles", "user_id", user.ID.String(), "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load profiles")

		return
	}

	response := apiProfilesResponse{Profiles: make([]apiProfile, 0, len(profiles))}
	for _, profile := range profiles {
		response.Profiles = append(response.Profiles, apiProfile{
			ID:             profile.ID,
			FleetID:        profile.FleetID,
			FleetIDs:       profile.FleetIDs,
			FleetName:      profile.FleetName,
			Name:           profile.Name,
			Description:    profile.Description,
			LatestRevision: profile.LatestRevision,
			ConfigHash:     profile.ConfigHash,
			CreatedAt:      profile.CreatedAt,
		})
	}

	writeJSON(c, response)
}

// APIProfile returns the latest configuration for a specific visible profile.
func APIProfile(c flamego.Context, user *db.User) {
	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	profile, _, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrProfileNotFound), errors.Is(err, db.ErrAccessDenied):
			writeJSONError(c, http.StatusNotFound, "Profile not found")
		default:
			logger.Error("failed to load api profile", "profile_id", profileID, "user_id", user.ID.String(), "error", err)
			writeJSONError(c, http.StatusInternalServerError, "Failed to load profile")
		}

		return
	}

	detail, err := newAPIProfileDetail(profile)
	if err != nil {
		logger.Error("failed to encode api profile detail", "profile_id", profileID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load profile")

		return
	}

	writeJSON(c, apiProfileDetailResponse{Profile: detail})
}

// APIReplaceProfile replaces the stored profile config JSON and optional revision metadata.
func APIReplaceProfile(c flamego.Context, user *db.User) {
	handleAPIProfileMutation(c, user, true)
}

// APIPatchProfile applies partial profile configuration updates.
func APIPatchProfile(c flamego.Context, user *db.User) {
	handleAPIProfileMutation(c, user, false)
}

func newAPIProfileDetail(profile db.ProfileEdit) (apiProfileDetail, error) {
	config, err := parseProfileConfig(profile.ConfigJSON)
	if err != nil {
		return apiProfileDetail{}, err
	}

	packages, err := packagesFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		return apiProfileDetail{}, err
	}

	kernelConfig, err := profileKernelConfigFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		return apiProfileDetail{}, err
	}

	openclawMicroVMEnabled, err := openclawMicrovmEnabledFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		return apiProfileDetail{}, err
	}

	normalizedConfig, err := normalizeAPIProfileConfig(config)
	if err != nil {
		return apiProfileDetail{}, err
	}

	return apiProfileDetail{
		ID:                     profile.ID,
		FleetID:                profile.FleetID,
		FleetIDs:               append([]string(nil), profile.FleetIDs...),
		FleetName:              profile.FleetName,
		Name:                   profile.Name,
		Description:            profile.Description,
		LatestRevision:         profile.LatestRevision,
		ConfigHash:             profile.ConfigHash,
		ConfigSchemaVersion:    profile.ConfigSchemaVersion,
		CreatedAt:              profile.CreatedAt,
		Config:                 normalizedConfig,
		Packages:               packages,
		Kernel:                 newAPIProfileKernelConfig(kernelConfig),
		OpenClawMicroVMEnabled: openclawMicroVMEnabled,
		RawNix:                 strings.TrimSpace(profile.RawNix),
	}, nil
}

func newAPIProfileKernelConfig(config ProfileKernelConfig) apiProfileKernelConfig {
	patches := make([]apiProfileKernelPatch, 0, len(config.SourceOverride.Patches))
	for _, patch := range config.SourceOverride.Patches {
		patches = append(patches, apiProfileKernelPatch{
			Name:          patch.Name,
			SHA256:        patch.SHA256,
			ContentBase64: patch.ContentBase64,
		})
	}

	return apiProfileKernelConfig{
		Attr: config.Attr,
		SourceOverride: apiProfileKernelSourceOverride{
			Enabled: config.SourceOverride.Enabled,
			URL:     config.SourceOverride.URL,
			Ref:     config.SourceOverride.Ref,
			Rev:     config.SourceOverride.Rev,
			Patches: patches,
		},
	}
}

func normalizeAPIProfileConfig(config map[string]any) (map[string]any, error) {
	encoded, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}

	var normalized map[string]any
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}

	return normalized, nil
}

func handleAPIProfileMutation(c flamego.Context, user *db.User, replace bool) {
	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		switch {
		case errors.Is(err, db.ErrProfileNotFound), errors.Is(err, db.ErrAccessDenied):
			writeJSONError(c, http.StatusNotFound, "Profile not found")
		default:
			logger.Error("failed to load api profile for mutation", "profile_id", profileID, "user_id", user.ID.String(), "error", err)
			writeJSONError(c, http.StatusInternalServerError, "Failed to load profile")
		}

		return
	}

	if !canManage {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	request, fieldSet, err := decodeAPIProfileMutationRequest(c.Request(), replace)
	if err != nil {
		writeAPIProfileMutationError(c, err)

		return
	}

	var input db.CreateProfileInput
	if replace {
		input, err = buildAPIProfileReplaceInput(c.Request().Context(), profile, request, fieldSet)
	} else {
		input, err = buildAPIProfilePatchInput(c.Request().Context(), profile, request, fieldSet)
	}
	if err != nil {
		writeAPIProfileMutationError(c, err)

		return
	}

	createdNewRevision, err := db.UpdateProfile(c.Request().Context(), profileID, input)
	if err != nil {
		writeAPIProfileMutationError(c, err)

		return
	}

	updatedProfile, err := db.GetProfileForEdit(c.Request().Context(), profileID)
	if err != nil {
		logger.Error("failed to reload profile after api mutation", "profile_id", profileID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to reload profile")

		return
	}

	detail, err := newAPIProfileDetail(updatedProfile)
	if err != nil {
		logger.Error("failed to encode updated api profile detail", "profile_id", profileID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to load profile")

		return
	}

	writeJSON(c, apiProfileMutationResponse{
		Profile:            detail,
		CreatedNewRevision: createdNewRevision,
	})
}

func decodeAPIProfileMutationRequest(r *flamego.Request, requireConfig bool) (apiProfileMutationRequest, map[string]struct{}, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body().ReadCloser(), maxAPIProfileMutationBodyBytes+1))
	if err != nil {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Failed to read request body"}
	}

	if len(body) == 0 {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Request body is required"}
	}

	if len(body) > maxAPIProfileMutationBodyBytes {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Request body is too large"}
	}

	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err != nil {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Request body must be a JSON object"}
	}

	fieldSet := make(map[string]struct{}, len(rawFields))
	for key := range rawFields {
		fieldSet[key] = struct{}{}
	}

	if len(fieldSet) == 0 {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Request body must include at least one field"}
	}

	var request apiProfileMutationRequest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Request body contains invalid fields or values"}
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Request body must contain a single JSON object"}
	}

	if requireConfig {
		if _, ok := fieldSet["config"]; !ok {
			return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Config is required"}
		}

		if request.Config == nil {
			return apiProfileMutationRequest{}, nil, &apiRequestError{message: "Config must be a JSON object"}
		}
	}

	return request, fieldSet, nil
}

func buildAPIProfileReplaceInput(ctx context.Context, profile db.ProfileEdit, request apiProfileMutationRequest, fieldSet map[string]struct{}) (db.CreateProfileInput, error) {
	configJSON, err := json.Marshal(request.Config)
	if err != nil {
		return db.CreateProfileInput{}, &apiRequestError{message: "Config must be valid JSON"}
	}

	updatedConfigJSON, err := applyAPIProfileMutationFields(string(configJSON), request, fieldSet)
	if err != nil {
		return db.CreateProfileInput{}, err
	}

	return buildAPIProfileMutationInput(ctx, profile, updatedConfigJSON, request)
}

func buildAPIProfilePatchInput(ctx context.Context, profile db.ProfileEdit, request apiProfileMutationRequest, fieldSet map[string]struct{}) (db.CreateProfileInput, error) {
	currentConfig, err := parseProfileConfig(profile.ConfigJSON)
	if err != nil {
		return db.CreateProfileInput{}, err
	}

	if _, ok := fieldSet["config"]; ok {
		if request.Config == nil {
			return db.CreateProfileInput{}, &apiRequestError{message: "Config must be a JSON object"}
		}

		mergeAPIProfileConfig(currentConfig, request.Config)
	}

	configJSON, err := json.Marshal(currentConfig)
	if err != nil {
		return db.CreateProfileInput{}, &apiRequestError{message: "Config must be valid JSON"}
	}

	updatedConfigJSON, err := applyAPIProfileMutationFields(string(configJSON), request, fieldSet)
	if err != nil {
		return db.CreateProfileInput{}, err
	}

	return buildAPIProfileMutationInput(ctx, profile, updatedConfigJSON, request)
}

func applyAPIProfileMutationFields(configJSON string, request apiProfileMutationRequest, fieldSet map[string]struct{}) (string, error) {
	updatedConfigJSON := configJSON
	var err error

	if request.Packages != nil {
		updatedConfigJSON, err = profileConfigWithPackages(updatedConfigJSON, *request.Packages)
		if err != nil {
			return "", err
		}
	}

	if request.Kernel != nil {
		updatedConfigJSON, err = profileConfigWithKernel(updatedConfigJSON, request.Kernel.toProfileKernelConfig())
		if err != nil {
			return "", err
		}
	}

	if request.OpenClawMicroVMEnabled != nil {
		updatedConfigJSON, err = profileConfigWithOpenclawMicrovmEnabled(updatedConfigJSON, *request.OpenClawMicroVMEnabled)
		if err != nil {
			return "", err
		}
	}

	if _, ok := fieldSet["config"]; ok || request.Packages != nil || request.Kernel != nil || request.OpenClawMicroVMEnabled != nil {
		if err := validateAPIProfileConfig(updatedConfigJSON); err != nil {
			return "", err
		}
	}

	return updatedConfigJSON, nil
}

func buildAPIProfileMutationInput(ctx context.Context, profile db.ProfileEdit, configJSON string, request apiProfileMutationRequest) (db.CreateProfileInput, error) {
	if err := validateAPIProfileKernelSelection(ctx, configJSON); err != nil {
		return db.CreateProfileInput{}, err
	}

	rawNix := profile.RawNix
	if request.RawNix != nil {
		rawNix = *request.RawNix
	}

	configSchemaVersion := profile.ConfigSchemaVersion
	if request.ConfigSchemaVersion != nil {
		configSchemaVersion = *request.ConfigSchemaVersion
	}

	return db.CreateProfileInput{
		FleetIDs:            append([]string(nil), profile.FleetIDs...),
		Name:                profile.Name,
		Description:         profile.Description,
		ConfigJSON:          configJSON,
		RawNix:              rawNix,
		ConfigSchemaVersion: configSchemaVersion,
	}, nil
}

func validateAPIProfileConfig(configJSON string) error {
	if _, err := parseProfileConfig(configJSON); err != nil {
		return &apiRequestError{message: mutationErrorMessage(err)}
	}

	if _, err := packagesFromProfileConfig(configJSON); err != nil {
		return &apiRequestError{message: mutationErrorMessage(err)}
	}

	if _, err := profileKernelConfigFromProfileConfig(configJSON); err != nil {
		return &apiRequestError{message: mutationErrorMessage(err)}
	}

	if _, err := openclawMicrovmEnabledFromProfileConfig(configJSON); err != nil {
		return &apiRequestError{message: mutationErrorMessage(err)}
	}

	if _, err := profileSecurityConfigFromProfileConfig(configJSON); err != nil {
		message := mutationErrorMessage(err)
		if message == "Operation failed" {
			message = err.Error()
		}

		return &apiRequestError{message: message}
	}

	return nil
}

func validateAPIProfileKernelSelection(ctx context.Context, configJSON string) error {
	kernelConfig, err := profileKernelConfigFromProfileConfig(configJSON)
	if err != nil {
		return &apiRequestError{message: mutationErrorMessage(err)}
	}

	allowedAttrs := map[string]struct{}{}
	if kernelConfig.Attr != "" {
		kernelOptions, err := listAvailableKernelOptions(ctx)
		if err != nil {
			return fmt.Errorf("failed to load available kernels: %w", err)
		}

		allowedAttrs = kernelOptionSet(kernelOptions)
	}

	if err := validateProfileKernelConfig(kernelConfig, allowedAttrs); err != nil {
		return &apiRequestError{message: err.Error()}
	}

	return nil
}

func mergeAPIProfileConfig(target map[string]any, patch map[string]any) {
	for key, value := range patch {
		target[key] = value
	}
}

func writeAPIProfileMutationError(c flamego.Context, err error) {
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
	case errors.Is(err, db.ErrInvalidProfileConfigJSON),
		errors.Is(err, db.ErrProfileConfigMustBeObject),
		errors.Is(err, db.ErrInvalidConfigSchemaVersion):
		writeJSONError(c, http.StatusBadRequest, mutationErrorMessage(err))
	default:
		logger.Error("api profile mutation failed", "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to update profile")
	}
}

func (input apiProfileKernelConfigInput) toProfileKernelConfig() ProfileKernelConfig {
	config := ProfileKernelConfig{Attr: strings.TrimSpace(input.Attr)}
	if input.SourceOverride == nil {
		return normalizeProfileKernelConfig(config)
	}

	patches := make([]ProfileKernelPatch, 0, len(input.SourceOverride.Patches))
	for _, patch := range input.SourceOverride.Patches {
		patches = append(patches, ProfileKernelPatch{
			Name:          patch.Name,
			SHA256:        patch.SHA256,
			ContentBase64: patch.ContentBase64,
		})
	}

	config.SourceOverride = ProfileKernelSourceOverride{
		Enabled: input.SourceOverride.Enabled,
		URL:     strings.TrimSpace(input.SourceOverride.URL),
		Ref:     strings.TrimSpace(input.SourceOverride.Ref),
		Rev:     strings.TrimSpace(input.SourceOverride.Rev),
		Patches: patches,
	}

	return normalizeProfileKernelConfig(config)
}
