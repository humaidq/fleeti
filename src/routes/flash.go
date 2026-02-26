/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"encoding/gob"
	"strings"

	"github.com/flamego/session"
	"github.com/flamego/template"
)

// FlashType represents the type of flash message.
type FlashType string

// FlashType values represent supported flash message categories.
const (
	FlashError   FlashType = "error"
	FlashSuccess FlashType = "success"
	FlashWarning FlashType = "warning"
	FlashInfo    FlashType = "info"
)

// FlashMessage represents a flash message shown to the user.
type FlashMessage struct {
	Type    FlashType
	Message string
}

func init() {
	// Register FlashMessage with gob for session serialization.
	gob.Register(FlashMessage{})
}

// SetFlash sets a typed flash message in the session.
func SetFlash(s session.Session, typ FlashType, message string) {
	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}

	s.SetFlash(FlashMessage{
		Type:    typ,
		Message: trimmed,
	})
}

// SetErrorFlash sets an error flash message in the session.
func SetErrorFlash(s session.Session, message string) {
	SetFlash(s, FlashError, message)
}

// SetSuccessFlash sets a success flash message in the session.
func SetSuccessFlash(s session.Session, message string) {
	SetFlash(s, FlashSuccess, message)
}

// SetWarningFlash sets a warning flash message in the session.
func SetWarningFlash(s session.Session, message string) {
	SetFlash(s, FlashWarning, message)
}

// SetInfoFlash sets an info flash message in the session.
func SetInfoFlash(s session.Session, message string) {
	SetFlash(s, FlashInfo, message)
}

func setPageErrorFlash(data template.Data, message string) {
	if _, exists := data["Flash"]; exists {
		return
	}

	trimmed := strings.TrimSpace(message)
	if trimmed == "" {
		return
	}

	data["Flash"] = FlashMessage{
		Type:    FlashError,
		Message: trimmed,
	}
}
