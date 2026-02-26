/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const (
	packageSearchChannel     = "unstable"
	packageSearchTimeout     = 20 * time.Second
	packageSearchResultLimit = 40
)

type PackageSearchResult struct {
	AttrName     string
	AttrSet      string
	PackageName  string
	Version      string
	Description  string
	Programs     string
	Homepage     string
	License      string
	Position     string
	AlreadyAdded bool
	Addable      bool
	AddError     string
}

type nixSearchResult struct {
	Type               string             `json:"type"`
	PackagePName       string             `json:"package_pname"`
	PackageAttrName    string             `json:"package_attr_name"`
	PackageAttrSet     string             `json:"package_attr_set"`
	PackageDescription string             `json:"package_description"`
	PackagePrograms    []string           `json:"package_programs"`
	PackageHomepage    []string           `json:"package_homepage"`
	PackagePVersion    string             `json:"package_pversion"`
	PackagePosition    string             `json:"package_position"`
	PackageLicense     []nixSearchLicense `json:"package_license"`
}

type nixSearchLicense struct {
	FullName string `json:"fullName"`
}

func searchNixPackages(ctx context.Context, query string, existingPackages map[string]struct{}) ([]PackageSearchResult, int, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []PackageSearchResult{}, 0, nil
	}

	searchCtx, cancel := context.WithTimeout(ctx, packageSearchTimeout)
	defer cancel()

	command := exec.CommandContext(searchCtx, "nix-search", "--channel="+packageSearchChannel, "--json", query)
	output, err := command.CombinedOutput()
	if errors.Is(searchCtx.Err(), context.DeadlineExceeded) {
		return nil, 0, fmt.Errorf("search timed out")
	}

	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, 0, fmt.Errorf("nix-search is not installed on this server")
		}

		message := "nix-search failed"
		details := compactCommandOutput(string(output), 260)
		if details != "" {
			message += ": " + details
		}

		return nil, 0, errors.New(message)
	}

	rawResults, err := parseNixSearchResults(output)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to parse nix-search output")
	}

	results := make([]PackageSearchResult, 0, len(rawResults))
	for _, raw := range rawResults {
		if raw.Type != "" && raw.Type != "package" {
			continue
		}

		attrName := strings.TrimSpace(raw.PackageAttrName)
		if attrName == "" {
			continue
		}

		result := PackageSearchResult{
			AttrName:    attrName,
			AttrSet:     normalizePackageAttrSet(raw.PackageAttrSet),
			PackageName: strings.TrimSpace(raw.PackagePName),
			Version:     strings.TrimSpace(raw.PackagePVersion),
			Description: strings.TrimSpace(raw.PackageDescription),
			Programs:    strings.Join(limitStrings(trimmedStrings(raw.PackagePrograms), 4), ", "),
			Homepage:    firstNonEmpty(raw.PackageHomepage),
			License:     summarizeLicenses(raw.PackageLicense, 2),
			Position:    strings.TrimSpace(raw.PackagePosition),
			Addable:     true,
		}

		if _, exists := existingPackages[attrName]; exists {
			result.AlreadyAdded = true
			result.Addable = false
		} else if _, err := packageNameToNixExpression(attrName); err != nil {
			result.Addable = false
			result.AddError = "Unsupported attr"
		}

		results = append(results, result)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].AttrName < results[j].AttrName
	})

	total := len(results)
	if len(results) > packageSearchResultLimit {
		results = results[:packageSearchResultLimit]
	}

	return results, total, nil
}

func parseNixSearchResults(output []byte) ([]nixSearchResult, error) {
	trimmed := bytes.TrimSpace(output)
	if len(trimmed) == 0 {
		return []nixSearchResult{}, nil
	}

	results := make([]nixSearchResult, 0)
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &results); err != nil {
			return nil, err
		}

		return results, nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(trimmed))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var result nixSearchResult
		if err := json.Unmarshal([]byte(line), &result); err != nil {
			return nil, err
		}

		results = append(results, result)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func normalizePackageAttrSet(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "No package set" {
		return ""
	}

	return trimmed
}

func summarizeLicenses(licenses []nixSearchLicense, limit int) string {
	if len(licenses) == 0 {
		return ""
	}

	names := make([]string, 0, len(licenses))
	for _, license := range licenses {
		name := strings.TrimSpace(license.FullName)
		if name == "" {
			continue
		}

		names = append(names, name)
	}

	if len(names) == 0 {
		return ""
	}

	extra := ""
	if limit > 0 && len(names) > limit {
		names = names[:limit]
		extra = " +more"
	}

	return strings.Join(names, ", ") + extra
}

func trimmedStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item == "" {
			continue
		}

		trimmed = append(trimmed, item)
	}

	return trimmed
}

func firstNonEmpty(values []string) string {
	for _, value := range values {
		item := strings.TrimSpace(value)
		if item != "" {
			return item
		}
	}

	return ""
}

func limitStrings(values []string, limit int) []string {
	if limit <= 0 || len(values) <= limit {
		return values
	}

	return values[:limit]
}

func compactCommandOutput(output string, maxLength int) string {
	compact := strings.Join(strings.Fields(strings.TrimSpace(output)), " ")
	if len(compact) <= maxLength {
		return compact
	}

	return compact[:maxLength] + "..."
}
