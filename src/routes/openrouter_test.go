/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"strings"
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestProcessOpenRouterStreamLineExtractsContent(t *testing.T) {
	t.Parallel()

	chunk, done, err := processOpenRouterStreamLine([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n"))
	if err != nil {
		t.Fatalf("processOpenRouterStreamLine returned error: %v", err)
	}

	if done {
		t.Fatal("expected content chunk, got done")
	}

	if chunk != "hello" {
		t.Fatalf("expected chunk %q, got %q", "hello", chunk)
	}
}

func TestProcessOpenRouterStreamLineHandlesDone(t *testing.T) {
	t.Parallel()

	chunk, done, err := processOpenRouterStreamLine([]byte("data: [DONE]\n"))
	if err != nil {
		t.Fatalf("processOpenRouterStreamLine returned error: %v", err)
	}

	if !done {
		t.Fatal("expected done marker")
	}

	if chunk != "" {
		t.Fatalf("expected empty chunk, got %q", chunk)
	}
}

func TestStreamOpenRouterResponseAggregatesChunks(t *testing.T) {
	t.Parallel()

	body := strings.NewReader(strings.Join([]string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}",
		"",
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}",
		"",
		"data: [DONE]",
		"",
	}, "\n"))

	var chunks []string
	message, err := streamOpenRouterResponse(body, func(chunk string) error {
		chunks = append(chunks, chunk)
		return nil
	})
	if err != nil {
		t.Fatalf("streamOpenRouterResponse returned error: %v", err)
	}

	if message != "hello world" {
		t.Fatalf("expected aggregated message %q, got %q", "hello world", message)
	}

	if len(chunks) != 2 || chunks[0] != "hello" || chunks[1] != " world" {
		t.Fatalf("unexpected streamed chunks: %#v", chunks)
	}
}

func TestExecuteProfileWizardToolResetsDraftToOriginal(t *testing.T) {
	t.Parallel()

	originalDraft := profileWizardDraft{
		Name:                "Base",
		Description:         "Original",
		FleetIDs:            []string{"fleet-a"},
		ConfigJSON:          `{"packages":["vim"]}`,
		ConfigSchemaVersion: 1,
	}
	updatedDraft := profileWizardDraft{
		Name:                "Changed",
		Description:         "Changed description",
		FleetIDs:            []string{"fleet-b"},
		ConfigJSON:          `{"packages":["git"]}`,
		RawNix:              `{ services.openssh.enable = true; }`,
		ConfigSchemaVersion: 1,
	}

	result, resetDraft := executeProfileWizardTool(context.Background(), profileWizardModeAdapt, originalDraft, updatedDraft, []db.Fleet{{ID: "fleet-a", Name: "Fleet A"}}, openRouterToolCall{
		Function: openRouterToolCallTarget{Name: "reset_profile_draft", Arguments: `{}`},
	})

	if resetDraft.Name != originalDraft.Name || resetDraft.Description != originalDraft.Description || resetDraft.ConfigJSON != originalDraft.ConfigJSON || resetDraft.RawNix != originalDraft.RawNix {
		t.Fatalf("expected reset draft to match original, got %#v", resetDraft)
	}

	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("expected successful reset result, got %#v", result)
	}
}
