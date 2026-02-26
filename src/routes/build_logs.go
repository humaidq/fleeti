/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

const buildLogBatchLimit = 256

type buildLogLiveResponse struct {
	Status    string `json:"status"`
	Chunk     string `json:"chunk"`
	NextAfter int64  `json:"next_after"`
	Done      bool   `json:"done"`
}

// BuildLogPage renders the build log viewer.
func BuildLogPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Build Log")
	data["IsBuilds"] = true

	buildID := strings.TrimSpace(c.Param("id"))
	if buildID == "" {
		redirectWithMessage(c, s, "/builds", FlashError, "Build not found")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if errors.Is(err, db.ErrBuildNotFound) {
		redirectWithMessage(c, s, "/builds", FlashError, "Build not found")

		return
	}

	if err != nil {
		logger.Error("failed to load build log page", "build_id", buildID, "error", err)
		setPageErrorFlash(data, "Failed to load build")
		data["Error"] = "Failed to load build"
		t.HTML(http.StatusInternalServerError, "error")

		return
	}

	data["Build"] = build

	t.HTML(http.StatusOK, "build_log")
}

// BuildLogLive returns incremental build log content for polling clients.
func BuildLogLive(c flamego.Context) {
	buildID := strings.TrimSpace(c.Param("id"))
	if buildID == "" {
		c.ResponseWriter().WriteHeader(http.StatusNotFound)

		return
	}

	afterID, err := parseAfterID(c.Request().URL.Query().Get("after"))
	if err != nil {
		c.ResponseWriter().WriteHeader(http.StatusBadRequest)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if errors.Is(err, db.ErrBuildNotFound) {
		c.ResponseWriter().WriteHeader(http.StatusNotFound)

		return
	}

	if err != nil {
		logger.Error("failed to load build for live logs", "build_id", buildID, "error", err)
		c.ResponseWriter().WriteHeader(http.StatusInternalServerError)

		return
	}

	chunks, err := db.ListBuildLogChunksSince(c.Request().Context(), buildID, afterID, buildLogBatchLimit+1)
	if err != nil {
		logger.Error("failed to list build log chunks", "build_id", buildID, "after", afterID, "error", err)
		c.ResponseWriter().WriteHeader(http.StatusInternalServerError)

		return
	}

	hasMore := false
	if len(chunks) > buildLogBatchLimit {
		hasMore = true
		chunks = chunks[:buildLogBatchLimit]
	}

	nextAfter := afterID
	var output strings.Builder

	for _, chunk := range chunks {
		output.WriteString(chunk.Content)
		nextAfter = chunk.ID
	}

	payload := buildLogLiveResponse{
		Status:    build.Status,
		Chunk:     output.String(),
		NextAfter: nextAfter,
		Done:      isTerminalBuildStatus(build.Status) && !hasMore,
	}

	header := c.ResponseWriter().Header()
	header.Set("Content-Type", "application/json; charset=utf-8")

	if err := json.NewEncoder(c.ResponseWriter()).Encode(payload); err != nil {
		logger.Warn("failed to write build log response", "build_id", buildID, "error", err)
	}
}

// BuildInstallerLogPage renders the installer build log viewer.
func BuildInstallerLogPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Installer Build Log")
	data["IsBuilds"] = true

	buildID := strings.TrimSpace(c.Param("id"))
	if buildID == "" {
		redirectWithMessage(c, s, "/builds", FlashError, "Build not found")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if errors.Is(err, db.ErrBuildNotFound) {
		redirectWithMessage(c, s, "/builds", FlashError, "Build not found")

		return
	}

	if err != nil {
		logger.Error("failed to load installer build log page", "build_id", buildID, "error", err)
		setPageErrorFlash(data, "Failed to load build")
		data["Error"] = "Failed to load build"
		t.HTML(http.StatusInternalServerError, "error")

		return
	}

	data["Build"] = build

	t.HTML(http.StatusOK, "build_installer_log")
}

// BuildInstallerLogLive returns incremental installer build log content for polling clients.
func BuildInstallerLogLive(c flamego.Context) {
	buildID := strings.TrimSpace(c.Param("id"))
	if buildID == "" {
		c.ResponseWriter().WriteHeader(http.StatusNotFound)

		return
	}

	afterID, err := parseAfterID(c.Request().URL.Query().Get("after"))
	if err != nil {
		c.ResponseWriter().WriteHeader(http.StatusBadRequest)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if errors.Is(err, db.ErrBuildNotFound) {
		c.ResponseWriter().WriteHeader(http.StatusNotFound)

		return
	}

	if err != nil {
		logger.Error("failed to load build for live installer logs", "build_id", buildID, "error", err)
		c.ResponseWriter().WriteHeader(http.StatusInternalServerError)

		return
	}

	chunks, err := db.ListBuildInstallerLogChunksSince(c.Request().Context(), buildID, afterID, buildLogBatchLimit+1)
	if err != nil {
		logger.Error("failed to list build installer log chunks", "build_id", buildID, "after", afterID, "error", err)
		c.ResponseWriter().WriteHeader(http.StatusInternalServerError)

		return
	}

	hasMore := false
	if len(chunks) > buildLogBatchLimit {
		hasMore = true
		chunks = chunks[:buildLogBatchLimit]
	}

	nextAfter := afterID
	var output strings.Builder

	for _, chunk := range chunks {
		output.WriteString(chunk.Content)
		nextAfter = chunk.ID
	}

	payload := buildLogLiveResponse{
		Status:    build.InstallerStatus,
		Chunk:     output.String(),
		NextAfter: nextAfter,
		Done:      isTerminalInstallerBuildStatus(build.InstallerStatus) && !hasMore,
	}

	header := c.ResponseWriter().Header()
	header.Set("Content-Type", "application/json; charset=utf-8")

	if err := json.NewEncoder(c.ResponseWriter()).Encode(payload); err != nil {
		logger.Warn("failed to write installer build log response", "build_id", buildID, "error", err)
	}
}

func parseAfterID(raw string) (int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0, nil
	}

	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0, err
	}

	if value < 0 {
		return 0, strconv.ErrSyntax
	}

	return value, nil
}

func isTerminalBuildStatus(status string) bool {
	switch status {
	case db.BuildStatusSucceeded, db.BuildStatusFailed:
		return true
	default:
		return false
	}
}

func isTerminalInstallerBuildStatus(status string) bool {
	switch status {
	case db.BuildInstallerStatusSucceeded, db.BuildInstallerStatusFailed:
		return true
	default:
		return false
	}
}
