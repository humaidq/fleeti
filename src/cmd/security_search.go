/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/humaidq/fleeti/v2/routes"
)

type securitySearchRunArtifact struct {
	RunID        string                                    `json:"run_id"`
	TimestampUTC string                                    `json:"timestamp_utc"`
	GitCommit    string                                    `json:"git_commit,omitempty"`
	AppVersion   string                                    `json:"app_version,omitempty"`
	Status       string                                    `json:"status"`
	DurationMS   int64                                     `json:"duration_ms"`
	Error        string                                    `json:"error,omitempty"`
	Runner       securitySearchRunnerArtifact              `json:"runner"`
	Profile      securitySearchProfileArtifact             `json:"profile"`
	Search       securitySearchConfigArtifact              `json:"search"`
	Summary      securitySearchSummaryArtifact             `json:"summary"`
	Rounds       []routes.ProfileSecuritySearchRoundResult `json:"rounds"`
	Candidates   []routes.ProfileSecuritySearchCandidate   `json:"candidates"`
}

type securitySearchRunnerArtifact struct {
	Command string `json:"command"`
	Label   string `json:"label,omitempty"`
}

type securitySearchProfileArtifact struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Revision            int    `json:"revision"`
	ConfigSchemaVersion int    `json:"config_schema_version"`
}

type securitySearchConfigArtifact struct {
	Goal                 string                               `json:"goal"`
	Scenario             routes.ProfileSecuritySearchScenario `json:"scenario"`
	Model                string                               `json:"model"`
	Temperature          float64                              `json:"temperature"`
	TargetCandidateCount int                                  `json:"target_candidate_count"`
	BatchSize            int                                  `json:"batch_size"`
	MaxRounds            int                                  `json:"max_rounds"`
	AllowPartial         bool                                 `json:"allow_partial"`
	SaveRawResponses     bool                                 `json:"save_raw_responses"`
}

type securitySearchSummaryArtifact struct {
	EvaluatedCandidateCount int     `json:"evaluated_candidate_count"`
	UniqueCandidateCount    int     `json:"unique_candidate_count"`
	DuplicateCandidateCount int     `json:"duplicate_candidate_count"`
	ValidCandidateCount     int     `json:"valid_candidate_count"`
	NixValidCandidateCount  int     `json:"nix_valid_candidate_count"`
	BestScore               int     `json:"best_score"`
	AverageScore            float64 `json:"average_score"`
}

var CmdSecuritySearch = &cli.Command{
	Name:  "security-search",
	Usage: "Security search experiment commands",
	Commands: []*cli.Command{
		{
			Name:  "run",
			Usage: "Run a profile security search experiment",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "goal",
					Usage: "natural-language security goal for the search",
				},
				&cli.StringFlag{
					Name:  "output-dir",
					Usage: "directory where JSON run artifacts are written",
				},
				&cli.StringFlag{
					Name:  "label",
					Usage: "optional short label used in the artifact filename and metadata",
				},
				&cli.StringFlag{
					Name:  "model",
					Usage: "optional model override for this run",
				},
				&cli.Float64Flag{
					Name:  "temperature",
					Value: routes.DefaultProfileSecuritySearchRunOptions("").Temperature,
					Usage: "sampling temperature for candidate generation",
				},
				&cli.IntFlag{
					Name:  "target-count",
					Value: routes.DefaultProfileSecuritySearchRunOptions("").TargetCandidateCount,
					Usage: "number of unique candidates to evaluate",
				},
				&cli.IntFlag{
					Name:  "batch-size",
					Value: routes.DefaultProfileSecuritySearchRunOptions("").BatchSize,
					Usage: "number of candidates requested from the model per round",
				},
				&cli.IntFlag{
					Name:  "rounds",
					Value: routes.DefaultProfileSecuritySearchRunOptions("").MaxRounds,
					Usage: "maximum number of search rounds",
				},
				&cli.BoolFlag{
					Name:  "allow-partial",
					Value: true,
					Usage: "keep partial results when a later batch fails",
				},
				&cli.BoolFlag{
					Name:  "save-raw-responses",
					Usage: "include raw model JSON text for each round in the artifact",
				},
			},
			Action: runSecuritySearch,
		},
	},
}

func runSecuritySearch(ctx context.Context, cmd *cli.Command) error {
	goal := strings.TrimSpace(cmd.String("goal"))
	if goal == "" {
		return errSecuritySearchGoalRequired
	}

	outputDir := strings.TrimSpace(cmd.String("output-dir"))
	if outputDir == "" {
		return errSecuritySearchOutputDirRequired
	}

	ai := routes.NewProfileWizardAIFromEnv()
	if ai == nil || !ai.Enabled() {
		return fmt.Errorf("%s", ai.DisabledReason())
	}

	options := routes.DefaultProfileSecuritySearchRunOptions(goal)
	options.Model = strings.TrimSpace(cmd.String("model"))
	options.Temperature = cmd.Float64("temperature")
	options.TargetCandidateCount = cmd.Int("target-count")
	options.BatchSize = cmd.Int("batch-size")
	options.MaxRounds = cmd.Int("rounds")
	options.AllowPartial = cmd.Bool("allow-partial")
	options.SaveRawResponses = cmd.Bool("save-raw-responses")
	profile := routes.DefaultProfileSecuritySearchBaseProfile()

	startedAt := time.Now().UTC()
	result, runErr := ai.RunProfileSecuritySearch(ctx, profile, options, nil)
	duration := time.Since(startedAt)

	artifact := buildSecuritySearchRunArtifact(startedAt, duration, profile, options, result, runErr, strings.TrimSpace(cmd.String("label")))
	artifactPath, err := writeSecuritySearchRunArtifact(outputDir, artifact)
	if err != nil {
		return err
	}

	fmt.Printf("Saved security search artifact to %s\n", artifactPath)
	fmt.Printf("Status: %s\n", artifact.Status)
	fmt.Printf("Candidates: %d unique, %d duplicates, %d valid, best score %d\n", artifact.Summary.UniqueCandidateCount, artifact.Summary.DuplicateCandidateCount, artifact.Summary.ValidCandidateCount, artifact.Summary.BestScore)

	if runErr != nil {
		return runErr
	}

	return nil
}

func buildSecuritySearchRunArtifact(startedAt time.Time, duration time.Duration, profile routes.ProfileSecuritySearchBaseProfile, options routes.ProfileSecuritySearchRunOptions, result routes.ProfileSecuritySearchRunResult, runErr error, label string) securitySearchRunArtifact {
	status := "succeeded"
	if runErr != nil {
		status = "failed"
	} else if result.UniqueCandidateCount < result.TargetCandidateCount {
		status = "partial"
	}

	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}

	goal := result.Goal
	if strings.TrimSpace(goal) == "" {
		goal = options.Goal
	}

	model := result.Model
	if strings.TrimSpace(model) == "" {
		model = options.Model
	}

	return securitySearchRunArtifact{
		RunID:        securitySearchRunID(startedAt, label, profile.Name),
		TimestampUTC: startedAt.Format(time.RFC3339),
		GitCommit:    buildCommitForArtifact(),
		AppVersion:   BuildDisplayVersion(),
		Status:       status,
		DurationMS:   duration.Milliseconds(),
		Error:        errorMessage,
		Runner: securitySearchRunnerArtifact{
			Command: strings.Join(os.Args, " "),
			Label:   strings.TrimSpace(label),
		},
		Profile: securitySearchProfileArtifact{
			ID:                  strings.TrimSpace(profile.ID),
			Name:                strings.TrimSpace(profile.Name),
			Revision:            profile.Revision,
			ConfigSchemaVersion: profile.ConfigSchemaVersion,
		},
		Search: securitySearchConfigArtifact{
			Goal:                 goal,
			Scenario:             result.Scenario,
			Model:                model,
			Temperature:          firstNonZeroFloat(result.Temperature, options.Temperature),
			TargetCandidateCount: firstNonZeroInt(result.TargetCandidateCount, options.TargetCandidateCount),
			BatchSize:            firstNonZeroInt(result.BatchSize, options.BatchSize),
			MaxRounds:            firstNonZeroInt(result.MaxRounds, options.MaxRounds),
			AllowPartial:         result.AllowPartial || options.AllowPartial,
			SaveRawResponses:     options.SaveRawResponses,
		},
		Summary: securitySearchSummaryArtifact{
			EvaluatedCandidateCount: result.EvaluatedCandidateCount,
			UniqueCandidateCount:    result.UniqueCandidateCount,
			DuplicateCandidateCount: result.DuplicateCandidateCount,
			ValidCandidateCount:     result.ValidCandidateCount,
			NixValidCandidateCount:  result.NixValidCandidateCount,
			BestScore:               result.BestScore,
			AverageScore:            result.AverageScore,
		},
		Rounds:     append([]routes.ProfileSecuritySearchRoundResult(nil), result.Rounds...),
		Candidates: append([]routes.ProfileSecuritySearchCandidate(nil), result.Candidates...),
	}
}

func writeSecuritySearchRunArtifact(outputDir string, artifact securitySearchRunArtifact) (string, error) {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return "", errSecuritySearchOutputDirRequired
	}

	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	path, err := nextSecuritySearchArtifactPath(outputDir, artifact.RunID)
	if err != nil {
		return "", err
	}

	encoded, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to encode security search artifact: %w", err)
	}

	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o640); err != nil {
		return "", fmt.Errorf("failed to write security search artifact: %w", err)
	}

	return path, nil
}

func nextSecuritySearchArtifactPath(outputDir, runID string) (string, error) {
	baseName := strings.TrimSpace(runID)
	if baseName == "" {
		baseName = "security-search"
	}

	for suffix := 0; ; suffix++ {
		name := baseName
		if suffix > 0 {
			name += "-" + strconv.Itoa(suffix+1)
		}

		path := filepath.Join(outputDir, name+".json")
		_, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			return path, nil
		}
		if err != nil {
			return "", fmt.Errorf("failed to inspect artifact path: %w", err)
		}
	}
}

func securitySearchRunID(startedAt time.Time, label, profileName string) string {
	slug := slugifyArtifactSegment(label)
	if slug == "" {
		slug = slugifyArtifactSegment(profileName)
	}
	if slug == "" {
		slug = "security-search"
	}

	return startedAt.UTC().Format("20060102T150405Z") + "-" + slug
}

func buildCommitForArtifact() string {
	commit := strings.TrimSpace(BuildCommit)
	if commit == "" || commit == "unknown" {
		return ""
	}

	return commit
}

func firstNonZeroInt(primary, fallback int) int {
	if primary != 0 {
		return primary
	}

	return fallback
}

func firstNonZeroFloat(primary, fallback float64) float64 {
	if primary != 0 {
		return primary
	}

	return fallback
}

func slugifyArtifactSegment(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastDash = false
			continue
		}

		if lastDash || builder.Len() == 0 {
			continue
		}

		builder.WriteByte('-')
		lastDash = true
	}

	result := strings.Trim(builder.String(), "-")
	if len(result) > 48 {
		result = strings.Trim(result[:48], "-")
	}

	return result
}
