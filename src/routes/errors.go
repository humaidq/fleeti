/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import "errors"

var (
	errSessionUserMissing        = errors.New("session user missing")
	errWebAuthnRPIDRequired      = errors.New("WEBAUTHN_RP_ID is required")
	errWebAuthnRPOriginsRequired = errors.New("WEBAUTHN_RP_ORIGINS is required")
	errSetupUserMissing          = errors.New("setup user missing")
	errInvalidSetupUser          = errors.New("invalid setup user")
	errDisplayNameMissing        = errors.New("display name missing")
	errRegistrationUserMissing   = errors.New("registration user missing")
)
