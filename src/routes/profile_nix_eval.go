/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	profileNixValidationVersion = "validation"
	profileNixValidationFleetID = "validation"
	nixOSConfigEvalTarget       = ".#nixosConfigurations.fleeti.config.system.build.toplevel.drvPath"
)

type profileNixEvaluationResult struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
	DrvPath  string   `json:"drv_path,omitempty"`
}

type nixOSOptionChildList struct {
	Path     string   `json:"path"`
	Leaf     bool     `json:"leaf"`
	Children []string `json:"children"`
	Error    string   `json:"error,omitempty"`
}

type nixOSOptionDescription struct {
	Path        string   `json:"path"`
	Found       bool     `json:"found"`
	Leaf        bool     `json:"leaf"`
	Description string   `json:"description,omitempty"`
	Type        string   `json:"type,omitempty"`
	Children    []string `json:"children,omitempty"`
	Error       string   `json:"error,omitempty"`
}

func evaluateProfileDraftAgainstPinnedNix(ctx context.Context, draft profileWizardDraft) profileNixEvaluationResult {
	draft = normalizeProfileWizardDraft(draft)
	packages, err := packagesFromProfileConfig(draft.ConfigJSON)
	if err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{mutationErrorMessage(err)}}
	}

	kernelConfig, err := profileKernelConfigFromProfileConfig(draft.ConfigJSON)
	if err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{mutationErrorMessage(err)}}
	}

	openclawEnabled, err := openclawMicrovmEnabledFromProfileConfig(draft.ConfigJSON)
	if err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{mutationErrorMessage(err)}}
	}

	securityConfig, err := profileSecurityConfigFromProfileConfig(draft.ConfigJSON)
	if err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{err.Error()}}
	}

	workspaceRoot, err := os.MkdirTemp("", "fleeti-nix-eval-*")
	if err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{fmt.Sprintf("failed to create temporary nix evaluation workspace: %v", err)}}
	}

	defer func() {
		if removeErr := os.RemoveAll(workspaceRoot); removeErr != nil {
			logger.Warn("failed to clean temporary nix evaluation workspace", "workspace", workspaceRoot, "error", removeErr)
		}
	}()

	workspaceNixOSDir := filepath.Join(workspaceRoot, nixosSourceDirName)
	if err := populateNixOSWorkspace(workspaceNixOSDir); err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{fmt.Sprintf("failed to prepare nix evaluation workspace: %v", err)}}
	}

	fleetID := profileNixValidationFleetID
	if len(draft.FleetIDs) > 0 {
		fleetID = strings.TrimSpace(draft.FleetIDs[0])
	}

	// Foreign imports are intentionally omitted from draft validation: they are
	// fetched from remote (possibly private) flakes and are validated at build
	// time. The generated module is a no-op stub here.
	if err := writeBuildOverridesModule(workspaceNixOSDir, profileNixValidationVersion, fleetID, packages, kernelConfig, securityConfig, openclawEnabled, draft.RawNix, nil); err != nil {
		return profileNixEvaluationResult{Valid: false, Errors: []string{err.Error()}}
	}

	drvPath, evalErr := runNixEvalCommand(ctx, workspaceNixOSDir, nixOSConfigEvalTarget)
	if evalErr != nil {
		return profileNixEvaluationResult{Valid: false, Errors: cleanNixEvalErrors(evalErr.Error())}
	}

	return profileNixEvaluationResult{Valid: true, Errors: []string{}, Warnings: []string{}, DrvPath: strings.TrimSpace(drvPath)}
}

func evaluateProfileInputAgainstPinnedNix(ctx context.Context, input db.CreateProfileInput) profileNixEvaluationResult {
	return evaluateProfileDraftAgainstPinnedNix(ctx, profileWizardDraft{
		Name:                strings.TrimSpace(input.Name),
		Description:         strings.TrimSpace(input.Description),
		FleetIDs:            append([]string(nil), input.FleetIDs...),
		ConfigJSON:          strings.TrimSpace(input.ConfigJSON),
		RawNix:              strings.TrimSpace(input.RawNix),
		ConfigSchemaVersion: input.ConfigSchemaVersion,
	})
}

func runNixEvalCommand(ctx context.Context, workdir string, target string) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", fmt.Errorf("nix eval target is required")
	}

	cmd := exec.CommandContext(ctx, "nix", "eval", "--raw", "--show-trace", target)
	cmd.Dir = workdir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		combined := strings.TrimSpace(stderr.String())
		if combined == "" {
			combined = strings.TrimSpace(stdout.String())
		}
		if combined == "" {
			combined = err.Error()
		}

		return "", fmt.Errorf("%s", combined)
	}

	return stdout.String(), nil
}

func cleanNixEvalErrors(message string) []string {
	message = strings.TrimSpace(message)
	if message == "" {
		return []string{"Nix evaluation failed"}
	}

	lines := strings.Split(message, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "warning:") || strings.HasPrefix(trimmed, "error (ignored):") || strings.HasPrefix(trimmed, "at ") || strings.HasPrefix(trimmed, "…") {
			continue
		}

		cleaned = append(cleaned, strings.TrimPrefix(trimmed, "error: "))
	}

	if len(cleaned) == 0 {
		cleaned = append(cleaned, message)
	}

	return uniqueSortedStrings(cleaned)
}

func listPinnedNixOSOptionChildren(ctx context.Context, path string, limit int) nixOSOptionChildList {
	normalizedPath, err := normalizeNixOSOptionPath(path)
	if err != nil {
		return nixOSOptionChildList{Path: strings.TrimSpace(path), Leaf: false, Children: []string{}, Error: err.Error()}
	}

	value, err := evalPinnedNixOSOptionNode(ctx, normalizedPath)
	if err != nil {
		return nixOSOptionChildList{Path: normalizedPath, Leaf: false, Children: []string{}, Error: err.Error()}
	}

	node, ok := value.(map[string]any)
	if !ok {
		return nixOSOptionChildList{Path: normalizedPath, Leaf: true, Children: []string{}}
	}

	if nodeType, _ := node["_type"].(string); nodeType == "option" {
		return nixOSOptionChildList{Path: normalizedPath, Leaf: true, Children: []string{}}
	}

	children := make([]string, 0, len(node))
	for child := range node {
		children = append(children, child)
	}

	sort.Strings(children)
	if limit > 0 && len(children) > limit {
		children = append([]string(nil), children[:limit]...)
	}

	return nixOSOptionChildList{Path: normalizedPath, Leaf: false, Children: children}
}

func describePinnedNixOSOption(ctx context.Context, path string) nixOSOptionDescription {
	normalizedPath, err := normalizeNixOSOptionPath(path)
	if err != nil {
		return nixOSOptionDescription{Path: strings.TrimSpace(path), Found: false, Error: err.Error()}
	}

	value, err := evalPinnedNixOSOptionNode(ctx, normalizedPath)
	if err != nil {
		return nixOSOptionDescription{Path: normalizedPath, Found: false, Error: err.Error()}
	}

	node, ok := value.(map[string]any)
	if !ok {
		return nixOSOptionDescription{Path: normalizedPath, Found: true, Leaf: true}
	}

	if nodeType, _ := node["_type"].(string); nodeType == "option" {
		description, _ := node["description"].(string)
		typeNode, _ := node["type"].(map[string]any)
		typeDescription, _ := typeNode["description"].(string)

		return nixOSOptionDescription{
			Path:        normalizedPath,
			Found:       true,
			Leaf:        true,
			Description: strings.TrimSpace(description),
			Type:        strings.TrimSpace(typeDescription),
		}
	}

	children := make([]string, 0, len(node))
	for child := range node {
		children = append(children, child)
	}

	sort.Strings(children)

	return nixOSOptionDescription{
		Path:     normalizedPath,
		Found:    true,
		Leaf:     false,
		Children: children,
	}
}

func evalPinnedNixOSOptionNode(ctx context.Context, path string) (any, error) {
	flakeDir, err := resolveNixOSFlakeDirectory()
	if err != nil {
		return nil, err
	}

	segments := []string{}
	if path != "" {
		segments = strings.Split(path, ".")
	}

	encodedPath, err := json.Marshal(segments)
	if err != nil {
		return nil, fmt.Errorf("failed to encode nix option path: %w", err)
	}

	expr := fmt.Sprintf(`
let
  flake = builtins.getFlake (toString ./.);
  root = flake.nixosConfigurations.fleeti.options;
  path = builtins.fromJSON ''%s'';
  resolve = current: remaining:
    if remaining == [] then current else resolve current.${builtins.head remaining} (builtins.tail remaining);
in
  resolve root path
`, string(encodedPath))

	cmd := exec.CommandContext(ctx, "nix", "eval", "--json", "--impure", "--expr", expr)
	cmd.Dir = flakeDir

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}

		return nil, fmt.Errorf("%s", strings.Join(cleanNixEvalErrors(message), "; "))
	}

	var decoded any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		return nil, fmt.Errorf("failed to decode nix option result: %w", err)
	}

	return decoded, nil
}

func normalizeNixOSOptionPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}

	segments := strings.Split(path, ".")
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			return "", fmt.Errorf("nix option path contains an empty segment")
		}

		for _, value := range trimmed {
			if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') || (value >= '0' && value <= '9') || value == '_' || value == '-' || value == '+' || value == '\'' {
				continue
			}

			return "", fmt.Errorf("nix option path contains unsupported characters")
		}

		normalized = append(normalized, trimmed)
	}

	return strings.Join(normalized, "."), nil
}
