/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	apiKeyPrefix            = "flt_"
	apiKeyRandomBytes       = 32
	apiKeyDisplayPrefixSize = 16
)

// UserAPIKey represents an API key owned by a user.
type UserAPIKey struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	KeyPrefix string
	Label     *string
	CreatedAt time.Time
	LastUsed  *time.Time
}

// CreateUserAPIKey creates a new API key for a user and returns the raw secret once.
func CreateUserAPIKey(ctx context.Context, userID string, label string) (*UserAPIKey, string, error) {
	if pool == nil {
		return nil, "", ErrDatabaseConnectionNotInitialized
	}

	parsedUserID, err := uuid.Parse(strings.TrimSpace(userID))
	if err != nil {
		return nil, "", fmt.Errorf("invalid user id: %w", err)
	}

	rawKey, keyPrefixValue, keyHash, err := generateAPIKey()
	if err != nil {
		return nil, "", err
	}

	var labelPtr *string
	if trimmedLabel := strings.TrimSpace(label); trimmedLabel != "" {
		labelPtr = &trimmedLabel
	}

	var item UserAPIKey

	err = pool.QueryRow(ctx, `
		INSERT INTO user_api_keys (user_id, key_hash, key_prefix, label)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, key_prefix, label, created_at, last_used_at
	`, parsedUserID, keyHash, keyPrefixValue, labelPtr).Scan(
		&item.ID,
		&item.UserID,
		&item.KeyPrefix,
		&item.Label,
		&item.CreatedAt,
		&item.LastUsed,
	)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create api key: %w", err)
	}

	return &item, rawKey, nil
}

// ListUserAPIKeys returns API keys for a user.
func ListUserAPIKeys(ctx context.Context, userID string) ([]UserAPIKey, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := pool.Query(ctx, `
		SELECT id, user_id, key_prefix, label, created_at, last_used_at
		FROM user_api_keys
		WHERE user_id = $1
		ORDER BY created_at DESC
	`, strings.TrimSpace(userID))
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}

	defer rows.Close()

	keys := make([]UserAPIKey, 0)
	for rows.Next() {
		var item UserAPIKey

		if err := rows.Scan(
			&item.ID,
			&item.UserID,
			&item.KeyPrefix,
			&item.Label,
			&item.CreatedAt,
			&item.LastUsed,
		); err != nil {
			return nil, fmt.Errorf("failed to scan api key: %w", err)
		}

		keys = append(keys, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating api keys: %w", err)
	}

	return keys, nil
}

// DeleteUserAPIKey removes an API key by ID for a user.
func DeleteUserAPIKey(ctx context.Context, userID string, keyID string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	command, err := pool.Exec(ctx, `DELETE FROM user_api_keys WHERE id = $1 AND user_id = $2`, strings.TrimSpace(keyID), strings.TrimSpace(userID))
	if err != nil {
		return fmt.Errorf("failed to delete api key: %w", err)
	}

	if command.RowsAffected() == 0 {
		return ErrAPIKeyNotFound
	}

	return nil
}

// AuthenticateUserAPIKey resolves a user by API key and updates the key's last-used timestamp.
func AuthenticateUserAPIKey(ctx context.Context, rawKey string) (*User, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	trimmedKey := strings.TrimSpace(rawKey)
	if trimmedKey == "" {
		return nil, ErrAPIKeyNotFound
	}

	keyHash := hashAPIKey(trimmedKey)

	var (
		user     User
		apiKeyID uuid.UUID
	)

	err := pool.QueryRow(ctx, `
		SELECT u.id, u.display_name, u.is_admin, u.created_at, u.updated_at, k.id
		FROM user_api_keys k
		INNER JOIN users u ON u.id = k.user_id
		WHERE k.key_hash = $1
	`, keyHash).Scan(
		&user.ID,
		&user.DisplayName,
		&user.IsAdmin,
		&user.CreatedAt,
		&user.UpdatedAt,
		&apiKeyID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrAPIKeyNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to authenticate api key: %w", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE user_api_keys SET last_used_at = NOW() WHERE id = $1`, apiKeyID); err != nil {
		return nil, fmt.Errorf("failed to update api key last used time: %w", err)
	}

	return &user, nil
}

func generateAPIKey() (string, string, []byte, error) {
	buffer := make([]byte, apiKeyRandomBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", nil, fmt.Errorf("failed to generate api key: %w", err)
	}

	rawKey := apiKeyPrefix + base64.RawURLEncoding.EncodeToString(buffer)
	keyPrefixValue := rawKey
	if len(keyPrefixValue) > apiKeyDisplayPrefixSize {
		keyPrefixValue = keyPrefixValue[:apiKeyDisplayPrefixSize]
	}

	return rawKey, keyPrefixValue, hashAPIKey(rawKey), nil
}

func hashAPIKey(rawKey string) []byte {
	sum := sha256.Sum256([]byte(rawKey))

	return sum[:]
}
