/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package cmd

import "strings"

var (
	BuildVersion = "dev"
	BuildCommit  = "unknown"
)

func BuildDisplayVersion() string {
	version := strings.TrimSpace(BuildVersion)
	if version == "" || version == "dev" {
		return ""
	}

	commit := strings.TrimSpace(BuildCommit)
	if commit == "" || commit == "unknown" {
		return version
	}

	return version + " (" + commit + ")"
}
