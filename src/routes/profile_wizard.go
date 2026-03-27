/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	profileWizardStateSessionKey = "profile_wizard_state"
	profileWizardModeCreate      = "create"
	profileWizardModeAdapt       = "adapt"

	defaultProfileWizardConfigJSON = `{"packages":[]}`
	profileWizardConversationLimit = 24
	maxProfileWizardMessageBytes   = 16 * 1024
)

type profileWizardState struct {
	Mode          string                           `json:"mode"`
	BaseProfileID string                           `json:"base_profile_id,omitempty"`
	OriginalDraft profileWizardDraft               `json:"original_draft"`
	Draft         profileWizardDraft               `json:"draft"`
	Conversation  []profileWizardConversationEntry `json:"conversation"`
}

type profileWizardDraft struct {
	ProfileID            string   `json:"profile_id,omitempty"`
	Name                 string   `json:"name"`
	Description          string   `json:"description,omitempty"`
	FleetIDs             []string `json:"fleet_ids"`
	ConfigJSON           string   `json:"config_json"`
	RawNix               string   `json:"raw_nix,omitempty"`
	ConfigSchemaVersion  int      `json:"config_schema_version"`
	OpenWizardSuggestion string   `json:"-"`
}

type profileWizardConversationEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type profileWizardFleetOption struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type profileWizardKernelSummary struct {
	Attr                  string `json:"attr,omitempty"`
	Summary               string `json:"summary"`
	SourceOverrideEnabled bool   `json:"source_override_enabled"`
	PatchCount            int    `json:"patch_count"`
}

type profileWizardPlannedChange struct {
	Label  string `json:"label"`
	Detail string `json:"detail"`
}

type profileWizardDraftSummary struct {
	Mode                   string                       `json:"mode"`
	ProfileID              string                       `json:"profile_id,omitempty"`
	Name                   string                       `json:"name"`
	Description            string                       `json:"description,omitempty"`
	FleetIDs               []string                     `json:"fleet_ids"`
	FleetNames             []string                     `json:"fleet_names"`
	Packages               []string                     `json:"packages"`
	PackageCount           int                          `json:"package_count"`
	Kernel                 profileWizardKernelSummary   `json:"kernel"`
	OpenClawMicroVMEnabled bool                         `json:"openclaw_microvm_enabled"`
	OpenClawSummary        string                       `json:"openclaw_summary"`
	RawNix                 string                       `json:"raw_nix,omitempty"`
	HasRawNix              bool                         `json:"has_raw_nix"`
	ConfigSchemaVersion    int                          `json:"config_schema_version"`
	PlannedChanges         []profileWizardPlannedChange `json:"planned_changes"`
	PlannedChangeCount     int                          `json:"planned_change_count"`
}

type profileWizardValidation struct {
	CanApply bool     `json:"can_apply"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

type profileWizardResponse struct {
	Available       bool                             `json:"available"`
	DisabledReason  string                           `json:"disabled_reason,omitempty"`
	Mode            string                           `json:"mode"`
	ChatPath        string                           `json:"chat_path"`
	ApplyPath       string                           `json:"apply_path"`
	DiscardPath     string                           `json:"discard_path"`
	ResetPath       string                           `json:"reset_path"`
	ManualPath      string                           `json:"manual_path"`
	Conversation    []profileWizardConversationEntry `json:"conversation"`
	Draft           profileWizardDraftSummary        `json:"draft"`
	Validation      profileWizardValidation          `json:"validation"`
	AvailableFleets []profileWizardFleetOption       `json:"available_fleets"`
	Redirect        string                           `json:"redirect,omitempty"`
}

type profileWizardChatRequest struct {
	Message string `json:"message"`
}

type profileWizardApplyResponse struct {
	Redirect string `json:"redirect"`
}

// ProfileWizardPage renders the AI-assisted profile draft workflow.
func ProfileWizardPage(c flamego.Context, s session.Session, t template.Template, data template.Data, ai *ProfileWizardAI) {
	setPage(data, "AI Profile Wizard")
	data["IsProfiles"] = true

	ctx := c.Request().Context()
	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		redirectWithMessage(c, s, "/profiles", FlashError, "Access restricted")

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	profile, fleets, state, err := prepareProfileWizardState(ctx, s, user, profileID, strings.TrimSpace(c.Query("reset")) == "1")
	if err != nil {
		handleProfileWizardPageError(c, s, profileID, err)

		return
	}

	chatPath := profileWizardChatPath(profileID)
	applyPath := profileWizardApplyPath(profileID)
	discardPath := profileWizardDiscardPath(profileID)
	resetPath := profileWizardPagePath(profileID) + "?reset=1"
	manualPath := "/profiles/new"
	pageTitle := "Create Profile With AI"
	introText := "Describe the profile you want, and the wizard will keep a draft until you apply it."

	if profile.ID != "" {
		pageTitle = "Adapt Profile With AI"
		introText = "Describe the changes you want, and the wizard will update a draft of this profile before anything is saved."
		manualPath = profileViewPath(profile.ID)

		data["Profile"] = profile
		data["ProfileNavActive"] = "wizard"
		data["CanManageProfile"] = true
		setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "AI Wizard"))
	} else {
		setBreadcrumbs(data, []BreadcrumbItem{
			{Name: "Profiles", URL: "/profiles"},
			{Name: "AI Wizard", IsCurrent: true},
		})
	}

	response, err := newProfileWizardResponse(ctx, state, fleets, ai, chatPath, applyPath, discardPath, resetPath, manualPath)
	if err != nil {
		logger.Error("failed to build profile wizard response", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile wizard")
		response = profileWizardResponse{
			Available:      ai != nil && ai.Enabled(),
			DisabledReason: profileWizardDisabledReason(ai),
			Mode:           state.Mode,
			ChatPath:       chatPath,
			ApplyPath:      applyPath,
			DiscardPath:    discardPath,
			ResetPath:      resetPath,
			ManualPath:     manualPath,
			Conversation:   cloneProfileWizardConversation(state.Conversation),
		}
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		logger.Error("failed to encode profile wizard state", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile wizard")
		encoded = []byte(`{"available":false,"disabled_reason":"Failed to load profile wizard"}`)
	}

	data["ProfileWizardTitle"] = pageTitle
	data["ProfileWizardIntroText"] = introText
	data["ProfileWizardBackPath"] = manualPath
	data["ProfileWizardResetPath"] = resetPath
	data["ProfileWizardMode"] = state.Mode
	data["ProfileWizardAvailable"] = response.Available
	data["ProfileWizardDisabledReason"] = response.DisabledReason
	data["ProfileWizardStateJSON"] = string(encoded)

	t.HTML(http.StatusOK, "profile_wizard")
}

// ProfileWizardChat updates the in-session wizard draft through the configured LLM.
func ProfileWizardChat(c flamego.Context, s session.Session, ai *ProfileWizardAI) {
	ctx := c.Request().Context()
	w := c.ResponseWriter()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	sendEvent := func(event, data string) {
		if event != "" {
			if _, err := w.Write([]byte("event: " + event + "\n")); err != nil {
				logger.Error("failed to write profile wizard event", "event", event, "error", err)
				return
			}
		}

		escaped := strings.ReplaceAll(data, "\n", "\ndata: ")
		if _, err := w.Write([]byte("data: " + escaped + "\n\n")); err != nil {
			logger.Error("failed to write profile wizard event data", "event", event, "error", err)
			return
		}

		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}

	sendError := func(message string) {
		sendEvent("error", message)
	}

	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		sendError("Access restricted")

		return
	}

	if ai == nil || !ai.Enabled() {
		sendError(profileWizardDisabledReason(ai))

		return
	}

	request, err := decodeProfileWizardChatRequest(c.Request())
	if err != nil {
		sendError(err.Error())

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	profile, fleets, state, err := prepareProfileWizardState(ctx, s, user, profileID, false)
	if err != nil {
		sendError(mutationErrorMessage(err))

		return
	}

	aiInput := profileWizardAIInput{
		Mode:            state.Mode,
		BaseProfileID:   state.BaseProfileID,
		OriginalDraft:   state.OriginalDraft,
		Draft:           state.Draft,
		Conversation:    state.Conversation,
		UserMessage:     request.Message,
		AvailableFleets: fleets,
	}

	draft, guidance, err := ai.ResolveDraft(ctx, aiInput)
	if err != nil {
		logger.Error("profile wizard resolve failed", "profile_id", profileID, "error", err)
		sendError("AI wizard request failed")

		return
	}

	state.Draft = draft
	assistantMessage := ""
	streamedMessage, streamErr := ai.StreamReply(ctx, aiInput, draft, guidance, func(chunk string) error {
		assistantMessage += chunk
		sendEvent("chunk", chunk)

		return nil
	})
	if streamErr != nil {
		logger.Error("profile wizard reply stream failed", "profile_id", profileID, "error", streamErr)
		if strings.TrimSpace(assistantMessage) == "" {
			sendError("AI wizard request failed")

			return
		}

		logger.Warn("saving partial profile wizard reply after stream failure", "profile_id", profileID)
	} else {
		assistantMessage = streamedMessage
	}

	state.Conversation = appendProfileWizardConversation(state.Conversation,
		profileWizardConversationEntry{Role: "user", Content: strings.TrimSpace(request.Message)},
		profileWizardConversationEntry{Role: "assistant", Content: strings.TrimSpace(assistantMessage)},
	)
	setProfileWizardState(s, state)

	response, err := newProfileWizardResponse(ctx, state, fleets, ai, profileWizardChatPath(profile.ID), profileWizardApplyPath(profile.ID), profileWizardDiscardPath(profile.ID), profileWizardPagePath(profile.ID)+"?reset=1", profileWizardManualPath(profile.ID))
	if err != nil {
		logger.Error("failed to build profile wizard chat response", "profile_id", profileID, "error", err)
		sendError("Failed to update profile wizard")

		return
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		logger.Error("failed to encode profile wizard chat response", "profile_id", profileID, "error", err)
		sendError("Failed to update profile wizard")

		return
	}

	sendEvent("state", string(encoded))
	sendEvent("done", "")
}

// ProfileWizardApply persists the current session draft to the database.
func ProfileWizardApply(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()
	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	_, fleets, state, err := prepareProfileWizardState(ctx, s, user, profileID, false)
	if err != nil {
		writeProfileWizardMutationError(c, err)

		return
	}

	validation := validateProfileWizardDraft(ctx, state.Draft, fleets)
	if !validation.CanApply {
		message := "Draft is not ready to apply"
		if len(validation.Errors) > 0 {
			message = validation.Errors[0]
		}
		writeJSONError(c, http.StatusBadRequest, message)

		return
	}

	if err := ensureUserCanManageFleetIDs(ctx, user, state.Draft.FleetIDs); err != nil {
		writeProfileWizardMutationError(c, err)

		return
	}

	input := profileWizardCreateProfileInput(state.Draft)
	evaluation := evaluateProfileInputAgainstPinnedNix(ctx, input)
	if !evaluation.Valid {
		message := "Pinned Nix evaluation failed"
		if len(evaluation.Errors) > 0 {
			message = evaluation.Errors[0]
		}

		writeJSONError(c, http.StatusBadRequest, message)

		return
	}

	redirectPath := "/profiles"
	successMessage := "Profile created"

	if profileID == "" {
		if err := db.CreateProfile(ctx, input, user.ID.String()); err != nil {
			writeProfileWizardMutationError(c, err)

			return
		}

		if createdProfileID, ok := findCreatedProfileForWizardRedirect(ctx, user, state.Draft); ok {
			redirectPath = profileViewPath(createdProfileID)
		}
	} else {
		if _, err := db.UpdateProfile(ctx, profileID, input); err != nil {
			writeProfileWizardMutationError(c, err)

			return
		}

		redirectPath = profileViewPath(profileID)
		successMessage = "Profile updated"
	}

	clearProfileWizardState(s)
	SetSuccessFlash(s, successMessage)
	writeJSON(c, profileWizardApplyResponse{Redirect: redirectPath})
}

// ProfileWizardDiscard clears the wizard draft and redirects away from the wizard.
func ProfileWizardDiscard(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()
	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	_, _, _, err = prepareProfileWizardState(ctx, s, user, profileID, false)
	if err != nil {
		writeProfileWizardMutationError(c, err)

		return
	}

	clearProfileWizardState(s)
	writeJSON(c, profileWizardApplyResponse{Redirect: profileWizardManualPath(profileID)})
}

func prepareProfileWizardState(ctx context.Context, s session.Session, user *db.User, profileID string, forceReset bool) (db.ProfileEdit, []db.Fleet, profileWizardState, error) {
	if user == nil {
		return db.ProfileEdit{}, nil, profileWizardState{}, db.ErrAccessDenied
	}

	fleets, err := db.ListFleetsForUser(ctx, user.ID.String(), user.IsAdmin)
	if err != nil {
		return db.ProfileEdit{}, nil, profileWizardState{}, err
	}

	manageableFleets := manageableFleetsForUser(user, fleets)
	profileID = strings.TrimSpace(profileID)
	mode := profileWizardModeCreate
	profile := db.ProfileEdit{}

	if profileID != "" {
		mode = profileWizardModeAdapt

		loadedProfile, canManage, err := resolveProfileAccessContext(ctx, user, profileID)
		if err != nil {
			return db.ProfileEdit{}, manageableFleets, profileWizardState{}, err
		}

		if !canManage {
			return db.ProfileEdit{}, manageableFleets, profileWizardState{}, db.ErrAccessDenied
		}

		profile = loadedProfile
	}

	state, ok := getProfileWizardState(s)
	if forceReset || !ok || profileWizardStateMismatch(state, mode, profile.ID) {
		state = newProfileWizardState(mode, profile)
		setProfileWizardState(s, state)
	} else if isZeroProfileWizardDraft(state.OriginalDraft) {
		state.OriginalDraft = newProfileWizardState(mode, profile).OriginalDraft
		setProfileWizardState(s, state)
	}

	return profile, manageableFleets, state, nil
}

func newProfileWizardState(mode string, profile db.ProfileEdit) profileWizardState {
	draft := profileWizardDraft{
		Name:                strings.TrimSpace(profile.Name),
		Description:         strings.TrimSpace(profile.Description),
		FleetIDs:            append([]string(nil), profile.FleetIDs...),
		ConfigJSON:          strings.TrimSpace(profile.ConfigJSON),
		RawNix:              strings.TrimSpace(profile.RawNix),
		ConfigSchemaVersion: profile.ConfigSchemaVersion,
	}

	if strings.TrimSpace(draft.ConfigJSON) == "" {
		draft.ConfigJSON = defaultProfileWizardConfigJSON
	}

	if draft.ConfigSchemaVersion <= 0 {
		draft.ConfigSchemaVersion = 1
	}

	state := profileWizardState{
		Mode:          mode,
		OriginalDraft: draft,
		Conversation:  []profileWizardConversationEntry{{Role: "assistant", Content: profileWizardIntroMessage(mode, profile.Name)}},
		Draft:         draft,
	}

	if strings.TrimSpace(profile.ID) != "" {
		state.BaseProfileID = profile.ID
		state.Draft.ProfileID = profile.ID
	}

	return state
}

func profileWizardIntroMessage(mode, profileName string) string {
	if strings.TrimSpace(mode) == profileWizardModeAdapt {
		name := strings.TrimSpace(profileName)
		if name == "" {
			name = "this profile"
		}

		return fmt.Sprintf("I loaded %s as a draft. I can help you change packages, fleets, kernel settings, OpenClaw, and raw Nix. I only update this draft during chat and nothing is saved until you press Apply. If you want to start over, just tell me to reset the draft. What would you like to change?", name)
	}

	return "Welcome - I can help you build a profile by chat. Tell me about fleets, packages, kernel choices, OpenClaw, or raw Nix requirements, and I will keep everything as a draft until you press Apply. If you want to start over at any point, just tell me to reset the draft. What kind of profile would you like to create?"
}

func profileWizardStateMismatch(state profileWizardState, mode, profileID string) bool {
	if strings.TrimSpace(state.Mode) != strings.TrimSpace(mode) {
		return true
	}

	return strings.TrimSpace(state.BaseProfileID) != strings.TrimSpace(profileID)
}

func getProfileWizardState(s session.Session) (profileWizardState, bool) {
	raw, ok := s.Get(profileWizardStateSessionKey).(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return profileWizardState{}, false
	}

	var state profileWizardState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		logger.Warn("failed to decode profile wizard state", "error", err)
		clearProfileWizardState(s)

		return profileWizardState{}, false
	}

	state.Mode = strings.TrimSpace(state.Mode)
	state.BaseProfileID = strings.TrimSpace(state.BaseProfileID)
	state.OriginalDraft = normalizeProfileWizardDraft(state.OriginalDraft)
	state.Draft = normalizeProfileWizardDraft(state.Draft)
	state.Conversation = normalizeProfileWizardConversation(state.Conversation)

	return state, true
}

func setProfileWizardState(s session.Session, state profileWizardState) {
	state.Mode = strings.TrimSpace(state.Mode)
	state.BaseProfileID = strings.TrimSpace(state.BaseProfileID)
	state.OriginalDraft = normalizeProfileWizardDraft(state.OriginalDraft)
	state.Draft = normalizeProfileWizardDraft(state.Draft)
	state.Conversation = normalizeProfileWizardConversation(state.Conversation)

	encoded, err := json.Marshal(state)
	if err != nil {
		logger.Error("failed to encode profile wizard state", "error", err)

		return
	}

	s.Set(profileWizardStateSessionKey, string(encoded))
}

func clearProfileWizardState(s session.Session) {
	s.Delete(profileWizardStateSessionKey)
}

func normalizeProfileWizardDraft(draft profileWizardDraft) profileWizardDraft {
	draft.ProfileID = strings.TrimSpace(draft.ProfileID)
	draft.Name = strings.TrimSpace(draft.Name)
	draft.Description = strings.TrimSpace(draft.Description)
	draft.RawNix = strings.TrimSpace(draft.RawNix)
	draft.FleetIDs = normalizeProfileWizardFleetIDs(draft.FleetIDs)
	draft.ConfigJSON = strings.TrimSpace(draft.ConfigJSON)
	if draft.ConfigJSON == "" {
		draft.ConfigJSON = defaultProfileWizardConfigJSON
	}

	if draft.ConfigSchemaVersion <= 0 {
		draft.ConfigSchemaVersion = 1
	}

	return draft
}

func isZeroProfileWizardDraft(draft profileWizardDraft) bool {
	return strings.TrimSpace(draft.ProfileID) == "" &&
		strings.TrimSpace(draft.Name) == "" &&
		strings.TrimSpace(draft.Description) == "" &&
		len(draft.FleetIDs) == 0 &&
		strings.TrimSpace(draft.ConfigJSON) == "" &&
		strings.TrimSpace(draft.RawNix) == "" &&
		draft.ConfigSchemaVersion == 0
}

func normalizeProfileWizardConversation(entries []profileWizardConversationEntry) []profileWizardConversationEntry {
	if len(entries) == 0 {
		return []profileWizardConversationEntry{}
	}

	normalized := make([]profileWizardConversationEntry, 0, len(entries))
	for _, entry := range entries {
		role := strings.TrimSpace(entry.Role)
		content := strings.TrimSpace(entry.Content)
		if content == "" {
			continue
		}

		if role != "assistant" && role != "user" {
			continue
		}

		normalized = append(normalized, profileWizardConversationEntry{Role: role, Content: content})
	}

	if len(normalized) > profileWizardConversationLimit {
		normalized = append([]profileWizardConversationEntry(nil), normalized[len(normalized)-profileWizardConversationLimit:]...)
	}

	return normalized
}

func appendProfileWizardConversation(entries []profileWizardConversationEntry, additions ...profileWizardConversationEntry) []profileWizardConversationEntry {
	updated := append(append([]profileWizardConversationEntry(nil), entries...), additions...)

	return normalizeProfileWizardConversation(updated)
}

func cloneProfileWizardConversation(entries []profileWizardConversationEntry) []profileWizardConversationEntry {
	if len(entries) == 0 {
		return []profileWizardConversationEntry{}
	}

	cloned := make([]profileWizardConversationEntry, len(entries))
	copy(cloned, entries)

	return cloned
}

func newProfileWizardResponse(ctx context.Context, state profileWizardState, fleets []db.Fleet, ai *ProfileWizardAI, chatPath, applyPath, discardPath, resetPath, manualPath string) (profileWizardResponse, error) {
	draft := summarizeProfileWizardDraft(state, fleets)
	validation := validateProfileWizardDraft(ctx, state.Draft, fleets)

	response := profileWizardResponse{
		Available:       ai != nil && ai.Enabled(),
		DisabledReason:  profileWizardDisabledReason(ai),
		Mode:            state.Mode,
		ChatPath:        chatPath,
		ApplyPath:       applyPath,
		DiscardPath:     discardPath,
		ResetPath:       resetPath,
		ManualPath:      manualPath,
		Conversation:    cloneProfileWizardConversation(state.Conversation),
		Draft:           draft,
		Validation:      validation,
		AvailableFleets: profileWizardFleetOptions(fleets),
	}

	return response, nil
}

func summarizeProfileWizardDraft(state profileWizardState, fleets []db.Fleet) profileWizardDraftSummary {
	draft := normalizeProfileWizardDraft(state.Draft)
	original := normalizeProfileWizardDraft(state.OriginalDraft)
	packages, _ := packagesFromProfileConfig(draft.ConfigJSON)
	kernelConfig, _ := profileKernelConfigFromProfileConfig(draft.ConfigJSON)
	openclawEnabled, _ := openclawMicrovmEnabledFromProfileConfig(draft.ConfigJSON)
	plannedChanges := profileWizardPlannedChanges(state.Mode, original, draft, fleets)

	return profileWizardDraftSummary{
		Mode:                   state.Mode,
		ProfileID:              draft.ProfileID,
		Name:                   draft.Name,
		Description:            draft.Description,
		FleetIDs:               append([]string(nil), draft.FleetIDs...),
		FleetNames:             profileWizardSelectedFleetNames(fleets, draft.FleetIDs),
		Packages:               packages,
		PackageCount:           len(packages),
		Kernel:                 summarizeProfileWizardKernel(kernelConfig),
		OpenClawMicroVMEnabled: openclawEnabled,
		OpenClawSummary:        profileOpenclawMicroVMSummary(openclawEnabled),
		RawNix:                 draft.RawNix,
		HasRawNix:              strings.TrimSpace(draft.RawNix) != "",
		ConfigSchemaVersion:    draft.ConfigSchemaVersion,
		PlannedChanges:         plannedChanges,
		PlannedChangeCount:     len(plannedChanges),
	}
}

func summarizeProfileWizardKernel(config ProfileKernelConfig) profileWizardKernelSummary {
	return profileWizardKernelSummary{
		Attr:                  config.Attr,
		Summary:               profileKernelSummary(config),
		SourceOverrideEnabled: config.SourceOverride.Enabled,
		PatchCount:            len(config.SourceOverride.Patches),
	}
}

func profileWizardPlannedChanges(mode string, original, draft profileWizardDraft, fleets []db.Fleet) []profileWizardPlannedChange {
	original = normalizeProfileWizardDraft(original)
	draft = normalizeProfileWizardDraft(draft)

	changes := make([]profileWizardPlannedChange, 0, 8)

	if strings.TrimSpace(mode) == profileWizardModeCreate {
		if draft.Name != "" {
			changes = append(changes, profileWizardPlannedChange{Label: "Name", Detail: "Create profile as " + draft.Name})
		}
	} else if draft.Name != original.Name {
		detail := "Rename profile"
		if draft.Name == "" {
			detail = "Clear profile name"
		} else {
			detail = "Rename profile to " + draft.Name
		}
		changes = append(changes, profileWizardPlannedChange{Label: "Name", Detail: detail})
	}

	if draft.Description != original.Description {
		detail := "Update description"
		switch {
		case draft.Description == "":
			detail = "Remove description"
		case original.Description == "":
			detail = "Add description"
		}
		changes = append(changes, profileWizardPlannedChange{Label: "Description", Detail: detail})
	}

	if !sameStringSet(original.FleetIDs, draft.FleetIDs) {
		fleetNames := profileWizardSelectedFleetNames(fleets, draft.FleetIDs)
		detail := "Assign fleets"
		if len(fleetNames) > 0 {
			detail = "Assign to " + strings.Join(fleetNames, ", ")
		}
		changes = append(changes, profileWizardPlannedChange{Label: "Fleets", Detail: detail})
	}

	originalPackages, _ := packagesFromProfileConfig(original.ConfigJSON)
	draftPackages, _ := packagesFromProfileConfig(draft.ConfigJSON)
	addedPackages, removedPackages := diffProfileWizardStringLists(originalPackages, draftPackages)
	if len(addedPackages) > 0 || len(removedPackages) > 0 {
		parts := make([]string, 0, 2)
		if len(addedPackages) > 0 {
			parts = append(parts, "Add "+strings.Join(addedPackages, ", "))
		}
		if len(removedPackages) > 0 {
			parts = append(parts, "Remove "+strings.Join(removedPackages, ", "))
		}
		changes = append(changes, profileWizardPlannedChange{Label: "Packages", Detail: strings.Join(parts, "; ")})
	}

	originalKernelConfig, _ := profileKernelConfigFromProfileConfig(original.ConfigJSON)
	draftKernelConfig, _ := profileKernelConfigFromProfileConfig(draft.ConfigJSON)
	if !profileWizardKernelConfigsEqual(originalKernelConfig, draftKernelConfig) {
		detail := profileKernelSummary(draftKernelConfig)
		if draftKernelConfig.Attr == "" && !draftKernelConfig.SourceOverride.Enabled {
			detail = "Reset to pinned nixpkgs default"
		}
		changes = append(changes, profileWizardPlannedChange{Label: "Kernel", Detail: detail})
	}

	originalOpenClawEnabled, _ := openclawMicrovmEnabledFromProfileConfig(original.ConfigJSON)
	draftOpenClawEnabled, _ := openclawMicrovmEnabledFromProfileConfig(draft.ConfigJSON)
	if originalOpenClawEnabled != draftOpenClawEnabled {
		detail := "Disable OpenClaw MicroVM"
		if draftOpenClawEnabled {
			detail = "Enable OpenClaw MicroVM"
		}
		changes = append(changes, profileWizardPlannedChange{Label: "OpenClaw", Detail: detail})
	}

	if draft.RawNix != original.RawNix {
		detail := "Update raw Nix override"
		switch {
		case draft.RawNix == "":
			detail = "Remove raw Nix override"
		case original.RawNix == "":
			detail = "Add raw Nix override"
		}
		changes = append(changes, profileWizardPlannedChange{Label: "Raw Nix", Detail: detail})
	}

	if draft.ConfigSchemaVersion != original.ConfigSchemaVersion {
		changes = append(changes, profileWizardPlannedChange{Label: "Schema", Detail: fmt.Sprintf("Use config schema version %d", draft.ConfigSchemaVersion)})
	}

	return changes
}

func diffProfileWizardStringLists(original, updated []string) ([]string, []string) {
	original = normalizePackageList(original)
	updated = normalizePackageList(updated)

	updatedSet := make(map[string]struct{}, len(updated))
	for _, item := range updated {
		updatedSet[item] = struct{}{}
	}

	originalSet := make(map[string]struct{}, len(original))
	for _, item := range original {
		originalSet[item] = struct{}{}
	}

	added := make([]string, 0)
	for _, item := range updated {
		if _, ok := originalSet[item]; ok {
			continue
		}
		added = append(added, item)
	}

	removed := make([]string, 0)
	for _, item := range original {
		if _, ok := updatedSet[item]; ok {
			continue
		}
		removed = append(removed, item)
	}

	return added, removed
}

func profileWizardKernelConfigsEqual(left, right ProfileKernelConfig) bool {
	left = normalizeProfileKernelConfig(left)
	right = normalizeProfileKernelConfig(right)

	if left.Attr != right.Attr || left.SourceOverride.Enabled != right.SourceOverride.Enabled || left.SourceOverride.URL != right.SourceOverride.URL || left.SourceOverride.Ref != right.SourceOverride.Ref || left.SourceOverride.Rev != right.SourceOverride.Rev {
		return false
	}

	if len(left.SourceOverride.Patches) != len(right.SourceOverride.Patches) {
		return false
	}

	for i := range left.SourceOverride.Patches {
		if left.SourceOverride.Patches[i] != right.SourceOverride.Patches[i] {
			return false
		}
	}

	return true
}

func profileWizardSelectedFleetNames(fleets []db.Fleet, selectedIDs []string) []string {
	if len(selectedIDs) == 0 {
		return []string{}
	}

	byID := make(map[string]string, len(fleets))
	for _, fleet := range fleets {
		byID[strings.TrimSpace(fleet.ID)] = strings.TrimSpace(fleet.Name)
	}

	names := make([]string, 0, len(selectedIDs))
	for _, fleetID := range normalizeProfileWizardFleetIDs(selectedIDs) {
		name := strings.TrimSpace(byID[fleetID])
		if name == "" {
			name = fleetID
		}

		names = append(names, name)
	}

	return names
}

func profileWizardFleetOptions(fleets []db.Fleet) []profileWizardFleetOption {
	if len(fleets) == 0 {
		return []profileWizardFleetOption{}
	}

	options := make([]profileWizardFleetOption, 0, len(fleets))
	for _, fleet := range fleets {
		options = append(options, profileWizardFleetOption{
			ID:          strings.TrimSpace(fleet.ID),
			Name:        strings.TrimSpace(fleet.Name),
			Description: strings.TrimSpace(fleet.Description),
		})
	}

	sort.Slice(options, func(i, j int) bool {
		return options[i].Name < options[j].Name
	})

	return options
}

func validateProfileWizardDraft(ctx context.Context, draft profileWizardDraft, fleets []db.Fleet) profileWizardValidation {
	draft = normalizeProfileWizardDraft(draft)
	errorsList := make([]string, 0)
	warnings := make([]string, 0)

	if draft.Name == "" {
		errorsList = append(errorsList, "Name is required")
	}

	if len(draft.FleetIDs) == 0 {
		errorsList = append(errorsList, "Select at least one fleet")
	} else {
		allowedFleetIDs := make(map[string]struct{}, len(fleets))
		for _, fleet := range fleets {
			id := strings.TrimSpace(fleet.ID)
			if id == "" {
				continue
			}

			allowedFleetIDs[id] = struct{}{}
		}

		for _, fleetID := range draft.FleetIDs {
			if _, ok := allowedFleetIDs[fleetID]; ok {
				continue
			}

			errorsList = append(errorsList, "Selected fleet is no longer available")
			break
		}
	}

	if draft.ConfigSchemaVersion <= 0 {
		errorsList = append(errorsList, "Config schema version is invalid")
	}

	if _, err := parseProfileConfig(draft.ConfigJSON); err != nil {
		errorsList = append(errorsList, mutationErrorMessage(err))
	} else {
		packages, err := packagesFromProfileConfig(draft.ConfigJSON)
		if err != nil {
			errorsList = append(errorsList, mutationErrorMessage(err))
		} else {
			for _, pkg := range packages {
				if _, err := packageNameToNixExpression(pkg); err != nil {
					errorsList = append(errorsList, fmt.Sprintf("Package %q is invalid", pkg))
				}
			}
		}

		kernelConfig, err := profileKernelConfigFromProfileConfig(draft.ConfigJSON)
		if err != nil {
			errorsList = append(errorsList, mutationErrorMessage(err))
		} else {
			allowedAttrs := map[string]struct{}{}
			if kernelConfig.Attr != "" {
				kernelOptions, optionsErr := listAvailableKernelOptions(ctx)
				if optionsErr != nil {
					errorsList = append(errorsList, "Failed to validate selected kernel against pinned nixpkgs")
				} else {
					allowedAttrs = kernelOptionSet(kernelOptions)
				}
			}

			if err := validateProfileKernelConfig(kernelConfig, allowedAttrs); err != nil {
				errorsList = append(errorsList, err.Error())
			}
		}

		if _, err := openclawMicrovmEnabledFromProfileConfig(draft.ConfigJSON); err != nil {
			errorsList = append(errorsList, mutationErrorMessage(err))
		}
	}

	packages, _ := packagesFromProfileConfig(draft.ConfigJSON)
	if len(packages) == 0 && strings.TrimSpace(draft.RawNix) == "" {
		warnings = append(warnings, "Draft has no packages or raw Nix overrides yet")
	}

	errorsList = uniqueSortedStrings(errorsList)
	warnings = uniqueSortedStrings(warnings)

	return profileWizardValidation{
		CanApply: len(errorsList) == 0,
		Errors:   errorsList,
		Warnings: warnings,
	}
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}

		if _, ok := seen[trimmed]; ok {
			continue
		}

		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	sort.Strings(result)

	return result
}

func profileWizardCreateProfileInput(draft profileWizardDraft) db.CreateProfileInput {
	draft = normalizeProfileWizardDraft(draft)

	return db.CreateProfileInput{
		FleetIDs:            append([]string(nil), draft.FleetIDs...),
		Name:                draft.Name,
		Description:         draft.Description,
		ConfigJSON:          draft.ConfigJSON,
		RawNix:              draft.RawNix,
		ConfigSchemaVersion: draft.ConfigSchemaVersion,
	}
}

func findCreatedProfileForWizardRedirect(ctx context.Context, user *db.User, draft profileWizardDraft) (string, bool) {
	if user == nil {
		return "", false
	}

	profiles, err := db.ListProfilesForUser(ctx, user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Warn("failed to reload profiles for wizard redirect", "error", err)

		return "", false
	}

	name := strings.TrimSpace(draft.Name)
	fleetIDs := normalizeProfileWizardFleetIDs(draft.FleetIDs)
	for _, profile := range profiles {
		if strings.TrimSpace(profile.Name) != name {
			continue
		}

		if sameStringSet(profile.FleetIDs, fleetIDs) {
			return profile.ID, true
		}
	}

	for _, profile := range profiles {
		if strings.TrimSpace(profile.Name) == name {
			return profile.ID, true
		}
	}

	return "", false
}

func sameStringSet(left, right []string) bool {
	left = normalizeProfileWizardFleetIDs(left)
	right = normalizeProfileWizardFleetIDs(right)
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

func decodeProfileWizardChatRequest(r *flamego.Request) (profileWizardChatRequest, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body().ReadCloser(), maxProfileWizardMessageBytes+1))
	if err != nil {
		return profileWizardChatRequest{}, fmt.Errorf("Message is required")
	}

	if len(body) == 0 || len(body) > maxProfileWizardMessageBytes {
		return profileWizardChatRequest{}, fmt.Errorf("Message is required")
	}

	var request profileWizardChatRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return profileWizardChatRequest{}, fmt.Errorf("Message is required")
	}

	request.Message = strings.TrimSpace(request.Message)
	if request.Message == "" {
		return profileWizardChatRequest{}, fmt.Errorf("Message is required")
	}

	return request, nil
}

func handleProfileWizardPageError(c flamego.Context, s session.Session, profileID string, err error) {
	path := "/profiles"
	if strings.TrimSpace(profileID) != "" {
		path = profileViewPath(profileID)
	}

	handleMutationError(c, s, path, err)
}

func writeProfileWizardMutationError(c flamego.Context, err error) {
	status := http.StatusInternalServerError
	message := "Failed to update profile wizard"

	switch {
	case errorsIsAny(err, db.ErrProfileNotFound):
		status = http.StatusNotFound
		message = "Profile not found"
	case errorsIsAny(err, db.ErrAccessDenied):
		status = http.StatusForbidden
		message = "Access restricted"
	default:
		message = mutationErrorMessage(err)
		if message == "Internal error" || message == "Failed to update profile" {
			message = "Failed to update profile wizard"
		}
	}

	writeJSONError(c, status, message)
}

func errorsIsAny(err error, targets ...error) bool {
	for _, target := range targets {
		if target == nil {
			continue
		}

		if errors.Is(err, target) {
			return true
		}
	}

	return false
}

func profileWizardDisabledReason(ai *ProfileWizardAI) string {
	if ai == nil {
		return "AI wizard is unavailable"
	}

	return ai.DisabledReason()
}

func profileWizardPagePath(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "/profiles/wizard"
	}

	return "/profiles/" + profileID + "/wizard"
}

func profileWizardChatPath(profileID string) string {
	return profileWizardPagePath(profileID) + "/chat"
}

func profileWizardApplyPath(profileID string) string {
	return profileWizardPagePath(profileID) + "/apply"
}

func profileWizardDiscardPath(profileID string) string {
	return profileWizardPagePath(profileID) + "/discard"
}

func profileWizardManualPath(profileID string) string {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "/profiles/new"
	}

	return profileViewPath(profileID)
}

func normalizeProfileWizardFleetIDs(fleetIDs []string) []string {
	if len(fleetIDs) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(fleetIDs))
	seen := make(map[string]struct{}, len(fleetIDs))
	for _, fleetID := range fleetIDs {
		trimmed := strings.TrimSpace(fleetID)
		if trimmed == "" {
			continue
		}

		if _, ok := seen[trimmed]; ok {
			continue
		}

		normalized = append(normalized, trimmed)
		seen[trimmed] = struct{}{}
	}

	sort.Strings(normalized)

	return normalized
}
