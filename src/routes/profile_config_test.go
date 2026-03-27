/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"net/url"
	"strings"
	"testing"
)

func TestOpenclawMicrovmEnabledFromProfileConfigDefaultsToFalse(t *testing.T) {
	t.Parallel()

	enabled, err := openclawMicrovmEnabledFromProfileConfig(`{"packages":["vim"]}`)
	if err != nil {
		t.Fatalf("openclawMicrovmEnabledFromProfileConfig returned error: %v", err)
	}

	if enabled {
		t.Fatal("expected OpenClaw MicroVM to default to disabled")
	}
}

func TestProfileConfigWithOpenclawMicrovmEnabledAddsFlag(t *testing.T) {
	t.Parallel()

	updated, err := profileConfigWithOpenclawMicrovmEnabled(`{"packages":["vim"]}`, true)
	if err != nil {
		t.Fatalf("profileConfigWithOpenclawMicrovmEnabled returned error: %v", err)
	}

	if !strings.Contains(updated, `"packages":["vim"]`) {
		t.Fatalf("expected package list to be preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `"openclaw_microvm_enabled":true`) {
		t.Fatalf("expected openclaw microvm flag to be enabled, got: %s", updated)
	}
}

func TestProfileConfigWithOpenclawMicrovmEnabledRemovesFlag(t *testing.T) {
	t.Parallel()

	updated, err := profileConfigWithOpenclawMicrovmEnabled(`{"packages":["vim"],"openclaw_microvm_enabled":true}`, false)
	if err != nil {
		t.Fatalf("profileConfigWithOpenclawMicrovmEnabled returned error: %v", err)
	}

	if strings.Contains(updated, `"openclaw_microvm_enabled"`) {
		t.Fatalf("expected openclaw microvm flag to be removed, got: %s", updated)
	}
}

func TestOpenclawMicrovmEnabledFromProfileConfigRejectsNonBoolean(t *testing.T) {
	t.Parallel()

	_, err := openclawMicrovmEnabledFromProfileConfig(`{"openclaw_microvm_enabled":"yes"}`)
	if err == nil {
		t.Fatal("expected error for non-boolean openclaw microvm flag")
	}
}

func TestProfileSecurityConfigFromProfileConfigParsesSecuritySettings(t *testing.T) {
	t.Parallel()

	config, err := profileSecurityConfigFromProfileConfig(`{"security":{"firewall":{"enable":true,"allowed_tcp_ports":[443,22,80],"allowed_udp_ports":[53]},"blacklisted_kernel_modules":["usb-storage","firewire-core"],"apparmor":{"enable":true},"password_policy":{"pwquality":{"enable":true,"minimum_length":14,"minimum_digits":1,"minimum_upper":1,"minimum_lower":1,"minimum_other":1,"retry_count":3},"expiry":{"enable":true,"maximum_days":90,"warning_days":14}},"website_blocking":{"enable":true,"block_categories":["social","porn"]}}}`)
	if err != nil {
		t.Fatalf("profileSecurityConfigFromProfileConfig returned error: %v", err)
	}

	if !config.Configured {
		t.Fatal("expected security config to be marked configured")
	}

	if !config.Firewall.Enable {
		t.Fatal("expected firewall to be enabled")
	}

	if len(config.Firewall.AllowedTCPPorts) != 3 || config.Firewall.AllowedTCPPorts[0] != 22 || config.Firewall.AllowedTCPPorts[2] != 443 {
		t.Fatalf("expected normalized TCP ports, got %#v", config.Firewall.AllowedTCPPorts)
	}

	if len(config.BlacklistedKernelModules) != 2 || config.BlacklistedKernelModules[0] != "firewire-core" {
		t.Fatalf("expected normalized blacklisted kernel modules, got %#v", config.BlacklistedKernelModules)
	}

	if !config.AppArmor.Enable {
		t.Fatal("expected apparmor to be enabled")
	}

	if !config.PasswordPolicy.PWQuality.Enable || config.PasswordPolicy.PWQuality.MinimumLength != 14 {
		t.Fatalf("expected password quality policy to be parsed, got %#v", config.PasswordPolicy.PWQuality)
	}

	if !config.PasswordPolicy.Expiry.Enable || config.PasswordPolicy.Expiry.MaximumDays != 90 || config.PasswordPolicy.Expiry.WarningDays != 14 {
		t.Fatalf("expected password expiry policy to be parsed, got %#v", config.PasswordPolicy.Expiry)
	}

	if !config.WebsiteBlocking.Enable {
		t.Fatal("expected website blocking to be enabled")
	}

	if len(config.WebsiteBlocking.BlockCategories) != 2 || config.WebsiteBlocking.BlockCategories[0] != "porn" || config.WebsiteBlocking.BlockCategories[1] != "social" {
		t.Fatalf("expected normalized website blocking categories, got %#v", config.WebsiteBlocking.BlockCategories)
	}
}

func TestProfileConfigWithSecurityAddsSection(t *testing.T) {
	t.Parallel()

	updated, err := profileConfigWithSecurity(`{"packages":["vim"]}`, ProfileSecurityConfig{
		Configured: true,
		Firewall: ProfileSecurityFirewallConfig{
			Enable:          true,
			AllowedTCPPorts: []int{22, 443},
		},
		AppArmor: ProfileSecurityAppArmorConfig{Enable: true},
		PasswordPolicy: ProfileSecurityPasswordPolicyConfig{
			PWQuality: ProfileSecurityPWQualityConfig{
				Enable:        true,
				MinimumLength: 12,
				MinimumDigits: 1,
				MinimumUpper:  1,
				MinimumLower:  1,
				MinimumOther:  1,
				RetryCount:    3,
			},
			Expiry: ProfileSecurityPasswordExpiryConfig{
				Enable:      true,
				MaximumDays: 90,
				WarningDays: 14,
			},
		},
	})
	if err != nil {
		t.Fatalf("profileConfigWithSecurity returned error: %v", err)
	}

	if !strings.Contains(updated, `"packages":["vim"]`) {
		t.Fatalf("expected packages to be preserved, got: %s", updated)
	}

	if !strings.Contains(updated, `"security"`) {
		t.Fatalf("expected security section to be present, got: %s", updated)
	}

	if !strings.Contains(updated, `"allowed_tcp_ports":[22,443]`) {
		t.Fatalf("expected firewall TCP ports to be stored, got: %s", updated)
	}

	if !strings.Contains(updated, `"apparmor":{"enable":true}`) {
		t.Fatalf("expected apparmor enable flag to be stored, got: %s", updated)
	}

	if !strings.Contains(updated, `"password_policy"`) {
		t.Fatalf("expected password policy section to be stored, got: %s", updated)
	}

	if !strings.Contains(updated, `"pwquality":{"enable":true,"minimum_digits":1,"minimum_length":12,"minimum_lower":1,"minimum_other":1,"minimum_upper":1,"retry_count":3}`) {
		t.Fatalf("expected pwquality settings to be stored, got: %s", updated)
	}

	if !strings.Contains(updated, `"expiry":{"enable":true,"maximum_days":90,"warning_days":14}`) {
		t.Fatalf("expected password expiry settings to be stored, got: %s", updated)
	}

	if !strings.Contains(updated, `"website_blocking":{"block_categories":[],"enable":false}`) {
		t.Fatalf("expected website blocking defaults to be stored, got: %s", updated)
	}
}

func TestProfileSecurityConfigFromFormRejectsInvalidPort(t *testing.T) {
	t.Parallel()

	_, err := profileSecurityConfigFromForm(url.Values{
		"firewall_allowed_tcp_ports": {"22, abc"},
	})
	if err == nil {
		t.Fatal("expected invalid firewall port error")
	}
}

func TestProfileSecurityConfigFromProfileConfigRejectsUnsupportedWebsiteCategory(t *testing.T) {
	t.Parallel()

	_, err := profileSecurityConfigFromProfileConfig(`{"security":{"website_blocking":{"enable":true,"block_categories":["unknown"]}}}`)
	if err == nil {
		t.Fatal("expected unsupported website blocking category error")
	}
}

func TestProfileSecurityConfigFromFormDefaultsPasswordPolicyValues(t *testing.T) {
	t.Parallel()

	config, err := profileSecurityConfigFromForm(url.Values{
		"password_pwquality_enabled": {"1"},
		"password_expiry_enabled":    {"1"},
	})
	if err != nil {
		t.Fatalf("profileSecurityConfigFromForm returned error: %v", err)
	}

	if config.PasswordPolicy.PWQuality.MinimumLength != defaultProfileSecurityPWQualityMinimumLength || config.PasswordPolicy.PWQuality.RetryCount != defaultProfileSecurityPWQualityRetryCount {
		t.Fatalf("expected default pwquality values, got %#v", config.PasswordPolicy.PWQuality)
	}

	if config.PasswordPolicy.Expiry.MaximumDays != defaultProfileSecurityPasswordExpiryMaxDays || config.PasswordPolicy.Expiry.WarningDays != defaultProfileSecurityPasswordExpiryWarnDays {
		t.Fatalf("expected default password expiry values, got %#v", config.PasswordPolicy.Expiry)
	}
}
