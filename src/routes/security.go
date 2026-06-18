/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

// PasskeyInfo represents a passkey entry on the security page.
type PasskeyInfo struct {
	ID        string
	Label     string
	CreatedAt time.Time
	LastUsed  *time.Time
}

// APIKeyInfo represents an API key entry on the security page.
type APIKeyInfo struct {
	ID        string
	Label     string
	Preview   string
	CreatedAt time.Time
	LastUsed  *time.Time
}

// InviteInfo represents a provisioning invite.
type InviteInfo struct {
	ID          string
	DisplayName string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	ExpiresIn   string
	IsExpired   bool
	SetupURL    string
}

const (
	generatedAPIKeySessionKey      = "generated_api_key"
	generatedAPIKeyLabelSessionKey = "generated_api_key_label"
	adminAPIProfilesURL            = "https://admin.fleeti.ae/api/v1/profiles"
)

// Security renders the security page for passkey, API key, and invite management.
func Security(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Security")
	data["IsSecurity"] = true

	userID, ok := getSessionUserID(s)
	if !ok {
		data["Error"] = "Unable to resolve current user"

		t.HTML(http.StatusInternalServerError, "security")

		return
	}

	ctx := c.Request().Context()

	passkeys, err := db.ListUserPasskeys(ctx, userID)
	if err != nil {
		data["Error"] = "Failed to load passkey information"

		t.HTML(http.StatusInternalServerError, "security")

		return
	}

	passkeyInfos := make([]PasskeyInfo, 0, len(passkeys))
	for i, passkey := range passkeys {
		label := fmt.Sprintf("Passkey %d", i+1)
		if passkey.Label != nil && strings.TrimSpace(*passkey.Label) != "" {
			label = strings.TrimSpace(*passkey.Label)
		}

		passkeyInfos = append(passkeyInfos, PasskeyInfo{
			ID:        passkey.ID.String(),
			Label:     label,
			CreatedAt: passkey.CreatedAt,
			LastUsed:  passkey.LastUsedAt,
		})
	}

	data["Passkeys"] = passkeyInfos
	data["PasskeyCount"] = len(passkeyInfos)
	data["APIProfilesURL"] = adminAPIProfilesURL

	apiKeys, err := db.ListUserAPIKeys(ctx, userID)
	if err != nil {
		logger.Error("failed to load api keys", "error", err)
		data["APIKeyError"] = "Failed to load API keys"
	} else {
		apiKeyInfos := make([]APIKeyInfo, 0, len(apiKeys))
		for i, apiKey := range apiKeys {
			label := fmt.Sprintf("API key %d", i+1)
			if apiKey.Label != nil && strings.TrimSpace(*apiKey.Label) != "" {
				label = strings.TrimSpace(*apiKey.Label)
			}

			preview := strings.TrimSpace(apiKey.KeyPrefix)
			if preview != "" {
				preview += "..."
			}

			apiKeyInfos = append(apiKeyInfos, APIKeyInfo{
				ID:        apiKey.ID.String(),
				Label:     label,
				Preview:   preview,
				CreatedAt: apiKey.CreatedAt,
				LastUsed:  apiKey.LastUsed,
			})
		}

		data["APIKeys"] = apiKeyInfos
	}

	generatedAPIKey, generatedAPIKeyLabel := consumeGeneratedAPIKey(s)
	if generatedAPIKey != "" {
		data["GeneratedAPIKey"] = generatedAPIKey
		data["GeneratedAPIKeyLabel"] = generatedAPIKeyLabel
	}

	t.HTML(http.StatusOK, "security")
}

// DeletePasskey removes a passkey for the current user.
func DeletePasskey(c flamego.Context, s session.Session) {
	userID, ok := getSessionUserID(s)
	if !ok {
		SetErrorFlash(s, "Unable to resolve current user")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	count, err := db.CountUserPasskeys(c.Request().Context(), userID)
	if err != nil {
		SetErrorFlash(s, "Failed to load passkeys")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if count <= 1 {
		SetWarningFlash(s, "You must keep at least one passkey")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	passkeyID := c.Param("id")
	if passkeyID == "" {
		SetErrorFlash(s, "Missing passkey ID")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if err := db.DeleteUserPasskey(c.Request().Context(), userID, passkeyID); err != nil {
		SetErrorFlash(s, "Failed to delete passkey")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Passkey deleted")
	c.Redirect("/security", http.StatusSeeOther)
}

// CreateAPIKey generates a new API key for the current user.
func CreateAPIKey(c flamego.Context, s session.Session) {
	userID, ok := getSessionUserID(s)
	if !ok {
		SetErrorFlash(s, "Unable to resolve current user")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		SetErrorFlash(s, "Failed to parse form")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	label := strings.TrimSpace(c.Request().Form.Get("label"))
	apiKey, rawKey, err := db.CreateUserAPIKey(c.Request().Context(), userID, label)
	if err != nil {
		logger.Error("failed to create api key", "error", err)
		SetErrorFlash(s, "Failed to generate API key")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	storeGeneratedAPIKey(s, rawKey, apiKeyDisplayLabel(apiKey.Label))
	SetSuccessFlash(s, "API key generated")
	c.Redirect("/security", http.StatusSeeOther)
}

// DeleteAPIKey removes an API key for the current user.
func DeleteAPIKey(c flamego.Context, s session.Session) {
	userID, ok := getSessionUserID(s)
	if !ok {
		SetErrorFlash(s, "Unable to resolve current user")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	apiKeyID := strings.TrimSpace(c.Param("id"))
	if apiKeyID == "" {
		SetErrorFlash(s, "Missing API key ID")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if err := db.DeleteUserAPIKey(c.Request().Context(), userID, apiKeyID); err != nil {
		if errors.Is(err, db.ErrAPIKeyNotFound) {
			SetErrorFlash(s, "API key not found")
		} else {
			logger.Error("failed to delete api key", "api_key_id", apiKeyID, "error", err)
			SetErrorFlash(s, "Failed to delete API key")
		}

		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "API key deleted")
	c.Redirect("/security", http.StatusSeeOther)
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}

	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	parts := make([]string, 0, 2)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}

	if days > 0 {
		if hours > 0 {
			parts = append(parts, fmt.Sprintf("%dh", hours))
		} else if minutes > 0 {
			parts = append(parts, fmt.Sprintf("%dm", minutes))
		}
	} else if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
		if minutes > 0 {
			parts = append(parts, fmt.Sprintf("%dm", minutes))
		}
	} else {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}

	if len(parts) == 0 {
		return "in 0m"
	}

	if len(parts) == 1 {
		return "in " + parts[0]
	}

	return "in " + parts[0] + " " + parts[1]
}

func buildExternalURL(r *flamego.Request, path string) string {
	scheme := "http"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = strings.TrimSpace(strings.Split(proto, ",")[0])
	} else if r.TLS != nil {
		scheme = "https"
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}

	if host == "" {
		return path
	}

	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	return scheme + "://" + host + path
}

func apiKeyDisplayLabel(label *string) string {
	if label == nil {
		return ""
	}

	return strings.TrimSpace(*label)
}

func storeGeneratedAPIKey(s session.Session, rawKey string, label string) {
	s.Set(generatedAPIKeySessionKey, strings.TrimSpace(rawKey))
	s.Set(generatedAPIKeyLabelSessionKey, strings.TrimSpace(label))
}

func consumeGeneratedAPIKey(s session.Session) (string, string) {
	rawKey, _ := s.Get(generatedAPIKeySessionKey).(string)
	label, _ := s.Get(generatedAPIKeyLabelSessionKey).(string)

	s.Delete(generatedAPIKeySessionKey)
	s.Delete(generatedAPIKeyLabelSessionKey)

	return strings.TrimSpace(rawKey), strings.TrimSpace(label)
}
