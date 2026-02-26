/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"
)

// LoginForm renders the login page.
func LoginForm(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	next := sanitizeNextPath(c.Query("next"))
	if strings.TrimSpace(c.Query("next")) == "" {
		next = "/"
	}

	if isSessionAuthenticated(s, time.Now()) {
		c.Redirect(next, http.StatusSeeOther)

		return
	}

	setPage(data, "Login")
	data["HeaderOnly"] = true
	data["Next"] = next

	t.HTML(http.StatusOK, "login")
}

// Logout clears authenticated state for the current session.
func Logout(s session.Session, c flamego.Context) {
	clearAuthenticatedSession(s)
	c.Redirect("/login", http.StatusSeeOther)
}

// RequireAuth blocks unauthenticated access.
func RequireAuth(s session.Session, c flamego.Context) {
	if !isSessionAuthenticated(s, time.Now()) {
		next := sanitizeNextPath(c.Request().Header.Get("Referer"))
		if c.Request().Method == http.MethodGet || c.Request().Method == http.MethodHead {
			next = sanitizeNextPath(c.Request().URL.RequestURI())
		}

		redirectURL := "/login?next=" + url.QueryEscape(next)
		c.Redirect(redirectURL, http.StatusFound)

		return
	}

	c.Next()
}

func sanitizeNextPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}

	if strings.Contains(raw, "\n") || strings.Contains(raw, "\r") {
		return "/"
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil {
			return "/"
		}

		path := parsed.EscapedPath()
		if path == "" {
			path = "/"
		}

		if strings.HasPrefix(path, "//") {
			return "/"
		}

		if parsed.RawQuery != "" {
			return path + "?" + parsed.RawQuery
		}

		return path
	}

	if !strings.HasPrefix(raw, "/") {
		return "/"
	}

	if strings.HasPrefix(raw, "//") {
		return "/"
	}

	return raw
}
