/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	profileSecurityConfigKeyName             = "security"
	profileSecurityFirewallConfigKeyName     = "firewall"
	profileSecurityAppArmorConfigKeyName     = "apparmor"
	profileSecurityPasswordPolicyConfigName  = "password_policy"
	profileSecurityPWQualityConfigName       = "pwquality"
	profileSecurityPasswordExpiryConfigName  = "expiry"
	profileSecurityWebsiteBlockingConfigName = "website_blocking"
)

const (
	defaultProfileSecurityPWQualityMinimumLength = 12
	defaultProfileSecurityPWQualityRetryCount    = 3
	defaultProfileSecurityPasswordExpiryMaxDays  = 90
	defaultProfileSecurityPasswordExpiryWarnDays = 14
)

var profileSecurityKernelModuleNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

var profileSecurityWebsiteBlockingCategories = []string{
	"fakenews",
	"gambling",
	"porn",
	"social",
}

var profileSecurityWebsiteBlockingCategorySet = map[string]struct{}{
	"fakenews": {},
	"gambling": {},
	"porn":     {},
	"social":   {},
}

type ProfileSecurityFirewallConfig struct {
	Enable          bool
	AllowedTCPPorts []int
	AllowedUDPPorts []int
}

type ProfileSecurityAppArmorConfig struct {
	Enable bool
}

type ProfileSecurityWebsiteBlockingConfig struct {
	Enable          bool
	BlockCategories []string
}

type ProfileSecurityPWQualityConfig struct {
	Enable        bool
	MinimumLength int
	MinimumDigits int
	MinimumUpper  int
	MinimumLower  int
	MinimumOther  int
	RetryCount    int
}

type ProfileSecurityPasswordExpiryConfig struct {
	Enable      bool
	MaximumDays int
	WarningDays int
}

type ProfileSecurityPasswordPolicyConfig struct {
	PWQuality ProfileSecurityPWQualityConfig
	Expiry    ProfileSecurityPasswordExpiryConfig
}

type ProfileSecurityConfig struct {
	Configured               bool
	Firewall                 ProfileSecurityFirewallConfig
	BlacklistedKernelModules []string
	AppArmor                 ProfileSecurityAppArmorConfig
	PasswordPolicy           ProfileSecurityPasswordPolicyConfig
	WebsiteBlocking          ProfileSecurityWebsiteBlockingConfig
}

func profileSecurityConfigFromProfileConfig(configJSON string) (ProfileSecurityConfig, error) {
	config, err := parseProfileConfig(configJSON)
	if err != nil {
		return ProfileSecurityConfig{}, err
	}

	rawSecurity, exists := config[profileSecurityConfigKeyName]
	if !exists || rawSecurity == nil {
		return ProfileSecurityConfig{}, nil
	}

	decodedSecurity, ok := rawSecurity.(map[string]any)
	if !ok {
		return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
	}

	securityConfig := ProfileSecurityConfig{Configured: true}

	rawFirewall, exists := decodedSecurity[profileSecurityFirewallConfigKeyName]
	if exists && rawFirewall != nil {
		decodedFirewall, ok := rawFirewall.(map[string]any)
		if !ok {
			return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
		}

		securityConfig.Firewall.Enable, err = optionalBoolField(decodedFirewall, "enable")
		if err != nil {
			return ProfileSecurityConfig{}, err
		}

		securityConfig.Firewall.AllowedTCPPorts, err = optionalIntListField(decodedFirewall, "allowed_tcp_ports")
		if err != nil {
			return ProfileSecurityConfig{}, err
		}

		securityConfig.Firewall.AllowedUDPPorts, err = optionalIntListField(decodedFirewall, "allowed_udp_ports")
		if err != nil {
			return ProfileSecurityConfig{}, err
		}
	}

	securityConfig.BlacklistedKernelModules, err = optionalStringListField(decodedSecurity, "blacklisted_kernel_modules")
	if err != nil {
		return ProfileSecurityConfig{}, err
	}

	rawAppArmor, exists := decodedSecurity[profileSecurityAppArmorConfigKeyName]
	if exists && rawAppArmor != nil {
		decodedAppArmor, ok := rawAppArmor.(map[string]any)
		if !ok {
			return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
		}

		securityConfig.AppArmor.Enable, err = optionalBoolField(decodedAppArmor, "enable")
		if err != nil {
			return ProfileSecurityConfig{}, err
		}
	}

	rawPasswordPolicy, exists := decodedSecurity[profileSecurityPasswordPolicyConfigName]
	if exists && rawPasswordPolicy != nil {
		decodedPasswordPolicy, ok := rawPasswordPolicy.(map[string]any)
		if !ok {
			return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
		}

		rawPWQuality, exists := decodedPasswordPolicy[profileSecurityPWQualityConfigName]
		if exists && rawPWQuality != nil {
			decodedPWQuality, ok := rawPWQuality.(map[string]any)
			if !ok {
				return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
			}

			securityConfig.PasswordPolicy.PWQuality.Enable, err = optionalBoolField(decodedPWQuality, "enable")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.PWQuality.MinimumLength, err = optionalIntField(decodedPWQuality, "minimum_length")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.PWQuality.MinimumDigits, err = optionalIntField(decodedPWQuality, "minimum_digits")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.PWQuality.MinimumUpper, err = optionalIntField(decodedPWQuality, "minimum_upper")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.PWQuality.MinimumLower, err = optionalIntField(decodedPWQuality, "minimum_lower")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.PWQuality.MinimumOther, err = optionalIntField(decodedPWQuality, "minimum_other")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.PWQuality.RetryCount, err = optionalIntField(decodedPWQuality, "retry_count")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}
		}

		rawExpiry, exists := decodedPasswordPolicy[profileSecurityPasswordExpiryConfigName]
		if exists && rawExpiry != nil {
			decodedExpiry, ok := rawExpiry.(map[string]any)
			if !ok {
				return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
			}

			securityConfig.PasswordPolicy.Expiry.Enable, err = optionalBoolField(decodedExpiry, "enable")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.Expiry.MaximumDays, err = optionalIntField(decodedExpiry, "maximum_days")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}

			securityConfig.PasswordPolicy.Expiry.WarningDays, err = optionalIntField(decodedExpiry, "warning_days")
			if err != nil {
				return ProfileSecurityConfig{}, err
			}
		}
	}

	rawWebsiteBlocking, exists := decodedSecurity[profileSecurityWebsiteBlockingConfigName]
	if exists && rawWebsiteBlocking != nil {
		decodedWebsiteBlocking, ok := rawWebsiteBlocking.(map[string]any)
		if !ok {
			return ProfileSecurityConfig{}, db.ErrInvalidProfileConfigJSON
		}

		securityConfig.WebsiteBlocking.Enable, err = optionalBoolField(decodedWebsiteBlocking, "enable")
		if err != nil {
			return ProfileSecurityConfig{}, err
		}

		securityConfig.WebsiteBlocking.BlockCategories, err = optionalStringListField(decodedWebsiteBlocking, "block_categories")
		if err != nil {
			return ProfileSecurityConfig{}, err
		}
	}

	securityConfig = normalizeProfileSecurityConfig(securityConfig)
	if err := validateProfileSecurityConfig(securityConfig); err != nil {
		return ProfileSecurityConfig{}, err
	}

	return securityConfig, nil
}

func profileSecurityConfigFromForm(values url.Values) (ProfileSecurityConfig, error) {
	firewallAllowedTCPPorts, err := parseProfileSecurityPortList(values.Get("firewall_allowed_tcp_ports"))
	if err != nil {
		return ProfileSecurityConfig{}, err
	}

	firewallAllowedUDPPorts, err := parseProfileSecurityPortList(values.Get("firewall_allowed_udp_ports"))
	if err != nil {
		return ProfileSecurityConfig{}, err
	}

	blacklistedKernelModules, err := parseProfileSecurityKernelModuleList(values.Get("blacklisted_kernel_modules"))
	if err != nil {
		return ProfileSecurityConfig{}, err
	}

	pwqualityMinLength, err := parseProfileSecurityFormInt(
		values.Get("password_pwquality_min_length"),
		defaultProfileSecurityPWQualityMinimumLength,
	)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password minimum length must be a whole number")
	}

	pwqualityMinDigits, err := parseProfileSecurityFormInt(values.Get("password_pwquality_min_digits"), 1)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password minimum digits must be a whole number")
	}

	pwqualityMinUpper, err := parseProfileSecurityFormInt(values.Get("password_pwquality_min_upper"), 1)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password minimum uppercase characters must be a whole number")
	}

	pwqualityMinLower, err := parseProfileSecurityFormInt(values.Get("password_pwquality_min_lower"), 1)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password minimum lowercase characters must be a whole number")
	}

	pwqualityMinOther, err := parseProfileSecurityFormInt(values.Get("password_pwquality_min_other"), 1)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password minimum special characters must be a whole number")
	}

	pwqualityRetryCount, err := parseProfileSecurityFormInt(
		values.Get("password_pwquality_retry_count"),
		defaultProfileSecurityPWQualityRetryCount,
	)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password retry count must be a whole number")
	}

	passwordExpiryMaxDays, err := parseProfileSecurityFormInt(
		values.Get("password_expiry_max_days"),
		defaultProfileSecurityPasswordExpiryMaxDays,
	)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password expiry maximum days must be a whole number")
	}

	passwordExpiryWarnDays, err := parseProfileSecurityFormInt(
		values.Get("password_expiry_warning_days"),
		defaultProfileSecurityPasswordExpiryWarnDays,
	)
	if err != nil {
		return ProfileSecurityConfig{}, fmt.Errorf("password expiry warning days must be a whole number")
	}

	websiteBlockingCategories := make([]string, 0, len(profileSecurityWebsiteBlockingCategories))
	for _, category := range profileSecurityWebsiteBlockingCategories {
		if strings.TrimSpace(values.Get("website_block_"+category)) == "" {
			continue
		}

		websiteBlockingCategories = append(websiteBlockingCategories, category)
	}

	securityConfig := normalizeProfileSecurityConfig(ProfileSecurityConfig{
		Configured: true,
		Firewall: ProfileSecurityFirewallConfig{
			Enable:          strings.TrimSpace(values.Get("firewall_enabled")) != "",
			AllowedTCPPorts: firewallAllowedTCPPorts,
			AllowedUDPPorts: firewallAllowedUDPPorts,
		},
		BlacklistedKernelModules: blacklistedKernelModules,
		AppArmor: ProfileSecurityAppArmorConfig{
			Enable: strings.TrimSpace(values.Get("apparmor_enabled")) != "",
		},
		PasswordPolicy: ProfileSecurityPasswordPolicyConfig{
			PWQuality: ProfileSecurityPWQualityConfig{
				Enable:        strings.TrimSpace(values.Get("password_pwquality_enabled")) != "",
				MinimumLength: pwqualityMinLength,
				MinimumDigits: pwqualityMinDigits,
				MinimumUpper:  pwqualityMinUpper,
				MinimumLower:  pwqualityMinLower,
				MinimumOther:  pwqualityMinOther,
				RetryCount:    pwqualityRetryCount,
			},
			Expiry: ProfileSecurityPasswordExpiryConfig{
				Enable:      strings.TrimSpace(values.Get("password_expiry_enabled")) != "",
				MaximumDays: passwordExpiryMaxDays,
				WarningDays: passwordExpiryWarnDays,
			},
		},
		WebsiteBlocking: ProfileSecurityWebsiteBlockingConfig{
			Enable:          strings.TrimSpace(values.Get("website_blocking_enabled")) != "",
			BlockCategories: websiteBlockingCategories,
		},
	})

	if err := validateProfileSecurityConfig(securityConfig); err != nil {
		return ProfileSecurityConfig{}, err
	}

	return securityConfig, nil
}

func normalizeProfileSecurityConfig(config ProfileSecurityConfig) ProfileSecurityConfig {
	config.Firewall.AllowedTCPPorts = normalizeProfileSecurityPortList(config.Firewall.AllowedTCPPorts)
	config.Firewall.AllowedUDPPorts = normalizeProfileSecurityPortList(config.Firewall.AllowedUDPPorts)
	config.BlacklistedKernelModules = normalizeProfileSecurityKernelModuleList(config.BlacklistedKernelModules)
	config.WebsiteBlocking.BlockCategories = normalizeProfileSecurityWebsiteBlockingCategories(config.WebsiteBlocking.BlockCategories)

	return config
}

func validateProfileSecurityConfig(config ProfileSecurityConfig) error {
	for _, port := range config.Firewall.AllowedTCPPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("firewall TCP ports must be between 1 and 65535")
		}
	}

	for _, port := range config.Firewall.AllowedUDPPorts {
		if port < 1 || port > 65535 {
			return fmt.Errorf("firewall UDP ports must be between 1 and 65535")
		}
	}

	for _, moduleName := range config.BlacklistedKernelModules {
		if !profileSecurityKernelModuleNamePattern.MatchString(moduleName) {
			return fmt.Errorf("blacklisted kernel module names may contain only letters, numbers, dots, dashes, underscores, and plus signs")
		}
	}

	if config.PasswordPolicy.PWQuality.Enable {
		if config.PasswordPolicy.PWQuality.MinimumLength < 1 {
			return fmt.Errorf("password minimum length must be at least 1")
		}

		if config.PasswordPolicy.PWQuality.MinimumDigits < 0 {
			return fmt.Errorf("password minimum digits cannot be negative")
		}

		if config.PasswordPolicy.PWQuality.MinimumUpper < 0 {
			return fmt.Errorf("password minimum uppercase characters cannot be negative")
		}

		if config.PasswordPolicy.PWQuality.MinimumLower < 0 {
			return fmt.Errorf("password minimum lowercase characters cannot be negative")
		}

		if config.PasswordPolicy.PWQuality.MinimumOther < 0 {
			return fmt.Errorf("password minimum special characters cannot be negative")
		}

		if config.PasswordPolicy.PWQuality.RetryCount < 1 {
			return fmt.Errorf("password retry count must be at least 1")
		}
	}

	if config.PasswordPolicy.Expiry.Enable {
		if config.PasswordPolicy.Expiry.MaximumDays < 1 {
			return fmt.Errorf("password expiry maximum days must be at least 1")
		}

		if config.PasswordPolicy.Expiry.WarningDays < 0 {
			return fmt.Errorf("password expiry warning days cannot be negative")
		}

		if config.PasswordPolicy.Expiry.WarningDays > config.PasswordPolicy.Expiry.MaximumDays {
			return fmt.Errorf("password expiry warning days cannot exceed maximum days")
		}
	}

	for _, category := range config.WebsiteBlocking.BlockCategories {
		if _, ok := profileSecurityWebsiteBlockingCategorySet[category]; !ok {
			return fmt.Errorf("website blocking category %q is not supported", category)
		}
	}

	return nil
}

func profileConfigWithSecurity(configJSON string, securityConfig ProfileSecurityConfig) (string, error) {
	config, err := parseProfileConfig(configJSON)
	if err != nil {
		return "", err
	}

	securityConfig = normalizeProfileSecurityConfig(securityConfig)
	if err := validateProfileSecurityConfig(securityConfig); err != nil {
		return "", err
	}

	if !securityConfig.Configured {
		delete(config, profileSecurityConfigKeyName)
	} else {
		config[profileSecurityConfigKeyName] = profileSecurityConfigValue(securityConfig)
	}

	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}

	return string(encoded), nil
}

func profileSecurityConfigValue(securityConfig ProfileSecurityConfig) map[string]any {
	securityConfig = normalizeProfileSecurityConfig(securityConfig)

	return map[string]any{
		profileSecurityFirewallConfigKeyName: map[string]any{
			"enable":            securityConfig.Firewall.Enable,
			"allowed_tcp_ports": securityConfig.Firewall.AllowedTCPPorts,
			"allowed_udp_ports": securityConfig.Firewall.AllowedUDPPorts,
		},
		"blacklisted_kernel_modules": securityConfig.BlacklistedKernelModules,
		profileSecurityAppArmorConfigKeyName: map[string]any{
			"enable": securityConfig.AppArmor.Enable,
		},
		profileSecurityPasswordPolicyConfigName: map[string]any{
			profileSecurityPWQualityConfigName: map[string]any{
				"enable":         securityConfig.PasswordPolicy.PWQuality.Enable,
				"minimum_length": securityConfig.PasswordPolicy.PWQuality.MinimumLength,
				"minimum_digits": securityConfig.PasswordPolicy.PWQuality.MinimumDigits,
				"minimum_upper":  securityConfig.PasswordPolicy.PWQuality.MinimumUpper,
				"minimum_lower":  securityConfig.PasswordPolicy.PWQuality.MinimumLower,
				"minimum_other":  securityConfig.PasswordPolicy.PWQuality.MinimumOther,
				"retry_count":    securityConfig.PasswordPolicy.PWQuality.RetryCount,
			},
			profileSecurityPasswordExpiryConfigName: map[string]any{
				"enable":       securityConfig.PasswordPolicy.Expiry.Enable,
				"maximum_days": securityConfig.PasswordPolicy.Expiry.MaximumDays,
				"warning_days": securityConfig.PasswordPolicy.Expiry.WarningDays,
			},
		},
		profileSecurityWebsiteBlockingConfigName: map[string]any{
			"enable":           securityConfig.WebsiteBlocking.Enable,
			"block_categories": securityConfig.WebsiteBlocking.BlockCategories,
		},
	}
}

func profileSecuritySummary(config ProfileSecurityConfig) string {
	if !config.Configured {
		return "Not configured"
	}

	parts := []string{}
	if config.Firewall.Enable {
		parts = append(parts, fmt.Sprintf("Firewall enabled (%d TCP, %d UDP)", len(config.Firewall.AllowedTCPPorts), len(config.Firewall.AllowedUDPPorts)))
	} else {
		parts = append(parts, "Firewall disabled")
	}

	if len(config.BlacklistedKernelModules) > 0 {
		parts = append(parts, fmt.Sprintf("%d blacklisted kernel module", len(config.BlacklistedKernelModules)))
		if len(config.BlacklistedKernelModules) != 1 {
			parts[len(parts)-1] += "s"
		}
	} else {
		parts = append(parts, "No blacklisted kernel modules")
	}

	if config.AppArmor.Enable {
		parts = append(parts, "AppArmor enabled")
	} else {
		parts = append(parts, "AppArmor disabled")
	}

	if config.PasswordPolicy.PWQuality.Enable {
		parts = append(parts, fmt.Sprintf("PWQuality enabled (min %d chars)", config.PasswordPolicy.PWQuality.MinimumLength))
	} else {
		parts = append(parts, "PWQuality disabled")
	}

	if config.PasswordPolicy.Expiry.Enable {
		parts = append(parts, fmt.Sprintf("Password expiry %d days", config.PasswordPolicy.Expiry.MaximumDays))
	} else {
		parts = append(parts, "Password expiry disabled")
	}

	if config.WebsiteBlocking.Enable {
		websiteBlocking := "Website blocking enabled"
		if len(config.WebsiteBlocking.BlockCategories) > 0 {
			websiteBlocking += fmt.Sprintf(" (%d extra ", len(config.WebsiteBlocking.BlockCategories))
			if len(config.WebsiteBlocking.BlockCategories) != 1 {
				websiteBlocking += "categories"
			} else {
				websiteBlocking += "category"
			}
			websiteBlocking += ")"
		}

		parts = append(parts, websiteBlocking)
	} else {
		parts = append(parts, "Website blocking disabled")
	}

	return strings.Join(parts, " - ")
}

func profileSecurityWebsiteBlockingSelections(categories []string) map[string]bool {
	selected := make(map[string]bool, len(profileSecurityWebsiteBlockingCategories))
	for _, category := range normalizeProfileSecurityWebsiteBlockingCategories(categories) {
		selected[category] = true
	}

	return selected
}

func formatProfileSecurityPortList(ports []int) string {
	if len(ports) == 0 {
		return ""
	}

	parts := make([]string, 0, len(ports))
	for _, port := range normalizeProfileSecurityPortList(ports) {
		parts = append(parts, strconv.Itoa(port))
	}

	return strings.Join(parts, ", ")
}

func buildSecurityOverridesBlock(config ProfileSecurityConfig) string {
	if !config.Configured {
		return ""
	}

	config = normalizeProfileSecurityConfig(config)

	blocks := []string{fmt.Sprintf(`  networking.firewall.enable = lib.mkForce %s;
  networking.firewall.allowedTCPPorts = lib.mkForce [%s ];
  networking.firewall.allowedUDPPorts = lib.mkForce [%s ];

  boot.blacklistedKernelModules = lib.mkForce [%s ];

  security.apparmor.enable = lib.mkForce %s;
  security.apparmor.packages = lib.mkForce %s;

  networking.stevenblack.enable = lib.mkForce %s;
  networking.stevenblack.block = lib.mkForce [%s ];`,
		profileSecurityNixBoolLiteral(config.Firewall.Enable),
		profileSecurityNixIntList(config.Firewall.AllowedTCPPorts),
		profileSecurityNixIntList(config.Firewall.AllowedUDPPorts),
		profileSecurityNixStringList(config.BlacklistedKernelModules),
		profileSecurityNixBoolLiteral(config.AppArmor.Enable),
		profileSecurityNixAppArmorPackages(config.AppArmor.Enable),
		profileSecurityNixBoolLiteral(config.WebsiteBlocking.Enable),
		profileSecurityNixStringList(config.WebsiteBlocking.BlockCategories),
	)}

	if pwqualityBlock := buildSecurityPWQualityOverridesBlock(config.PasswordPolicy.PWQuality); pwqualityBlock != "" {
		blocks = append(blocks, pwqualityBlock)
	}

	if expiryBlock := buildSecurityPasswordExpiryOverridesBlock(config.PasswordPolicy.Expiry); expiryBlock != "" {
		blocks = append(blocks, expiryBlock)
	}

	return strings.Join(blocks, "\n\n")
}

func normalizeProfileSecurityPortList(ports []int) []int {
	if len(ports) == 0 {
		return []int{}
	}

	seen := make(map[int]struct{}, len(ports))
	normalized := make([]int, 0, len(ports))
	for _, port := range ports {
		if _, exists := seen[port]; exists {
			continue
		}

		seen[port] = struct{}{}
		normalized = append(normalized, port)
	}

	sort.Ints(normalized)

	return normalized
}

func normalizeProfileSecurityKernelModuleList(moduleNames []string) []string {
	if len(moduleNames) == 0 {
		return []string{}
	}

	seen := make(map[string]struct{}, len(moduleNames))
	normalized := make([]string, 0, len(moduleNames))
	for _, moduleName := range moduleNames {
		trimmed := strings.TrimSpace(moduleName)
		if trimmed == "" {
			continue
		}

		if _, exists := seen[trimmed]; exists {
			continue
		}

		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}

	sort.Strings(normalized)

	return normalized
}

func normalizeProfileSecurityWebsiteBlockingCategories(categories []string) []string {
	if len(categories) == 0 {
		return []string{}
	}

	selected := make(map[string]struct{}, len(categories))
	for _, category := range categories {
		trimmed := strings.TrimSpace(category)
		if trimmed == "" {
			continue
		}

		if _, ok := profileSecurityWebsiteBlockingCategorySet[trimmed]; !ok {
			selected[trimmed] = struct{}{}
			continue
		}

		selected[trimmed] = struct{}{}
	}

	normalized := make([]string, 0, len(selected))
	for _, category := range profileSecurityWebsiteBlockingCategories {
		if _, ok := selected[category]; ok {
			normalized = append(normalized, category)
		}
	}

	for category := range selected {
		if _, ok := profileSecurityWebsiteBlockingCategorySet[category]; ok {
			continue
		}

		normalized = append(normalized, category)
	}

	return normalized
}

func parseProfileSecurityPortList(raw string) ([]int, error) {
	parts := splitProfileSecurityList(raw)
	if len(parts) == 0 {
		return []int{}, nil
	}

	ports := make([]int, 0, len(parts))
	for _, part := range parts {
		port, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("firewall ports must be whole numbers")
		}

		ports = append(ports, port)
	}

	return normalizeProfileSecurityPortList(ports), nil
}

func parseProfileSecurityKernelModuleList(raw string) ([]string, error) {
	moduleNames := normalizeProfileSecurityKernelModuleList(splitProfileSecurityList(raw))
	for _, moduleName := range moduleNames {
		if !profileSecurityKernelModuleNamePattern.MatchString(moduleName) {
			return nil, fmt.Errorf("blacklisted kernel module names may contain only letters, numbers, dots, dashes, underscores, and plus signs")
		}
	}

	return moduleNames, nil
}

func splitProfileSecurityList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}

	parts := strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', '\n', '\r', '\t', ' ':
			return true
		default:
			return false
		}
	})

	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}

		normalized = append(normalized, trimmed)
	}

	return normalized
}

func optionalIntListField(values map[string]any, key string) ([]int, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return []int{}, nil
	}

	rawList, ok := raw.([]any)
	if !ok {
		return nil, db.ErrInvalidProfileConfigJSON
	}

	valuesList := make([]int, 0, len(rawList))
	for _, rawValue := range rawList {
		parsed, err := profileSecurityIntValue(rawValue)
		if err != nil {
			return nil, err
		}

		valuesList = append(valuesList, parsed)
	}

	return normalizeProfileSecurityPortList(valuesList), nil
}

func optionalIntField(values map[string]any, key string) (int, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return 0, nil
	}

	return profileSecurityIntValue(raw)
}

func optionalStringListField(values map[string]any, key string) ([]string, error) {
	raw, exists := values[key]
	if !exists || raw == nil {
		return []string{}, nil
	}

	rawList, ok := raw.([]any)
	if !ok {
		return nil, db.ErrInvalidProfileConfigJSON
	}

	valuesList := make([]string, 0, len(rawList))
	for _, rawValue := range rawList {
		value, ok := rawValue.(string)
		if !ok {
			return nil, db.ErrInvalidProfileConfigJSON
		}

		valuesList = append(valuesList, strings.TrimSpace(value))
	}

	return valuesList, nil
}

func profileSecurityIntValue(raw any) (int, error) {
	switch value := raw.(type) {
	case float64:
		parsed := int(value)
		if value != float64(parsed) {
			return 0, db.ErrInvalidProfileConfigJSON
		}

		return parsed, nil
	case int:
		return value, nil
	case int64:
		return int(value), nil
	default:
		return 0, db.ErrInvalidProfileConfigJSON
	}
}

func parseProfileSecurityFormInt(raw string, defaultValue int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defaultValue, nil
	}

	return strconv.Atoi(trimmed)
}

func profileSecurityNixBoolLiteral(enabled bool) string {
	if enabled {
		return "true"
	}

	return "false"
}

func profileSecurityNixIntList(values []int) string {
	normalized := normalizeProfileSecurityPortList(values)
	if len(normalized) == 0 {
		return ""
	}

	parts := make([]string, 0, len(normalized))
	for _, value := range normalized {
		parts = append(parts, " "+strconv.Itoa(value))
	}

	return strings.Join(parts, "")
}

func profileSecurityNixStringList(values []string) string {
	if len(values) == 0 {
		return ""
	}

	parts := make([]string, 0, len(values))
	for _, value := range values {
		parts = append(parts, fmt.Sprintf(` "%s"`, escapeNixString(value)))
	}

	return strings.Join(parts, "")
}

func profileSecurityNixAppArmorPackages(enabled bool) string {
	if !enabled {
		return "[]"
	}

	return "(with pkgs; [ apparmor-utils apparmor-profiles ])"
}

func buildSecurityPWQualityOverridesBlock(config ProfileSecurityPWQualityConfig) string {
	if !config.Enable {
		return ""
	}

	return fmt.Sprintf(`  security.pam.services.passwd.rules.password.pwquality = {
    enable = true;
    order = config.security.pam.services.passwd.rules.password.unix.order - 10;
    control = "requisite";
    modulePath = "${pkgs.libpwquality.lib}/lib/security/pam_pwquality.so";
    settings = {
      retry = %d;
      minlen = %d;
      dcredit = %d;
      ucredit = %d;
      lcredit = %d;
      ocredit = %d;
    };
  };

  security.pam.services.chpasswd.rules.password.pwquality = {
    enable = true;
    order = config.security.pam.services.chpasswd.rules.password.unix.order - 10;
    control = "requisite";
    modulePath = "${pkgs.libpwquality.lib}/lib/security/pam_pwquality.so";
    settings = {
      retry = %d;
      minlen = %d;
      dcredit = %d;
      ucredit = %d;
      lcredit = %d;
      ocredit = %d;
    };
  };`,
		config.RetryCount,
		config.MinimumLength,
		-profileSecurityPWQualityRequiredValue(config.MinimumDigits),
		-profileSecurityPWQualityRequiredValue(config.MinimumUpper),
		-profileSecurityPWQualityRequiredValue(config.MinimumLower),
		-profileSecurityPWQualityRequiredValue(config.MinimumOther),
		config.RetryCount,
		config.MinimumLength,
		-profileSecurityPWQualityRequiredValue(config.MinimumDigits),
		-profileSecurityPWQualityRequiredValue(config.MinimumUpper),
		-profileSecurityPWQualityRequiredValue(config.MinimumLower),
		-profileSecurityPWQualityRequiredValue(config.MinimumOther),
	)
}

func buildSecurityPasswordExpiryOverridesBlock(config ProfileSecurityPasswordExpiryConfig) string {
	if !config.Enable {
		return ""
	}

	return fmt.Sprintf(`  security.loginDefs.settings.PASS_MAX_DAYS = lib.mkForce %d;
  security.loginDefs.settings.PASS_WARN_AGE = lib.mkForce %d;`, config.MaximumDays, config.WarningDays)
}

func profileSecurityPWQualityRequiredValue(value int) int {
	if value < 0 {
		return 0
	}

	return value
}
