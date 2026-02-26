/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// Security renders the security page for passkey and invite management.
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

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil {
		logger.Error("failed to resolve admin state", "error", err)
		isAdmin = false
	}

	data["IsAdmin"] = isAdmin

	if isAdmin {
		baseSetupURL := buildExternalURL(c.Request(), "/setup")
		now := time.Now()

		invites, err := db.ListPendingUserInvites(ctx)
		if err != nil {
			logger.Error("failed to load invites", "error", err)
			data["InviteError"] = "Failed to load user invites"
		} else {
			inviteInfos := make([]InviteInfo, 0, len(invites))
			for _, invite := range invites {
				displayName := "New user"
				if invite.DisplayName != nil && strings.TrimSpace(*invite.DisplayName) != "" {
					displayName = strings.TrimSpace(*invite.DisplayName)
				}

				expiresAt := invite.CreatedAt.Add(24 * time.Hour)
				setupURL := baseSetupURL + "?token=" + url.QueryEscape(invite.Token)

				inviteInfos = append(inviteInfos, InviteInfo{
					ID:          invite.ID.String(),
					DisplayName: displayName,
					CreatedAt:   invite.CreatedAt,
					ExpiresAt:   expiresAt,
					ExpiresIn:   formatDuration(expiresAt.Sub(now)),
					IsExpired:   !expiresAt.After(now),
					SetupURL:    setupURL,
				})
			}

			data["UserInvites"] = inviteInfos
		}

		expiredInvites, expiredErr := db.ListExpiredUserInvites(ctx)
		if expiredErr != nil {
			logger.Error("failed to load expired invites", "error", expiredErr)
			data["InviteError"] = "Failed to load user invites"
		} else {
			expiredInviteInfos := make([]InviteInfo, 0, len(expiredInvites))
			for _, invite := range expiredInvites {
				displayName := "New user"
				if invite.DisplayName != nil && strings.TrimSpace(*invite.DisplayName) != "" {
					displayName = strings.TrimSpace(*invite.DisplayName)
				}

				expiresAt := invite.CreatedAt.Add(24 * time.Hour)
				expiredInviteInfos = append(expiredInviteInfos, InviteInfo{
					ID:          invite.ID.String(),
					DisplayName: displayName,
					CreatedAt:   invite.CreatedAt,
					ExpiresAt:   expiresAt,
					ExpiresIn:   formatDuration(expiresAt.Sub(now)),
					IsExpired:   true,
				})
			}

			data["ExpiredUserInvites"] = expiredInviteInfos
		}
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

// CreateUserInvite generates a new invite token (admin only).
func CreateUserInvite(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		SetErrorFlash(s, "Failed to parse form")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	userID, _ := getSessionUserID(s)
	displayName := strings.TrimSpace(c.Request().Form.Get("display_name"))

	if _, err := db.CreateUserInvite(ctx, userID, displayName); err != nil {
		SetErrorFlash(s, "Failed to create invite")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Invite created")
	c.Redirect("/security", http.StatusSeeOther)
}

// RegenerateUserInvite refreshes an expired invite link (admin only).
func RegenerateUserInvite(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	inviteID := strings.TrimSpace(c.Param("id"))
	if inviteID == "" {
		SetErrorFlash(s, "Missing invite ID")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if _, err := db.RegenerateExpiredUserInvite(ctx, inviteID); err != nil {
		switch {
		case errors.Is(err, db.ErrInviteNotExpired):
			SetWarningFlash(s, "Invite has not expired yet")
		default:
			SetErrorFlash(s, "Failed to regenerate invite")
		}

		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Invite link regenerated")
	c.Redirect("/security", http.StatusSeeOther)
}

// DeleteUserInvite revokes a pending invite (admin only).
func DeleteUserInvite(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	inviteID := c.Param("id")
	if inviteID == "" {
		SetErrorFlash(s, "Missing invite ID")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	if err := db.DeleteUserInvite(ctx, inviteID); err != nil {
		SetErrorFlash(s, "Failed to revoke invite")
		c.Redirect("/security", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Invite revoked")
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
