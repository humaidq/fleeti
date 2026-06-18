/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const (
	deviceTokenPrefix            = "fltd_"
	deviceTokenRandomBytes       = 32
	deviceTokenDisplayPrefixSize = 16

	// enrollmentCodeAlphabet excludes ambiguous characters (I, O, 0, 1) and has
	// exactly 32 symbols so byte-modulo selection is unbiased.
	enrollmentCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	enrollmentCodeLength   = 6

	enrollmentTTL = "15 minutes"
)

// DeviceDetail extends the inventory Device summary with telemetry/identity fields
// for the per-device page.
type DeviceDetail struct {
	Device

	MachineID        string
	ReportedVersion  string
	AvailableVersion string
	AgentVersion     string
	LastTelemetryAt  string
	Paired           bool
}

// DeviceTelemetryRecord is one stored telemetry sample.
type DeviceTelemetryRecord struct {
	ID              string
	ReportedVersion string
	UpdateState     string
	PayloadJSON     string
	CreatedAt       string
}

// DeviceEnrollment is the agent-visible state of a pairing code.
type DeviceEnrollment struct {
	Code      string
	Status    string
	ExpiresAt string
}

// DeviceCommand is a queued remote command for a device.
type DeviceCommand struct {
	ID            string
	Kind          string
	TargetVersion string
	Status        string
}

// UpdateDeviceInput holds admin-editable device fields.
type UpdateDeviceInput struct {
	Hostname     string
	SerialNumber string
}

// StartEnrollmentInput is what a device reports when requesting a pairing code.
type StartEnrollmentInput struct {
	FleetID   string
	MachineID string
	Hostname  string
	Serial    string
	Version   string
}

// TelemetryInput is one telemetry submission from a paired device.
type TelemetryInput struct {
	DeviceID         string
	ReportedVersion  string
	AvailableVersion string
	AgentVersion     string
	UpdateState      string
	PayloadJSON      string
}

// DeviceCommandRecord is a command with its outcome, for the device history view.
type DeviceCommandRecord struct {
	ID            string
	Kind          string
	TargetVersion string
	Status        string
	Result        string
	CreatedAt     string
	CompletedAt   string
}

// GetDeviceByID loads a single device with its telemetry/identity fields.
func GetDeviceByID(ctx context.Context, id string) (*DeviceDetail, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrDeviceNotFound
	}

	var item DeviceDetail

	err := pool.QueryRow(ctx, `
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
			to_char(d.created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
			d.machine_id,
			d.reported_version,
			d.available_version,
			d.agent_version,
			COALESCE(to_char(d.last_telemetry_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), ''),
			EXISTS(SELECT 1 FROM device_tokens t WHERE t.device_id = d.id)
		FROM devices d
		JOIN fleets f ON f.id = d.fleet_id
		LEFT JOIN releases curr ON curr.id = d.current_release_id
		LEFT JOIN releases des ON des.id = d.desired_release_id
		WHERE d.id::text = $1
	`, id).Scan(
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
		&item.MachineID,
		&item.ReportedVersion,
		&item.AvailableVersion,
		&item.AgentVersion,
		&item.LastTelemetryAt,
		&item.Paired,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeviceNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to load device: %w", err)
	}

	return &item, nil
}

// UpdateDevice updates the admin-editable fields of a device.
func UpdateDevice(ctx context.Context, id string, input UpdateDeviceInput) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	id = strings.TrimSpace(id)
	if id == "" {
		return ErrDeviceNotFound
	}

	input.Hostname = strings.TrimSpace(input.Hostname)
	input.SerialNumber = strings.TrimSpace(input.SerialNumber)

	if input.Hostname == "" {
		return ErrHostnameRequired
	}

	if input.SerialNumber == "" {
		return ErrSerialRequired
	}

	command, err := pool.Exec(ctx, `
		UPDATE devices
		SET hostname = $2, serial_number = $3
		WHERE id::text = $1
	`, id, input.Hostname, input.SerialNumber)

	if uniqueViolation(err) {
		if strings.Contains(err.Error(), "devices_serial_number_key") {
			return ErrDeviceSerialAlreadyExists
		}

		return ErrDeviceHostnameAlreadyExists
	}

	if err != nil {
		return fmt.Errorf("failed to update device: %w", err)
	}

	if command.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}

	return nil
}

// ListDeviceTelemetry returns the most recent telemetry samples for a device.
func ListDeviceTelemetry(ctx context.Context, deviceID string, limit int) ([]DeviceTelemetryRecord, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	if limit <= 0 {
		limit = 50
	}

	rows, err := pool.Query(ctx, `
		SELECT
			id::text,
			reported_version,
			update_state,
			payload::text,
			to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		FROM device_telemetry
		WHERE device_id::text = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, strings.TrimSpace(deviceID), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list device telemetry: %w", err)
	}

	defer rows.Close()

	records := make([]DeviceTelemetryRecord, 0)
	for rows.Next() {
		var record DeviceTelemetryRecord

		if err := rows.Scan(&record.ID, &record.ReportedVersion, &record.UpdateState, &record.PayloadJSON, &record.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan telemetry record: %w", err)
		}

		records = append(records, record)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during telemetry rows iteration: %w", err)
	}

	return records, nil
}

// RecordDeviceTelemetry stores a telemetry sample and refreshes the device summary.
func RecordDeviceTelemetry(ctx context.Context, input TelemetryInput) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	deviceID := strings.TrimSpace(input.DeviceID)
	if deviceID == "" {
		return ErrDeviceNotFound
	}

	payload := strings.TrimSpace(input.PayloadJSON)
	if payload == "" {
		payload = "{}"
	}

	state := strings.TrimSpace(input.UpdateState)
	if state != "" {
		normalized, err := normalizeDeviceState(state)
		if err != nil {
			return err
		}

		state = normalized
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin telemetry transaction: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		INSERT INTO device_telemetry (device_id, payload, reported_version, update_state)
		VALUES ($1, $2::jsonb, $3, $4)
	`, deviceID, payload, input.ReportedVersion, state); err != nil {
		return fmt.Errorf("failed to insert telemetry: %w", err)
	}

	if state != "" {
		_, err = tx.Exec(ctx, `
			UPDATE devices
			SET reported_version = $2, available_version = $3, agent_version = $4, update_state = $5,
				last_seen_at = now(), last_telemetry_at = now()
			WHERE id::text = $1
		`, deviceID, input.ReportedVersion, input.AvailableVersion, input.AgentVersion, state)
	} else {
		_, err = tx.Exec(ctx, `
			UPDATE devices
			SET reported_version = $2, available_version = $3, agent_version = $4,
				last_seen_at = now(), last_telemetry_at = now()
			WHERE id::text = $1
		`, deviceID, input.ReportedVersion, input.AvailableVersion, input.AgentVersion)
	}

	if err != nil {
		return fmt.Errorf("failed to update device telemetry summary: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit telemetry: %w", err)
	}

	return nil
}

// StartEnrollment registers (or refreshes) a pending pairing code for a device.
// A device keeps a single stable code per (fleet, machine) until it is claimed.
func StartEnrollment(ctx context.Context, input StartEnrollmentInput) (*DeviceEnrollment, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	fleetID := strings.TrimSpace(input.FleetID)
	if fleetID == "" {
		return nil, ErrFleetRequired
	}

	machineID := strings.TrimSpace(input.MachineID)
	if machineID == "" {
		return nil, ErrMachineIDRequired
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to begin enrollment transaction: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	var (
		code    string
		expires string
	)

	// Reuse an existing pending code so a rebooting device keeps the same code.
	err = tx.QueryRow(ctx, `
		UPDATE device_enrollments
		SET reported_hostname = $3, reported_serial = $4, reported_version = $5,
			expires_at = now() + interval '`+enrollmentTTL+`'
		WHERE fleet_id::text = $1 AND machine_id = $2 AND status = 'pending'
		RETURNING code, to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
	`, fleetID, machineID, input.Hostname, input.Serial, input.Version).Scan(&code, &expires)

	if errors.Is(err, pgx.ErrNoRows) {
		code, err = uniqueEnrollmentCode(ctx, tx)
		if err != nil {
			return nil, err
		}

		err = tx.QueryRow(ctx, `
			INSERT INTO device_enrollments
				(code, fleet_id, machine_id, reported_hostname, reported_serial, reported_version, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending', now() + interval '`+enrollmentTTL+`')
			RETURNING to_char(expires_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS')
		`, code, fleetID, machineID, input.Hostname, input.Serial, input.Version).Scan(&expires)

		if foreignKeyViolation(err) {
			return nil, ErrFleetNotFound
		}

		if err != nil {
			return nil, fmt.Errorf("failed to create enrollment: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("failed to refresh enrollment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("failed to commit enrollment: %w", err)
	}

	return &DeviceEnrollment{Code: code, Status: "pending", ExpiresAt: expires}, nil
}

// PollEnrollment reports the status of a pairing code to the owning device and,
// once claimed, issues the device token exactly once (until it is actually used).
func PollEnrollment(ctx context.Context, code string, machineID string) (status string, deviceID string, deviceToken string, err error) {
	if pool == nil {
		return "", "", "", ErrDatabaseConnectionNotInitialized
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return "", "", "", ErrEnrollmentCodeRequired
	}

	machineID = strings.TrimSpace(machineID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to begin poll transaction: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	var (
		enrollID      string
		enrollStatus  string
		enrollDevice  string
		storedMachine string
		expired       bool
	)

	err = tx.QueryRow(ctx, `
		SELECT id::text, status, COALESCE(device_id::text, ''), machine_id, (expires_at < now())
		FROM device_enrollments
		WHERE upper(code) = upper($1)
		FOR UPDATE
	`, code).Scan(&enrollID, &enrollStatus, &enrollDevice, &storedMachine, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", "", ErrEnrollmentNotFound
	}

	if err != nil {
		return "", "", "", fmt.Errorf("failed to load enrollment: %w", err)
	}

	if storedMachine != "" && machineID != "" && storedMachine != machineID {
		// Do not leak another device's enrollment.
		return "", "", "", ErrEnrollmentNotFound
	}

	switch enrollStatus {
	case "claimed":
		token, err := ensureDeviceTokenForPoll(ctx, tx, enrollDevice)
		if err != nil {
			return "", "", "", err
		}

		if err := tx.Commit(ctx); err != nil {
			return "", "", "", fmt.Errorf("failed to commit poll: %w", err)
		}

		return "claimed", enrollDevice, token, nil
	case "pending":
		if expired {
			if _, err := tx.Exec(ctx, `UPDATE device_enrollments SET status = 'expired' WHERE id::text = $1`, enrollID); err != nil {
				return "", "", "", fmt.Errorf("failed to expire enrollment: %w", err)
			}

			if err := tx.Commit(ctx); err != nil {
				return "", "", "", fmt.Errorf("failed to commit poll: %w", err)
			}

			return "expired", "", "", nil
		}

		return "pending", "", "", nil
	default:
		return "expired", "", "", nil
	}
}

// ensureDeviceTokenForPoll returns a freshly issued device token if the device has
// none or only an unused one (re-pair); returns "" once the device has used a token.
func ensureDeviceTokenForPoll(ctx context.Context, tx pgx.Tx, deviceID string) (string, error) {
	if strings.TrimSpace(deviceID) == "" {
		return "", nil
	}

	var (
		total int
		used  int
	)

	if err := tx.QueryRow(ctx, `
		SELECT count(*), count(*) FILTER (WHERE last_used_at IS NOT NULL)
		FROM device_tokens
		WHERE device_id::text = $1
	`, deviceID).Scan(&total, &used); err != nil {
		return "", fmt.Errorf("failed to inspect device tokens: %w", err)
	}

	if used > 0 {
		return "", nil
	}

	if total > 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM device_tokens WHERE device_id::text = $1`, deviceID); err != nil {
			return "", fmt.Errorf("failed to clear stale device tokens: %w", err)
		}
	}

	rawToken, prefix, hash, err := generateDeviceToken()
	if err != nil {
		return "", err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO device_tokens (device_id, token_hash, token_prefix)
		VALUES ($1, $2, $3)
	`, deviceID, hash, prefix); err != nil {
		return "", fmt.Errorf("failed to issue device token: %w", err)
	}

	return rawToken, nil
}

// ClaimEnrollmentCode is the administrator action: it auto-derives the fleet from
// the pending enrollment, creates (or re-pairs) the device, and marks the code
// claimed. The device token is delivered to the device on its next poll.
func ClaimEnrollmentCode(ctx context.Context, code string, claimedByUserID string) (string, error) {
	if pool == nil {
		return "", ErrDatabaseConnectionNotInitialized
	}

	code = strings.TrimSpace(code)
	if code == "" {
		return "", ErrEnrollmentCodeRequired
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to begin claim transaction: %w", err)
	}

	defer func() { _ = tx.Rollback(ctx) }()

	var (
		enrollID     string
		enrollStatus string
		fleetID      string
		machineID    string
		reportedHost string
		reportedSer  string
		reportedVer  string
		expired      bool
	)

	err = tx.QueryRow(ctx, `
		SELECT id::text, status, fleet_id::text, machine_id,
			reported_hostname, reported_serial, reported_version, (expires_at < now())
		FROM device_enrollments
		WHERE upper(code) = upper($1)
		FOR UPDATE
	`, code).Scan(&enrollID, &enrollStatus, &fleetID, &machineID, &reportedHost, &reportedSer, &reportedVer, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrEnrollmentNotFound
	}

	if err != nil {
		return "", fmt.Errorf("failed to load enrollment: %w", err)
	}

	if enrollStatus == "claimed" {
		return "", ErrEnrollmentAlreadyClaimed
	}

	if enrollStatus == "expired" || expired {
		if enrollStatus != "expired" {
			if _, err := tx.Exec(ctx, `UPDATE device_enrollments SET status = 'expired' WHERE id::text = $1`, enrollID); err != nil {
				return "", fmt.Errorf("failed to expire enrollment: %w", err)
			}

			if err := tx.Commit(ctx); err != nil {
				return "", fmt.Errorf("failed to commit expiry: %w", err)
			}
		}

		return "", ErrEnrollmentExpired
	}

	deviceID := ""
	if machineID != "" {
		err = tx.QueryRow(ctx, `SELECT id::text FROM devices WHERE machine_id = $1`, machineID).Scan(&deviceID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("failed to look up existing device: %w", err)
		}
	}

	if deviceID != "" {
		// Re-pair: refresh the existing device and force a fresh token to be issued.
		if _, err := tx.Exec(ctx, `
			UPDATE devices SET reported_version = $2, last_seen_at = now() WHERE id::text = $1
		`, deviceID, reportedVer); err != nil {
			return "", fmt.Errorf("failed to refresh device: %w", err)
		}

		if _, err := tx.Exec(ctx, `DELETE FROM device_tokens WHERE device_id::text = $1`, deviceID); err != nil {
			return "", fmt.Errorf("failed to reset device tokens: %w", err)
		}
	} else {
		base := machineID
		if base == "" {
			base = code
		}

		hostname := strings.TrimSpace(reportedHost)
		if hostname == "" {
			hostname = "fleeti-" + shorten(base, 8)
		}

		serial := strings.TrimSpace(reportedSer)
		if serial == "" {
			serial = "pending-" + base
		}

		hostname, err = uniqueDeviceHostname(ctx, tx, fleetID, hostname)
		if err != nil {
			return "", err
		}

		var serialTaken bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM devices WHERE serial_number = $1)`, serial).Scan(&serialTaken); err != nil {
			return "", fmt.Errorf("failed to check serial: %w", err)
		}

		if serialTaken {
			return "", ErrDeviceSerialAlreadyExists
		}

		err = tx.QueryRow(ctx, `
			INSERT INTO devices
				(fleet_id, hostname, serial_number, machine_id, reported_version, update_state, attestation_level, last_seen_at)
			VALUES ($1, $2, $3, $4, $5, 'idle', 'unknown', now())
			RETURNING id::text
		`, fleetID, hostname, serial, machineID, reportedVer).Scan(&deviceID)

		if uniqueViolation(err) {
			if strings.Contains(err.Error(), "devices_serial_number_key") {
				return "", ErrDeviceSerialAlreadyExists
			}

			return "", ErrDeviceHostnameAlreadyExists
		}

		if foreignKeyViolation(err) {
			return "", ErrFleetNotFound
		}

		if err != nil {
			return "", fmt.Errorf("failed to create device: %w", err)
		}
	}

	var claimedBy *string
	if trimmed := strings.TrimSpace(claimedByUserID); trimmed != "" {
		claimedBy = &trimmed
	}

	if _, err := tx.Exec(ctx, `
		UPDATE device_enrollments
		SET status = 'claimed', device_id = $2, claimed_by_user_id = $3, claimed_at = now()
		WHERE id::text = $1
	`, enrollID, deviceID, claimedBy); err != nil {
		return "", fmt.Errorf("failed to mark enrollment claimed: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("failed to commit claim: %w", err)
	}

	return deviceID, nil
}

// AuthenticateDeviceToken resolves a device by its token and records last-used time.
func AuthenticateDeviceToken(ctx context.Context, rawToken string) (*Device, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	trimmed := strings.TrimSpace(rawToken)
	if trimmed == "" {
		return nil, ErrDeviceTokenNotFound
	}

	tokenHash := hashAPIKey(trimmed)

	var (
		device  Device
		tokenID uuid.UUID
	)

	err := pool.QueryRow(ctx, `
		SELECT d.id::text, f.id::text, f.name, d.hostname, d.serial_number, d.update_state, d.attestation_level, t.id
		FROM device_tokens t
		JOIN devices d ON d.id = t.device_id
		JOIN fleets f ON f.id = d.fleet_id
		WHERE t.token_hash = $1
	`, tokenHash).Scan(
		&device.ID,
		&device.FleetID,
		&device.FleetName,
		&device.Hostname,
		&device.SerialNumber,
		&device.UpdateState,
		&device.AttestationLevel,
		&tokenID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrDeviceTokenNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("failed to authenticate device token: %w", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE device_tokens SET last_used_at = NOW() WHERE id = $1`, tokenID); err != nil {
		return nil, fmt.Errorf("failed to update device token last used time: %w", err)
	}

	return &device, nil
}

// CreateDeviceCommand queues a remote command for a device (groundwork for the
// future web-triggered force update / reboot).
func CreateDeviceCommand(ctx context.Context, deviceID string, kind string, targetVersion string, userID string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "update" && kind != "reboot" {
		return fmt.Errorf("invalid command kind: %s", kind)
	}

	var createdBy *string
	if trimmed := strings.TrimSpace(userID); trimmed != "" {
		createdBy = &trimmed
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO device_commands (device_id, kind, target_version, created_by_user_id)
		VALUES ($1, $2, $3, $4)
	`, strings.TrimSpace(deviceID), kind, strings.TrimSpace(targetVersion), createdBy)

	if uniqueViolation(err) {
		return ErrDeviceCommandPending
	}

	if foreignKeyViolation(err) {
		return ErrDeviceNotFound
	}

	if err != nil {
		return fmt.Errorf("failed to create device command: %w", err)
	}

	return nil
}

// ListRecentDeviceCommands returns recent commands and their outcomes for a device.
func ListRecentDeviceCommands(ctx context.Context, deviceID string, limit int) ([]DeviceCommandRecord, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	if limit <= 0 {
		limit = 10
	}

	rows, err := pool.Query(ctx, `
		SELECT
			id::text,
			kind,
			target_version,
			status,
			result,
			to_char(created_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'),
			COALESCE(to_char(completed_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS'), '')
		FROM device_commands
		WHERE device_id::text = $1
		ORDER BY created_at DESC
		LIMIT $2
	`, strings.TrimSpace(deviceID), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list recent device commands: %w", err)
	}

	defer rows.Close()

	commands := make([]DeviceCommandRecord, 0)
	for rows.Next() {
		var command DeviceCommandRecord

		if err := rows.Scan(
			&command.ID,
			&command.Kind,
			&command.TargetVersion,
			&command.Status,
			&command.Result,
			&command.CreatedAt,
			&command.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("failed to scan device command record: %w", err)
		}

		commands = append(commands, command)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during device command rows iteration: %w", err)
	}

	return commands, nil
}

// ListPendingDeviceCommands returns commands awaiting execution by a device.
func ListPendingDeviceCommands(ctx context.Context, deviceID string) ([]DeviceCommand, error) {
	if pool == nil {
		return nil, ErrDatabaseConnectionNotInitialized
	}

	rows, err := pool.Query(ctx, `
		SELECT id::text, kind, target_version, status
		FROM device_commands
		WHERE device_id::text = $1 AND status = 'pending'
		ORDER BY created_at ASC
	`, strings.TrimSpace(deviceID))
	if err != nil {
		return nil, fmt.Errorf("failed to list device commands: %w", err)
	}

	defer rows.Close()

	commands := make([]DeviceCommand, 0)
	for rows.Next() {
		var command DeviceCommand

		if err := rows.Scan(&command.ID, &command.Kind, &command.TargetVersion, &command.Status); err != nil {
			return nil, fmt.Errorf("failed to scan device command: %w", err)
		}

		commands = append(commands, command)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed during device command rows iteration: %w", err)
	}

	return commands, nil
}

// MarkDeviceCommandResult records the outcome of a command reported by a device.
func MarkDeviceCommandResult(ctx context.Context, commandID string, deviceID string, status string, result string) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	status = strings.ToLower(strings.TrimSpace(status))
	if status != "acknowledged" && status != "succeeded" && status != "failed" {
		return ErrInvalidStatus
	}

	completed := status == "succeeded" || status == "failed"

	command, err := pool.Exec(ctx, `
		UPDATE device_commands
		SET status = $3, result = $4,
			completed_at = CASE WHEN $5 THEN now() ELSE completed_at END
		WHERE id::text = $1 AND device_id::text = $2
	`, strings.TrimSpace(commandID), strings.TrimSpace(deviceID), status, result, completed)
	if err != nil {
		return fmt.Errorf("failed to update device command: %w", err)
	}

	if command.RowsAffected() == 0 {
		return ErrDeviceCommandNotFound
	}

	return nil
}

func uniqueEnrollmentCode(ctx context.Context, tx pgx.Tx) (string, error) {
	for range 16 {
		code, err := generateEnrollmentCode()
		if err != nil {
			return "", err
		}

		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM device_enrollments WHERE code = $1)`, code).Scan(&exists); err != nil {
			return "", fmt.Errorf("failed to check enrollment code: %w", err)
		}

		if !exists {
			return code, nil
		}
	}

	return "", fmt.Errorf("failed to allocate a unique enrollment code")
}

func uniqueDeviceHostname(ctx context.Context, tx pgx.Tx, fleetID string, base string) (string, error) {
	candidate := base
	for attempt := 1; attempt <= 50; attempt++ {
		var exists bool
		if err := tx.QueryRow(ctx, `
			SELECT EXISTS(SELECT 1 FROM devices WHERE fleet_id::text = $1 AND hostname = $2)
		`, fleetID, candidate).Scan(&exists); err != nil {
			return "", fmt.Errorf("failed to check hostname: %w", err)
		}

		if !exists {
			return candidate, nil
		}

		candidate = fmt.Sprintf("%s-%d", base, attempt+1)
	}

	return "", ErrDeviceHostnameAlreadyExists
}

func generateDeviceToken() (string, string, []byte, error) {
	buffer := make([]byte, deviceTokenRandomBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", "", nil, fmt.Errorf("failed to generate device token: %w", err)
	}

	rawToken := deviceTokenPrefix + base64.RawURLEncoding.EncodeToString(buffer)
	prefix := rawToken
	if len(prefix) > deviceTokenDisplayPrefixSize {
		prefix = prefix[:deviceTokenDisplayPrefixSize]
	}

	return rawToken, prefix, hashAPIKey(rawToken), nil
}

func generateEnrollmentCode() (string, error) {
	buffer := make([]byte, enrollmentCodeLength)
	if _, err := rand.Read(buffer); err != nil {
		return "", fmt.Errorf("failed to generate enrollment code: %w", err)
	}

	out := make([]byte, enrollmentCodeLength)
	for i, b := range buffer {
		out[i] = enrollmentCodeAlphabet[int(b)%len(enrollmentCodeAlphabet)]
	}

	return string(out), nil
}

func shorten(value string, length int) string {
	if len(value) <= length {
		return value
	}

	return value[:length]
}
