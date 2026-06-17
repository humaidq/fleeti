/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"errors"
	"net/http"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/db"
)

var authenticateDeviceToken = db.AuthenticateDeviceToken

func resolveAPIDevice(c flamego.Context) (*db.Device, error) {
	rawKey, err := parseAPIBearerToken(c.Request().Header.Get("Authorization"))
	if err != nil {
		return nil, err
	}

	device, err := authenticateDeviceToken(c.Request().Context(), rawKey)
	if err != nil {
		return nil, err
	}

	return device, nil
}

// RequireDeviceAuth authenticates device-agent requests by device token and
// injects the resolved device.
func RequireDeviceAuth() flamego.Handler {
	return func(c flamego.Context) {
		device, err := resolveAPIDevice(c)
		if err != nil {
			writeAPIDeviceAuthError(c, err)

			return
		}

		c.Map(device)
		c.Next()
	}
}

func writeAPIDeviceAuthError(c flamego.Context, err error) {
	switch {
	case errors.Is(err, errAPIAuthorizationMissing):
		writeAPIUnauthorized(c, "Device token required")
	case errors.Is(err, errAPIAuthorizationInvalid), errors.Is(err, db.ErrDeviceTokenNotFound):
		writeAPIUnauthorized(c, "Invalid device token")
	default:
		logger.Error("failed to authenticate device request", "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to authenticate request")
	}
}
