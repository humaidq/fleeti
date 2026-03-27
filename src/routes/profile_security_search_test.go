/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import "testing"

func TestProfileSecuritySearchJobStoreTracksProgress(t *testing.T) {
	t.Parallel()

	store := newProfileSecuritySearchJobStore()
	state, err := store.Create("profile-a", "user-a", "web server", profileSecuritySearchScenarioView{RequiredTCPPorts: []int{22, 443}})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	if state.Status != profileSecuritySearchJobStatusQueued || state.Done {
		t.Fatalf("unexpected initial state: %#v", state)
	}

	store.Update(state.JobID, func(updated *profileSecuritySearchJobStatusResponse) {
		updated.Status = profileSecuritySearchJobStatusRunning
		updated.ProgressMessage = "Evaluated 4 of 24 unique candidates"
		updated.EvaluatedCandidateCount = 4
		updated.UniqueCandidateCount = 4
	})

	record, ok := store.Get(state.JobID)
	if !ok {
		t.Fatal("expected job record to be present")
	}

	if record.ProfileID != "profile-a" || record.OwnerUserID != "user-a" {
		t.Fatalf("unexpected job metadata: %#v", record)
	}

	if record.State.Status != profileSecuritySearchJobStatusRunning || record.State.EvaluatedCandidateCount != 4 {
		t.Fatalf("unexpected updated state: %#v", record.State)
	}
}

func TestDecodeProfileSecuritySearchModelResponseHandlesCodeFence(t *testing.T) {
	t.Parallel()

	decoded, err := decodeProfileSecuritySearchModelResponse("```json\n{\"candidates\":[{\"rationale\":\"tight web host\",\"config\":{\"firewall\":{\"enable\":true,\"allowed_tcp_ports\":[22,443],\"allowed_udp_ports\":[]},\"blacklisted_kernel_modules\":[\"usb-storage\"],\"apparmor\":{\"enable\":true},\"password_policy\":{\"pwquality\":{\"enable\":true,\"minimum_length\":14,\"minimum_digits\":1,\"minimum_upper\":1,\"minimum_lower\":1,\"minimum_other\":1,\"retry_count\":3},\"expiry\":{\"enable\":true,\"maximum_days\":90,\"warning_days\":14}},\"website_blocking\":{\"enable\":false,\"block_categories\":[]}}}]}\n```")
	if err != nil {
		t.Fatalf("decodeProfileSecuritySearchModelResponse returned error: %v", err)
	}

	if len(decoded.Candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(decoded.Candidates))
	}

	if !decoded.Candidates[0].Config.Firewall.Enable || len(decoded.Candidates[0].Config.Firewall.AllowedTCPPorts) != 2 {
		t.Fatalf("unexpected firewall config: %#v", decoded.Candidates[0].Config.Firewall)
	}
}

func TestProfileSecuritySearchScenarioFromGoalExtractsPorts(t *testing.T) {
	t.Parallel()

	scenario := profileSecuritySearchScenarioFromGoal("Internet-facing web server with SSH, HTTPS, DNS, and port 8443 for admin traffic")

	if len(scenario.RequiredTCPPorts) != 5 || scenario.RequiredTCPPorts[0] != 22 || scenario.RequiredTCPPorts[1] != 53 || scenario.RequiredTCPPorts[2] != 80 || scenario.RequiredTCPPorts[3] != 443 || scenario.RequiredTCPPorts[4] != 8443 {
		t.Fatalf("unexpected required TCP ports: %#v", scenario.RequiredTCPPorts)
	}

	if len(scenario.RequiredUDPPorts) != 1 || scenario.RequiredUDPPorts[0] != 53 {
		t.Fatalf("unexpected required UDP ports: %#v", scenario.RequiredUDPPorts)
	}
}

func TestProfileSecurityAPIConfigRoundTripPreservesNewSecurityFields(t *testing.T) {
	t.Parallel()

	config := ProfileSecurityConfig{
		Configured: true,
		Firewall: ProfileSecurityFirewallConfig{
			Enable:          true,
			AllowedTCPPorts: []int{443, 22},
			AllowedUDPPorts: []int{53},
		},
		BlacklistedKernelModules: []string{"usb-storage", "firewire-core"},
		AppArmor:                 ProfileSecurityAppArmorConfig{Enable: true},
		PasswordPolicy: ProfileSecurityPasswordPolicyConfig{
			PWQuality: ProfileSecurityPWQualityConfig{
				Enable:        true,
				MinimumLength: 16,
				MinimumDigits: 2,
				MinimumUpper:  1,
				MinimumLower:  1,
				MinimumOther:  1,
				RetryCount:    4,
			},
			Expiry: ProfileSecurityPasswordExpiryConfig{
				Enable:      true,
				MaximumDays: 60,
				WarningDays: 10,
			},
		},
		WebsiteBlocking: ProfileSecurityWebsiteBlockingConfig{
			Enable:          true,
			BlockCategories: []string{"social", "porn"},
		},
	}

	roundTripped := profileSecurityAPIConfigFromModel(config).toModel()

	if !roundTripped.PasswordPolicy.PWQuality.Enable || roundTripped.PasswordPolicy.PWQuality.MinimumLength != 16 || roundTripped.PasswordPolicy.PWQuality.RetryCount != 4 {
		t.Fatalf("unexpected pwquality round trip: %#v", roundTripped.PasswordPolicy.PWQuality)
	}

	if !roundTripped.PasswordPolicy.Expiry.Enable || roundTripped.PasswordPolicy.Expiry.MaximumDays != 60 || roundTripped.PasswordPolicy.Expiry.WarningDays != 10 {
		t.Fatalf("unexpected expiry round trip: %#v", roundTripped.PasswordPolicy.Expiry)
	}

	if len(roundTripped.WebsiteBlocking.BlockCategories) != 2 || roundTripped.WebsiteBlocking.BlockCategories[0] != "porn" || roundTripped.WebsiteBlocking.BlockCategories[1] != "social" {
		t.Fatalf("unexpected website blocking categories: %#v", roundTripped.WebsiteBlocking.BlockCategories)
	}
}

func TestScoreProfileSecuritySearchCandidatePenalizesMissingRequiredPorts(t *testing.T) {
	t.Parallel()

	config := ProfileSecurityConfig{
		Configured: true,
		Firewall: ProfileSecurityFirewallConfig{
			Enable:          true,
			AllowedTCPPorts: []int{22},
		},
		AppArmor: ProfileSecurityAppArmorConfig{Enable: true},
	}

	evaluation := profileSecuritySearchCandidateEvaluation{
		NixEvaluation:   profileNixEvaluationResult{Valid: true},
		MissingTCPPorts: []int{443},
	}

	score := scoreProfileSecuritySearchCandidate(config, evaluation)
	if score >= 40 {
		t.Fatalf("expected missing required port penalty, got score %d", score)
	}
}
