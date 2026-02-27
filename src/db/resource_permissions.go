/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"fmt"
	"strings"
)

type ViewerUser struct {
	ID          string
	DisplayName string
	IsAdmin     bool
}

func ListViewerUsers(ctx context.Context) ([]ViewerUser, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			id::text,
			display_name,
			is_admin
		FROM users
		ORDER BY lower(display_name) ASC, id ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list users for permissions: %w", err)
	}

	defer rows.Close()

	users := make([]ViewerUser, 0)
	for rows.Next() {
		var user ViewerUser

		if err := rows.Scan(&user.ID, &user.DisplayName, &user.IsAdmin); err != nil {
			return nil, fmt.Errorf("failed to scan permission user: %w", err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during permission user rows iteration: %w", err)
	}

	return users, nil
}

func ListFleetViewerUsers(ctx context.Context, fleetID string) ([]ViewerUser, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return nil, ErrFleetRequired
	}

	rows, err := p.Query(ctx, `
		SELECT
			u.id::text,
			u.display_name,
			u.is_admin
		FROM fleet_user_permissions fup
		JOIN users u ON u.id = fup.user_id
		WHERE fup.fleet_id::text = $1
		ORDER BY lower(u.display_name) ASC, u.id ASC
	`, fleetID)
	if err != nil {
		return nil, fmt.Errorf("failed to list fleet permission users: %w", err)
	}

	defer rows.Close()

	users := make([]ViewerUser, 0)
	for rows.Next() {
		var user ViewerUser

		if err := rows.Scan(&user.ID, &user.DisplayName, &user.IsAdmin); err != nil {
			return nil, fmt.Errorf("failed to scan fleet permission user: %w", err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during fleet permission user rows iteration: %w", err)
	}

	return users, nil
}

func GrantFleetViewer(ctx context.Context, fleetID, userID, createdBy string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	userID = strings.TrimSpace(userID)
	createdBy = strings.TrimSpace(createdBy)

	if fleetID == "" {
		return ErrFleetRequired
	}

	if userID == "" {
		return ErrUserNotFound
	}

	var createdByArg any
	if createdBy != "" {
		createdByArg = createdBy
	}

	_, err := p.Exec(ctx, `
		INSERT INTO fleet_user_permissions (fleet_id, user_id, created_by)
		VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (fleet_id, user_id) DO NOTHING
	`, fleetID, userID, createdByArg)
	if foreignKeyViolation(err) {
		if strings.Contains(err.Error(), "fleet_user_permissions_fleet_id_fkey") {
			return ErrFleetNotFound
		}

		if strings.Contains(err.Error(), "fleet_user_permissions_user_id_fkey") {
			return ErrUserNotFound
		}
	}

	if err != nil {
		return fmt.Errorf("failed to grant fleet permission: %w", err)
	}

	return nil
}

func RevokeFleetViewer(ctx context.Context, fleetID, userID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	userID = strings.TrimSpace(userID)

	if fleetID == "" {
		return ErrFleetRequired
	}

	if userID == "" {
		return ErrUserNotFound
	}

	_, err := p.Exec(ctx, `
		DELETE FROM fleet_user_permissions
		WHERE fleet_id::text = $1
		  AND user_id::text = $2
	`, fleetID, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke fleet permission: %w", err)
	}

	return nil
}

func ListProfileViewerUsers(ctx context.Context, profileID string) ([]ViewerUser, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return nil, ErrProfileNotFound
	}

	rows, err := p.Query(ctx, `
		SELECT
			u.id::text,
			u.display_name,
			u.is_admin
		FROM profile_user_permissions pup
		JOIN users u ON u.id = pup.user_id
		WHERE pup.profile_id::text = $1
		ORDER BY lower(u.display_name) ASC, u.id ASC
	`, profileID)
	if err != nil {
		return nil, fmt.Errorf("failed to list profile permission users: %w", err)
	}

	defer rows.Close()

	users := make([]ViewerUser, 0)
	for rows.Next() {
		var user ViewerUser

		if err := rows.Scan(&user.ID, &user.DisplayName, &user.IsAdmin); err != nil {
			return nil, fmt.Errorf("failed to scan profile permission user: %w", err)
		}

		users = append(users, user)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during profile permission user rows iteration: %w", err)
	}

	return users, nil
}

func GrantProfileViewer(ctx context.Context, profileID, userID, createdBy string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	userID = strings.TrimSpace(userID)
	createdBy = strings.TrimSpace(createdBy)

	if profileID == "" {
		return ErrProfileNotFound
	}

	if userID == "" {
		return ErrUserNotFound
	}

	var createdByArg any
	if createdBy != "" {
		createdByArg = createdBy
	}

	_, err := p.Exec(ctx, `
		INSERT INTO profile_user_permissions (profile_id, user_id, created_by)
		VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (profile_id, user_id) DO NOTHING
	`, profileID, userID, createdByArg)
	if foreignKeyViolation(err) {
		if strings.Contains(err.Error(), "profile_user_permissions_profile_id_fkey") {
			return ErrProfileNotFound
		}

		if strings.Contains(err.Error(), "profile_user_permissions_user_id_fkey") {
			return ErrUserNotFound
		}
	}

	if err != nil {
		return fmt.Errorf("failed to grant profile permission: %w", err)
	}

	return nil
}

func RevokeProfileViewer(ctx context.Context, profileID, userID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	userID = strings.TrimSpace(userID)

	if profileID == "" {
		return ErrProfileNotFound
	}

	if userID == "" {
		return ErrUserNotFound
	}

	_, err := p.Exec(ctx, `
		DELETE FROM profile_user_permissions
		WHERE profile_id::text = $1
		  AND user_id::text = $2
	`, profileID, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke profile permission: %w", err)
	}

	return nil
}
