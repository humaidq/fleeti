/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/flamego/flamego"
)

const (
	checksumManifestFileName   = "SHA256SUMS"
	updateChecksumManifestPath = "/update/SHA256SUMS"
	updateChecksumPathPrefix   = "/update/"
	updateChecksumPathSuffix   = "/SHA256SUMS"
)

var (
	nixStoreRawArtifactPattern = regexp.MustCompile(`^[^/]+_[^/]+\.nix-store\.raw$`)
	ukiArtifactPattern         = regexp.MustCompile(`^[^/]+_[^/]+\.efi$`)
	fleetIDPathPattern         = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
)

type updateArtifact struct {
	name string
	path string
}

// DynamicSHA256SUMS serves /update/{fleet-id}/SHA256SUMS from fleet artifacts.
func DynamicSHA256SUMS(updatesDir string) flamego.Handler {
	return func(c flamego.Context) {
		req := c.Request()
		requestPath := req.URL.Path

		if requestPath == updateChecksumManifestPath {
			c.ResponseWriter().WriteHeader(http.StatusNotFound)

			return
		}

		fleetID, matched := fleetIDFromChecksumPath(requestPath)
		if !matched {
			c.Next()

			return
		}

		if req.Method != http.MethodGet && req.Method != http.MethodHead {
			header := c.ResponseWriter().Header()
			header.Set("Allow", "GET, HEAD")
			c.ResponseWriter().WriteHeader(http.StatusMethodNotAllowed)

			return
		}

		content, err := renderUpdateChecksumsForDirectory(filepath.Join(updatesDir, fleetID))
		if os.IsNotExist(err) {
			c.ResponseWriter().WriteHeader(http.StatusNotFound)

			return
		}

		if err != nil {
			logger.Error("failed to generate fleet update checksums", "directory", updatesDir, "fleet_id", fleetID, "error", err)
			c.ResponseWriter().WriteHeader(http.StatusInternalServerError)

			return
		}

		header := c.ResponseWriter().Header()
		header.Set("Content-Type", "text/plain; charset=utf-8")
		header.Set("Cache-Control", "no-store, max-age=0")
		header.Set("Pragma", "no-cache")
		header.Set("Expires", "0")
		header.Set("Content-Length", strconv.Itoa(len(content)))

		if req.Method == http.MethodHead {
			c.ResponseWriter().WriteHeader(http.StatusOK)

			return
		}

		c.ResponseWriter().WriteHeader(http.StatusOK)

		if _, err := c.ResponseWriter().Write(content); err != nil {
			logger.Warn("failed to write update checksums response", "directory", updatesDir, "fleet_id", fleetID, "error", err)
		}
	}
}

func fleetIDFromChecksumPath(path string) (string, bool) {
	if !strings.HasPrefix(path, updateChecksumPathPrefix) {
		return "", false
	}

	if !strings.HasSuffix(path, updateChecksumPathSuffix) {
		return "", false
	}

	fleetID := strings.TrimSuffix(strings.TrimPrefix(path, updateChecksumPathPrefix), updateChecksumPathSuffix)
	if fleetID == "" {
		return "", false
	}

	if strings.Contains(fleetID, "/") {
		return "", false
	}

	if !fleetIDPathPattern.MatchString(fleetID) {
		return "", false
	}

	return fleetID, true
}

func isUpdateArtifactFileName(name string) bool {
	if name == "" || strings.Contains(name, "/") {
		return false
	}

	return nixStoreRawArtifactPattern.MatchString(name) || ukiArtifactPattern.MatchString(name)
}

func renderUpdateChecksumsForDirectory(updatesDir string) ([]byte, error) {
	artifacts, err := collectUpdateArtifacts(updatesDir)
	if err != nil {
		return nil, err
	}

	return renderUpdateChecksums(artifacts)
}

func collectUpdateArtifacts(updatesDir string) ([]updateArtifact, error) {
	entries, err := os.ReadDir(updatesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read updates directory: %w", err)
	}

	artifacts := make([]updateArtifact, 0, len(entries))

	for _, entry := range entries {
		name := entry.Name()

		if name == checksumManifestFileName {
			continue
		}

		if !isUpdateArtifactFileName(name) {
			continue
		}

		if entry.IsDir() {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to read metadata for %s: %w", name, err)
		}

		if !info.Mode().IsRegular() {
			continue
		}

		artifacts = append(artifacts, updateArtifact{
			name: name,
			path: filepath.Join(updatesDir, name),
		})
	}

	sort.Slice(artifacts, func(i, j int) bool {
		return artifacts[i].name < artifacts[j].name
	})

	return artifacts, nil
}

func renderUpdateChecksums(artifacts []updateArtifact) ([]byte, error) {
	var output strings.Builder

	for _, artifact := range artifacts {
		digest, err := sha256File(artifact.path)
		if err != nil {
			return nil, fmt.Errorf("failed to checksum %s: %w", artifact.name, err)
		}

		output.WriteString(digest)
		output.WriteString("  ")
		output.WriteString(artifact.name)
		output.WriteByte('\n')
	}

	return []byte(output.String()), nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", fmt.Errorf("failed to hash file: %w", err)
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
