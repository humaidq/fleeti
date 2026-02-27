/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import "errors"

var (
	ErrDatabaseConnectionNotInitialized = errors.New("database connection not initialized")
	ErrDatabaseURLEnvVarNotSet          = errors.New("DATABASE_URL environment variable is not set")
	ErrDatabaseNameNotSpecified         = errors.New("no database name specified in DATABASE_URL")
	ErrDisplayNameRequired              = errors.New("display name is required")
	ErrUserNotFound                     = errors.New("user not found")
	ErrInvalidUserHandle                = errors.New("invalid user handle")
	ErrPasskeyNotFound                  = errors.New("passkey not found")
	ErrSetupAlreadyCompleted            = errors.New("setup already completed")
	ErrInviteInvalidOrUsed              = errors.New("invite is no longer valid")
	ErrInvalidCreatorID                 = errors.New("invalid creator ID")
	ErrInviteNotFound                   = errors.New("invite not found")
	ErrInviteNotExpired                 = errors.New("invite is not expired")
	ErrAccessDenied                     = errors.New("access denied")
	ErrAdminRequired                    = errors.New("admin required")

	ErrNameRequired = errors.New("name is required")

	ErrFleetAlreadyExists          = errors.New("fleet already exists")
	ErrFleetNotFound               = errors.New("fleet not found")
	ErrProfileAlreadyExists        = errors.New("profile already exists")
	ErrProfileNotFound             = errors.New("profile not found")
	ErrProfileHasNoRevisions       = errors.New("profile has no revisions")
	ErrProfileFleetRequired        = errors.New("profile must belong to a fleet")
	ErrProfileNotAssignedToFleet   = errors.New("profile is not assigned to fleet")
	ErrBuildVersionAlreadyExists   = errors.New("build version already exists for this profile and fleet")
	ErrBuildNotFound               = errors.New("build not found")
	ErrBuildNotReadyForInstaller   = errors.New("build must succeed before installer can be built")
	ErrBuildInstallerAlreadyQueued = errors.New("installer build is already queued or running")
	ErrReleaseNotFound             = errors.New("release not found")
	ErrReleaseFleetNotConfigured   = errors.New("release build has no fleet")
	ErrReleaseWithdrawn            = errors.New("release is withdrawn")
	ErrReleaseVersionAlreadyExists = errors.New("release version already exists")
	ErrDeviceHostnameAlreadyExists = errors.New("device hostname already exists in this fleet")
	ErrDeviceSerialAlreadyExists   = errors.New("device serial number already exists")
	ErrRolloutNotFound             = errors.New("rollout not found")
	ErrRolloutFleetReleaseMismatch = errors.New("release does not belong to fleet")

	ErrInvalidProfileConfigJSON             = errors.New("profile configuration must be valid JSON")
	ErrProfileConfigMustBeObject            = errors.New("profile configuration JSON must be an object")
	ErrInvalidConfigSchemaVersion           = errors.New("invalid profile config schema version")
	ErrInvalidPostgresSessionIniterArgument = errors.New("invalid PostgresSessionIniter argument")

	ErrProfileRequired     = errors.New("profile is required")
	ErrBuildRequired       = errors.New("build is required")
	ErrFleetRequired       = errors.New("fleet is required")
	ErrReleaseRequired     = errors.New("release is required")
	ErrVersionRequired     = errors.New("version is required")
	ErrVersionMustBeSemver = errors.New("version must follow semver format (e.g. v1.1.0)")
	ErrHostnameRequired    = errors.New("hostname is required")
	ErrSerialRequired      = errors.New("serial number is required")
	ErrInvalidStatus       = errors.New("invalid status")
	ErrInvalidStrategy     = errors.New("invalid rollout strategy")
	ErrInvalidStageValue   = errors.New("stage percent must be 100 for all-at-once rollouts")
)
