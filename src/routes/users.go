/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

// UserRow represents a user entry on the admin users page.
type UserRow struct {
	ID             string
	DisplayName    string
	IsAdmin        bool
	CreatedAt      time.Time
	LastLoginAt    *time.Time
	PasskeyCount   int
	IsSelf         bool
	CanDelete      bool
	HasReset       bool
	ResetURL       string
	ResetInviteID  string
	ResetExpiresIn string
}

// UsersPage renders the admin-only user management page.
func UsersPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/", http.StatusSeeOther)

		return
	}

	setPage(data, "Users")
	data["IsUsers"] = true

	currentUserID, _ := getSessionUserID(s)

	adminCount, err := db.CountAdmins(ctx)
	if err != nil {
		logger.Error("failed to count admins", "error", err)
		data["Error"] = "Failed to load users"

		t.HTML(http.StatusInternalServerError, "users")

		return
	}

	users, err := db.ListUsers(ctx)
	if err != nil {
		logger.Error("failed to list users", "error", err)
		data["Error"] = "Failed to load users"

		t.HTML(http.StatusInternalServerError, "users")

		return
	}

	baseSetupURL := buildExternalURL(c.Request(), "/setup")
	now := time.Now()

	// Map the newest active recovery link per user.
	resetByUser := make(map[string]db.UserInvite)

	recoveryInvites, err := db.ListPendingRecoveryInvites(ctx)
	if err != nil {
		logger.Error("failed to load recovery links", "error", err)
	} else {
		for _, invite := range recoveryInvites {
			if invite.UserID == nil {
				continue
			}

			key := invite.UserID.String()
			if _, exists := resetByUser[key]; !exists {
				resetByUser[key] = invite
			}
		}
	}

	rows := make([]UserRow, 0, len(users))
	for _, user := range users {
		id := user.ID.String()
		isSelf := id == currentUserID

		row := UserRow{
			ID:           id,
			DisplayName:  user.DisplayName,
			IsAdmin:      user.IsAdmin,
			CreatedAt:    user.CreatedAt,
			LastLoginAt:  user.LastLoginAt,
			PasskeyCount: user.PasskeyCount,
			IsSelf:       isSelf,
			CanDelete:    !isSelf && !(user.IsAdmin && adminCount <= 1),
		}

		if invite, ok := resetByUser[id]; ok {
			expiresAt := invite.CreatedAt.Add(24 * time.Hour)
			row.HasReset = true
			row.ResetURL = baseSetupURL + "?token=" + url.QueryEscape(invite.Token)
			row.ResetInviteID = invite.ID.String()
			row.ResetExpiresIn = formatDuration(expiresAt.Sub(now))
		}

		rows = append(rows, row)
	}

	data["Users"] = rows

	loadInviteData(ctx, c, data)

	t.HTML(http.StatusOK, "users")
}

// loadInviteData populates pending and expired new-user provisioning invites.
func loadInviteData(ctx context.Context, c flamego.Context, data template.Data) {
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
			inviteInfos = append(inviteInfos, InviteInfo{
				ID:          invite.ID.String(),
				DisplayName: displayName,
				CreatedAt:   invite.CreatedAt,
				ExpiresAt:   expiresAt,
				ExpiresIn:   formatDuration(expiresAt.Sub(now)),
				IsExpired:   !expiresAt.After(now),
				SetupURL:    baseSetupURL + "?token=" + url.QueryEscape(invite.Token),
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

// CreateUserResetLink generates a passkey recovery link for a user (admin only).
func CreateUserResetLink(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	targetID := strings.TrimSpace(c.Param("id"))
	if targetID == "" {
		SetErrorFlash(s, "Missing user ID")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	createdBy, _ := getSessionUserID(s)

	if _, err := db.CreateUserRecoveryInvite(ctx, createdBy, targetID); err != nil {
		if errors.Is(err, db.ErrUserNotFound) {
			SetErrorFlash(s, "User not found")
		} else {
			logger.Error("failed to create recovery link", "user_id", targetID, "error", err)
			SetErrorFlash(s, "Failed to generate reset link")
		}

		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Reset link generated")
	c.Redirect("/users", http.StatusSeeOther)
}

// DeleteUser removes a user account (admin only).
func DeleteUser(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	targetID := strings.TrimSpace(c.Param("id"))
	if targetID == "" {
		SetErrorFlash(s, "Missing user ID")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	currentUserID, _ := getSessionUserID(s)
	if targetID == currentUserID {
		SetWarningFlash(s, "You cannot delete your own account")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	target, err := db.GetUserByID(ctx, targetID)
	if err != nil {
		if errors.Is(err, db.ErrUserNotFound) {
			SetErrorFlash(s, "User not found")
		} else {
			logger.Error("failed to load user", "user_id", targetID, "error", err)
			SetErrorFlash(s, "Failed to delete user")
		}

		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	if target.IsAdmin {
		adminCount, err := db.CountAdmins(ctx)
		if err != nil {
			logger.Error("failed to count admins", "error", err)
			SetErrorFlash(s, "Failed to delete user")
			c.Redirect("/users", http.StatusSeeOther)

			return
		}

		if adminCount <= 1 {
			SetWarningFlash(s, "Cannot delete the last admin")
			c.Redirect("/users", http.StatusSeeOther)

			return
		}
	}

	if err := db.DeleteUser(ctx, targetID); err != nil {
		logger.Error("failed to delete user", "user_id", targetID, "error", err)
		SetErrorFlash(s, "Failed to delete user")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "User deleted")
	c.Redirect("/users", http.StatusSeeOther)
}

// CreateUserInvite generates a new invite token (admin only).
func CreateUserInvite(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		SetErrorFlash(s, "Failed to parse form")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	userID, _ := getSessionUserID(s)
	displayName := strings.TrimSpace(c.Request().Form.Get("display_name"))

	if _, err := db.CreateUserInvite(ctx, userID, displayName); err != nil {
		SetErrorFlash(s, "Failed to create invite")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Invite created")
	c.Redirect("/users", http.StatusSeeOther)
}

// RegenerateUserInvite refreshes an expired invite link (admin only).
func RegenerateUserInvite(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	inviteID := strings.TrimSpace(c.Param("id"))
	if inviteID == "" {
		SetErrorFlash(s, "Missing invite ID")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	if _, err := db.RegenerateExpiredUserInvite(ctx, inviteID); err != nil {
		switch {
		case errors.Is(err, db.ErrInviteNotExpired):
			SetWarningFlash(s, "Invite has not expired yet")
		default:
			SetErrorFlash(s, "Failed to regenerate invite")
		}

		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Invite link regenerated")
	c.Redirect("/users", http.StatusSeeOther)
}

// DeleteUserInvite revokes a pending invite (admin only).
func DeleteUserInvite(c flamego.Context, s session.Session) {
	ctx := c.Request().Context()

	isAdmin, err := resolveSessionIsAdmin(ctx, s)
	if err != nil || !isAdmin {
		SetErrorFlash(s, "Access restricted")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	inviteID := c.Param("id")
	if inviteID == "" {
		SetErrorFlash(s, "Missing invite ID")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	if err := db.DeleteUserInvite(ctx, inviteID); err != nil {
		SetErrorFlash(s, "Failed to revoke invite")
		c.Redirect("/users", http.StatusSeeOther)

		return
	}

	SetSuccessFlash(s, "Invite revoked")
	c.Redirect("/users", http.StatusSeeOther)
}
