/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"errors"
	"net/http"
	"strings"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/db"
)

var authenticateUserAPIKey = db.AuthenticateUserAPIKey

var (
	errAPIAuthorizationMissing = errors.New("api authorization missing")
	errAPIAuthorizationInvalid = errors.New("api authorization invalid")
)

func resolveAPIUser(c flamego.Context) (*db.User, error) {
	rawKey, err := parseAPIBearerToken(c.Request().Header.Get("Authorization"))
	if err != nil {
		return nil, err
	}

	user, err := authenticateUserAPIKey(c.Request().Context(), rawKey)
	if err != nil {
		return nil, err
	}

	return user, nil
}

// RequireAPIUser authenticates API requests and injects the resolved user.
func RequireAPIUser() flamego.Handler {
	return func(c flamego.Context) {
		user, err := resolveAPIUser(c)
		if err != nil {
			writeAPIAuthError(c, err)

			return
		}

		c.Map(user)
		c.Next()
	}
}

func parseAPIBearerToken(headerValue string) (string, error) {
	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return "", errAPIAuthorizationMissing
	}

	const bearerPrefix = "Bearer "
	if len(headerValue) <= len(bearerPrefix) || !strings.EqualFold(headerValue[:len(bearerPrefix)], bearerPrefix) {
		return "", errAPIAuthorizationInvalid
	}

	token := strings.TrimSpace(headerValue[len(bearerPrefix):])
	if token == "" {
		return "", errAPIAuthorizationInvalid
	}

	return token, nil
}

func writeAPIUnauthorized(c flamego.Context, message string) {
	c.ResponseWriter().Header().Set("WWW-Authenticate", "Bearer")
	writeJSONError(c, http.StatusUnauthorized, message)
}

func writeAPIAuthError(c flamego.Context, err error) {
	switch {
	case errors.Is(err, errAPIAuthorizationMissing):
		writeAPIUnauthorized(c, "API key required")
	case errors.Is(err, errAPIAuthorizationInvalid), errors.Is(err, db.ErrAPIKeyNotFound):
		writeAPIUnauthorized(c, "Invalid API key")
	default:
		logger.Error("failed to authenticate api request", "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to authenticate request")
	}
}
