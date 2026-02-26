/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	webauthnLoginSessionKey    = "webauthn_login"
	webauthnRegisterSessionKey = "webauthn_register"
	webauthnSetupSessionKey    = "webauthn_setup"

	authenticatedExpiresAtSessionKey = "authenticated_expires_at"
	authenticatedRememberDuration    = 14 * 24 * time.Hour
	authenticatedShortDuration       = time.Hour

	webauthnSetupUserIDKey      = "webauthn_setup_user_id"
	webauthnSetupDisplayNameKey = "webauthn_setup_display_name"
	webauthnSetupLabelKey       = "webauthn_setup_label"

	webauthnRegisterUserIDKey = "webauthn_register_user_id"
	webauthnRegisterLabelKey  = "webauthn_register_label"

	webauthnBootstrapAllowedKey = "webauthn_bootstrap_allowed"
	webauthnInviteAllowedKey    = "webauthn_invite_allowed"
	webauthnInviteIDKey         = "webauthn_invite_id"
	webauthnSetupIsAdminKey     = "webauthn_setup_is_admin"
)

func init() {
	gob.Register(webauthn.SessionData{})
}

// NewWebAuthnFromEnv builds the WebAuthn configuration using environment variables.
func NewWebAuthnFromEnv() (*webauthn.WebAuthn, error) {
	rpID := strings.TrimSpace(os.Getenv("WEBAUTHN_RP_ID"))
	rpOrigins := splitEnvList(os.Getenv("WEBAUTHN_RP_ORIGINS"))

	rpName := strings.TrimSpace(os.Getenv("WEBAUTHN_RP_NAME"))
	if rpName == "" {
		rpName = "Fleeti"
	}

	if rpID == "" {
		return nil, errWebAuthnRPIDRequired
	}

	if len(rpOrigins) == 0 {
		return nil, errWebAuthnRPOriginsRequired
	}

	config := &webauthn.Config{
		RPID:                  rpID,
		RPDisplayName:         rpName,
		RPOrigins:             rpOrigins,
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			UserVerification: protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login: webauthn.TimeoutConfig{
				Enforce: true,
			},
			Registration: webauthn.TimeoutConfig{
				Enforce: true,
			},
		},
	}

	w, err := webauthn.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize webauthn: %w", err)
	}

	return w, nil
}

// SetupForm renders the bootstrap and invite setup page.
func SetupForm(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	if isSessionAuthenticated(s, time.Now()) {
		c.Redirect("/", http.StatusSeeOther)

		return
	}

	setPage(data, "Setup")
	data["HeaderOnly"] = true

	s.Delete(webauthnInviteAllowedKey)
	s.Delete(webauthnInviteIDKey)

	ctx := c.Request().Context()

	count, err := db.CountUsers(ctx)
	if err != nil {
		data["Error"] = "Failed to load authentication state"

		t.HTML(http.StatusInternalServerError, "setup")

		return
	}

	token := strings.TrimSpace(c.Query("token"))
	if count == 0 {
		bootstrapToken := strings.TrimSpace(os.Getenv("BOOTSTRAP_TOKEN"))
		if bootstrapToken == "" {
			s.Delete(webauthnBootstrapAllowedKey)

			data["Error"] = "Setup is unavailable"

			t.HTML(http.StatusForbidden, "setup")

			return
		}

		if token == "" || token != bootstrapToken {
			s.Delete(webauthnBootstrapAllowedKey)

			data["Error"] = "Invalid setup link"

			t.HTML(http.StatusForbidden, "setup")

			return
		}

		s.Set(webauthnBootstrapAllowedKey, true)

		data["BootstrapReady"] = true
		data["DisplayName"] = "Admin"
		data["IsInviteSetup"] = false

		t.HTML(http.StatusOK, "setup")

		return
	}

	s.Delete(webauthnBootstrapAllowedKey)

	if token == "" {
		data["Error"] = "Invalid setup link"

		t.HTML(http.StatusForbidden, "setup")

		return
	}

	invite, err := db.GetUserInviteByToken(ctx, token)
	if err != nil {
		data["Error"] = "Failed to load setup link"

		t.HTML(http.StatusInternalServerError, "setup")

		return
	}

	if invite == nil || invite.UsedAt != nil {
		s.Delete(webauthnInviteAllowedKey)
		s.Delete(webauthnInviteIDKey)

		data["Error"] = "Invalid setup link"

		t.HTML(http.StatusForbidden, "setup")

		return
	}

	s.Set(webauthnInviteAllowedKey, true)
	s.Set(webauthnInviteIDKey, invite.ID.String())

	data["BootstrapReady"] = true
	data["IsInviteSetup"] = true
	if invite.DisplayName != nil {
		data["DisplayName"] = *invite.DisplayName
	}

	t.HTML(http.StatusOK, "setup")
}

// SetupStart begins WebAuthn registration for setup or invite provisioning.
func SetupStart(c flamego.Context, s session.Session, web *webauthn.WebAuthn) {
	ctx := c.Request().Context()

	if isSessionAuthenticated(s, time.Now()) {
		writeJSONError(c, http.StatusForbidden, "setup not permitted")

		return
	}

	isBootstrap := isBootstrapAllowed(s)
	isInvite := isInviteAllowed(s)
	if !isBootstrap && !isInvite {
		writeJSONError(c, http.StatusForbidden, "setup not permitted")

		return
	}

	if isBootstrap {
		count, err := db.CountUsers(ctx)
		if err != nil {
			writeJSONError(c, http.StatusInternalServerError, "failed to load authentication state")

			return
		}

		if count > 0 {
			writeJSONError(c, http.StatusBadRequest, "setup already completed")

			return
		}

		bootstrapToken := strings.TrimSpace(os.Getenv("BOOTSTRAP_TOKEN"))
		if bootstrapToken == "" {
			writeJSONError(c, http.StatusForbidden, "setup is unavailable")

			return
		}
	}

	var inviteID string

	if isInvite {
		storedInviteID, ok := getInviteID(s)
		if !ok {
			writeJSONError(c, http.StatusBadRequest, "invite token missing")

			return
		}

		invite, err := db.GetUserInviteByID(ctx, storedInviteID)
		if err != nil {
			writeJSONError(c, http.StatusInternalServerError, "failed to load invite")

			return
		}

		if invite == nil || invite.UsedAt != nil {
			writeJSONError(c, http.StatusBadRequest, "invite is no longer valid")

			return
		}

		inviteID = invite.ID.String()
	}

	var request struct {
		DisplayName string `json:"displayName"`
		Label       string `json:"label"`
	}

	if err := json.NewDecoder(c.Request().Body().ReadCloser()).Decode(&request); err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid request body")

		return
	}

	displayName := strings.TrimSpace(request.DisplayName)
	if displayName == "" {
		writeJSONError(c, http.StatusBadRequest, "display name is required")

		return
	}

	userID := uuid.New()
	label := strings.TrimSpace(request.Label)

	user := newWebAuthnUser(&db.User{
		ID:          userID,
		DisplayName: displayName,
		IsAdmin:     isBootstrap,
	}, nil)

	options, sessionData, err := web.BeginRegistration(user,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
	)
	if err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to start registration")

		return
	}

	s.Set(webauthnSetupSessionKey, *sessionData)
	s.Set(webauthnSetupUserIDKey, userID.String())
	s.Set(webauthnSetupDisplayNameKey, displayName)
	s.Set(webauthnSetupLabelKey, label)
	s.Set(webauthnSetupIsAdminKey, isBootstrap)

	if inviteID != "" {
		s.Set(webauthnInviteIDKey, inviteID)
	}

	writeJSON(c, options)
}

// SetupFinish completes WebAuthn registration for setup or invite provisioning.
func SetupFinish(c flamego.Context, s session.Session, web *webauthn.WebAuthn) {
	ctx := c.Request().Context()

	if isSessionAuthenticated(s, time.Now()) {
		writeJSONError(c, http.StatusForbidden, "setup not permitted")

		return
	}

	if !isBootstrapAllowed(s) && !isInviteAllowed(s) {
		writeJSONError(c, http.StatusForbidden, "setup not permitted")

		return
	}

	isAdmin, ok := getSetupIsAdmin(s)
	if !ok {
		writeJSONError(c, http.StatusBadRequest, "setup state missing")

		return
	}

	setupSession, ok := getSessionData(s, webauthnSetupSessionKey)
	if !ok {
		writeJSONError(c, http.StatusBadRequest, "setup session missing")

		return
	}

	userID, displayName, label, err := getSetupSessionData(s)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, err.Error())

		return
	}

	user := newWebAuthnUser(&db.User{
		ID:          userID,
		DisplayName: displayName,
		IsAdmin:     isAdmin,
	}, nil)

	credential, err := web.FinishRegistration(user, *setupSession, c.Request().Request)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "failed to finish registration")

		return
	}

	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}

	var inviteID *string
	if !isAdmin {
		value, ok := getInviteID(s)
		if !ok {
			writeJSONError(c, http.StatusBadRequest, "invite token missing")

			return
		}

		inviteID = &value
	}

	createdUser, err := db.FinalizeSetupRegistration(ctx, db.FinalizeSetupRegistrationInput{
		UserID:      userID,
		DisplayName: displayName,
		IsAdmin:     isAdmin,
		InviteID:    inviteID,
		Credential:  *credential,
		Label:       labelPtr,
	})
	if err != nil {
		switch {
		case errors.Is(err, db.ErrSetupAlreadyCompleted):
			writeJSONError(c, http.StatusBadRequest, "setup already completed")
		case errors.Is(err, db.ErrInviteInvalidOrUsed):
			writeJSONError(c, http.StatusBadRequest, "invite is no longer valid")
		default:
			writeJSONError(c, http.StatusInternalServerError, "failed to finalize setup")
		}

		return
	}

	setAuthenticatedSession(s, createdUser, time.Now(), true)
	clearSetupSession(s)

	writeJSON(c, map[string]string{"redirect": "/"})
}

// PasskeyLoginStart begins a discoverable passkey login.
func PasskeyLoginStart(c flamego.Context, s session.Session, web *webauthn.WebAuthn) {
	options, sessionData, err := web.BeginDiscoverableLogin()
	if err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to start login")

		return
	}

	s.Set(webauthnLoginSessionKey, *sessionData)

	writeJSON(c, options)
}

// PasskeyLoginFinish validates a passkey login.
func PasskeyLoginFinish(c flamego.Context, s session.Session, web *webauthn.WebAuthn) {
	loginSession, ok := getSessionData(s, webauthnLoginSessionKey)
	if !ok {
		writeJSONError(c, http.StatusBadRequest, "login session missing")

		return
	}

	user, credential, err := web.FinishPasskeyLogin(func(rawID, userHandle []byte) (webauthn.User, error) {
		return loadWebAuthnUserByHandle(c.Request().Context(), rawID, userHandle)
	}, *loginSession, c.Request().Request)
	if err != nil {
		logger.Error("passkey login verification failed", "error", err)
		writeJSONError(c, http.StatusUnauthorized, "failed to verify passkey")

		return
	}

	waUser, ok := user.(*webauthnUser)
	if !ok {
		writeJSONError(c, http.StatusInternalServerError, "unexpected user type")

		return
	}

	if err := db.UpdateUserPasskeyCredential(c.Request().Context(), waUser.user.ID.String(), *credential, time.Now()); err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to update passkey")

		return
	}

	rememberLogin := shouldRememberLogin(c.Query("remember"))
	setAuthenticatedSession(s, waUser.user, time.Now(), rememberLogin)
	s.Delete(webauthnLoginSessionKey)

	next := sanitizeNextPath(c.Query("next"))
	if strings.TrimSpace(c.Query("next")) == "" {
		next = "/"
	}

	writeJSON(c, map[string]string{"redirect": next})
}

// PasskeyRegistrationStart begins registration for an additional passkey.
func PasskeyRegistrationStart(c flamego.Context, s session.Session, web *webauthn.WebAuthn) {
	userID, ok := getSessionUserID(s)
	if !ok {
		writeJSONError(c, http.StatusUnauthorized, "not authenticated")

		return
	}

	waUser, err := loadWebAuthnUser(c.Request().Context(), userID)
	if err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to load user")

		return
	}

	var request struct {
		Label string `json:"label"`
	}

	if err := json.NewDecoder(c.Request().Body().ReadCloser()).Decode(&request); err != nil {
		writeJSONError(c, http.StatusBadRequest, "invalid request body")

		return
	}

	label := strings.TrimSpace(request.Label)

	exclude := webauthn.Credentials(waUser.credentials).CredentialDescriptors()

	options, sessionData, err := web.BeginRegistration(waUser,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(exclude),
	)
	if err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to start registration")

		return
	}

	s.Set(webauthnRegisterSessionKey, *sessionData)
	s.Set(webauthnRegisterUserIDKey, waUser.user.ID.String())
	s.Set(webauthnRegisterLabelKey, label)

	writeJSON(c, options)
}

// PasskeyRegistrationFinish completes registration for an additional passkey.
func PasskeyRegistrationFinish(c flamego.Context, s session.Session, web *webauthn.WebAuthn) {
	registerSession, ok := getSessionData(s, webauthnRegisterSessionKey)
	if !ok {
		writeJSONError(c, http.StatusBadRequest, "registration session missing")

		return
	}

	userID, label, err := getRegisterSessionData(s)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, err.Error())

		return
	}

	waUser, err := loadWebAuthnUser(c.Request().Context(), userID)
	if err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to load user")

		return
	}

	credential, err := web.FinishRegistration(waUser, *registerSession, c.Request().Request)
	if err != nil {
		writeJSONError(c, http.StatusBadRequest, "failed to finish registration")

		return
	}

	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}

	if _, err := db.AddUserPasskey(c.Request().Context(), waUser.user.ID.String(), *credential, labelPtr); err != nil {
		writeJSONError(c, http.StatusInternalServerError, "failed to save passkey")

		return
	}

	clearRegisterSession(s)

	writeJSON(c, map[string]bool{"ok": true})
}

func setAuthenticatedSession(s session.Session, user *db.User, now time.Time, remember bool) {
	expiresAt := now.Add(authenticatedShortDuration)
	if remember {
		expiresAt = now.Add(authenticatedRememberDuration)
	}

	s.Set("authenticated", true)
	s.Set("user_id", user.ID.String())
	s.Set("user_display_name", user.DisplayName)
	s.Set("user_is_admin", user.IsAdmin)
	s.Set("userID", user.ID.String())
	s.Set(authenticatedExpiresAtSessionKey, expiresAt.Unix())
}

func clearAuthenticatedSession(s session.Session) {
	s.Delete("authenticated")
	s.Delete("user_id")
	s.Delete("user_display_name")
	s.Delete("user_is_admin")
	s.Delete("userID")
	s.Delete(authenticatedExpiresAtSessionKey)
}

func isSessionAuthenticated(s session.Session, now time.Time) bool {
	authenticated, ok := s.Get("authenticated").(bool)
	if !ok || !authenticated {
		return false
	}

	expiresAt, hasExpiry := getAuthenticatedSessionExpiry(s)
	if !hasExpiry {
		authenticated, ok = s.Get("authenticated").(bool)

		return ok && authenticated
	}

	if !expiresAt.After(now) {
		clearAuthenticatedSession(s)

		return false
	}

	return true
}

func getAuthenticatedSessionExpiry(s session.Session) (time.Time, bool) {
	val := s.Get(authenticatedExpiresAtSessionKey)
	if val == nil {
		return time.Time{}, false
	}

	switch v := val.(type) {
	case int64:
		return time.Unix(v, 0), true
	case int:
		return time.Unix(int64(v), 0), true
	case float64:
		return time.Unix(int64(v), 0), true
	case time.Time:
		return v, true
	case *time.Time:
		if v == nil {
			s.Delete(authenticatedExpiresAtSessionKey)

			return time.Time{}, false
		}

		return *v, true
	default:
		s.Delete(authenticatedExpiresAtSessionKey)
		clearAuthenticatedSession(s)

		return time.Time{}, false
	}
}

func shouldRememberLogin(raw string) bool {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return true
	}

	switch raw {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

func getSessionUserID(s session.Session) (string, bool) {
	if val := s.Get("user_id"); val != nil {
		if userID, ok := val.(string); ok && userID != "" {
			return userID, true
		}
	}

	return "", false
}

func getSessionData(s session.Session, key string) (*webauthn.SessionData, bool) {
	return getSessionDataAt(s, key, time.Now())
}

func getSessionDataAt(s session.Session, key string, now time.Time) (*webauthn.SessionData, bool) {
	val := s.Get(key)
	if val == nil {
		return nil, false
	}

	var data webauthn.SessionData

	switch v := val.(type) {
	case webauthn.SessionData:
		data = v
	case *webauthn.SessionData:
		if v == nil {
			s.Delete(key)

			return nil, false
		}

		data = *v
	default:
		s.Delete(key)

		return nil, false
	}

	if data.Expires.IsZero() || !data.Expires.After(now) {
		s.Delete(key)

		return nil, false
	}

	return &data, true
}

func getSetupSessionData(s session.Session) (uuid.UUID, string, string, error) {
	userIDRaw, ok := s.Get(webauthnSetupUserIDKey).(string)
	if !ok || userIDRaw == "" {
		return uuid.UUID{}, "", "", errSetupUserMissing
	}

	userID, err := uuid.Parse(userIDRaw)
	if err != nil {
		return uuid.UUID{}, "", "", errInvalidSetupUser
	}

	displayName, ok := s.Get(webauthnSetupDisplayNameKey).(string)
	if !ok || displayName == "" {
		return uuid.UUID{}, "", "", errDisplayNameMissing
	}

	label, _ := s.Get(webauthnSetupLabelKey).(string)

	return userID, displayName, strings.TrimSpace(label), nil
}

func getRegisterSessionData(s session.Session) (string, string, error) {
	userID, ok := s.Get(webauthnRegisterUserIDKey).(string)
	if !ok || userID == "" {
		return "", "", errRegistrationUserMissing
	}

	label, _ := s.Get(webauthnRegisterLabelKey).(string)

	return userID, strings.TrimSpace(label), nil
}

func clearSetupSession(s session.Session) {
	s.Delete(webauthnSetupSessionKey)
	s.Delete(webauthnSetupUserIDKey)
	s.Delete(webauthnSetupDisplayNameKey)
	s.Delete(webauthnSetupLabelKey)
	s.Delete(webauthnBootstrapAllowedKey)
	s.Delete(webauthnInviteAllowedKey)
	s.Delete(webauthnInviteIDKey)
	s.Delete(webauthnSetupIsAdminKey)
}

func clearRegisterSession(s session.Session) {
	s.Delete(webauthnRegisterSessionKey)
	s.Delete(webauthnRegisterUserIDKey)
	s.Delete(webauthnRegisterLabelKey)
}

func isBootstrapAllowed(s session.Session) bool {
	allowed, ok := s.Get(webauthnBootstrapAllowedKey).(bool)

	return ok && allowed
}

func isInviteAllowed(s session.Session) bool {
	allowed, ok := s.Get(webauthnInviteAllowedKey).(bool)

	return ok && allowed
}

func getInviteID(s session.Session) (string, bool) {
	val, ok := s.Get(webauthnInviteIDKey).(string)
	if !ok || strings.TrimSpace(val) == "" {
		return "", false
	}

	return val, true
}

func getSetupIsAdmin(s session.Session) (bool, bool) {
	val, ok := s.Get(webauthnSetupIsAdminKey).(bool)

	return val, ok
}

type webauthnUser struct {
	user        *db.User
	credentials []webauthn.Credential
}

func newWebAuthnUser(user *db.User, credentials []webauthn.Credential) *webauthnUser {
	return &webauthnUser{
		user:        user,
		credentials: credentials,
	}
}

func (u *webauthnUser) WebAuthnID() []byte {
	return u.user.ID[:]
}

func (u *webauthnUser) WebAuthnName() string {
	return u.user.DisplayName
}

func (u *webauthnUser) WebAuthnDisplayName() string {
	return u.user.DisplayName
}

func (u *webauthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

func loadWebAuthnUser(ctx context.Context, userID string) (*webauthnUser, error) {
	user, err := db.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to load user %q: %w", userID, err)
	}

	credentials, err := db.LoadUserCredentials(ctx, user.ID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials for user %q: %w", user.ID.String(), err)
	}

	return newWebAuthnUser(user, credentials), nil
}

func loadWebAuthnUserByHandle(ctx context.Context, _ []byte, userHandle []byte) (*webauthnUser, error) {
	user, err := db.GetUserByWebAuthnID(ctx, userHandle)
	if err != nil {
		return nil, fmt.Errorf("failed to load user by webauthn handle: %w", err)
	}

	credentials, err := db.LoadUserCredentials(ctx, user.ID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials for user %q: %w", user.ID.String(), err)
	}

	return newWebAuthnUser(user, credentials), nil
}

func splitEnvList(raw string) []string {
	values := make([]string, 0)

	for _, item := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(item); value != "" {
			values = append(values, value)
		}
	}

	return values
}

func writeJSON(c flamego.Context, payload any) {
	c.ResponseWriter().Header().Set("Content-Type", "application/json")

	if err := json.NewEncoder(c.ResponseWriter()).Encode(payload); err != nil {
		logger.Error("error encoding webauthn response", "error", err)
	}
}

func writeJSONError(c flamego.Context, status int, message string) {
	c.ResponseWriter().Header().Set("Content-Type", "application/json")
	c.ResponseWriter().WriteHeader(status)

	if err := json.NewEncoder(c.ResponseWriter()).Encode(map[string]string{"error": message}); err != nil {
		logger.Error("error encoding webauthn error", "error", err)
	}
}
