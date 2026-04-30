/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"fmt"
	"strings"
)

type ProfileSecuritySearchScenario = profileSecuritySearchScenarioView
type ProfileSecuritySearchCandidate = profileSecuritySearchCandidateView

type ProfileSecuritySearchBaseProfile struct {
	ID                  string   `json:"id,omitempty"`
	Name                string   `json:"name"`
	Description         string   `json:"description,omitempty"`
	Revision            int      `json:"revision,omitempty"`
	FleetIDs            []string `json:"fleet_ids"`
	ConfigSchemaVersion int      `json:"config_schema_version"`
	ConfigJSON          string   `json:"config_json"`
	RawNix              string   `json:"raw_nix,omitempty"`
}

type ProfileSecuritySearchRunOptions struct {
	Goal                 string  `json:"goal"`
	Model                string  `json:"model,omitempty"`
	Temperature          float64 `json:"temperature"`
	TargetCandidateCount int     `json:"target_candidate_count"`
	BatchSize            int     `json:"batch_size"`
	MaxRounds            int     `json:"max_rounds"`
	AllowPartial         bool    `json:"allow_partial"`
	SaveRawResponses     bool    `json:"save_raw_responses"`
}

type ProfileSecuritySearchRoundResult struct {
	Round                   int      `json:"round"`
	RequestedCandidateCount int      `json:"requested_candidate_count"`
	GeneratedCandidateCount int      `json:"generated_candidate_count"`
	AcceptedCandidateCount  int      `json:"accepted_candidate_count"`
	DuplicateCandidateCount int      `json:"duplicate_candidate_count"`
	EvaluatedCandidateCount int      `json:"evaluated_candidate_count"`
	BestScoreAfterRound     int      `json:"best_score_after_round"`
	AverageScoreAfterRound  float64  `json:"average_score_after_round"`
	TopCandidateIDs         []string `json:"top_candidate_ids"`
	RawModelResponse        string   `json:"raw_model_response,omitempty"`
}

type ProfileSecuritySearchRunResult struct {
	Goal                    string                             `json:"goal"`
	Scenario                ProfileSecuritySearchScenario      `json:"scenario"`
	Model                   string                             `json:"model"`
	Temperature             float64                            `json:"temperature"`
	TargetCandidateCount    int                                `json:"target_candidate_count"`
	BatchSize               int                                `json:"batch_size"`
	MaxRounds               int                                `json:"max_rounds"`
	AllowPartial            bool                               `json:"allow_partial"`
	SaveRawResponses        bool                               `json:"save_raw_responses"`
	EvaluatedCandidateCount int                                `json:"evaluated_candidate_count"`
	UniqueCandidateCount    int                                `json:"unique_candidate_count"`
	DuplicateCandidateCount int                                `json:"duplicate_candidate_count"`
	ValidCandidateCount     int                                `json:"valid_candidate_count"`
	NixValidCandidateCount  int                                `json:"nix_valid_candidate_count"`
	BestScore               int                                `json:"best_score"`
	AverageScore            float64                            `json:"average_score"`
	Rounds                  []ProfileSecuritySearchRoundResult `json:"rounds"`
	Candidates              []ProfileSecuritySearchCandidate   `json:"candidates"`
}

func DefaultProfileSecuritySearchRunOptions(goal string) ProfileSecuritySearchRunOptions {
	return ProfileSecuritySearchRunOptions{
		Goal:                 strings.TrimSpace(goal),
		Temperature:          defaultProfileSecuritySearchTemperature,
		TargetCandidateCount: profileSecuritySearchTargetCount,
		BatchSize:            profileSecuritySearchBatchSize,
		MaxRounds:            profileSecuritySearchMaxRounds,
		AllowPartial:         true,
		SaveRawResponses:     false,
	}
}

func DefaultProfileSecuritySearchBaseProfile() ProfileSecuritySearchBaseProfile {
	return ProfileSecuritySearchBaseProfile{
		Name:                "security-search-base",
		Description:         "Minimal base profile for security search experiments",
		Revision:            1,
		FleetIDs:            []string{profileNixValidationFleetID},
		ConfigSchemaVersion: 1,
		ConfigJSON:          defaultProfileWizardConfigJSON,
		RawNix:              "",
	}
}

func (ai *ProfileWizardAI) RunProfileSecuritySearch(ctx context.Context, profile ProfileSecuritySearchBaseProfile, options ProfileSecuritySearchRunOptions, onProgress func(profileSecuritySearchProgress)) (ProfileSecuritySearchRunResult, error) {
	options, err := normalizeProfileSecuritySearchRunOptions(options)
	if err != nil {
		return ProfileSecuritySearchRunResult{}, err
	}

	if ai == nil || !ai.Enabled() {
		return ProfileSecuritySearchRunResult{}, fmt.Errorf("profile security search ai is disabled")
	}

	if strings.TrimSpace(options.Model) == "" {
		options.Model = strings.TrimSpace(ai.model)
	}

	profile = normalizeProfileSecuritySearchBaseProfile(profile)

	currentSecurity, err := profileSecurityConfigFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		return ProfileSecuritySearchRunResult{}, fmt.Errorf("failed to parse current security settings: %w", err)
	}

	return ai.searchProfileSecurityCandidates(ctx, profile, currentSecurity, options, onProgress)
}

func normalizeProfileSecuritySearchBaseProfile(profile ProfileSecuritySearchBaseProfile) ProfileSecuritySearchBaseProfile {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Description = strings.TrimSpace(profile.Description)
	profile.RawNix = strings.TrimSpace(profile.RawNix)
	profile.ConfigJSON = strings.TrimSpace(profile.ConfigJSON)

	if profile.Name == "" {
		profile.Name = DefaultProfileSecuritySearchBaseProfile().Name
	}

	if profile.Description == "" {
		profile.Description = DefaultProfileSecuritySearchBaseProfile().Description
	}

	if profile.ConfigJSON == "" {
		profile.ConfigJSON = defaultProfileWizardConfigJSON
	}

	if profile.ConfigSchemaVersion == 0 {
		profile.ConfigSchemaVersion = 1
	}

	if len(profile.FleetIDs) == 0 {
		profile.FleetIDs = append([]string(nil), DefaultProfileSecuritySearchBaseProfile().FleetIDs...)
	} else {
		profile.FleetIDs = append([]string(nil), profile.FleetIDs...)
	}

	if profile.Revision == 0 {
		profile.Revision = 1
	}

	return profile
}

func normalizeProfileSecuritySearchRunOptions(options ProfileSecuritySearchRunOptions) (ProfileSecuritySearchRunOptions, error) {
	goal := strings.TrimSpace(options.Goal)
	if goal == "" {
		return ProfileSecuritySearchRunOptions{}, fmt.Errorf("goal is required")
	}

	if len(goal) > profileSecuritySearchGoalMaxBytes {
		return ProfileSecuritySearchRunOptions{}, fmt.Errorf("goal is too long")
	}

	options.Goal = goal
	options.Model = strings.TrimSpace(options.Model)

	if options.Temperature == 0 {
		options.Temperature = defaultProfileSecuritySearchTemperature
	}

	if options.Temperature < 0 || options.Temperature > 2 {
		return ProfileSecuritySearchRunOptions{}, fmt.Errorf("temperature must be between 0 and 2")
	}

	if options.TargetCandidateCount == 0 {
		options.TargetCandidateCount = profileSecuritySearchTargetCount
	}

	if options.BatchSize == 0 {
		options.BatchSize = profileSecuritySearchBatchSize
	}

	if options.MaxRounds == 0 {
		options.MaxRounds = profileSecuritySearchMaxRounds
	}

	if options.TargetCandidateCount < 1 {
		return ProfileSecuritySearchRunOptions{}, fmt.Errorf("target candidate count must be at least 1")
	}

	if options.BatchSize < 1 {
		return ProfileSecuritySearchRunOptions{}, fmt.Errorf("batch size must be at least 1")
	}

	if options.MaxRounds < 1 {
		return ProfileSecuritySearchRunOptions{}, fmt.Errorf("max rounds must be at least 1")
	}

	return options, nil
}

func summarizeProfileSecuritySearchCandidates(candidates []profileSecuritySearchCandidateView) (validCount int, nixValidCount int, bestScore int, averageScore float64) {
	if len(candidates) == 0 {
		return 0, 0, 0, 0
	}

	bestScore = candidates[0].Score
	totalScore := 0
	for _, candidate := range candidates {
		totalScore += candidate.Score
		if candidate.Evaluation.Valid {
			validCount++
		}
		if candidate.Evaluation.NixEvaluation.Valid {
			nixValidCount++
		}
		if candidate.Score > bestScore {
			bestScore = candidate.Score
		}
	}

	averageScore = float64(totalScore) / float64(len(candidates))

	return validCount, nixValidCount, bestScore, averageScore
}

func topProfileSecuritySearchCandidateIDs(candidates []profileSecuritySearchCandidateView, limit int) []string {
	if len(candidates) == 0 || limit <= 0 {
		return []string{}
	}

	ranked := rankedProfileSecuritySearchCandidates(candidates)
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}

	ids := make([]string, 0, len(ranked))
	for _, candidate := range ranked {
		ids = append(ids, candidate.ID)
	}

	return ids
}
