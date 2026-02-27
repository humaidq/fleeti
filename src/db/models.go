/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var semanticVersionPattern = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

const (
	BuildStatusQueued    = "queued"
	BuildStatusRunning   = "running"
	BuildStatusSucceeded = "succeeded"
	BuildStatusFailed    = "failed"

	BuildInstallerStatusNotRequested = "not_requested"
	BuildInstallerStatusQueued       = "queued"
	BuildInstallerStatusRunning      = "running"
	BuildInstallerStatusSucceeded    = "succeeded"
	BuildInstallerStatusFailed       = "failed"

	ReleaseStatusActive    = "active"
	ReleaseStatusWithdrawn = "withdrawn"

	DeviceStateIdle        = "idle"
	DeviceStateDownloading = "downloading"
	DeviceStateApplying    = "applying"
	DeviceStateRebooting   = "rebooting"
	DeviceStateHealthy     = "healthy"
	DeviceStateDegraded    = "degraded"
	DeviceStateFailed      = "failed"

	RolloutStrategyStaged    = "staged"
	RolloutStrategyAllAtOnce = "all-at-once"

	RolloutStatusPlanned    = "planned"
	RolloutStatusInProgress = "in_progress"
	RolloutStatusPaused     = "paused"
	RolloutStatusCompleted  = "completed"
	RolloutStatusFailed     = "failed"

	VisibilityPrivate = "private"
	VisibilityVisible = "visible"
)

type DashboardCounts struct {
	Fleets         int64
	Profiles       int64
	Builds         int64
	Releases       int64
	Devices        int64
	Rollouts       int64
	HealthyDevices int64
}

type Fleet struct {
	ID          string
	Name        string
	Description string
	OwnerUserID string
	Visibility  string
	CreatedAt   string
}

type Profile struct {
	ID             string
	FleetID        string
	FleetIDs       []string
	FleetName      string
	Name           string
	Description    string
	OwnerUserID    string
	Visibility     string
	LatestRevision int
	ConfigHash     string
	CreatedAt      string
}

type ProfileEdit struct {
	ID                  string
	FleetID             string
	FleetIDs            []string
	FleetName           string
	Name                string
	Description         string
	OwnerUserID         string
	Visibility          string
	LatestRevision      int
	ConfigHash          string
	ConfigSchemaVersion int
	ConfigJSON          string
	RawNix              string
	CreatedAt           string
}

type Build struct {
	ID                string
	ProfileID         string
	ProfileName       string
	FleetID           string
	FleetName         string
	ProfileRevisionID string
	ProfileRevision   int
	Version           string
	Status            string
	Artifact          string
	InstallerStatus   string
	InstallerArtifact string
	CreatedAt         string
}

type BuildLogChunk struct {
	ID      int64
	Content string
}

type Release struct {
	ID          string
	BuildID     string
	Build       string
	ProfileName string
	FleetID     string
	FleetName   string
	Channel     string
	Version     string
	Notes       string
	Status      string
	PublishedAt string
}

type ReleaseDeploymentInfo struct {
	ReleaseID      string
	ReleaseVersion string
	BuildID        string
	FleetID        string
	FleetName      string
}

type ReleaseTakedownInfo struct {
	ReleaseID       string
	BuildID         string
	FleetID         string
	FleetName       string
	Status          string
	IsCurrentlyLive bool
}

type Device struct {
	ID                    string
	FleetID               string
	FleetName             string
	Hostname              string
	SerialNumber          string
	CurrentReleaseVersion string
	DesiredReleaseVersion string
	UpdateState           string
	AttestationLevel      string
	LastSeenAt            string
	CreatedAt             string
}

type Rollout struct {
	ID             string
	FleetID        string
	FleetName      string
	ReleaseID      string
	ReleaseVersion string
	ReleaseStatus  string
	Strategy       string
	StagePercent   int
	Status         string
	StartedAt      string
	CompletedAt    string
	CreatedAt      string
}

type CreateProfileInput struct {
	FleetIDs            []string
	Name                string
	Description         string
	ConfigJSON          string
	RawNix              string
	ConfigSchemaVersion int
}

type CreateBuildInput struct {
	ProfileID string
	FleetID   string
	Version   string
}

type CreateReleaseInput struct {
	BuildID string
	Channel string
	Version string
	Notes   string
}

type CreateDeviceInput struct {
	FleetID          string
	Hostname         string
	SerialNumber     string
	UpdateState      string
	AttestationLevel string
}

type CreateRolloutInput struct {
	FleetID      string
	ReleaseID    string
	Strategy     string
	StagePercent int
	Status       string
}

func GetDashboardCounts(ctx context.Context) (DashboardCounts, error) {
	p := GetPool()
	if p == nil {
		return DashboardCounts{}, ErrDatabaseConnectionNotInitialized
	}

	var counts DashboardCounts

	err := p.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*) FROM fleets),
			(SELECT COUNT(*) FROM profiles),
			(SELECT COUNT(*) FROM builds),
			(SELECT COUNT(*) FROM releases),
			(SELECT COUNT(*) FROM devices),
			(SELECT COUNT(*) FROM rollouts),
			(SELECT COUNT(*) FROM devices WHERE update_state = 'healthy')
	`).Scan(
		&counts.Fleets,
		&counts.Profiles,
		&counts.Builds,
		&counts.Releases,
		&counts.Devices,
		&counts.Rollouts,
		&counts.HealthyDevices,
	)
	if err != nil {
		return DashboardCounts{}, fmt.Errorf("failed to fetch dashboard counts: %w", err)
	}

	return counts, nil
}

func ListFleets(ctx context.Context) ([]Fleet, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			id::text,
			name,
			description,
			COALESCE(owner_user_id::text, ''),
			visibility,
			to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM fleets
		ORDER BY created_at DESC, name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list fleets: %w", err)
	}

	defer rows.Close()

	fleets := make([]Fleet, 0)
	for rows.Next() {
		var item Fleet

		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.Visibility, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan fleet: %w", err)
		}

		fleets = append(fleets, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during fleet rows iteration: %w", err)
	}

	return fleets, nil
}

func CreateFleet(ctx context.Context, name, description, ownerUserID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)
	ownerUserID = strings.TrimSpace(ownerUserID)

	var owner any
	if ownerUserID != "" {
		owner = ownerUserID
	}

	if name == "" {
		return ErrNameRequired
	}

	_, err := p.Exec(ctx, `
		INSERT INTO fleets (name, description, owner_user_id, visibility)
		VALUES ($1, $2, $3, $4)
	`, name, description, owner, VisibilityPrivate)
	if uniqueViolation(err) {
		return ErrFleetAlreadyExists
	}

	if err != nil {
		return fmt.Errorf("failed to create fleet: %w", err)
	}

	return nil
}

func GetFleetByID(ctx context.Context, fleetID string) (Fleet, error) {
	p := GetPool()
	if p == nil {
		return Fleet{}, ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return Fleet{}, ErrFleetRequired
	}

	var item Fleet

	err := p.QueryRow(ctx, `
		SELECT
			id::text,
			name,
			description,
			COALESCE(owner_user_id::text, ''),
			visibility,
			to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM fleets
		WHERE id::text = $1
	`, fleetID).Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.Visibility, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Fleet{}, ErrFleetNotFound
	}

	if err != nil {
		return Fleet{}, fmt.Errorf("failed to load fleet: %w", err)
	}

	return item, nil
}

func ListFleetsForUser(ctx context.Context, userID string, isAdmin bool) ([]Fleet, error) {
	if isAdmin {
		return ListFleets(ctx)
	}

	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	userID = strings.TrimSpace(userID)

	rows, err := p.Query(ctx, `
		SELECT
			id::text,
			name,
			description,
			COALESCE(owner_user_id::text, ''),
			visibility,
			to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM fleets
		WHERE owner_user_id::text = $1
		   OR visibility = $2
		   OR EXISTS (
			SELECT 1
			FROM fleet_user_permissions fup
			WHERE fup.fleet_id = fleets.id
			  AND fup.user_id::text = $1
		)
		ORDER BY created_at DESC, name ASC
	`, userID, VisibilityVisible)
	if err != nil {
		return nil, fmt.Errorf("failed to list fleets for user: %w", err)
	}

	defer rows.Close()

	fleets := make([]Fleet, 0)
	for rows.Next() {
		var item Fleet

		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.Visibility, &item.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan fleet for user: %w", err)
		}

		fleets = append(fleets, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during fleet rows iteration for user: %w", err)
	}

	return fleets, nil
}

func UserCanViewFleet(ctx context.Context, userID string, isAdmin bool, fleetID string) (bool, error) {
	p := GetPool()
	if p == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	userID = strings.TrimSpace(userID)
	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return false, ErrFleetRequired
	}

	var canView bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM fleets
			WHERE id::text = $1
			  AND (
				$2
				OR owner_user_id::text = $3
				OR visibility = $4
				OR EXISTS (
					SELECT 1
					FROM fleet_user_permissions fup
					WHERE fup.fleet_id = fleets.id
					  AND fup.user_id::text = $3
				)
			  )
		)
	`, fleetID, isAdmin, userID, VisibilityVisible).Scan(&canView)
	if err != nil {
		return false, fmt.Errorf("failed to validate fleet access: %w", err)
	}

	return canView, nil
}

func UserCanManageFleet(ctx context.Context, userID string, isAdmin bool, fleetID string) (bool, error) {
	p := GetPool()
	if p == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	userID = strings.TrimSpace(userID)
	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return false, ErrFleetRequired
	}

	var canManage bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM fleets
			WHERE id::text = $1
			  AND ($2 OR owner_user_id::text = $3)
		)
	`, fleetID, isAdmin, userID).Scan(&canManage)
	if err != nil {
		return false, fmt.Errorf("failed to validate fleet management access: %w", err)
	}

	return canManage, nil
}

func SetFleetVisibility(ctx context.Context, fleetID, visibility string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return ErrFleetRequired
	}

	normalizedVisibility, err := normalizeVisibility(visibility)
	if err != nil {
		return err
	}

	result, err := p.Exec(ctx, `
		UPDATE fleets
		SET visibility = $2
		WHERE id::text = $1
	`, fleetID, normalizedVisibility)
	if err != nil {
		return fmt.Errorf("failed to update fleet visibility: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrFleetNotFound
	}

	return nil
}

func UpdateFleet(ctx context.Context, fleetID, name, description string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	name = strings.TrimSpace(name)
	description = strings.TrimSpace(description)

	if fleetID == "" {
		return ErrFleetRequired
	}

	if name == "" {
		return ErrNameRequired
	}

	result, err := p.Exec(ctx, `
		UPDATE fleets
		SET name = $2,
			description = $3
		WHERE id::text = $1
	`, fleetID, name, description)
	if uniqueViolation(err) {
		return ErrFleetAlreadyExists
	}

	if err != nil {
		return fmt.Errorf("failed to update fleet: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrFleetNotFound
	}

	return nil
}

func DeleteFleet(ctx context.Context, fleetID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return ErrFleetRequired
	}

	result, err := p.Exec(ctx, `
		DELETE FROM fleets
		WHERE id::text = $1
	`, fleetID)
	if err != nil {
		return fmt.Errorf("failed to delete fleet: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrFleetNotFound
	}

	return nil
}

func ListProfiles(ctx context.Context) ([]Profile, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			p.id::text,
			COALESCE(string_agg(DISTINCT f.id::text, ',' ORDER BY f.id::text), ''),
			COALESCE(string_agg(DISTINCT f.name, ', ' ORDER BY f.name), ''),
			p.name,
			p.description,
			COALESCE(p.owner_user_id::text, ''),
			p.visibility,
			COALESCE(pr.revision, 0),
			COALESCE(pr.config_hash, ''),
			to_char(p.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM profiles p
		LEFT JOIN profile_fleets pf ON pf.profile_id = p.id
		LEFT JOIN fleets f ON f.id = pf.fleet_id
		LEFT JOIN LATERAL (
			SELECT
				revision,
				config_hash
			FROM profile_revisions
			WHERE profile_id = p.id
			ORDER BY revision DESC
			LIMIT 1
		) pr ON true
		GROUP BY p.id, p.name, p.description, p.owner_user_id, p.visibility, pr.revision, pr.config_hash, p.created_at
		ORDER BY p.created_at DESC, p.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list profiles: %w", err)
	}

	defer rows.Close()

	profiles := make([]Profile, 0)
	for rows.Next() {
		var item Profile
		var fleetIDsCSV string

		if err := rows.Scan(
			&item.ID,
			&fleetIDsCSV,
			&item.FleetName,
			&item.Name,
			&item.Description,
			&item.OwnerUserID,
			&item.Visibility,
			&item.LatestRevision,
			&item.ConfigHash,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan profile: %w", err)
		}

		item.FleetIDs = splitCommaSeparatedValues(fleetIDsCSV)
		if len(item.FleetIDs) > 0 {
			item.FleetID = item.FleetIDs[0]
		}

		profiles = append(profiles, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during profile rows iteration: %w", err)
	}

	return profiles, nil
}

func ListProfilesForUser(ctx context.Context, userID string, isAdmin bool) ([]Profile, error) {
	if isAdmin {
		return ListProfiles(ctx)
	}

	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	userID = strings.TrimSpace(userID)

	rows, err := p.Query(ctx, `
		SELECT
			p.id::text,
			COALESCE(string_agg(DISTINCT f.id::text, ',' ORDER BY f.id::text), ''),
			COALESCE(string_agg(DISTINCT f.name, ', ' ORDER BY f.name), ''),
			p.name,
			p.description,
			COALESCE(p.owner_user_id::text, ''),
			p.visibility,
			COALESCE(pr.revision, 0),
			COALESCE(pr.config_hash, ''),
			to_char(p.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM profiles p
		LEFT JOIN profile_fleets pf ON pf.profile_id = p.id
		LEFT JOIN fleets f ON f.id = pf.fleet_id
		LEFT JOIN LATERAL (
			SELECT
				revision,
				config_hash
			FROM profile_revisions
			WHERE profile_id = p.id
			ORDER BY revision DESC
			LIMIT 1
		) pr ON true
		WHERE p.owner_user_id::text = $1
		   OR p.visibility = $2
		   OR EXISTS (
			SELECT 1
			FROM profile_user_permissions pup
			WHERE pup.profile_id = p.id
			  AND pup.user_id::text = $1
		)
		GROUP BY p.id, p.name, p.description, p.owner_user_id, p.visibility, pr.revision, pr.config_hash, p.created_at
		ORDER BY p.created_at DESC, p.name ASC
	`, userID, VisibilityVisible)
	if err != nil {
		return nil, fmt.Errorf("failed to list profiles for user: %w", err)
	}

	defer rows.Close()

	profiles := make([]Profile, 0)
	for rows.Next() {
		var item Profile
		var fleetIDsCSV string

		if err := rows.Scan(
			&item.ID,
			&fleetIDsCSV,
			&item.FleetName,
			&item.Name,
			&item.Description,
			&item.OwnerUserID,
			&item.Visibility,
			&item.LatestRevision,
			&item.ConfigHash,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan profile for user: %w", err)
		}

		item.FleetIDs = splitCommaSeparatedValues(fleetIDsCSV)
		if len(item.FleetIDs) > 0 {
			item.FleetID = item.FleetIDs[0]
		}

		profiles = append(profiles, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during profile rows iteration for user: %w", err)
	}

	return profiles, nil
}

func UserCanViewProfile(ctx context.Context, userID string, isAdmin bool, profileID string) (bool, error) {
	p := GetPool()
	if p == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	userID = strings.TrimSpace(userID)
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return false, ErrProfileNotFound
	}

	var canView bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profiles
			WHERE id::text = $1
			  AND (
				$2
				OR owner_user_id::text = $3
				OR visibility = $4
				OR EXISTS (
					SELECT 1
					FROM profile_user_permissions pup
					WHERE pup.profile_id = profiles.id
					  AND pup.user_id::text = $3
				)
			  )
		)
	`, profileID, isAdmin, userID, VisibilityVisible).Scan(&canView)
	if err != nil {
		return false, fmt.Errorf("failed to validate profile access: %w", err)
	}

	return canView, nil
}

func UserCanManageProfile(ctx context.Context, userID string, isAdmin bool, profileID string) (bool, error) {
	p := GetPool()
	if p == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	userID = strings.TrimSpace(userID)
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return false, ErrProfileNotFound
	}

	var canManage bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profiles
			WHERE id::text = $1
			  AND ($2 OR owner_user_id::text = $3)
		)
	`, profileID, isAdmin, userID).Scan(&canManage)
	if err != nil {
		return false, fmt.Errorf("failed to validate profile management access: %w", err)
	}

	return canManage, nil
}

func SetProfileVisibility(ctx context.Context, profileID, visibility string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return ErrProfileNotFound
	}

	normalizedVisibility, err := normalizeVisibility(visibility)
	if err != nil {
		return err
	}

	result, err := p.Exec(ctx, `
		UPDATE profiles
		SET visibility = $2
		WHERE id::text = $1
	`, profileID, normalizedVisibility)
	if err != nil {
		return fmt.Errorf("failed to update profile visibility: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrProfileNotFound
	}

	return nil
}

func GetProfileForEdit(ctx context.Context, profileID string) (ProfileEdit, error) {
	p := GetPool()
	if p == nil {
		return ProfileEdit{}, ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return ProfileEdit{}, ErrProfileNotFound
	}

	var item ProfileEdit

	err := p.QueryRow(ctx, `
		SELECT
			pr.id::text,
			pr.name,
			pr.description,
			COALESCE(pr.owner_user_id::text, ''),
			pr.visibility,
			to_char(pr.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM profiles pr
		WHERE pr.id::text = $1
	`, profileID).Scan(&item.ID, &item.Name, &item.Description, &item.OwnerUserID, &item.Visibility, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileEdit{}, ErrProfileNotFound
	}

	if err != nil {
		return ProfileEdit{}, fmt.Errorf("failed to load profile: %w", err)
	}

	assignedFleetIDs, assignedFleetNames, err := getProfileFleetAssignments(ctx, p, profileID)
	if err != nil {
		return ProfileEdit{}, err
	}

	item.FleetIDs = assignedFleetIDs
	if len(assignedFleetIDs) > 0 {
		item.FleetID = assignedFleetIDs[0]
	}

	item.FleetName = strings.Join(assignedFleetNames, ", ")

	err = p.QueryRow(ctx, `
		SELECT
			revision,
			config_hash,
			config_schema_version,
			config_json::text,
			raw_nix
		FROM profile_revisions
		WHERE profile_id::text = $1
		ORDER BY revision DESC
		LIMIT 1
	`, profileID).Scan(
		&item.LatestRevision,
		&item.ConfigHash,
		&item.ConfigSchemaVersion,
		&item.ConfigJSON,
		&item.RawNix,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileEdit{}, ErrProfileHasNoRevisions
	}

	if err != nil {
		return ProfileEdit{}, fmt.Errorf("failed to load latest profile revision: %w", err)
	}

	return item, nil
}

func CreateProfile(ctx context.Context, input CreateProfileInput, ownerUserID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	input.Name = strings.TrimSpace(input.Name)
	input.FleetIDs = normalizeFleetIDs(input.FleetIDs)
	input.Description = strings.TrimSpace(input.Description)
	input.RawNix = strings.TrimSpace(input.RawNix)
	ownerUserID = strings.TrimSpace(ownerUserID)

	var owner any
	if ownerUserID != "" {
		owner = ownerUserID
	}

	if input.Name == "" {
		return ErrNameRequired
	}

	if len(input.FleetIDs) == 0 {
		return ErrFleetRequired
	}

	primaryFleetID := input.FleetIDs[0]

	if input.ConfigSchemaVersion == 0 {
		input.ConfigSchemaVersion = 1
	}

	if input.ConfigSchemaVersion <= 0 {
		return ErrInvalidConfigSchemaVersion
	}

	canonicalConfigJSON, err := normalizeProfileConfigJSON(input.ConfigJSON)
	if err != nil {
		return err
	}

	configHash := calculateProfileConfigHash(input.ConfigSchemaVersion, canonicalConfigJSON, input.RawNix)

	tx, err := p.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin profile transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var profileID string

	err = tx.QueryRow(ctx, `
		INSERT INTO profiles (name, description, fleet_id, owner_user_id, visibility)
		VALUES ($1, $2, $3::uuid, $4, $5)
		RETURNING id::text
	`, input.Name, input.Description, primaryFleetID, owner, VisibilityPrivate).Scan(&profileID)
	if uniqueViolation(err) {
		return ErrProfileAlreadyExists
	}

	if foreignKeyViolation(err) {
		return ErrFleetNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to create profile: %w", err)
	}

	if err := replaceProfileFleetAssignments(ctx, tx, profileID, input.FleetIDs); err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO profile_revisions (
			profile_id,
			revision,
			config_schema_version,
			config_json,
			raw_nix,
			foreign_imports_enabled,
			foreign_imports,
			config_hash
		)
		VALUES ($1, 1, $2, $3::jsonb, $4, FALSE, '[]'::jsonb, $5)
	`, profileID, input.ConfigSchemaVersion, canonicalConfigJSON, input.RawNix, configHash)
	if err != nil {
		return fmt.Errorf("failed to create initial profile revision: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit profile transaction: %w", err)
	}

	return nil
}

func UpdateProfile(ctx context.Context, profileID string, input CreateProfileInput) (bool, error) {
	p := GetPool()
	if p == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	input.FleetIDs = normalizeFleetIDs(input.FleetIDs)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.RawNix = strings.TrimSpace(input.RawNix)

	if profileID == "" {
		return false, ErrProfileNotFound
	}

	if input.Name == "" {
		return false, ErrNameRequired
	}

	if len(input.FleetIDs) == 0 {
		return false, ErrFleetRequired
	}

	primaryFleetID := input.FleetIDs[0]

	canonicalConfigJSON, err := normalizeProfileConfigJSON(input.ConfigJSON)
	if err != nil {
		return false, err
	}

	tx, err := p.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to begin profile update transaction: %w", err)
	}

	defer func() {
		_ = tx.Rollback(ctx)
	}()

	result, err := tx.Exec(ctx, `
		UPDATE profiles
		SET
			name = $2,
			description = $3,
			fleet_id = $4::uuid
		WHERE id::text = $1
	`, profileID, input.Name, input.Description, primaryFleetID)
	if uniqueViolation(err) {
		return false, ErrProfileAlreadyExists
	}

	if foreignKeyViolation(err) {
		return false, ErrFleetNotFound
	}

	if err != nil {
		return false, fmt.Errorf("failed to update profile: %w", err)
	}

	if result.RowsAffected() == 0 {
		return false, ErrProfileNotFound
	}

	if err := replaceProfileFleetAssignments(ctx, tx, profileID, input.FleetIDs); err != nil {
		return false, err
	}

	var latestRevision int
	var latestConfigHash string
	var latestConfigSchemaVersion int

	err = tx.QueryRow(ctx, `
		SELECT
			revision,
			config_hash,
			config_schema_version
		FROM profile_revisions
		WHERE profile_id::text = $1
		ORDER BY revision DESC
		LIMIT 1
		FOR UPDATE
	`, profileID).Scan(&latestRevision, &latestConfigHash, &latestConfigSchemaVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrProfileHasNoRevisions
	}

	if err != nil {
		return false, fmt.Errorf("failed to load latest profile revision: %w", err)
	}

	configSchemaVersion := input.ConfigSchemaVersion
	if configSchemaVersion == 0 {
		configSchemaVersion = latestConfigSchemaVersion
	}

	if configSchemaVersion == 0 {
		configSchemaVersion = 1
	}

	if configSchemaVersion <= 0 {
		return false, ErrInvalidConfigSchemaVersion
	}

	configHash := calculateProfileConfigHash(configSchemaVersion, canonicalConfigJSON, input.RawNix)
	createdNewRevision := configHash != latestConfigHash

	if createdNewRevision {
		_, err = tx.Exec(ctx, `
			INSERT INTO profile_revisions (
				profile_id,
				revision,
				config_schema_version,
				config_json,
				raw_nix,
				foreign_imports_enabled,
				foreign_imports,
				config_hash
			)
			VALUES ($1::uuid, $2, $3, $4::jsonb, $5, FALSE, '[]'::jsonb, $6)
		`, profileID, latestRevision+1, configSchemaVersion, canonicalConfigJSON, input.RawNix, configHash)
		if err != nil {
			return false, fmt.Errorf("failed to create profile revision: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("failed to commit profile update transaction: %w", err)
	}

	return createdNewRevision, nil
}

func DeleteProfile(ctx context.Context, profileID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return ErrProfileNotFound
	}

	result, err := p.Exec(ctx, `
		DELETE FROM profiles
		WHERE id::text = $1
	`, profileID)
	if err != nil {
		return fmt.Errorf("failed to delete profile: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrProfileNotFound
	}

	return nil
}

type profileFleetQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func getProfileFleetAssignments(ctx context.Context, queryer profileFleetQueryer, profileID string) ([]string, []string, error) {
	rows, err := queryer.Query(ctx, `
		SELECT
			f.id::text,
			f.name
		FROM profile_fleets pf
		JOIN fleets f ON f.id = pf.fleet_id
		WHERE pf.profile_id::text = $1
		ORDER BY f.name ASC, f.id ASC
	`, profileID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list profile fleets: %w", err)
	}

	defer rows.Close()

	fleetIDs := make([]string, 0)
	fleetNames := make([]string, 0)

	for rows.Next() {
		var fleetID string
		var fleetName string

		if err := rows.Scan(&fleetID, &fleetName); err != nil {
			return nil, nil, fmt.Errorf("failed to scan profile fleet assignment: %w", err)
		}

		fleetIDs = append(fleetIDs, fleetID)
		fleetNames = append(fleetNames, fleetName)
	}

	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed during profile fleet assignment rows iteration: %w", err)
	}

	return fleetIDs, fleetNames, nil
}

func replaceProfileFleetAssignments(ctx context.Context, tx pgx.Tx, profileID string, fleetIDs []string) error {
	normalizedFleetIDs := normalizeFleetIDs(fleetIDs)
	if len(normalizedFleetIDs) == 0 {
		return ErrFleetRequired
	}

	_, err := tx.Exec(ctx, `
		DELETE FROM profile_fleets
		WHERE profile_id = $1::uuid
	`, profileID)
	if err != nil {
		return fmt.Errorf("failed to clear profile fleet assignments: %w", err)
	}

	for _, fleetID := range normalizedFleetIDs {
		_, err = tx.Exec(ctx, `
			INSERT INTO profile_fleets (profile_id, fleet_id)
			VALUES ($1::uuid, $2::uuid)
		`, profileID, fleetID)
		if foreignKeyViolation(err) {
			return ErrFleetNotFound
		}

		if err != nil {
			return fmt.Errorf("failed to assign profile to fleet: %w", err)
		}
	}

	return nil
}

func normalizeFleetIDs(rawFleetIDs []string) []string {
	if len(rawFleetIDs) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(rawFleetIDs))
	seen := make(map[string]struct{}, len(rawFleetIDs))

	for _, rawFleetID := range rawFleetIDs {
		fleetID := strings.TrimSpace(rawFleetID)
		if fleetID == "" {
			continue
		}

		if _, exists := seen[fleetID]; exists {
			continue
		}

		seen[fleetID] = struct{}{}
		normalized = append(normalized, fleetID)
	}

	return normalized
}

func splitCommaSeparatedValues(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return []string{}
	}

	segments := strings.Split(value, ",")
	normalized := make([]string, 0, len(segments))

	for _, segment := range segments {
		trimmed := strings.TrimSpace(segment)
		if trimmed == "" {
			continue
		}

		normalized = append(normalized, trimmed)
	}

	return normalized
}

func normalizeProfileConfigJSON(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		trimmed = "{}"
	}

	var decoded any

	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return "", ErrInvalidProfileConfigJSON
	}

	if _, ok := decoded.(map[string]any); !ok {
		return "", ErrProfileConfigMustBeObject
	}

	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("failed to normalize profile config JSON: %w", err)
	}

	return string(canonical), nil
}

func calculateProfileConfigHash(configSchemaVersion int, canonicalConfigJSON, rawNix string) string {
	material := fmt.Sprintf("%d\n%s\n%s\n%s", configSchemaVersion, canonicalConfigJSON, strings.TrimSpace(rawNix), "[]")
	sum := sha256.Sum256([]byte(material))

	return hex.EncodeToString(sum[:])
}

func ListBuilds(ctx context.Context) ([]Build, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			b.id::text,
			p.id::text,
			p.name,
			COALESCE(f.id::text, ''),
			COALESCE(f.name, ''),
			pr.id::text,
			pr.revision,
			b.version,
			b.status,
			b.artifact_path,
			b.installer_status,
			b.installer_artifact_path,
			to_char(b.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM builds b
		JOIN profile_revisions pr ON pr.id = b.profile_revision_id
		JOIN profiles p ON p.id = pr.profile_id
		LEFT JOIN fleets f ON f.id = b.fleet_id
		ORDER BY b.created_at DESC, p.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list builds: %w", err)
	}

	defer rows.Close()

	builds := make([]Build, 0)
	for rows.Next() {
		var item Build

		if err := rows.Scan(
			&item.ID,
			&item.ProfileID,
			&item.ProfileName,
			&item.FleetID,
			&item.FleetName,
			&item.ProfileRevisionID,
			&item.ProfileRevision,
			&item.Version,
			&item.Status,
			&item.Artifact,
			&item.InstallerStatus,
			&item.InstallerArtifact,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan build: %w", err)
		}

		builds = append(builds, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during build rows iteration: %w", err)
	}

	return builds, nil
}

func GetBuildByID(ctx context.Context, buildID string) (Build, error) {
	p := GetPool()
	if p == nil {
		return Build{}, ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return Build{}, ErrBuildRequired
	}

	var item Build

	err := p.QueryRow(ctx, `
		SELECT
			b.id::text,
			p.id::text,
			p.name,
			COALESCE(f.id::text, ''),
			COALESCE(f.name, ''),
			pr.id::text,
			pr.revision,
			b.version,
			b.status,
			b.artifact_path,
			b.installer_status,
			b.installer_artifact_path,
			to_char(b.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM builds b
		JOIN profile_revisions pr ON pr.id = b.profile_revision_id
		JOIN profiles p ON p.id = pr.profile_id
		LEFT JOIN fleets f ON f.id = b.fleet_id
		WHERE b.id::text = $1
	`, buildID).Scan(
		&item.ID,
		&item.ProfileID,
		&item.ProfileName,
		&item.FleetID,
		&item.FleetName,
		&item.ProfileRevisionID,
		&item.ProfileRevision,
		&item.Version,
		&item.Status,
		&item.Artifact,
		&item.InstallerStatus,
		&item.InstallerArtifact,
		&item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Build{}, ErrBuildNotFound
	}

	if err != nil {
		return Build{}, fmt.Errorf("failed to load build: %w", err)
	}

	return item, nil
}

func DeleteBuild(ctx context.Context, buildID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return ErrBuildRequired
	}

	result, err := p.Exec(ctx, `
		DELETE FROM builds
		WHERE id::text = $1
	`, buildID)
	if err != nil {
		return fmt.Errorf("failed to delete build: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrBuildNotFound
	}

	return nil
}

func GetBuildExecutionMetadata(ctx context.Context, buildID string) (string, string, error) {
	p := GetPool()
	if p == nil {
		return "", "", ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return "", "", ErrBuildRequired
	}

	var configJSON string
	var fleetID string

	err := p.QueryRow(ctx, `
		SELECT
			pr.config_json::text,
			COALESCE(b.fleet_id::text, '')
		FROM builds b
		JOIN profile_revisions pr ON pr.id = b.profile_revision_id
		WHERE b.id::text = $1
	`, buildID).Scan(&configJSON, &fleetID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrBuildNotFound
	}

	if err != nil {
		return "", "", fmt.Errorf("failed to load build profile execution metadata: %w", err)
	}

	if fleetID == "" {
		return "", "", ErrProfileFleetRequired
	}

	return configJSON, fleetID, nil
}

func CreateBuild(ctx context.Context, input CreateBuildInput) (string, error) {
	p := GetPool()
	if p == nil {
		return "", ErrDatabaseConnectionNotInitialized
	}

	input.ProfileID = strings.TrimSpace(input.ProfileID)
	input.FleetID = strings.TrimSpace(input.FleetID)
	input.Version = strings.TrimSpace(input.Version)
	status := BuildStatusQueued
	artifact := ""

	if input.ProfileID == "" {
		return "", ErrProfileRequired
	}

	if input.FleetID == "" {
		return "", ErrFleetRequired
	}

	if input.Version == "" {
		return "", ErrVersionRequired
	}

	if !isSemanticVersion(input.Version) {
		return "", ErrVersionMustBeSemver
	}

	var profileExists bool

	err := p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profiles
			WHERE id::text = $1
		)
	`, input.ProfileID).Scan(&profileExists)
	if err != nil {
		return "", fmt.Errorf("failed to load profile for build: %w", err)
	}

	if !profileExists {
		return "", ErrProfileNotFound
	}

	var fleetExists bool

	err = p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM fleets
			WHERE id::text = $1
		)
	`, input.FleetID).Scan(&fleetExists)
	if err != nil {
		return "", fmt.Errorf("failed to load fleet for build: %w", err)
	}

	if !fleetExists {
		return "", ErrFleetNotFound
	}

	var profileAssignedToFleet bool

	err = p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM profile_fleets
			WHERE profile_id::text = $1
			  AND fleet_id::text = $2
		)
	`, input.ProfileID, input.FleetID).Scan(&profileAssignedToFleet)
	if err != nil {
		return "", fmt.Errorf("failed to validate profile fleet assignment for build: %w", err)
	}

	if !profileAssignedToFleet {
		return "", ErrProfileNotAssignedToFleet
	}

	var buildID string

	err = p.QueryRow(ctx, `
		WITH latest_revision AS (
			SELECT id
			FROM profile_revisions
			WHERE profile_id = $1
			ORDER BY revision DESC
			LIMIT 1
		)
		INSERT INTO builds (profile_revision_id, fleet_id, version, status, artifact_path)
		SELECT id, $2::uuid, $3, $4, $5
		FROM latest_revision
		RETURNING id::text
	`, input.ProfileID, input.FleetID, input.Version, status, artifact).Scan(&buildID)

	if uniqueViolation(err) {
		return "", ErrBuildVersionAlreadyExists
	}

	if foreignKeyViolation(err) {
		if strings.Contains(err.Error(), "builds_fleet_id_fkey") {
			return "", ErrFleetNotFound
		}

		if strings.Contains(err.Error(), "builds_profile_revision_id_fkey") {
			return "", ErrProfileNotFound
		}
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrProfileHasNoRevisions
	}

	if err != nil {
		return "", fmt.Errorf("failed to create build: %w", err)
	}

	return buildID, nil
}

func AppendBuildLogChunk(ctx context.Context, buildID, chunk string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return ErrBuildRequired
	}

	if chunk == "" {
		return nil
	}

	_, err := p.Exec(ctx, `
		INSERT INTO build_log_chunks (build_id, chunk)
		VALUES ($1::uuid, $2)
	`, buildID, chunk)
	if foreignKeyViolation(err) {
		return ErrBuildNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to append build log chunk: %w", err)
	}

	return nil
}

func AppendBuildInstallerLogChunk(ctx context.Context, buildID, chunk string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return ErrBuildRequired
	}

	if chunk == "" {
		return nil
	}

	_, err := p.Exec(ctx, `
		INSERT INTO build_installer_log_chunks (build_id, chunk)
		VALUES ($1::uuid, $2)
	`, buildID, chunk)
	if foreignKeyViolation(err) {
		return ErrBuildNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to append build installer log chunk: %w", err)
	}

	return nil
}

func ListBuildLogChunksSince(ctx context.Context, buildID string, afterID int64, limit int) ([]BuildLogChunk, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return nil, ErrBuildRequired
	}

	if afterID < 0 {
		afterID = 0
	}

	if limit <= 0 {
		limit = 128
	}

	if limit > 512 {
		limit = 512
	}

	rows, err := p.Query(ctx, `
		SELECT id, chunk
		FROM build_log_chunks
		WHERE build_id = $1::uuid
		  AND id > $2
		ORDER BY id ASC
		LIMIT $3
	`, buildID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list build log chunks: %w", err)
	}

	defer rows.Close()

	chunks := make([]BuildLogChunk, 0)
	for rows.Next() {
		var chunk BuildLogChunk

		if err := rows.Scan(&chunk.ID, &chunk.Content); err != nil {
			return nil, fmt.Errorf("failed to scan build log chunk: %w", err)
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during build log chunk rows iteration: %w", err)
	}

	return chunks, nil
}

func ListBuildInstallerLogChunksSince(ctx context.Context, buildID string, afterID int64, limit int) ([]BuildLogChunk, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return nil, ErrBuildRequired
	}

	if afterID < 0 {
		afterID = 0
	}

	if limit <= 0 {
		limit = 128
	}

	if limit > 512 {
		limit = 512
	}

	rows, err := p.Query(ctx, `
		SELECT id, chunk
		FROM build_installer_log_chunks
		WHERE build_id = $1::uuid
		  AND id > $2
		ORDER BY id ASC
		LIMIT $3
	`, buildID, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list build installer log chunks: %w", err)
	}

	defer rows.Close()

	chunks := make([]BuildLogChunk, 0)
	for rows.Next() {
		var chunk BuildLogChunk

		if err := rows.Scan(&chunk.ID, &chunk.Content); err != nil {
			return nil, fmt.Errorf("failed to scan build installer log chunk: %w", err)
		}

		chunks = append(chunks, chunk)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during build installer log chunk rows iteration: %w", err)
	}

	return chunks, nil
}

func UpdateBuild(ctx context.Context, buildID, status, artifact string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	artifact = strings.TrimSpace(artifact)

	if buildID == "" {
		return ErrBuildRequired
	}

	normalizedStatus, err := normalizeBuildStatus(status)
	if err != nil {
		return err
	}

	result, err := p.Exec(ctx, `
		UPDATE builds
		SET
			status = $2,
			artifact_path = $3,
			started_at = CASE
				WHEN $2 = 'running' AND started_at IS NULL THEN now()
				ELSE started_at
			END,
			finished_at = CASE
				WHEN $2 IN ('succeeded', 'failed') THEN now()
				ELSE finished_at
			END
		WHERE id = $1::uuid
	`, buildID, normalizedStatus, artifact)
	if err != nil {
		return fmt.Errorf("failed to update build: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrBuildNotFound
	}

	return nil
}

func QueueBuildInstaller(ctx context.Context, buildID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return ErrBuildRequired
	}

	var buildStatus string
	var installerStatus string

	err := p.QueryRow(ctx, `
		SELECT
			status,
			installer_status
		FROM builds
		WHERE id::text = $1
	`, buildID).Scan(&buildStatus, &installerStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBuildNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to load build installer state: %w", err)
	}

	if buildStatus != BuildStatusSucceeded {
		return ErrBuildNotReadyForInstaller
	}

	if installerStatus == BuildInstallerStatusQueued || installerStatus == BuildInstallerStatusRunning {
		return ErrBuildInstallerAlreadyQueued
	}

	if _, err := p.Exec(ctx, `
		DELETE FROM build_installer_log_chunks
		WHERE build_id = $1::uuid
	`, buildID); err != nil {
		return fmt.Errorf("failed to clear installer logs: %w", err)
	}

	result, err := p.Exec(ctx, `
		UPDATE builds
		SET
			installer_status = $2,
			installer_artifact_path = '',
			installer_started_at = NULL,
			installer_finished_at = NULL
		WHERE id = $1::uuid
	`, buildID, BuildInstallerStatusQueued)
	if err != nil {
		return fmt.Errorf("failed to queue installer build: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrBuildNotFound
	}

	return nil
}

func UpdateBuildInstaller(ctx context.Context, buildID, status, artifact string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	buildID = strings.TrimSpace(buildID)
	artifact = strings.TrimSpace(artifact)

	if buildID == "" {
		return ErrBuildRequired
	}

	normalizedStatus, err := normalizeBuildInstallerStatus(status)
	if err != nil {
		return err
	}

	result, err := p.Exec(ctx, `
		UPDATE builds
		SET
			installer_status = $2,
			installer_artifact_path = $3,
			installer_started_at = CASE
				WHEN $2 = 'running' AND installer_started_at IS NULL THEN now()
				WHEN $2 = 'queued' THEN NULL
				ELSE installer_started_at
			END,
			installer_finished_at = CASE
				WHEN $2 IN ('succeeded', 'failed') THEN now()
				WHEN $2 IN ('queued', 'running') THEN NULL
				ELSE installer_finished_at
			END
		WHERE id = $1::uuid
	`, buildID, normalizedStatus, artifact)
	if err != nil {
		return fmt.Errorf("failed to update build installer state: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrBuildNotFound
	}

	return nil
}

func FailRunningBuilds(ctx context.Context) (int64, error) {
	p := GetPool()
	if p == nil {
		return 0, ErrDatabaseConnectionNotInitialized
	}

	result, err := p.Exec(ctx, `
		UPDATE builds
		SET
			status = $1,
			artifact_path = '',
			finished_at = now()
		WHERE status = $2
	`, BuildStatusFailed, BuildStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("failed to mark running builds as failed: %w", err)
	}

	return result.RowsAffected(), nil
}

func FailRunningBuildInstallers(ctx context.Context) (int64, error) {
	p := GetPool()
	if p == nil {
		return 0, ErrDatabaseConnectionNotInitialized
	}

	result, err := p.Exec(ctx, `
		UPDATE builds
		SET
			installer_status = $1,
			installer_artifact_path = '',
			installer_finished_at = now()
		WHERE installer_status = $2
	`, BuildInstallerStatusFailed, BuildInstallerStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("failed to mark running installer builds as failed: %w", err)
	}

	return result.RowsAffected(), nil
}

func ListReleases(ctx context.Context) ([]Release, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			r.id::text,
			b.id::text,
			b.version,
			p.name,
			COALESCE(f.id::text, ''),
			COALESCE(f.name, ''),
			r.channel,
			r.version,
			r.notes,
			r.status,
			to_char(r.published_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM releases r
		JOIN builds b ON b.id = r.build_id
		JOIN profile_revisions pr ON pr.id = b.profile_revision_id
		JOIN profiles p ON p.id = pr.profile_id
		LEFT JOIN fleets f ON f.id = b.fleet_id
		ORDER BY r.published_at DESC, r.version DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	defer rows.Close()

	releases := make([]Release, 0)
	for rows.Next() {
		var item Release

		if err := rows.Scan(
			&item.ID,
			&item.BuildID,
			&item.Build,
			&item.ProfileName,
			&item.FleetID,
			&item.FleetName,
			&item.Channel,
			&item.Version,
			&item.Notes,
			&item.Status,
			&item.PublishedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan release: %w", err)
		}

		releases = append(releases, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during release rows iteration: %w", err)
	}

	return releases, nil
}

func GetReleaseByID(ctx context.Context, releaseID string) (Release, error) {
	p := GetPool()
	if p == nil {
		return Release{}, ErrDatabaseConnectionNotInitialized
	}

	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return Release{}, ErrReleaseRequired
	}

	var item Release

	err := p.QueryRow(ctx, `
		SELECT
			r.id::text,
			b.id::text,
			b.version,
			p.name,
			COALESCE(f.id::text, ''),
			COALESCE(f.name, ''),
			r.channel,
			r.version,
			r.notes,
			r.status,
			to_char(r.published_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM releases r
		JOIN builds b ON b.id = r.build_id
		JOIN profile_revisions pr ON pr.id = b.profile_revision_id
		JOIN profiles p ON p.id = pr.profile_id
		LEFT JOIN fleets f ON f.id = b.fleet_id
		WHERE r.id::text = $1
	`, releaseID).Scan(
		&item.ID,
		&item.BuildID,
		&item.Build,
		&item.ProfileName,
		&item.FleetID,
		&item.FleetName,
		&item.Channel,
		&item.Version,
		&item.Notes,
		&item.Status,
		&item.PublishedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Release{}, ErrReleaseNotFound
	}

	if err != nil {
		return Release{}, fmt.Errorf("failed to get release by id: %w", err)
	}

	return item, nil
}

func DeleteRelease(ctx context.Context, releaseID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return ErrReleaseRequired
	}

	result, err := p.Exec(ctx, `
		DELETE FROM releases
		WHERE id::text = $1
	`, releaseID)
	if err != nil {
		return fmt.Errorf("failed to delete release: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrReleaseNotFound
	}

	return nil
}

func CreateRelease(ctx context.Context, input CreateReleaseInput) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	input.BuildID = strings.TrimSpace(input.BuildID)
	input.Channel = strings.ToLower(strings.TrimSpace(input.Channel))
	input.Version = strings.TrimSpace(input.Version)
	input.Notes = strings.TrimSpace(input.Notes)

	if input.BuildID == "" {
		return ErrBuildRequired
	}

	if input.Version == "" {
		return ErrVersionRequired
	}

	if !isSemanticVersion(input.Version) {
		return ErrVersionMustBeSemver
	}

	if input.Channel == "" {
		input.Channel = "stable"
	}

	_, err := p.Exec(ctx, `
		INSERT INTO releases (build_id, channel, version, notes)
		VALUES ($1, $2, $3, $4)
	`, input.BuildID, input.Channel, input.Version, input.Notes)

	if uniqueViolation(err) {
		return ErrReleaseVersionAlreadyExists
	}

	if foreignKeyViolation(err) {
		return ErrBuildNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to create release: %w", err)
	}

	return nil
}

func GetReleaseTakedownInfo(ctx context.Context, releaseID string) (ReleaseTakedownInfo, error) {
	p := GetPool()
	if p == nil {
		return ReleaseTakedownInfo{}, ErrDatabaseConnectionNotInitialized
	}

	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return ReleaseTakedownInfo{}, ErrReleaseRequired
	}

	var item ReleaseTakedownInfo

	err := p.QueryRow(ctx, `
		SELECT
			r.id::text,
			b.id::text,
			COALESCE(f.id::text, ''),
			COALESCE(f.name, ''),
			r.status
		FROM releases r
		JOIN builds b ON b.id = r.build_id
		LEFT JOIN fleets f ON f.id = b.fleet_id
		WHERE r.id::text = $1
	`, releaseID).Scan(&item.ReleaseID, &item.BuildID, &item.FleetID, &item.FleetName, &item.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseTakedownInfo{}, ErrReleaseNotFound
	}

	if err != nil {
		return ReleaseTakedownInfo{}, fmt.Errorf("failed to load release takedown info: %w", err)
	}

	if item.FleetID == "" {
		return ReleaseTakedownInfo{}, ErrReleaseFleetNotConfigured
	}

	var currentLiveReleaseID string
	err = p.QueryRow(ctx, `
		SELECT COALESCE((
			SELECT release_id::text
			FROM rollouts
			WHERE fleet_id = $1::uuid
				AND status = $2
			ORDER BY completed_at DESC NULLS LAST, created_at DESC
			LIMIT 1
		), '')
	`, item.FleetID, RolloutStatusCompleted).Scan(&currentLiveReleaseID)
	if err != nil {
		return ReleaseTakedownInfo{}, fmt.Errorf("failed to resolve current fleet release: %w", err)
	}

	item.IsCurrentlyLive = currentLiveReleaseID == item.ReleaseID

	return item, nil
}

func SetReleaseStatus(ctx context.Context, releaseID, status string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return ErrReleaseRequired
	}

	normalizedStatus, err := normalizeReleaseStatus(status)
	if err != nil {
		return err
	}

	result, err := p.Exec(ctx, `
		UPDATE releases
		SET status = $2
		WHERE id::text = $1
	`, releaseID, normalizedStatus)
	if err != nil {
		return fmt.Errorf("failed to set release status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrReleaseNotFound
	}

	return nil
}

func isSemanticVersion(value string) bool {
	value = strings.TrimSpace(value)

	return semanticVersionPattern.MatchString(value)
}

func ListDevices(ctx context.Context) ([]Device, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			d.id::text,
			f.id::text,
			f.name,
			d.hostname,
			d.serial_number,
			COALESCE(curr.version, ''),
			COALESCE(des.version, ''),
			d.update_state,
			d.attestation_level,
			COALESCE(to_char(d.last_seen_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
			to_char(d.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM devices d
		JOIN fleets f ON f.id = d.fleet_id
		LEFT JOIN releases curr ON curr.id = d.current_release_id
		LEFT JOIN releases des ON des.id = d.desired_release_id
		ORDER BY d.created_at DESC, d.hostname ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list devices: %w", err)
	}

	defer rows.Close()

	devices := make([]Device, 0)
	for rows.Next() {
		var item Device

		if err := rows.Scan(
			&item.ID,
			&item.FleetID,
			&item.FleetName,
			&item.Hostname,
			&item.SerialNumber,
			&item.CurrentReleaseVersion,
			&item.DesiredReleaseVersion,
			&item.UpdateState,
			&item.AttestationLevel,
			&item.LastSeenAt,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan device: %w", err)
		}

		devices = append(devices, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during device rows iteration: %w", err)
	}

	return devices, nil
}

func CreateDevice(ctx context.Context, input CreateDeviceInput) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	input.FleetID = strings.TrimSpace(input.FleetID)
	input.Hostname = strings.TrimSpace(input.Hostname)
	input.SerialNumber = strings.TrimSpace(input.SerialNumber)
	input.AttestationLevel = strings.TrimSpace(input.AttestationLevel)

	state, err := normalizeDeviceState(input.UpdateState)
	if err != nil {
		return err
	}

	if input.FleetID == "" {
		return ErrFleetRequired
	}

	if input.Hostname == "" {
		return ErrHostnameRequired
	}

	if input.SerialNumber == "" {
		return ErrSerialRequired
	}

	if input.AttestationLevel == "" {
		input.AttestationLevel = "unknown"
	}

	_, err = p.Exec(ctx, `
		INSERT INTO devices (fleet_id, hostname, serial_number, update_state, attestation_level)
		VALUES ($1, $2, $3, $4, $5)
	`, input.FleetID, input.Hostname, input.SerialNumber, state, input.AttestationLevel)

	if uniqueViolation(err) {
		if strings.Contains(err.Error(), "devices_serial_number_key") {
			return ErrDeviceSerialAlreadyExists
		}

		return ErrDeviceHostnameAlreadyExists
	}

	if foreignKeyViolation(err) {
		return ErrFleetNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to create device: %w", err)
	}

	return nil
}

func DeleteDevicesByFleet(ctx context.Context, fleetID string) (int64, error) {
	p := GetPool()
	if p == nil {
		return 0, ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return 0, ErrFleetRequired
	}

	result, err := p.Exec(ctx, `
		DELETE FROM devices
		WHERE fleet_id::text = $1
	`, fleetID)
	if err != nil {
		return 0, fmt.Errorf("failed to delete fleet devices: %w", err)
	}

	return result.RowsAffected(), nil
}

func GetReleaseDeploymentInfo(ctx context.Context, releaseID string) (ReleaseDeploymentInfo, error) {
	p := GetPool()
	if p == nil {
		return ReleaseDeploymentInfo{}, ErrDatabaseConnectionNotInitialized
	}

	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return ReleaseDeploymentInfo{}, ErrReleaseRequired
	}

	var item ReleaseDeploymentInfo
	var releaseStatus string

	err := p.QueryRow(ctx, `
		SELECT
			r.id::text,
			r.version,
			r.status,
			b.id::text,
			COALESCE(f.id::text, ''),
			COALESCE(f.name, '')
		FROM releases r
		JOIN builds b ON b.id = r.build_id
		JOIN profile_revisions pr ON pr.id = b.profile_revision_id
		JOIN profiles p ON p.id = pr.profile_id
		LEFT JOIN fleets f ON f.id = b.fleet_id
		WHERE r.id::text = $1
	`, releaseID).Scan(&item.ReleaseID, &item.ReleaseVersion, &releaseStatus, &item.BuildID, &item.FleetID, &item.FleetName)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReleaseDeploymentInfo{}, ErrReleaseNotFound
	}

	if err != nil {
		return ReleaseDeploymentInfo{}, fmt.Errorf("failed to load release deployment info: %w", err)
	}

	if item.FleetID == "" {
		return ReleaseDeploymentInfo{}, ErrReleaseFleetNotConfigured
	}

	if releaseStatus == ReleaseStatusWithdrawn {
		return ReleaseDeploymentInfo{}, ErrReleaseWithdrawn
	}

	return item, nil
}

func SetFleetDesiredRelease(ctx context.Context, fleetID, releaseID string) (int64, error) {
	p := GetPool()
	if p == nil {
		return 0, ErrDatabaseConnectionNotInitialized
	}

	fleetID = strings.TrimSpace(fleetID)
	releaseID = strings.TrimSpace(releaseID)

	if fleetID == "" {
		return 0, ErrFleetRequired
	}

	if releaseID == "" {
		return 0, ErrReleaseRequired
	}

	result, err := p.Exec(ctx, `
		UPDATE devices
		SET desired_release_id = $2::uuid
		WHERE fleet_id = $1::uuid
	`, fleetID, releaseID)
	if err != nil {
		return 0, fmt.Errorf("failed to set fleet desired release: %w", err)
	}

	return result.RowsAffected(), nil
}

func ListRollouts(ctx context.Context) ([]Rollout, error) {
	p := GetPool()
	if p == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := p.Query(ctx, `
		SELECT
			r.id::text,
			f.id::text,
			f.name,
			rel.id::text,
			rel.version,
			rel.status,
			r.strategy,
			r.stage_percent,
			r.status,
			COALESCE(to_char(r.started_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
			COALESCE(to_char(r.completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
			to_char(r.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM rollouts r
		JOIN fleets f ON f.id = r.fleet_id
		JOIN releases rel ON rel.id = r.release_id
		ORDER BY r.created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to list rollouts: %w", err)
	}

	defer rows.Close()

	rollouts := make([]Rollout, 0)
	for rows.Next() {
		var item Rollout

		if err := rows.Scan(
			&item.ID,
			&item.FleetID,
			&item.FleetName,
			&item.ReleaseID,
			&item.ReleaseVersion,
			&item.ReleaseStatus,
			&item.Strategy,
			&item.StagePercent,
			&item.Status,
			&item.StartedAt,
			&item.CompletedAt,
			&item.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan rollout: %w", err)
		}

		rollouts = append(rollouts, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during rollout rows iteration: %w", err)
	}

	return rollouts, nil
}

func GetRolloutByID(ctx context.Context, rolloutID string) (Rollout, error) {
	p := GetPool()
	if p == nil {
		return Rollout{}, ErrDatabaseConnectionNotInitialized
	}

	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return Rollout{}, ErrRolloutNotFound
	}

	var item Rollout

	err := p.QueryRow(ctx, `
		SELECT
			r.id::text,
			f.id::text,
			f.name,
			rel.id::text,
			rel.version,
			rel.status,
			r.strategy,
			r.stage_percent,
			r.status,
			COALESCE(to_char(r.started_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
			COALESCE(to_char(r.completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
			to_char(r.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM rollouts r
		JOIN fleets f ON f.id = r.fleet_id
		JOIN releases rel ON rel.id = r.release_id
		WHERE r.id::text = $1
	`, rolloutID).Scan(
		&item.ID,
		&item.FleetID,
		&item.FleetName,
		&item.ReleaseID,
		&item.ReleaseVersion,
		&item.ReleaseStatus,
		&item.Strategy,
		&item.StagePercent,
		&item.Status,
		&item.StartedAt,
		&item.CompletedAt,
		&item.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return Rollout{}, ErrRolloutNotFound
	}

	if err != nil {
		return Rollout{}, fmt.Errorf("failed to get rollout by id: %w", err)
	}

	return item, nil
}

func DeleteRollout(ctx context.Context, rolloutID string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return ErrRolloutNotFound
	}

	result, err := p.Exec(ctx, `
		DELETE FROM rollouts
		WHERE id::text = $1
	`, rolloutID)
	if err != nil {
		return fmt.Errorf("failed to delete rollout: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrRolloutNotFound
	}

	return nil
}

func CreateRollout(ctx context.Context, input CreateRolloutInput) (string, error) {
	p := GetPool()
	if p == nil {
		return "", ErrDatabaseConnectionNotInitialized
	}

	input.FleetID = strings.TrimSpace(input.FleetID)
	input.ReleaseID = strings.TrimSpace(input.ReleaseID)

	strategy, err := normalizeRolloutStrategy(input.Strategy)
	if err != nil {
		return "", err
	}

	status, err := normalizeRolloutStatus(input.Status)
	if err != nil {
		return "", err
	}

	if input.FleetID == "" {
		return "", ErrFleetRequired
	}

	if input.ReleaseID == "" {
		return "", ErrReleaseRequired
	}

	if input.StagePercent <= 0 {
		input.StagePercent = 100
	}

	if input.StagePercent < 1 || input.StagePercent > 100 {
		return "", ErrInvalidStageValue
	}

	if strategy != RolloutStrategyAllAtOnce {
		return "", ErrInvalidStrategy
	}

	if input.StagePercent != 100 {
		return "", ErrInvalidStageValue
	}

	matched, err := releaseBelongsToFleet(ctx, input.ReleaseID, input.FleetID)
	if err != nil {
		return "", err
	}

	if !matched {
		return "", ErrRolloutFleetReleaseMismatch
	}

	var rolloutID string

	err = p.QueryRow(ctx, `
		INSERT INTO rollouts (fleet_id, release_id, strategy, stage_percent, status, started_at, completed_at)
		VALUES (
			$1::uuid,
			$2::uuid,
			$3,
			$4,
			$5,
			CASE
				WHEN $5 IN ('in_progress', 'completed', 'failed') THEN now()
				ELSE NULL
			END,
			CASE
				WHEN $5 IN ('completed', 'failed') THEN now()
				ELSE NULL
			END
		)
		RETURNING id::text
	`, input.FleetID, input.ReleaseID, strategy, input.StagePercent, status).Scan(&rolloutID)
	if foreignKeyViolation(err) {
		if strings.Contains(err.Error(), "rollouts_fleet_id_fkey") {
			return "", ErrFleetNotFound
		}

		if strings.Contains(err.Error(), "rollouts_release_id_fkey") {
			return "", ErrReleaseNotFound
		}
	}

	if err != nil {
		return "", fmt.Errorf("failed to create rollout: %w", err)
	}

	return rolloutID, nil
}

func UpdateRolloutStatus(ctx context.Context, rolloutID, status string) error {
	p := GetPool()
	if p == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	rolloutID = strings.TrimSpace(rolloutID)
	if rolloutID == "" {
		return ErrRolloutNotFound
	}

	normalizedStatus, err := normalizeRolloutStatus(status)
	if err != nil {
		return err
	}

	result, err := p.Exec(ctx, `
		UPDATE rollouts
		SET
			status = $2,
			started_at = CASE
				WHEN $2 IN ('in_progress', 'completed', 'failed') AND started_at IS NULL THEN now()
				WHEN $2 = 'planned' THEN NULL
				ELSE started_at
			END,
			completed_at = CASE
				WHEN $2 IN ('completed', 'failed') THEN now()
				ELSE NULL
			END
		WHERE id = $1::uuid
	`, rolloutID, normalizedStatus)
	if err != nil {
		return fmt.Errorf("failed to update rollout status: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrRolloutNotFound
	}

	return nil
}

func releaseBelongsToFleet(ctx context.Context, releaseID, fleetID string) (bool, error) {
	p := GetPool()
	if p == nil {
		return false, ErrDatabaseConnectionNotInitialized
	}

	releaseID = strings.TrimSpace(releaseID)
	fleetID = strings.TrimSpace(fleetID)

	if releaseID == "" {
		return false, ErrReleaseRequired
	}

	if fleetID == "" {
		return false, ErrFleetRequired
	}

	var releaseFleetID string
	var releaseStatus string

	err := p.QueryRow(ctx, `
		SELECT
			COALESCE(b.fleet_id::text, ''),
			r.status
		FROM releases r
		JOIN builds b ON b.id = r.build_id
		WHERE r.id::text = $1
	`, releaseID).Scan(&releaseFleetID, &releaseStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrReleaseNotFound
	}

	if err != nil {
		return false, fmt.Errorf("failed to validate rollout release fleet: %w", err)
	}

	if releaseFleetID == "" {
		return false, ErrReleaseFleetNotConfigured
	}

	if releaseStatus == ReleaseStatusWithdrawn {
		return false, ErrReleaseWithdrawn
	}

	return releaseFleetID == fleetID, nil
}

func normalizeBuildStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		return BuildStatusQueued, nil
	}

	if !containsString(validBuildStatuses(), status) {
		return "", ErrInvalidStatus
	}

	return status, nil
}

func normalizeBuildInstallerStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		return BuildInstallerStatusNotRequested, nil
	}

	if !containsString(validBuildInstallerStatuses(), status) {
		return "", ErrInvalidStatus
	}

	return status, nil
}

func normalizeDeviceState(raw string) (string, error) {
	state := strings.ToLower(strings.TrimSpace(raw))
	if state == "" {
		return DeviceStateIdle, nil
	}

	if !containsString(validDeviceStates(), state) {
		return "", ErrInvalidStatus
	}

	return state, nil
}

func normalizeRolloutStrategy(raw string) (string, error) {
	strategy := strings.ToLower(strings.TrimSpace(raw))
	if strategy == "" {
		return RolloutStrategyAllAtOnce, nil
	}

	if !containsString(validRolloutStrategies(), strategy) {
		return "", ErrInvalidStrategy
	}

	return strategy, nil
}

func normalizeRolloutStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		return RolloutStatusPlanned, nil
	}

	if !containsString(validRolloutStatuses(), status) {
		return "", ErrInvalidStatus
	}

	return status, nil
}

func normalizeReleaseStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status == "" {
		return ReleaseStatusActive, nil
	}

	if !containsString(validReleaseStatuses(), status) {
		return "", ErrInvalidStatus
	}

	return status, nil
}

func normalizeVisibility(raw string) (string, error) {
	visibility := strings.ToLower(strings.TrimSpace(raw))
	if visibility == "" {
		return VisibilityPrivate, nil
	}

	if !containsString(validVisibilityValues(), visibility) {
		return "", ErrInvalidVisibility
	}

	return visibility, nil
}

func validBuildStatuses() []string {
	return []string{
		BuildStatusQueued,
		BuildStatusRunning,
		BuildStatusSucceeded,
		BuildStatusFailed,
	}
}

func validBuildInstallerStatuses() []string {
	return []string{
		BuildInstallerStatusNotRequested,
		BuildInstallerStatusQueued,
		BuildInstallerStatusRunning,
		BuildInstallerStatusSucceeded,
		BuildInstallerStatusFailed,
	}
}

func validDeviceStates() []string {
	return []string{
		DeviceStateIdle,
		DeviceStateDownloading,
		DeviceStateApplying,
		DeviceStateRebooting,
		DeviceStateHealthy,
		DeviceStateDegraded,
		DeviceStateFailed,
	}
}

func validRolloutStrategies() []string {
	return []string{
		RolloutStrategyStaged,
		RolloutStrategyAllAtOnce,
	}
}

func validRolloutStatuses() []string {
	return []string{
		RolloutStatusPlanned,
		RolloutStatusInProgress,
		RolloutStatusPaused,
		RolloutStatusCompleted,
		RolloutStatusFailed,
	}
}

func validReleaseStatuses() []string {
	return []string{
		ReleaseStatusActive,
		ReleaseStatusWithdrawn,
	}
}

func validVisibilityValues() []string {
	return []string{
		VisibilityPrivate,
		VisibilityVisible,
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}

	return false
}

func uniqueViolation(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == "23505"
}

func foreignKeyViolation(err error) bool {
	if err == nil {
		return false
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}

	return pgErr.Code == "23503"
}
