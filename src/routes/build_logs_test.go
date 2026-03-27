/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"strings"
	"testing"

	"github.com/humaidq/fleeti/v2/db"
)

func TestBuildLogPayloadMarksDoneForTerminalBuildWithoutExtraChunks(t *testing.T) {
	t.Parallel()

	payload := buildLogPayload(db.BuildStatusSucceeded, 4, []db.BuildLogChunk{{ID: 5, Content: "hello\n"}}, true)
	if payload.Status != db.BuildStatusSucceeded {
		t.Fatalf("expected status to be preserved, got %q", payload.Status)
	}

	if payload.Chunk != "hello\n" {
		t.Fatalf("expected chunk content to be preserved, got %q", payload.Chunk)
	}

	if payload.NextAfter != 5 {
		t.Fatalf("expected next_after to advance, got %d", payload.NextAfter)
	}

	if !payload.Done {
		t.Fatal("expected payload to be marked done")
	}
}

func TestBuildLogPayloadKeepsPollingWhenMoreChunksRemain(t *testing.T) {
	t.Parallel()

	chunks := make([]db.BuildLogChunk, 0, buildLogBatchLimit+1)
	for i := 0; i < buildLogBatchLimit+1; i++ {
		chunks = append(chunks, db.BuildLogChunk{ID: int64(i + 1), Content: "x"})
	}

	payload := buildLogPayload(db.BuildStatusSucceeded, 0, chunks, true)
	if payload.Done {
		t.Fatal("expected payload to keep polling when extra chunks remain")
	}

	if payload.NextAfter != int64(buildLogBatchLimit) {
		t.Fatalf("expected next_after to stop at batch limit, got %d", payload.NextAfter)
	}

	if len(payload.Chunk) != buildLogBatchLimit || strings.Trim(payload.Chunk, "x") != "" {
		t.Fatalf("expected chunk output to be truncated at batch limit, got length %d", len(payload.Chunk))
	}
}
