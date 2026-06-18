/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// TestDeviceLifecycleIntegration exercises the full enrollment -> claim -> poll ->
// telemetry flow against a real PostgreSQL instance. It is skipped unless
// FLEETI_TEST_DATABASE_URL points at a disposable database.
func TestDeviceLifecycleIntegration(t *testing.T) {
	dsn := os.Getenv("FLEETI_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set FLEETI_TEST_DATABASE_URL to run the device lifecycle integration test")
	}

	t.Setenv("DATABASE_URL", dsn)

	ctx := context.Background()
	if err := Init(ctx); err != nil {
		t.Fatalf("Init: %v", err)
	}

	defer Close()

	if err := SyncSchema(ctx); err != nil {
		t.Fatalf("SyncSchema: %v", err)
	}

	suffix, err := generateEnrollmentCode()
	if err != nil {
		t.Fatalf("generateEnrollmentCode: %v", err)
	}

	machineID := "itest-machine-" + suffix

	var fleetID string
	if err := GetPool().QueryRow(ctx, `INSERT INTO fleets (name) VALUES ($1) RETURNING id::text`, "itest-fleet-"+suffix).Scan(&fleetID); err != nil {
		t.Fatalf("create fleet: %v", err)
	}

	// Start enrollment and confirm the code is stable across reboots.
	enr, err := StartEnrollment(ctx, StartEnrollmentInput{FleetID: fleetID, MachineID: machineID, Hostname: "host-" + suffix, Version: "1"})
	if err != nil {
		t.Fatalf("StartEnrollment: %v", err)
	}

	if len(enr.Code) != enrollmentCodeLength {
		t.Fatalf("unexpected code %q", enr.Code)
	}

	enr2, err := StartEnrollment(ctx, StartEnrollmentInput{FleetID: fleetID, MachineID: machineID, Hostname: "host-" + suffix, Version: "1"})
	if err != nil {
		t.Fatalf("StartEnrollment (reuse): %v", err)
	}

	if enr2.Code != enr.Code {
		t.Fatalf("expected stable code, got %q then %q", enr.Code, enr2.Code)
	}

	// Poll before claim: pending, no token.
	status, _, token, err := PollEnrollment(ctx, enr.Code, machineID)
	if err != nil {
		t.Fatalf("PollEnrollment (pending): %v", err)
	}

	if status != "pending" || token != "" {
		t.Fatalf("expected pending without token, got status=%q token=%q", status, token)
	}

	// A different machine must not see this enrollment.
	if _, _, _, err := PollEnrollment(ctx, enr.Code, "intruder"); !errors.Is(err, ErrEnrollmentNotFound) {
		t.Fatalf("expected ErrEnrollmentNotFound for wrong machine, got %v", err)
	}

	// Claim auto-derives the fleet and creates the device.
	deviceID, err := ClaimEnrollmentCode(ctx, enr.Code, "")
	if err != nil {
		t.Fatalf("ClaimEnrollmentCode: %v", err)
	}

	if _, err := ClaimEnrollmentCode(ctx, enr.Code, ""); !errors.Is(err, ErrEnrollmentAlreadyClaimed) {
		t.Fatalf("expected ErrEnrollmentAlreadyClaimed, got %v", err)
	}

	// Poll after claim delivers the token exactly once.
	status, polledDevice, token, err := PollEnrollment(ctx, enr.Code, machineID)
	if err != nil {
		t.Fatalf("PollEnrollment (claimed): %v", err)
	}

	if status != "claimed" || polledDevice != deviceID || !strings.HasPrefix(token, deviceTokenPrefix) {
		t.Fatalf("expected claimed with token, got status=%q device=%q token=%q", status, polledDevice, token)
	}

	device, err := AuthenticateDeviceToken(ctx, token)
	if err != nil {
		t.Fatalf("AuthenticateDeviceToken: %v", err)
	}

	if device.ID != deviceID {
		t.Fatalf("token resolved to wrong device %q", device.ID)
	}

	// Once the token is used, repeat polls no longer hand it out.
	if _, _, token2, err := PollEnrollment(ctx, enr.Code, machineID); err != nil || token2 != "" {
		t.Fatalf("expected no token on repeat poll, got token=%q err=%v", token2, err)
	}

	// Telemetry updates the device summary and history.
	if err := RecordDeviceTelemetry(ctx, TelemetryInput{
		DeviceID:        deviceID,
		ReportedVersion: "1.2.3",
		AgentVersion:    "1.0.0",
		UpdateState:     "healthy",
		PayloadJSON:     `{"reported_version":"1.2.3","update_state":"healthy"}`,
	}); err != nil {
		t.Fatalf("RecordDeviceTelemetry: %v", err)
	}

	detail, err := GetDeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetDeviceByID: %v", err)
	}

	if !detail.Paired || detail.ReportedVersion != "1.2.3" || detail.MachineID != machineID || detail.UpdateState != "healthy" {
		t.Fatalf("unexpected device detail: %+v", detail)
	}

	records, err := ListDeviceTelemetry(ctx, deviceID, 10)
	if err != nil {
		t.Fatalf("ListDeviceTelemetry: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 telemetry record, got %d", len(records))
	}

	// Admin edits the serial.
	if err := UpdateDevice(ctx, deviceID, UpdateDeviceInput{Hostname: detail.Hostname, SerialNumber: "SN-" + suffix}); err != nil {
		t.Fatalf("UpdateDevice: %v", err)
	}

	updated, err := GetDeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetDeviceByID (after update): %v", err)
	}

	if updated.SerialNumber != "SN-"+suffix {
		t.Fatalf("serial not updated, got %q", updated.SerialNumber)
	}

	// Re-pair: a fresh enrollment for the same machine reuses the device and
	// invalidates the previously issued token.
	enr3, err := StartEnrollment(ctx, StartEnrollmentInput{FleetID: fleetID, MachineID: machineID, Hostname: "host-" + suffix, Version: "1"})
	if err != nil {
		t.Fatalf("StartEnrollment (re-pair): %v", err)
	}

	if enr3.Code == enr.Code {
		t.Fatalf("expected a fresh code after the previous one was claimed")
	}

	rePairedDevice, err := ClaimEnrollmentCode(ctx, enr3.Code, "")
	if err != nil {
		t.Fatalf("ClaimEnrollmentCode (re-pair): %v", err)
	}

	if rePairedDevice != deviceID {
		t.Fatalf("re-pair should reuse the device, got %q want %q", rePairedDevice, deviceID)
	}

	if _, err := AuthenticateDeviceToken(ctx, token); !errors.Is(err, ErrDeviceTokenNotFound) {
		t.Fatalf("expected old token to be invalid after re-pair, got %v", err)
	}

	// Telemetry carrying an available version surfaces on the device.
	if err := RecordDeviceTelemetry(ctx, TelemetryInput{
		DeviceID:         deviceID,
		ReportedVersion:  "1.2.3",
		AvailableVersion: "1.2.4",
		UpdateState:      "healthy",
		PayloadJSON:      `{"available_version":"1.2.4"}`,
	}); err != nil {
		t.Fatalf("RecordDeviceTelemetry (available): %v", err)
	}

	withUpdate, err := GetDeviceByID(ctx, deviceID)
	if err != nil {
		t.Fatalf("GetDeviceByID (available): %v", err)
	}

	if withUpdate.AvailableVersion != "1.2.4" {
		t.Fatalf("expected available_version 1.2.4, got %q", withUpdate.AvailableVersion)
	}

	// Queue a force-update command; only one command may be pending at a time.
	if err := CreateDeviceCommand(ctx, deviceID, "update", "1.2.4", ""); err != nil {
		t.Fatalf("CreateDeviceCommand (update): %v", err)
	}

	if err := CreateDeviceCommand(ctx, deviceID, "reboot", "", ""); !errors.Is(err, ErrDeviceCommandPending) {
		t.Fatalf("expected ErrDeviceCommandPending for a second command, got %v", err)
	}

	pending, err := ListPendingDeviceCommands(ctx, deviceID)
	if err != nil {
		t.Fatalf("ListPendingDeviceCommands: %v", err)
	}

	if len(pending) != 1 || pending[0].Kind != "update" || pending[0].TargetVersion != "1.2.4" {
		t.Fatalf("unexpected pending commands: %+v", pending)
	}

	// The agent acknowledges then completes the command, freeing the pending slot.
	if err := MarkDeviceCommandResult(ctx, pending[0].ID, deviceID, "succeeded", "installed; rebooting"); err != nil {
		t.Fatalf("MarkDeviceCommandResult: %v", err)
	}

	if err := CreateDeviceCommand(ctx, deviceID, "reboot", "", ""); err != nil {
		t.Fatalf("CreateDeviceCommand (reboot after completion): %v", err)
	}

	recent, err := ListRecentDeviceCommands(ctx, deviceID, 10)
	if err != nil {
		t.Fatalf("ListRecentDeviceCommands: %v", err)
	}

	if len(recent) != 2 {
		t.Fatalf("expected 2 recent commands, got %d", len(recent))
	}
}
