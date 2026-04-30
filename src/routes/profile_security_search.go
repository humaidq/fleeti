/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/flamego/flamego"
	"github.com/flamego/session"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	profileSecuritySearchGoalMaxBytes       = 4 * 1024
	profileSecuritySearchTargetCount        = 24
	profileSecuritySearchBatchSize          = 8
	profileSecuritySearchMaxRounds          = 3
	defaultProfileSecuritySearchTemperature = 0.4
	profileSecuritySearchPromptTopCount     = 3
	profileSecuritySearchPromptWorstCount   = 2

	profileSecuritySearchJobStatusQueued    = "queued"
	profileSecuritySearchJobStatusRunning   = "running"
	profileSecuritySearchJobStatusSucceeded = "succeeded"
	profileSecuritySearchJobStatusFailed    = "failed"
)

var profileSecuritySearchPortPattern = regexp.MustCompile(`\b([1-9][0-9]{0,4})\b`)
var profileSecuritySearchPortPhrasePattern = regexp.MustCompile(`\bports?\b[^.;\n]*`)

type profileSecuritySearchRequest struct {
	Goal string `json:"goal"`
}

type profileSecuritySearchResponse struct {
	Goal                    string                               `json:"goal"`
	Scenario                profileSecuritySearchScenarioView    `json:"scenario"`
	EvaluatedCandidateCount int                                  `json:"evaluated_candidate_count"`
	UniqueCandidateCount    int                                  `json:"unique_candidate_count"`
	DuplicateCandidateCount int                                  `json:"duplicate_candidate_count"`
	Candidates              []profileSecuritySearchCandidateView `json:"candidates"`
}

type profileSecuritySearchStartResponse struct {
	JobID           string `json:"job_id"`
	Status          string `json:"status"`
	StatusPath      string `json:"status_path"`
	ProgressMessage string `json:"progress_message"`
	Done            bool   `json:"done"`
}

type profileSecuritySearchJobStatusResponse struct {
	JobID                   string                               `json:"job_id"`
	Status                  string                               `json:"status"`
	Done                    bool                                 `json:"done"`
	Goal                    string                               `json:"goal"`
	ProgressMessage         string                               `json:"progress_message"`
	CurrentRound            int                                  `json:"current_round"`
	TotalRounds             int                                  `json:"total_rounds"`
	TargetCandidateCount    int                                  `json:"target_candidate_count"`
	Scenario                profileSecuritySearchScenarioView    `json:"scenario"`
	EvaluatedCandidateCount int                                  `json:"evaluated_candidate_count"`
	UniqueCandidateCount    int                                  `json:"unique_candidate_count"`
	DuplicateCandidateCount int                                  `json:"duplicate_candidate_count"`
	Candidates              []profileSecuritySearchCandidateView `json:"candidates"`
	Error                   string                               `json:"error,omitempty"`
}

type profileSecuritySearchProgress struct {
	Status                  string
	ProgressMessage         string
	CurrentRound            int
	TotalRounds             int
	TargetCandidateCount    int
	EvaluatedCandidateCount int
	UniqueCandidateCount    int
	DuplicateCandidateCount int
	Candidates              []profileSecuritySearchCandidateView
	Scenario                profileSecuritySearchScenarioView
}

type profileSecuritySearchBatchGeneration struct {
	Candidates       []profileSecuritySearchLLMCandidate
	RawModelResponse string
}

type profileSecuritySearchJobRecord struct {
	ProfileID   string
	OwnerUserID string
	State       profileSecuritySearchJobStatusResponse
}

type profileSecuritySearchJobStore struct {
	mu   sync.RWMutex
	jobs map[string]profileSecuritySearchJobRecord
}

var profileSecuritySearchJobs = newProfileSecuritySearchJobStore()

type profileSecuritySearchScenario struct {
	RequiredTCPPorts []int
	RequiredUDPPorts []int
	Signals          []string
}

type profileSecuritySearchScenarioView struct {
	RequiredTCPPorts []int    `json:"required_tcp_ports"`
	RequiredUDPPorts []int    `json:"required_udp_ports"`
	Signals          []string `json:"signals"`
}

type profileSecuritySearchCandidateView struct {
	ID         string                                   `json:"id"`
	Rank       int                                      `json:"rank"`
	Score      int                                      `json:"score"`
	Summary    string                                   `json:"summary"`
	Rationale  string                                   `json:"rationale"`
	Config     profileSecurityAPIConfig                 `json:"config"`
	Evaluation profileSecuritySearchCandidateEvaluation `json:"evaluation"`
}

type profileSecuritySearchCandidateEvaluation struct {
	Valid                  bool                       `json:"valid"`
	Errors                 []string                   `json:"errors"`
	Warnings               []string                   `json:"warnings"`
	RequiredTCPPorts       []int                      `json:"required_tcp_ports"`
	RequiredUDPPorts       []int                      `json:"required_udp_ports"`
	MissingTCPPorts        []int                      `json:"missing_tcp_ports"`
	MissingUDPPorts        []int                      `json:"missing_udp_ports"`
	ExtraTCPPorts          []int                      `json:"extra_tcp_ports"`
	ExtraUDPPorts          []int                      `json:"extra_udp_ports"`
	NixEvaluation          profileNixEvaluationResult `json:"nix_evaluation"`
	FirewallEnabled        bool                       `json:"firewall_enabled"`
	AppArmorEnabled        bool                       `json:"apparmor_enabled"`
	PWQualityEnabled       bool                       `json:"pwquality_enabled"`
	PasswordExpiryEnabled  bool                       `json:"password_expiry_enabled"`
	WebsiteBlockingEnabled bool                       `json:"website_blocking_enabled"`
	BlacklistedModuleCount int                        `json:"blacklisted_module_count"`
	EnabledFeatureCount    int                        `json:"enabled_feature_count"`
	OpenTCPPortCount       int                        `json:"open_tcp_port_count"`
	OpenUDPPortCount       int                        `json:"open_udp_port_count"`
}

type profileSecuritySearchLLMResponse struct {
	Candidates []profileSecuritySearchLLMCandidate `json:"candidates"`
}

type profileSecuritySearchLLMCandidate struct {
	Rationale string                   `json:"rationale"`
	Config    profileSecurityAPIConfig `json:"config"`
}

type profileSecuritySearchPromptCandidate struct {
	Score           int      `json:"score"`
	Summary         string   `json:"summary"`
	Rationale       string   `json:"rationale"`
	MissingTCPPorts []int    `json:"missing_tcp_ports"`
	MissingUDPPorts []int    `json:"missing_udp_ports"`
	ExtraTCPPorts   []int    `json:"extra_tcp_ports"`
	ExtraUDPPorts   []int    `json:"extra_udp_ports"`
	Errors          []string `json:"errors"`
	Warnings        []string `json:"warnings"`
	NixValid        bool     `json:"nix_valid"`
}

type profileSecurityAPIConfig struct {
	Firewall                 profileSecurityAPIFirewallConfig        `json:"firewall"`
	BlacklistedKernelModules []string                                `json:"blacklisted_kernel_modules"`
	AppArmor                 profileSecurityAPIAppArmorConfig        `json:"apparmor"`
	PasswordPolicy           profileSecurityAPIPasswordPolicyConfig  `json:"password_policy"`
	WebsiteBlocking          profileSecurityAPIWebsiteBlockingConfig `json:"website_blocking"`
}

type profileSecurityAPIFirewallConfig struct {
	Enable          bool  `json:"enable"`
	AllowedTCPPorts []int `json:"allowed_tcp_ports"`
	AllowedUDPPorts []int `json:"allowed_udp_ports"`
}

type profileSecurityAPIAppArmorConfig struct {
	Enable bool `json:"enable"`
}

type profileSecurityAPIPasswordPolicyConfig struct {
	PWQuality profileSecurityAPIPWQualityConfig      `json:"pwquality"`
	Expiry    profileSecurityAPIPasswordExpiryConfig `json:"expiry"`
}

type profileSecurityAPIPWQualityConfig struct {
	Enable        bool `json:"enable"`
	MinimumLength int  `json:"minimum_length"`
	MinimumDigits int  `json:"minimum_digits"`
	MinimumUpper  int  `json:"minimum_upper"`
	MinimumLower  int  `json:"minimum_lower"`
	MinimumOther  int  `json:"minimum_other"`
	RetryCount    int  `json:"retry_count"`
}

type profileSecurityAPIPasswordExpiryConfig struct {
	Enable      bool `json:"enable"`
	MaximumDays int  `json:"maximum_days"`
	WarningDays int  `json:"warning_days"`
}

type profileSecurityAPIWebsiteBlockingConfig struct {
	Enable          bool     `json:"enable"`
	BlockCategories []string `json:"block_categories"`
}

func newProfileSecuritySearchJobStore() *profileSecuritySearchJobStore {
	return &profileSecuritySearchJobStore{jobs: map[string]profileSecuritySearchJobRecord{}}
}

func (store *profileSecuritySearchJobStore) Create(profileID, ownerUserID, goal string, scenario profileSecuritySearchScenarioView) (profileSecuritySearchJobStatusResponse, error) {
	jobID, err := newProfileSecuritySearchJobID()
	if err != nil {
		return profileSecuritySearchJobStatusResponse{}, err
	}

	state := profileSecuritySearchJobStatusResponse{
		JobID:                jobID,
		Status:               profileSecuritySearchJobStatusQueued,
		Done:                 false,
		Goal:                 goal,
		ProgressMessage:      "Queued security search",
		CurrentRound:         0,
		TotalRounds:          profileSecuritySearchMaxRounds,
		TargetCandidateCount: profileSecuritySearchTargetCount,
		Scenario:             scenario,
		Candidates:           []profileSecuritySearchCandidateView{},
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.jobs[jobID] = profileSecuritySearchJobRecord{
		ProfileID:   profileID,
		OwnerUserID: ownerUserID,
		State:       state,
	}

	return state, nil
}

func (store *profileSecuritySearchJobStore) Get(jobID string) (profileSecuritySearchJobRecord, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()

	record, ok := store.jobs[strings.TrimSpace(jobID)]
	if !ok {
		return profileSecuritySearchJobRecord{}, false
	}

	record.State.Candidates = cloneProfileSecuritySearchCandidates(record.State.Candidates)
	return record, true
}

func (store *profileSecuritySearchJobStore) Update(jobID string, update func(*profileSecuritySearchJobStatusResponse)) {
	store.mu.Lock()
	defer store.mu.Unlock()

	record, ok := store.jobs[strings.TrimSpace(jobID)]
	if !ok {
		return
	}

	update(&record.State)
	record.State.Candidates = cloneProfileSecuritySearchCandidates(record.State.Candidates)
	store.jobs[jobID] = record
}

func newProfileSecuritySearchJobID() (string, error) {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("failed to generate search job id: %w", err)
	}

	return hex.EncodeToString(buffer), nil
}

func cloneProfileSecuritySearchCandidates(candidates []profileSecuritySearchCandidateView) []profileSecuritySearchCandidateView {
	if len(candidates) == 0 {
		return []profileSecuritySearchCandidateView{}
	}

	cloned := make([]profileSecuritySearchCandidateView, len(candidates))
	copy(cloned, candidates)

	return cloned
}

func ProfileSecuritySearch(c flamego.Context, s session.Session, ai *ProfileWizardAI) {
	ctx := c.Request().Context()
	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	if ai == nil || !ai.Enabled() {
		writeJSONError(c, http.StatusServiceUnavailable, profileWizardDisabledReason(ai))

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	var request profileSecuritySearchRequest
	decoder := json.NewDecoder(c.Request().Body().ReadCloser())
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSONError(c, http.StatusBadRequest, "Invalid search request")

		return
	}

	goal := strings.TrimSpace(request.Goal)
	if goal == "" {
		writeJSONError(c, http.StatusBadRequest, "Goal is required")

		return
	}

	if len(goal) > profileSecuritySearchGoalMaxBytes {
		writeJSONError(c, http.StatusBadRequest, "Goal is too long")

		return
	}

	profile, err := loadProfileForSecuritySearch(ctx, user, profileID)
	if err != nil {
		writeProfileSecuritySearchError(c, err)

		return
	}

	scenario := profileSecuritySearchScenarioViewFromModel(profileSecuritySearchScenarioFromGoal(goal))
	jobState, err := profileSecuritySearchJobs.Create(profileID, user.ID.String(), goal, scenario)
	if err != nil {
		logger.Error("failed to create profile security search job", "profile_id", profileID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to start security search")

		return
	}

	jobID := jobState.JobID
	go runProfileSecuritySearchJob(jobID, ai, profile, goal)

	writeJSON(c, profileSecuritySearchStartResponse{
		JobID:           jobID,
		Status:          profileSecuritySearchJobStatusQueued,
		StatusPath:      profileSecuritySearchStatusPath(profileID, jobID),
		ProgressMessage: jobState.ProgressMessage,
		Done:            false,
	})
}

func ProfileSecuritySearchStatus(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()
	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	if _, err := loadProfileForSecuritySearch(ctx, user, profileID); err != nil {
		writeProfileSecuritySearchError(c, err)

		return
	}

	jobID := strings.TrimSpace(c.Param("job_id"))
	record, ok := profileSecuritySearchJobs.Get(jobID)
	if !ok || strings.TrimSpace(record.ProfileID) != profileID {
		writeJSONError(c, http.StatusNotFound, "Security search job not found")

		return
	}

	if !user.IsAdmin && strings.TrimSpace(record.OwnerUserID) != user.ID.String() {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	writeJSON(c, record.State)
}

func runProfileSecuritySearchJob(jobID string, ai *ProfileWizardAI, profile db.ProfileEdit, goal string) {
	profileSecuritySearchJobs.Update(jobID, func(state *profileSecuritySearchJobStatusResponse) {
		state.Status = profileSecuritySearchJobStatusRunning
		state.ProgressMessage = "Starting security search"
	})

	options := DefaultProfileSecuritySearchRunOptions(goal)
	response, err := ai.RunProfileSecuritySearch(context.Background(), profileSecuritySearchBaseProfileFromDB(profile), options, func(progress profileSecuritySearchProgress) {
		profileSecuritySearchJobs.Update(jobID, func(state *profileSecuritySearchJobStatusResponse) {
			state.Status = progress.Status
			state.Done = progress.Status == profileSecuritySearchJobStatusSucceeded || progress.Status == profileSecuritySearchJobStatusFailed
			state.ProgressMessage = progress.ProgressMessage
			state.CurrentRound = progress.CurrentRound
			state.TotalRounds = progress.TotalRounds
			state.TargetCandidateCount = progress.TargetCandidateCount
			state.Scenario = progress.Scenario
			state.EvaluatedCandidateCount = progress.EvaluatedCandidateCount
			state.UniqueCandidateCount = progress.UniqueCandidateCount
			state.DuplicateCandidateCount = progress.DuplicateCandidateCount
			state.Candidates = cloneProfileSecuritySearchCandidates(progress.Candidates)
		})
	})
	if err != nil {
		logger.Error("profile security search job failed", "profile_id", profile.ID, "job_id", jobID, "error", err)
		profileSecuritySearchJobs.Update(jobID, func(state *profileSecuritySearchJobStatusResponse) {
			state.Status = profileSecuritySearchJobStatusFailed
			state.Done = true
			state.ProgressMessage = "Security search failed"
			state.Error = "Security search failed"
		})

		return
	}

	profileSecuritySearchJobs.Update(jobID, func(state *profileSecuritySearchJobStatusResponse) {
		state.Status = profileSecuritySearchJobStatusSucceeded
		state.Done = true
		state.ProgressMessage = fmt.Sprintf("Evaluated %d unique candidates", response.UniqueCandidateCount)
		state.CurrentRound = len(response.Rounds)
		state.TotalRounds = response.MaxRounds
		state.TargetCandidateCount = response.TargetCandidateCount
		state.Scenario = response.Scenario
		state.EvaluatedCandidateCount = response.EvaluatedCandidateCount
		state.UniqueCandidateCount = response.UniqueCandidateCount
		state.DuplicateCandidateCount = response.DuplicateCandidateCount
		state.Candidates = cloneProfileSecuritySearchCandidates(response.Candidates)
		state.Error = ""
	})
}

func loadProfileForSecuritySearch(ctx context.Context, user *db.User, profileID string) (db.ProfileEdit, error) {
	profile, err := db.GetProfileForEdit(ctx, profileID)
	if err != nil {
		return db.ProfileEdit{}, err
	}

	canManage, err := db.UserCanManageProfile(ctx, user.ID.String(), user.IsAdmin, profileID)
	if err != nil {
		return db.ProfileEdit{}, err
	}

	if !canManage {
		return db.ProfileEdit{}, db.ErrAccessDenied
	}

	return profile, nil
}

func writeProfileSecuritySearchError(c flamego.Context, err error) {
	switch {
	case errors.Is(err, db.ErrProfileNotFound):
		writeJSONError(c, http.StatusNotFound, "Profile not found")
	case errors.Is(err, db.ErrAccessDenied):
		writeJSONError(c, http.StatusForbidden, "Access restricted")
	default:
		writeJSONError(c, http.StatusInternalServerError, mutationErrorMessage(err))
	}
}

func profileSecuritySearchBaseProfileFromDB(profile db.ProfileEdit) ProfileSecuritySearchBaseProfile {
	return normalizeProfileSecuritySearchBaseProfile(ProfileSecuritySearchBaseProfile{
		ID:                  strings.TrimSpace(profile.ID),
		Name:                strings.TrimSpace(profile.Name),
		Description:         strings.TrimSpace(profile.Description),
		Revision:            profile.LatestRevision,
		FleetIDs:            append([]string(nil), profile.FleetIDs...),
		ConfigSchemaVersion: profile.ConfigSchemaVersion,
		ConfigJSON:          strings.TrimSpace(profile.ConfigJSON),
		RawNix:              strings.TrimSpace(profile.RawNix),
	})
}

func (ai *ProfileWizardAI) searchProfileSecurityCandidates(ctx context.Context, profile ProfileSecuritySearchBaseProfile, currentSecurity ProfileSecurityConfig, options ProfileSecuritySearchRunOptions, onProgress func(profileSecuritySearchProgress)) (ProfileSecuritySearchRunResult, error) {
	scenario := profileSecuritySearchScenarioFromGoal(options.Goal)
	scenarioView := profileSecuritySearchScenarioViewFromModel(scenario)
	evaluated := make([]profileSecuritySearchCandidateView, 0, options.TargetCandidateCount)
	rounds := make([]ProfileSecuritySearchRoundResult, 0, options.MaxRounds)
	seen := make(map[string]struct{}, options.TargetCandidateCount)
	duplicateCount := 0
	if onProgress != nil {
		onProgress(profileSecuritySearchProgress{
			Status:                  profileSecuritySearchJobStatusRunning,
			ProgressMessage:         "Preparing security search",
			CurrentRound:            0,
			TotalRounds:             options.MaxRounds,
			TargetCandidateCount:    options.TargetCandidateCount,
			EvaluatedCandidateCount: 0,
			UniqueCandidateCount:    0,
			DuplicateCandidateCount: 0,
			Candidates:              []profileSecuritySearchCandidateView{},
			Scenario:                scenarioView,
		})
	}

	for round := 0; round < options.MaxRounds && len(evaluated) < options.TargetCandidateCount; round++ {
		if onProgress != nil {
			onProgress(profileSecuritySearchProgress{
				Status:                  profileSecuritySearchJobStatusRunning,
				ProgressMessage:         fmt.Sprintf("Generating candidate batch %d of %d", round+1, options.MaxRounds),
				CurrentRound:            round + 1,
				TotalRounds:             options.MaxRounds,
				TargetCandidateCount:    options.TargetCandidateCount,
				EvaluatedCandidateCount: len(evaluated),
				UniqueCandidateCount:    len(evaluated),
				DuplicateCandidateCount: duplicateCount,
				Candidates:              rankedProfileSecuritySearchCandidates(evaluated),
				Scenario:                scenarioView,
			})
		}

		requestedCount := options.BatchSize
		remaining := options.TargetCandidateCount - len(evaluated)
		if remaining < requestedCount {
			requestedCount = remaining
		}

		batch, err := ai.generateProfileSecurityCandidateBatch(ctx, profile, currentSecurity, options, scenario, evaluated, round+1, requestedCount)
		if err != nil {
			if len(evaluated) > 0 && options.AllowPartial {
				logger.Warn("profile security search batch failed after partial progress", "profile_id", profile.ID, "round", round+1, "error", err)
				break
			}

			result := buildProfileSecuritySearchRunResult(options, scenarioView, duplicateCount, rounds, evaluated)
			return result, err
		}

		acceptedCount := 0
		duplicateCountBeforeRound := duplicateCount
		for _, candidate := range batch.Candidates {
			config := candidate.Config.toModel()
			hash, err := profileSecuritySearchConfigHash(config)
			if err != nil {
				result := buildProfileSecuritySearchRunResult(options, scenarioView, duplicateCount, rounds, evaluated)
				return result, err
			}

			if _, ok := seen[hash]; ok {
				duplicateCount++
				if onProgress != nil {
					onProgress(profileSecuritySearchProgress{
						Status:                  profileSecuritySearchJobStatusRunning,
						ProgressMessage:         fmt.Sprintf("Skipped duplicate candidate in batch %d", round+1),
						CurrentRound:            round + 1,
						TotalRounds:             options.MaxRounds,
						TargetCandidateCount:    options.TargetCandidateCount,
						EvaluatedCandidateCount: len(evaluated),
						UniqueCandidateCount:    len(evaluated),
						DuplicateCandidateCount: duplicateCount,
						Candidates:              rankedProfileSecuritySearchCandidates(evaluated),
						Scenario:                scenarioView,
					})
				}
				continue
			}

			seen[hash] = struct{}{}
			evaluated = append(evaluated, evaluateProfileSecuritySearchCandidate(ctx, profile, config, scenario, strings.TrimSpace(candidate.Rationale), hash))
			acceptedCount++
			if onProgress != nil {
				onProgress(profileSecuritySearchProgress{
					Status:                  profileSecuritySearchJobStatusRunning,
					ProgressMessage:         fmt.Sprintf("Evaluated %d of %d unique candidates", len(evaluated), options.TargetCandidateCount),
					CurrentRound:            round + 1,
					TotalRounds:             options.MaxRounds,
					TargetCandidateCount:    options.TargetCandidateCount,
					EvaluatedCandidateCount: len(evaluated),
					UniqueCandidateCount:    len(evaluated),
					DuplicateCandidateCount: duplicateCount,
					Candidates:              rankedProfileSecuritySearchCandidates(evaluated),
					Scenario:                scenarioView,
				})
			}
			if len(evaluated) >= options.TargetCandidateCount {
				break
			}
		}

		_, _, bestScore, averageScore := summarizeProfileSecuritySearchCandidates(evaluated)
		roundResult := ProfileSecuritySearchRoundResult{
			Round:                   round + 1,
			RequestedCandidateCount: requestedCount,
			GeneratedCandidateCount: len(batch.Candidates),
			AcceptedCandidateCount:  acceptedCount,
			DuplicateCandidateCount: duplicateCount - duplicateCountBeforeRound,
			EvaluatedCandidateCount: len(evaluated),
			BestScoreAfterRound:     bestScore,
			AverageScoreAfterRound:  averageScore,
			TopCandidateIDs:         topProfileSecuritySearchCandidateIDs(evaluated, profileSecuritySearchPromptTopCount),
		}
		if options.SaveRawResponses {
			roundResult.RawModelResponse = batch.RawModelResponse
		}
		rounds = append(rounds, roundResult)
	}

	if len(evaluated) == 0 {
		return buildProfileSecuritySearchRunResult(options, scenarioView, duplicateCount, rounds, evaluated), fmt.Errorf("profile security search produced no candidates")
	}

	return buildProfileSecuritySearchRunResult(options, scenarioView, duplicateCount, rounds, evaluated), nil
}

func buildProfileSecuritySearchRunResult(options ProfileSecuritySearchRunOptions, scenarioView profileSecuritySearchScenarioView, duplicateCount int, evaluatedRounds []ProfileSecuritySearchRoundResult, evaluated []profileSecuritySearchCandidateView) ProfileSecuritySearchRunResult {
	evaluated = rankedProfileSecuritySearchCandidates(evaluated)
	validCount, nixValidCount, bestScore, averageScore := summarizeProfileSecuritySearchCandidates(evaluated)

	return ProfileSecuritySearchRunResult{
		Goal:                    options.Goal,
		Scenario:                scenarioView,
		Model:                   options.Model,
		Temperature:             options.Temperature,
		TargetCandidateCount:    options.TargetCandidateCount,
		BatchSize:               options.BatchSize,
		MaxRounds:               options.MaxRounds,
		AllowPartial:            options.AllowPartial,
		SaveRawResponses:        options.SaveRawResponses,
		EvaluatedCandidateCount: len(evaluated),
		UniqueCandidateCount:    len(evaluated),
		DuplicateCandidateCount: duplicateCount,
		ValidCandidateCount:     validCount,
		NixValidCandidateCount:  nixValidCount,
		BestScore:               bestScore,
		AverageScore:            averageScore,
		Rounds:                  append([]ProfileSecuritySearchRoundResult(nil), evaluatedRounds...),
		Candidates:              evaluated,
	}
}

func (ai *ProfileWizardAI) generateProfileSecurityCandidateBatch(ctx context.Context, profile ProfileSecuritySearchBaseProfile, currentSecurity ProfileSecurityConfig, options ProfileSecuritySearchRunOptions, scenario profileSecuritySearchScenario, evaluated []profileSecuritySearchCandidateView, round, requestedCount int) (profileSecuritySearchBatchGeneration, error) {
	messages := []openRouterChatMessage{
		{Role: "system", Content: buildProfileSecuritySearchSystemPrompt()},
		{Role: "user", Content: buildProfileSecuritySearchBatchPrompt(profile, currentSecurity, options.Goal, scenario, evaluated, round, options.MaxRounds, requestedCount)},
	}

	response, err := ai.createChatCompletion(ctx, openRouterChatRequest{
		Model:       options.Model,
		Messages:    messages,
		Temperature: options.Temperature,
	})
	if err != nil {
		return profileSecuritySearchBatchGeneration{}, err
	}

	if len(response.Choices) == 0 {
		return profileSecuritySearchBatchGeneration{}, fmt.Errorf("openrouter returned no choices")
	}

	messageText := extractOpenRouterMessageText(response.Choices[0].Message.Content)
	decoded, err := decodeProfileSecuritySearchModelResponse(messageText)
	if err != nil {
		return profileSecuritySearchBatchGeneration{}, err
	}

	return profileSecuritySearchBatchGeneration{Candidates: decoded.Candidates, RawModelResponse: strings.TrimSpace(messageText)}, nil
}

func buildProfileSecuritySearchSystemPrompt() string {
	return strings.TrimSpace("You generate operating system security configuration candidates for Fleeti. " +
		"Return JSON only, with no markdown fences or commentary. " +
		"Use the exact schema requested by the user message. " +
		"Each candidate must be meaningfully distinct, realistic, and compatible with the stated goal. " +
		"Prefer conservative hardening while preserving the goal's required services. " +
		"If password quality or password expiry is enabled, always include explicit positive numeric values. " +
		"Supported website blocking categories are fakenews, gambling, porn, and social.")
}

func buildProfileSecuritySearchBatchPrompt(profile ProfileSecuritySearchBaseProfile, currentSecurity ProfileSecurityConfig, goal string, scenario profileSecuritySearchScenario, evaluated []profileSecuritySearchCandidateView, round, totalRounds, requestedCount int) string {
	currentConfigJSON, _ := json.Marshal(profileSecurityAPIConfigFromModel(currentSecurity))
	scenarioJSON, _ := json.Marshal(profileSecuritySearchScenarioViewFromModel(scenario))
	priorJSON, _ := json.Marshal(profileSecuritySearchPromptCandidates(evaluated))

	baseConfigJSON := strings.TrimSpace(profile.ConfigJSON)
	if baseConfigJSON == "" {
		baseConfigJSON = defaultProfileWizardConfigJSON
	}

	return fmt.Sprintf(`Round %d of %d.
Generate exactly %d candidate security configurations for this profile and goal.

Return JSON in this exact shape:
{
  "candidates": [
    {
      "rationale": "short explanation",
      "config": {
        "firewall": {
          "enable": true,
          "allowed_tcp_ports": [22, 443],
          "allowed_udp_ports": []
        },
        "blacklisted_kernel_modules": ["usb-storage"],
        "apparmor": { "enable": true },
        "password_policy": {
          "pwquality": {
            "enable": true,
            "minimum_length": 14,
            "minimum_digits": 1,
            "minimum_upper": 1,
            "minimum_lower": 1,
            "minimum_other": 1,
            "retry_count": 3
          },
          "expiry": {
            "enable": true,
            "maximum_days": 90,
            "warning_days": 14
          }
        },
        "website_blocking": {
          "enable": false,
          "block_categories": []
        }
      }
    }
  ]
}

Requirements:
- Produce exactly %d candidates.
- All candidates must be distinct from one another and from previously evaluated candidates.
- Always include every key shown above. Use empty arrays or false when a feature is disabled.
- Minimize open ports unless the goal clearly needs them.
- Match the goal's service requirements and security posture.
- Keep rationales short and concrete.

Profile name: %s
Profile description: %s
Goal: %s
Current security summary: %s
Current security JSON: %s
Base profile config JSON (security is evaluated on top of this): %s
Evaluator scenario JSON: %s
Previously evaluated candidate summary JSON: %s
`, round, totalRounds, requestedCount, requestedCount,
		escapePromptJSONString(profile.Name),
		escapePromptJSONString(profile.Description),
		escapePromptJSONString(goal),
		escapePromptJSONString(profileSecuritySummary(currentSecurity)),
		string(currentConfigJSON),
		escapePromptJSONString(baseConfigJSON),
		string(scenarioJSON),
		string(priorJSON),
	)
}

func decodeProfileSecuritySearchModelResponse(raw string) (profileSecuritySearchLLMResponse, error) {
	raw = extractProfileSecuritySearchJSON(raw)
	if raw == "" {
		return profileSecuritySearchLLMResponse{}, fmt.Errorf("profile security search returned no JSON")
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()

	var decoded profileSecuritySearchLLMResponse
	if err := decoder.Decode(&decoded); err != nil {
		return profileSecuritySearchLLMResponse{}, fmt.Errorf("profile security search returned invalid JSON")
	}

	if len(decoded.Candidates) == 0 {
		return profileSecuritySearchLLMResponse{}, fmt.Errorf("profile security search returned no candidates")
	}

	return decoded, nil
}

func extractProfileSecuritySearchJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	if strings.HasPrefix(raw, "```") {
		lines := strings.Split(raw, "\n")
		if len(lines) >= 2 {
			lines = lines[1:]
		}
		for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "```" {
			lines = lines[:len(lines)-1]
		}
		raw = strings.TrimSpace(strings.Join(lines, "\n"))
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end >= start {
		return strings.TrimSpace(raw[start : end+1])
	}

	return raw
}

func evaluateProfileSecuritySearchCandidate(ctx context.Context, profile ProfileSecuritySearchBaseProfile, config ProfileSecurityConfig, scenario profileSecuritySearchScenario, rationale, hash string) profileSecuritySearchCandidateView {
	config = normalizeProfileSecurityConfig(config)
	config.Configured = true

	evaluation := profileSecuritySearchCandidateEvaluation{
		Errors:                 []string{},
		Warnings:               []string{},
		RequiredTCPPorts:       append([]int(nil), scenario.RequiredTCPPorts...),
		RequiredUDPPorts:       append([]int(nil), scenario.RequiredUDPPorts...),
		MissingTCPPorts:        missingProfileSecurityPorts(config.Firewall.AllowedTCPPorts, scenario.RequiredTCPPorts),
		MissingUDPPorts:        missingProfileSecurityPorts(config.Firewall.AllowedUDPPorts, scenario.RequiredUDPPorts),
		ExtraTCPPorts:          extraProfileSecurityPorts(config.Firewall.AllowedTCPPorts, scenario.RequiredTCPPorts),
		ExtraUDPPorts:          extraProfileSecurityPorts(config.Firewall.AllowedUDPPorts, scenario.RequiredUDPPorts),
		FirewallEnabled:        config.Firewall.Enable,
		AppArmorEnabled:        config.AppArmor.Enable,
		PWQualityEnabled:       config.PasswordPolicy.PWQuality.Enable,
		PasswordExpiryEnabled:  config.PasswordPolicy.Expiry.Enable,
		WebsiteBlockingEnabled: config.WebsiteBlocking.Enable,
		BlacklistedModuleCount: len(config.BlacklistedKernelModules),
		EnabledFeatureCount:    enabledProfileSecurityFeatureCount(config),
		OpenTCPPortCount:       len(config.Firewall.AllowedTCPPorts),
		OpenUDPPortCount:       len(config.Firewall.AllowedUDPPorts),
		NixEvaluation: profileNixEvaluationResult{
			Valid:    false,
			Errors:   []string{},
			Warnings: []string{},
		},
	}

	validationErr := validateProfileSecurityConfig(config)
	if validationErr != nil {
		evaluation.Errors = append(evaluation.Errors, validationErr.Error())
	} else {
		baseConfigJSON := strings.TrimSpace(profile.ConfigJSON)
		if baseConfigJSON == "" {
			baseConfigJSON = defaultProfileWizardConfigJSON
		}

		configJSON, err := profileConfigWithSecurity(baseConfigJSON, config)
		if err != nil {
			evaluation.Errors = append(evaluation.Errors, err.Error())
		} else {
			input := db.CreateProfileInput{
				FleetIDs:            append([]string(nil), profile.FleetIDs...),
				Name:                strings.TrimSpace(profile.Name),
				Description:         strings.TrimSpace(profile.Description),
				ConfigJSON:          configJSON,
				RawNix:              strings.TrimSpace(profile.RawNix),
				ConfigSchemaVersion: profile.ConfigSchemaVersion,
			}

			evaluation.NixEvaluation = evaluateProfileInputAgainstPinnedNix(ctx, input)
			if !evaluation.NixEvaluation.Valid {
				evaluation.Warnings = append(evaluation.Warnings, cleanProfileSecuritySearchMessages(evaluation.NixEvaluation.Errors)...)
			}
		}
	}

	if len(evaluation.MissingTCPPorts) > 0 {
		evaluation.Errors = append(evaluation.Errors, fmt.Sprintf("Missing required TCP ports: %s", formatProfileSecuritySearchPorts(evaluation.MissingTCPPorts)))
	}

	if len(evaluation.MissingUDPPorts) > 0 {
		evaluation.Errors = append(evaluation.Errors, fmt.Sprintf("Missing required UDP ports: %s", formatProfileSecuritySearchPorts(evaluation.MissingUDPPorts)))
	}

	if len(evaluation.ExtraTCPPorts) > 0 {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("Additional TCP ports exposed: %s", formatProfileSecuritySearchPorts(evaluation.ExtraTCPPorts)))
	}

	if len(evaluation.ExtraUDPPorts) > 0 {
		evaluation.Warnings = append(evaluation.Warnings, fmt.Sprintf("Additional UDP ports exposed: %s", formatProfileSecuritySearchPorts(evaluation.ExtraUDPPorts)))
	}

	evaluation.Errors = uniqueSortedStrings(evaluation.Errors)
	evaluation.Warnings = uniqueSortedStrings(evaluation.Warnings)
	evaluation.Valid = len(evaluation.Errors) == 0 && evaluation.NixEvaluation.Valid

	return profileSecuritySearchCandidateView{
		ID:         hash,
		Score:      scoreProfileSecuritySearchCandidate(config, evaluation),
		Summary:    profileSecuritySummary(config),
		Rationale:  strings.TrimSpace(rationale),
		Config:     profileSecurityAPIConfigFromModel(config),
		Evaluation: evaluation,
	}
}

func scoreProfileSecuritySearchCandidate(config ProfileSecurityConfig, evaluation profileSecuritySearchCandidateEvaluation) int {
	score := 0
	if evaluation.NixEvaluation.Valid {
		score += 40
	} else {
		score -= 60
	}

	if config.Firewall.Enable {
		score += 20
	} else {
		score -= 20
	}

	if config.AppArmor.Enable {
		score += 15
	}

	if config.PasswordPolicy.PWQuality.Enable {
		score += 12
		score += min(config.PasswordPolicy.PWQuality.MinimumLength, 20) - 8
	}

	if config.PasswordPolicy.Expiry.Enable {
		score += 8
	}

	if config.WebsiteBlocking.Enable {
		score += 5
	}

	score += min(len(config.BlacklistedKernelModules), 4) * 2
	score -= len(evaluation.MissingTCPPorts) * 40
	score -= len(evaluation.MissingUDPPorts) * 35
	score -= len(evaluation.ExtraTCPPorts) * 4
	score -= len(evaluation.ExtraUDPPorts) * 3
	score -= len(evaluation.Errors) * 10

	return score
}

func rankedProfileSecuritySearchCandidates(candidates []profileSecuritySearchCandidateView) []profileSecuritySearchCandidateView {
	ranked := cloneProfileSecuritySearchCandidates(candidates)
	sort.SliceStable(ranked, func(left, right int) bool {
		if ranked[left].Score != ranked[right].Score {
			return ranked[left].Score > ranked[right].Score
		}

		if ranked[left].Evaluation.Valid != ranked[right].Evaluation.Valid {
			return ranked[left].Evaluation.Valid
		}

		if ranked[left].Evaluation.NixEvaluation.Valid != ranked[right].Evaluation.NixEvaluation.Valid {
			return ranked[left].Evaluation.NixEvaluation.Valid
		}

		return ranked[left].ID < ranked[right].ID
	})

	for index := range ranked {
		ranked[index].Rank = index + 1
	}

	return ranked
}

func profileSecuritySearchScenarioFromGoal(goal string) profileSecuritySearchScenario {
	goal = strings.ToLower(strings.TrimSpace(goal))
	requiredTCP := []int{}
	requiredUDP := []int{}
	signals := []string{}

	addSignal := func(signal string) {
		signal = strings.TrimSpace(signal)
		if signal == "" {
			return
		}

		for _, existing := range signals {
			if existing == signal {
				return
			}
		}

		signals = append(signals, signal)
	}

	addPortsForTerms := func(terms []string, tcpPorts, udpPorts []int, signal string) {
		for _, term := range terms {
			if !strings.Contains(goal, term) {
				continue
			}

			requiredTCP = append(requiredTCP, tcpPorts...)
			requiredUDP = append(requiredUDP, udpPorts...)
			addSignal(signal)
			return
		}
	}

	addPortsForTerms([]string{"ssh", "secure shell"}, []int{22}, nil, "ssh")
	addPortsForTerms([]string{"https", "tls", "web server", "reverse proxy"}, []int{443}, nil, "https")
	addPortsForTerms([]string{"http ", " http", "website", "frontend"}, []int{80}, nil, "http")
	addPortsForTerms([]string{"dns", "resolver", "nameserver"}, []int{53}, []int{53}, "dns")
	addPortsForTerms([]string{"ntp", "time sync"}, nil, []int{123}, "ntp")
	addPortsForTerms([]string{"smtp", "mail relay"}, []int{25, 465, 587}, nil, "smtp")
	addPortsForTerms([]string{"imap"}, []int{143, 993}, nil, "imap")
	addPortsForTerms([]string{"postgres", "postgresql"}, []int{5432}, nil, "postgresql")
	addPortsForTerms([]string{"mysql", "mariadb"}, []int{3306}, nil, "mysql")
	addPortsForTerms([]string{"redis"}, []int{6379}, nil, "redis")
	addPortsForTerms([]string{"grafana"}, []int{3000}, nil, "grafana")
	addPortsForTerms([]string{"prometheus"}, []int{9090}, nil, "prometheus")
	addPortsForTerms([]string{"kubernetes", "k8s"}, []int{6443}, nil, "kubernetes")

	for _, segment := range profileSecuritySearchPortPhrasePattern.FindAllString(goal, -1) {
		for _, match := range profileSecuritySearchPortPattern.FindAllString(segment, -1) {
			port, err := strconv.Atoi(match)
			if err != nil || port < 1 || port > 65535 {
				continue
			}

			requiredTCP = append(requiredTCP, port)
			addSignal("explicit-port")
		}
	}

	return profileSecuritySearchScenario{
		RequiredTCPPorts: normalizeProfileSecurityPortList(requiredTCP),
		RequiredUDPPorts: normalizeProfileSecurityPortList(requiredUDP),
		Signals:          uniqueSortedStrings(signals),
	}
}

func profileSecuritySearchScenarioViewFromModel(scenario profileSecuritySearchScenario) profileSecuritySearchScenarioView {
	return profileSecuritySearchScenarioView{
		RequiredTCPPorts: append([]int(nil), scenario.RequiredTCPPorts...),
		RequiredUDPPorts: append([]int(nil), scenario.RequiredUDPPorts...),
		Signals:          append([]string(nil), scenario.Signals...),
	}
}

func profileSecuritySearchPromptCandidates(evaluated []profileSecuritySearchCandidateView) []profileSecuritySearchPromptCandidate {
	if len(evaluated) == 0 {
		return []profileSecuritySearchPromptCandidate{}
	}

	sorted := append([]profileSecuritySearchCandidateView(nil), evaluated...)
	sort.SliceStable(sorted, func(left, right int) bool {
		if sorted[left].Score != sorted[right].Score {
			return sorted[left].Score > sorted[right].Score
		}

		return sorted[left].ID < sorted[right].ID
	})

	selected := make([]profileSecuritySearchCandidateView, 0, profileSecuritySearchPromptTopCount+profileSecuritySearchPromptWorstCount)
	selected = append(selected, sorted[:min(len(sorted), profileSecuritySearchPromptTopCount)]...)
	if len(sorted) > profileSecuritySearchPromptTopCount {
		start := len(sorted) - profileSecuritySearchPromptWorstCount
		if start < profileSecuritySearchPromptTopCount {
			start = profileSecuritySearchPromptTopCount
		}
		selected = append(selected, sorted[start:]...)
	}

	promptCandidates := make([]profileSecuritySearchPromptCandidate, 0, len(selected))
	for _, candidate := range selected {
		promptCandidates = append(promptCandidates, profileSecuritySearchPromptCandidate{
			Score:           candidate.Score,
			Summary:         candidate.Summary,
			Rationale:       candidate.Rationale,
			MissingTCPPorts: append([]int(nil), candidate.Evaluation.MissingTCPPorts...),
			MissingUDPPorts: append([]int(nil), candidate.Evaluation.MissingUDPPorts...),
			ExtraTCPPorts:   append([]int(nil), candidate.Evaluation.ExtraTCPPorts...),
			ExtraUDPPorts:   append([]int(nil), candidate.Evaluation.ExtraUDPPorts...),
			Errors:          append([]string(nil), candidate.Evaluation.Errors...),
			Warnings:        append([]string(nil), candidate.Evaluation.Warnings...),
			NixValid:        candidate.Evaluation.NixEvaluation.Valid,
		})
	}

	return promptCandidates
}

func profileSecurityAPIConfigFromModel(config ProfileSecurityConfig) profileSecurityAPIConfig {
	config = normalizeProfileSecurityConfig(config)
	config = applyProfileSecuritySearchDefaults(config)

	return profileSecurityAPIConfig{
		Firewall: profileSecurityAPIFirewallConfig{
			Enable:          config.Firewall.Enable,
			AllowedTCPPorts: append([]int(nil), config.Firewall.AllowedTCPPorts...),
			AllowedUDPPorts: append([]int(nil), config.Firewall.AllowedUDPPorts...),
		},
		BlacklistedKernelModules: append([]string(nil), config.BlacklistedKernelModules...),
		AppArmor:                 profileSecurityAPIAppArmorConfig{Enable: config.AppArmor.Enable},
		PasswordPolicy: profileSecurityAPIPasswordPolicyConfig{
			PWQuality: profileSecurityAPIPWQualityConfig{
				Enable:        config.PasswordPolicy.PWQuality.Enable,
				MinimumLength: config.PasswordPolicy.PWQuality.MinimumLength,
				MinimumDigits: config.PasswordPolicy.PWQuality.MinimumDigits,
				MinimumUpper:  config.PasswordPolicy.PWQuality.MinimumUpper,
				MinimumLower:  config.PasswordPolicy.PWQuality.MinimumLower,
				MinimumOther:  config.PasswordPolicy.PWQuality.MinimumOther,
				RetryCount:    config.PasswordPolicy.PWQuality.RetryCount,
			},
			Expiry: profileSecurityAPIPasswordExpiryConfig{
				Enable:      config.PasswordPolicy.Expiry.Enable,
				MaximumDays: config.PasswordPolicy.Expiry.MaximumDays,
				WarningDays: config.PasswordPolicy.Expiry.WarningDays,
			},
		},
		WebsiteBlocking: profileSecurityAPIWebsiteBlockingConfig{
			Enable:          config.WebsiteBlocking.Enable,
			BlockCategories: append([]string(nil), config.WebsiteBlocking.BlockCategories...),
		},
	}
}

func (config profileSecurityAPIConfig) toModel() ProfileSecurityConfig {
	model := ProfileSecurityConfig{
		Configured: true,
		Firewall: ProfileSecurityFirewallConfig{
			Enable:          config.Firewall.Enable,
			AllowedTCPPorts: append([]int(nil), config.Firewall.AllowedTCPPorts...),
			AllowedUDPPorts: append([]int(nil), config.Firewall.AllowedUDPPorts...),
		},
		BlacklistedKernelModules: append([]string(nil), config.BlacklistedKernelModules...),
		AppArmor:                 ProfileSecurityAppArmorConfig{Enable: config.AppArmor.Enable},
		PasswordPolicy: ProfileSecurityPasswordPolicyConfig{
			PWQuality: ProfileSecurityPWQualityConfig{
				Enable:        config.PasswordPolicy.PWQuality.Enable,
				MinimumLength: config.PasswordPolicy.PWQuality.MinimumLength,
				MinimumDigits: config.PasswordPolicy.PWQuality.MinimumDigits,
				MinimumUpper:  config.PasswordPolicy.PWQuality.MinimumUpper,
				MinimumLower:  config.PasswordPolicy.PWQuality.MinimumLower,
				MinimumOther:  config.PasswordPolicy.PWQuality.MinimumOther,
				RetryCount:    config.PasswordPolicy.PWQuality.RetryCount,
			},
			Expiry: ProfileSecurityPasswordExpiryConfig{
				Enable:      config.PasswordPolicy.Expiry.Enable,
				MaximumDays: config.PasswordPolicy.Expiry.MaximumDays,
				WarningDays: config.PasswordPolicy.Expiry.WarningDays,
			},
		},
		WebsiteBlocking: ProfileSecurityWebsiteBlockingConfig{
			Enable:          config.WebsiteBlocking.Enable,
			BlockCategories: append([]string(nil), config.WebsiteBlocking.BlockCategories...),
		},
	}

	return applyProfileSecuritySearchDefaults(normalizeProfileSecurityConfig(model))
}

func applyProfileSecuritySearchDefaults(config ProfileSecurityConfig) ProfileSecurityConfig {
	config = normalizeProfileSecurityConfig(config)
	config.Configured = true

	if config.PasswordPolicy.PWQuality.Enable {
		if config.PasswordPolicy.PWQuality.MinimumLength == 0 {
			config.PasswordPolicy.PWQuality.MinimumLength = defaultProfileSecurityPWQualityMinimumLength
		}
		if config.PasswordPolicy.PWQuality.RetryCount == 0 {
			config.PasswordPolicy.PWQuality.RetryCount = defaultProfileSecurityPWQualityRetryCount
		}
		if config.PasswordPolicy.PWQuality.MinimumDigits == 0 {
			config.PasswordPolicy.PWQuality.MinimumDigits = 1
		}
		if config.PasswordPolicy.PWQuality.MinimumUpper == 0 {
			config.PasswordPolicy.PWQuality.MinimumUpper = 1
		}
		if config.PasswordPolicy.PWQuality.MinimumLower == 0 {
			config.PasswordPolicy.PWQuality.MinimumLower = 1
		}
		if config.PasswordPolicy.PWQuality.MinimumOther == 0 {
			config.PasswordPolicy.PWQuality.MinimumOther = 1
		}
	}

	if config.PasswordPolicy.Expiry.Enable {
		if config.PasswordPolicy.Expiry.MaximumDays == 0 {
			config.PasswordPolicy.Expiry.MaximumDays = defaultProfileSecurityPasswordExpiryMaxDays
		}
		if config.PasswordPolicy.Expiry.WarningDays == 0 {
			config.PasswordPolicy.Expiry.WarningDays = defaultProfileSecurityPasswordExpiryWarnDays
		}
	}

	return config
}

func profileSecuritySearchConfigHash(config ProfileSecurityConfig) (string, error) {
	encoded, err := json.Marshal(profileSecurityConfigValue(applyProfileSecuritySearchDefaults(config)))
	if err != nil {
		return "", fmt.Errorf("failed to hash security search config: %w", err)
	}

	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:6]), nil
}

func enabledProfileSecurityFeatureCount(config ProfileSecurityConfig) int {
	count := 0
	if config.Firewall.Enable {
		count++
	}
	if config.AppArmor.Enable {
		count++
	}
	if config.PasswordPolicy.PWQuality.Enable {
		count++
	}
	if config.PasswordPolicy.Expiry.Enable {
		count++
	}
	if config.WebsiteBlocking.Enable {
		count++
	}
	if len(config.BlacklistedKernelModules) > 0 {
		count++
	}

	return count
}

func missingProfileSecurityPorts(openPorts, requiredPorts []int) []int {
	if len(requiredPorts) == 0 {
		return []int{}
	}

	openSet := make(map[int]struct{}, len(openPorts))
	for _, port := range openPorts {
		openSet[port] = struct{}{}
	}

	missing := make([]int, 0, len(requiredPorts))
	for _, port := range requiredPorts {
		if _, ok := openSet[port]; ok {
			continue
		}

		missing = append(missing, port)
	}

	return normalizeProfileSecurityPortList(missing)
}

func extraProfileSecurityPorts(openPorts, requiredPorts []int) []int {
	if len(openPorts) == 0 {
		return []int{}
	}

	requiredSet := make(map[int]struct{}, len(requiredPorts))
	for _, port := range requiredPorts {
		requiredSet[port] = struct{}{}
	}

	extra := make([]int, 0, len(openPorts))
	for _, port := range openPorts {
		if _, ok := requiredSet[port]; ok {
			continue
		}

		extra = append(extra, port)
	}

	return normalizeProfileSecurityPortList(extra)
}

func formatProfileSecuritySearchPorts(ports []int) string {
	if len(ports) == 0 {
		return "none"
	}

	parts := make([]string, 0, len(ports))
	for _, port := range ports {
		parts = append(parts, strconv.Itoa(port))
	}

	return strings.Join(parts, ", ")
}

func cleanProfileSecuritySearchMessages(messages []string) []string {
	cleaned := make([]string, 0, len(messages))
	for _, message := range messages {
		trimmed := strings.TrimSpace(message)
		if trimmed == "" {
			continue
		}

		cleaned = append(cleaned, trimmed)
	}

	return cleaned
}

func escapePromptJSONString(value string) string {
	encoded, err := json.Marshal(strings.TrimSpace(value))
	if err != nil {
		return `""`
	}

	return string(encoded)
}
