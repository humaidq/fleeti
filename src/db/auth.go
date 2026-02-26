/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// User represents an authenticated account.
type User struct {
	ID          uuid.UUID
	DisplayName string
	IsAdmin     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UserPasskey represents a stored WebAuthn credential.
type UserPasskey struct {
	ID             uuid.UUID
	UserID         uuid.UUID
	CredentialID   []byte
	CredentialData []byte
	Label          *string
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

// FinalizeSetupRegistrationInput defines data for setup completion.
type FinalizeSetupRegistrationInput struct {
	UserID      uuid.UUID
	DisplayName string
	IsAdmin     bool
	InviteID    *string
	Credential  webauthn.Credential
	Label       *string
}

// FinalizeSetupRegistration creates a user and stores the initial passkey.
func FinalizeSetupRegistration(ctx context.Context, input FinalizeSetupRegistrationInput) (*User, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		return nil, ErrDisplayNameRequired
	}

	if !input.IsAdmin {
		if input.InviteID == nil || strings.TrimSpace(*input.InviteID) == "" {
			return nil, ErrInviteInvalidOrUsed
		}

		if _, err := uuid.Parse(strings.TrimSpace(*input.InviteID)); err != nil {
			return nil, ErrInviteInvalidOrUsed
		}
	}

	credentialData, err := encodeCredential(input.Credential)
	if err != nil {
		return nil, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start setup transaction: %w", err)
	}

	defer func() {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			logger.Warn("failed to rollback setup transaction", "error", rollbackErr)
		}
	}()

	if input.IsAdmin {
		if _, err := tx.Exec(ctx, `LOCK TABLE users IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return nil, fmt.Errorf("failed to lock users table: %w", err)
		}

		var count int

		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
			return nil, fmt.Errorf("failed to count users: %w", err)
		}

		if count > 0 {
			return nil, ErrSetupAlreadyCompleted
		}
	} else {
		command, err := tx.Exec(ctx, `
			UPDATE user_invites
			SET used_at = NOW()
			WHERE id = $1
			  AND used_at IS NULL
			  AND created_at >= NOW() - INTERVAL '24 hours'
		`, strings.TrimSpace(*input.InviteID))
		if err != nil {
			return nil, fmt.Errorf("failed to consume invite: %w", err)
		}

		if command.RowsAffected() == 0 {
			return nil, ErrInviteInvalidOrUsed
		}
	}

	var user User

	if err := tx.QueryRow(ctx, `
		INSERT INTO users (id, display_name, is_admin)
		VALUES ($1, $2, $3)
		RETURNING id, display_name, is_admin, created_at, updated_at
	`, input.UserID, displayName, input.IsAdmin).Scan(
		&user.ID,
		&user.DisplayName,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("failed to create user: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_passkeys (user_id, credential_id, credential_data, label, last_used_at)
		VALUES ($1, $2, $3, $4, NULL)
	`, user.ID, input.Credential.ID, credentialData, input.Label); err != nil {
		return nil, fmt.Errorf("failed to store passkey: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit setup transaction: %w", err)
	}

	return &user, nil
}

// CountUsers returns the number of users.
func CountUsers(ctx context.Context) (int, error) {
	if pool == nil {
		return 0, ErrDatabaseConnectionNotInitialized
	}

	var count int

	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count users: %w", err)
	}

	return count, nil
}

// GetUserByID returns a user by ID.
func GetUserByID(ctx context.Context, id string) (*User, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	var user User

	err := pool.QueryRow(ctx, `
		SELECT id, display_name, is_admin, created_at, updated_at
		FROM users
		WHERE id = $1
	`, id).Scan(
		&user.ID,
		&user.DisplayName,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrUserNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	return &user, nil
}

// GetUserByWebAuthnID resolves a user by WebAuthn user handle bytes.
func GetUserByWebAuthnID(ctx context.Context, userHandle []byte) (*User, error) {
	userID, err := uuid.FromBytes(userHandle)
	if err != nil {
		return nil, ErrInvalidUserHandle
	}

	return GetUserByID(ctx, userID.String())
}

// ListUserPasskeys returns passkeys for a user.
func ListUserPasskeys(ctx context.Context, userID string) ([]UserPasskey, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := pool.Query(ctx, `
		SELECT id, user_id, credential_id, credential_data, label, created_at, last_used_at
		FROM user_passkeys
		WHERE user_id = $1
		ORDER BY created_at ASC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list passkeys: %w", err)
	}

	defer rows.Close()

	passkeys := make([]UserPasskey, 0)

	for rows.Next() {
		var passkey UserPasskey

		if err := rows.Scan(
			&passkey.ID,
			&passkey.UserID,
			&passkey.CredentialID,
			&passkey.CredentialData,
			&passkey.Label,
			&passkey.CreatedAt,
			&passkey.LastUsedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan passkey: %w", err)
		}

		passkeys = append(passkeys, passkey)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating passkeys: %w", err)
	}

	return passkeys, nil
}

// CountUserPasskeys returns the number of passkeys for a user.
func CountUserPasskeys(ctx context.Context, userID string) (int, error) {
	if pool == nil {
		return 0, ErrDatabaseConnectionNotInitialized
	}

	var count int

	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM user_passkeys WHERE user_id = $1`, userID).Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count passkeys: %w", err)
	}

	return count, nil
}

// AddUserPasskey stores a new passkey for a user.
func AddUserPasskey(ctx context.Context, userID string, credential webauthn.Credential, label *string) (*UserPasskey, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	credentialData, err := encodeCredential(credential)
	if err != nil {
		return nil, err
	}

	var passkey UserPasskey

	err = pool.QueryRow(ctx, `
		INSERT INTO user_passkeys (user_id, credential_id, credential_data, label, last_used_at)
		VALUES ($1, $2, $3, $4, NULL)
		RETURNING id, user_id, credential_id, credential_data, label, created_at, last_used_at
	`, userID, credential.ID, credentialData, label).Scan(
		&passkey.ID,
		&passkey.UserID,
		&passkey.CredentialID,
		&passkey.CredentialData,
		&passkey.Label,
		&passkey.CreatedAt,
		&passkey.LastUsedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to store passkey: %w", err)
	}

	return &passkey, nil
}

// UpdateUserPasskeyCredential updates stored credential data and last used timestamp.
func UpdateUserPasskeyCredential(ctx context.Context, userID string, credential webauthn.Credential, lastUsed time.Time) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	credentialData, err := encodeCredential(credential)
	if err != nil {
		return err
	}

	command, err := pool.Exec(ctx, `
		UPDATE user_passkeys
		SET credential_data = $1, last_used_at = $2
		WHERE user_id = $3 AND credential_id = $4
	`, credentialData, lastUsed, userID, credential.ID)
	if err != nil {
		return fmt.Errorf("failed to update passkey: %w", err)
	}

	if command.RowsAffected() == 0 {
		return ErrPasskeyNotFound
	}

	return nil
}

// DeleteUserPasskey removes a passkey by ID.
func DeleteUserPasskey(ctx context.Context, userID string, passkeyID string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	command, err := pool.Exec(ctx, `DELETE FROM user_passkeys WHERE id = $1 AND user_id = $2`, passkeyID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete passkey: %w", err)
	}

	if command.RowsAffected() == 0 {
		return ErrPasskeyNotFound
	}

	return nil
}

// LoadUserCredentials loads WebAuthn credentials for a user.
func LoadUserCredentials(ctx context.Context, userID string) ([]webauthn.Credential, error) {
	passkeys, err := ListUserPasskeys(ctx, userID)
	if err != nil {
		return nil, err
	}

	credentials := make([]webauthn.Credential, 0, len(passkeys))
	for _, passkey := range passkeys {
		credential, err := decodeCredential(passkey.CredentialData)
		if err != nil {
			return nil, fmt.Errorf("failed to decode passkey credential: %w", err)
		}

		credentials = append(credentials, credential)
	}

	return credentials, nil
}

func encodeCredential(credential webauthn.Credential) ([]byte, error) {
	data, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("failed to encode credential: %w", err)
	}

	return data, nil
}

func decodeCredential(data []byte) (webauthn.Credential, error) {
	var credential webauthn.Credential

	if err := json.Unmarshal(data, &credential); err != nil {
		return webauthn.Credential{}, fmt.Errorf("failed to decode credential: %w", err)
	}

	return credential, nil
}
