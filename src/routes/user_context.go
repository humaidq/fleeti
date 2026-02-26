/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

// UserContextInjector loads session user metadata into templates.
func UserContextInjector() flamego.Handler {
	return func(c flamego.Context, s session.Session, data template.Data) {
		authenticated := isSessionAuthenticated(s, time.Now())

		data["IsAuthenticated"] = authenticated
		if !authenticated {
			return
		}

		if displayName, ok := s.Get("user_display_name").(string); ok {
			displayName = strings.TrimSpace(displayName)
			if displayName != "" {
				data["UserDisplayName"] = displayName
			}
		}

		isAdmin, err := resolveSessionIsAdmin(c.Request().Context(), s)
		if err != nil {
			logger.Error("failed to resolve user admin state", "error", err)

			return
		}

		data["IsAdmin"] = isAdmin
	}
}

func resolveSessionIsAdmin(ctx context.Context, s session.Session) (bool, error) {
	if isAdmin, ok := s.Get("user_is_admin").(bool); ok {
		return isAdmin, nil
	}

	user, err := resolveSessionUser(ctx, s)
	if err != nil {
		return false, err
	}

	return user.IsAdmin, nil
}

func resolveSessionUser(ctx context.Context, s session.Session) (*db.User, error) {
	userID, ok := getSessionUserID(s)
	if !ok {
		return nil, errSessionUserMissing
	}

	user, err := db.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load user %q: %w", userID, err)
	}

	s.Set("user_display_name", user.DisplayName)
	s.Set("user_is_admin", user.IsAdmin)

	return user, nil
}
