/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/db"
)

const maxAgentBodyBytes = 64 * 1024

// Best-effort abuse protection for the unauthenticated enrollment endpoints.
// Only enroll/start is limited (it creates rows); poll is idempotent, cheap, and
// machine-id-guarded, so it is left unlimited to avoid breaking NAT'd fleets.
var (
	enrollStartIPLimiter    = newRateLimiter(30, time.Minute)
	enrollStartFleetLimiter = newRateLimiter(60, time.Minute)
)

type agentEnrollStartRequest struct {
	FleetID      string `json:"fleet_id"`
	MachineID    string `json:"machine_id"`
	Hostname     string `json:"hostname"`
	Serial       string `json:"serial"`
	Version      string `json:"version"`
	AgentVersion string `json:"agent_version"`
}

type agentEnrollStartResponse struct {
	Code                string `json:"code"`
	Status              string `json:"status"`
	PollIntervalSeconds int    `json:"poll_interval_seconds"`
	ExpiresAt           string `json:"expires_at"`
}

type agentEnrollPollRequest struct {
	Code      string `json:"code"`
	MachineID string `json:"machine_id"`
}

type agentEnrollPollResponse struct {
	Status      string `json:"status"`
	DeviceID    string `json:"device_id,omitempty"`
	DeviceToken string `json:"device_token,omitempty"`
}

type agentTelemetryRequest struct {
	ReportedVersion  string `json:"reported_version"`
	AgentVersion     string `json:"agent_version"`
	UpdateState      string `json:"update_state"`
	UptimeSeconds    int64  `json:"uptime_seconds"`
	UpdatePending    bool   `json:"update_pending"`
	CurrentVersion   string `json:"current_version"`
	DesiredVersion   string `json:"desired_version"`
	AvailableVersion string `json:"available_version"`
}

type agentCommand struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	TargetVersion string `json:"target_version,omitempty"`
}

type agentCommandsResponse struct {
	Commands []agentCommand `json:"commands"`
}

type agentCommandResultRequest struct {
	Status string `json:"status"`
	Result string `json:"result"`
}

// AgentEnrollStart registers (or refreshes) a pairing code for an unpaired device.
// Unauthenticated: a pending enrollment grants nothing until an admin claims it.
func AgentEnrollStart(c flamego.Context) {
	if !enrollStartIPLimiter.allow(clientIP(c)) {
		writeJSONError(c, http.StatusTooManyRequests, "Too many enrollment requests")

		return
	}

	var req agentEnrollStartRequest
	if err := decodeAgentRequest(c.Request(), &req); err != nil {
		writeAgentRequestError(c, err)

		return
	}

	fleetID := strings.TrimSpace(req.FleetID)
	if fleetID == "" {
		writeJSONError(c, http.StatusBadRequest, "fleet_id is required")

		return
	}

	if strings.TrimSpace(req.MachineID) == "" {
		writeJSONError(c, http.StatusBadRequest, "machine_id is required")

		return
	}

	if !enrollStartFleetLimiter.allow(fleetID) {
		writeJSONError(c, http.StatusTooManyRequests, "Too many enrollment requests")

		return
	}

	enrollment, err := db.StartEnrollment(c.Request().Context(), db.StartEnrollmentInput{
		FleetID:   req.FleetID,
		MachineID: req.MachineID,
		Hostname:  req.Hostname,
		Serial:    req.Serial,
		Version:   req.Version,
	})
	if err != nil {
		writeAgentEnrollError(c, err)

		return
	}

	writeJSON(c, agentEnrollStartResponse{
		Code:                enrollment.Code,
		Status:              enrollment.Status,
		PollIntervalSeconds: 5,
		ExpiresAt:           enrollment.ExpiresAt,
	})
}

// AgentEnrollPoll reports pairing status to the owning device and delivers the
// device token once the enrollment has been claimed. Unauthenticated.
func AgentEnrollPoll(c flamego.Context) {
	var req agentEnrollPollRequest
	if err := decodeAgentRequest(c.Request(), &req); err != nil {
		writeAgentRequestError(c, err)

		return
	}

	if strings.TrimSpace(req.Code) == "" {
		writeJSONError(c, http.StatusBadRequest, "code is required")

		return
	}

	status, deviceID, token, err := db.PollEnrollment(c.Request().Context(), req.Code, req.MachineID)
	if err != nil {
		writeAgentEnrollError(c, err)

		return
	}

	writeJSON(c, agentEnrollPollResponse{Status: status, DeviceID: deviceID, DeviceToken: token})
}

// AgentTelemetry records a telemetry sample from a paired device.
func AgentTelemetry(c flamego.Context, device *db.Device) {
	body, err := readAgentObjectBody(c.Request())
	if err != nil {
		writeAgentRequestError(c, err)

		return
	}

	var req agentTelemetryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONError(c, http.StatusBadRequest, "Request body contains invalid values")

		return
	}

	if err := db.RecordDeviceTelemetry(c.Request().Context(), db.TelemetryInput{
		DeviceID:         device.ID,
		ReportedVersion:  req.ReportedVersion,
		AvailableVersion: req.AvailableVersion,
		AgentVersion:     req.AgentVersion,
		UpdateState:      req.UpdateState,
		PayloadJSON:      string(body),
	}); err != nil {
		if errors.Is(err, db.ErrInvalidStatus) {
			writeJSONError(c, http.StatusBadRequest, "Invalid update_state")

			return
		}

		logger.Error("failed to record device telemetry", "device_id", device.ID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to record telemetry")

		return
	}

	writeJSON(c, map[string]bool{"ok": true})
}

// AgentCommands returns the commands awaiting execution by a device.
func AgentCommands(c flamego.Context, device *db.Device) {
	commands, err := db.ListPendingDeviceCommands(c.Request().Context(), device.ID)
	if err != nil {
		logger.Error("failed to list device commands", "device_id", device.ID, "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to list commands")

		return
	}

	out := make([]agentCommand, 0, len(commands))
	for _, command := range commands {
		out = append(out, agentCommand{ID: command.ID, Kind: command.Kind, TargetVersion: command.TargetVersion})
	}

	writeJSON(c, agentCommandsResponse{Commands: out})
}

// AgentCommandResult records the outcome of a command reported by a device.
func AgentCommandResult(c flamego.Context, device *db.Device) {
	commandID := strings.TrimSpace(c.Param("id"))
	if commandID == "" {
		writeJSONError(c, http.StatusNotFound, "Command not found")

		return
	}

	var req agentCommandResultRequest
	if err := decodeAgentRequest(c.Request(), &req); err != nil {
		writeAgentRequestError(c, err)

		return
	}

	if err := db.MarkDeviceCommandResult(c.Request().Context(), commandID, device.ID, req.Status, req.Result); err != nil {
		switch {
		case errors.Is(err, db.ErrDeviceCommandNotFound):
			writeJSONError(c, http.StatusNotFound, "Command not found")
		case errors.Is(err, db.ErrInvalidStatus):
			writeJSONError(c, http.StatusBadRequest, "Invalid status")
		default:
			logger.Error("failed to record command result", "device_id", device.ID, "command_id", commandID, "error", err)
			writeJSONError(c, http.StatusInternalServerError, "Failed to record command result")
		}

		return
	}

	writeJSON(c, map[string]bool{"ok": true})
}

func decodeAgentRequest(r *flamego.Request, dst any) error {
	body, err := readAgentBody(r)
	if err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return &apiRequestError{message: "Request body contains invalid fields or values"}
	}

	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return &apiRequestError{message: "Request body must contain a single JSON object"}
	}

	return nil
}

// readAgentObjectBody reads the raw body and verifies it is a JSON object so it
// can be stored verbatim as a telemetry payload.
func readAgentObjectBody(r *flamego.Request) ([]byte, error) {
	body, err := readAgentBody(r)
	if err != nil {
		return nil, err
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, &apiRequestError{message: "Request body must be a JSON object"}
	}

	return body, nil
}

func readAgentBody(r *flamego.Request) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body().ReadCloser(), maxAgentBodyBytes+1))
	if err != nil {
		return nil, &apiRequestError{message: "Failed to read request body"}
	}

	if len(body) == 0 {
		return nil, &apiRequestError{message: "Request body is required"}
	}

	if len(body) > maxAgentBodyBytes {
		return nil, &apiRequestError{message: "Request body is too large"}
	}

	return body, nil
}

func writeAgentRequestError(c flamego.Context, err error) {
	var requestErr *apiRequestError
	if errors.As(err, &requestErr) {
		writeJSONError(c, http.StatusBadRequest, requestErr.message)

		return
	}

	logger.Error("device agent request failed", "error", err)
	writeJSONError(c, http.StatusInternalServerError, "Failed to process request")
}

func writeAgentEnrollError(c flamego.Context, err error) {
	var requestErr *apiRequestError
	if errors.As(err, &requestErr) {
		writeJSONError(c, http.StatusBadRequest, requestErr.message)

		return
	}

	switch {
	case errors.Is(err, db.ErrFleetNotFound):
		writeJSONError(c, http.StatusBadRequest, "Unknown fleet")
	case errors.Is(err, db.ErrFleetRequired):
		writeJSONError(c, http.StatusBadRequest, "fleet_id is required")
	case errors.Is(err, db.ErrMachineIDRequired):
		writeJSONError(c, http.StatusBadRequest, "machine_id is required")
	case errors.Is(err, db.ErrEnrollmentCodeRequired):
		writeJSONError(c, http.StatusBadRequest, "code is required")
	case errors.Is(err, db.ErrEnrollmentNotFound):
		writeJSONError(c, http.StatusNotFound, "Pairing code not found")
	case errors.Is(err, db.ErrEnrollmentExpired):
		writeJSONError(c, http.StatusConflict, "Pairing code has expired")
	case errors.Is(err, db.ErrEnrollmentAlreadyClaimed):
		writeJSONError(c, http.StatusConflict, "Pairing code was already used")
	default:
		logger.Error("device enrollment failed", "error", err)
		writeJSONError(c, http.StatusInternalServerError, "Failed to process enrollment")
	}
}

// rateLimiter is a small fixed-window per-key limiter.
type rateLimiter struct {
	mu     sync.Mutex
	hits   map[string]*rateWindow
	limit  int
	window time.Duration
}

type rateWindow struct {
	count   int
	resetAt time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{hits: make(map[string]*rateWindow), limit: limit, window: window}
}

func (rl *rateLimiter) allow(key string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if len(rl.hits) > 4096 {
		for k, w := range rl.hits {
			if now.After(w.resetAt) {
				delete(rl.hits, k)
			}
		}
	}

	window := rl.hits[key]
	if window == nil || now.After(window.resetAt) {
		rl.hits[key] = &rateWindow{count: 1, resetAt: now.Add(rl.window)}

		return true
	}

	if window.count >= rl.limit {
		return false
	}

	window.count++

	return true
}
