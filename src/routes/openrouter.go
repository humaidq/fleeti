/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	defaultOpenRouterEndpoint = "https://openrouter.ai/api/v1/chat/completions"
	defaultOpenRouterModel    = "openai/gpt-5-mini"
	openRouterRequestTimeout  = 60 * time.Second
	maxProfileWizardToolCalls = 8
	maxProfileWizardPackages  = 100
)

type ProfileWizardAI struct {
	apiKey     string
	endpoint   string
	model      string
	referer    string
	title      string
	httpClient *http.Client
}

type profileWizardAIInput struct {
	Mode            string
	BaseProfileID   string
	OriginalDraft   profileWizardDraft
	Draft           profileWizardDraft
	Conversation    []profileWizardConversationEntry
	UserMessage     string
	AvailableFleets []db.Fleet
}

type profileWizardAIResult struct {
	Message string
	Draft   profileWizardDraft
}

type openRouterChatRequest struct {
	Model       string                     `json:"model"`
	Messages    []openRouterChatMessage    `json:"messages"`
	Stream      bool                       `json:"stream,omitempty"`
	Tools       []openRouterToolDefinition `json:"tools,omitempty"`
	ToolChoice  string                     `json:"tool_choice,omitempty"`
	Temperature float64                    `json:"temperature,omitempty"`
}

type openRouterChatMessage struct {
	Role       string               `json:"role"`
	Content    any                  `json:"content,omitempty"`
	ToolCallID string               `json:"tool_call_id,omitempty"`
	Name       string               `json:"name,omitempty"`
	ToolCalls  []openRouterToolCall `json:"tool_calls,omitempty"`
}

type openRouterChatResponse struct {
	Choices []openRouterChoice  `json:"choices"`
	Error   *openRouterAPIError `json:"error,omitempty"`
}

type openRouterChoice struct {
	Message openRouterChoiceMessage `json:"message"`
}

type openRouterChoiceMessage struct {
	Content   any                  `json:"content"`
	ToolCalls []openRouterToolCall `json:"tool_calls"`
}

type openRouterToolCall struct {
	ID       string                   `json:"id"`
	Type     string                   `json:"type"`
	Function openRouterToolCallTarget `json:"function"`
}

type openRouterToolCallTarget struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openRouterToolDefinition struct {
	Type     string                           `json:"type"`
	Function openRouterToolDefinitionFunction `json:"function"`
}

type openRouterToolDefinitionFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type openRouterAPIError struct {
	Message string `json:"message"`
}

type openRouterStreamResponse struct {
	Choices []openRouterStreamChoice `json:"choices"`
	Error   *openRouterAPIError      `json:"error,omitempty"`
}

type openRouterStreamChoice struct {
	Delta openRouterStreamDelta `json:"delta"`
}

type openRouterStreamDelta struct {
	Content string `json:"content"`
}

type profileWizardDraftUpdateInput struct {
	Name                   *string   `json:"name"`
	Description            *string   `json:"description"`
	ClearDescription       bool      `json:"clear_description"`
	FleetIDs               *[]string `json:"fleet_ids"`
	ClearFleetIDs          bool      `json:"clear_fleet_ids"`
	Packages               *[]string `json:"packages"`
	ClearPackages          bool      `json:"clear_packages"`
	AddPackages            []string  `json:"add_packages"`
	RemovePackages         []string  `json:"remove_packages"`
	KernelAttr             *string   `json:"kernel_attr"`
	ClearKernel            bool      `json:"clear_kernel"`
	OpenClawMicroVMEnabled *bool     `json:"openclaw_microvm_enabled"`
	RawNix                 *string   `json:"raw_nix"`
	ClearRawNix            bool      `json:"clear_raw_nix"`
}

type profileWizardPackageToolInput struct {
	Query string `json:"query"`
}

type profileWizardNixOptionListInput struct {
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

type profileWizardNixOptionDescribeInput struct {
	Path string `json:"path"`
}

// NewProfileWizardAIFromEnv builds an optional OpenRouter-backed AI client.
func NewProfileWizardAIFromEnv() *ProfileWizardAI {
	endpoint := strings.TrimSpace(os.Getenv("OPENROUTER_BASE_URL"))
	if endpoint == "" {
		endpoint = defaultOpenRouterEndpoint
	}

	model := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
	if model == "" {
		model = defaultOpenRouterModel
	}

	return &ProfileWizardAI{
		apiKey:   strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")),
		endpoint: endpoint,
		model:    model,
		referer:  strings.TrimSpace(os.Getenv("OPENROUTER_HTTP_REFERER")),
		title:    "Fleeti",
		httpClient: &http.Client{
			Timeout: openRouterRequestTimeout,
		},
	}
}

func (ai *ProfileWizardAI) Enabled() bool {
	return ai != nil && strings.TrimSpace(ai.apiKey) != ""
}

func (ai *ProfileWizardAI) DisabledReason() string {
	if ai != nil && ai.Enabled() {
		return ""
	}

	return "Set OPENROUTER_API_KEY to enable the AI profile wizard"
}

func (ai *ProfileWizardAI) Chat(ctx context.Context, input profileWizardAIInput) (profileWizardAIResult, error) {
	draft, guidance, err := ai.ResolveDraft(ctx, input)
	if err != nil {
		return profileWizardAIResult{}, err
	}

	message, err := ai.StreamReply(ctx, input, draft, guidance, nil)
	if err != nil {
		return profileWizardAIResult{}, err
	}

	return profileWizardAIResult{Message: message, Draft: draft}, nil
}

func (ai *ProfileWizardAI) ResolveDraft(ctx context.Context, input profileWizardAIInput) (profileWizardDraft, string, error) {
	if ai == nil || !ai.Enabled() {
		return profileWizardDraft{}, "", fmt.Errorf("profile wizard ai is disabled")
	}

	draft := normalizeProfileWizardDraft(input.Draft)
	messages := []openRouterChatMessage{{Role: "system", Content: buildProfileWizardSystemPrompt(input.Mode, draft, input.AvailableFleets)}}
	for _, entry := range normalizeProfileWizardConversation(input.Conversation) {
		messages = append(messages, openRouterChatMessage{Role: entry.Role, Content: entry.Content})
	}
	messages = append(messages, openRouterChatMessage{Role: "user", Content: strings.TrimSpace(input.UserMessage)})

	for range maxProfileWizardToolCalls {
		response, err := ai.createChatCompletion(ctx, openRouterChatRequest{
			Model:       ai.model,
			Messages:    messages,
			Tools:       profileWizardToolDefinitions(),
			ToolChoice:  "auto",
			Temperature: 0.2,
		})
		if err != nil {
			return profileWizardDraft{}, "", err
		}

		if len(response.Choices) == 0 {
			return profileWizardDraft{}, "", fmt.Errorf("openrouter returned no choices")
		}

		assistantMessage := response.Choices[0].Message
		assistantText := extractOpenRouterMessageText(assistantMessage.Content)
		if len(assistantMessage.ToolCalls) == 0 {
			return draft, strings.TrimSpace(assistantText), nil
		}

		messages = append(messages, openRouterChatMessage{
			Role:      "assistant",
			Content:   assistantText,
			ToolCalls: assistantMessage.ToolCalls,
		})

		for _, toolCall := range assistantMessage.ToolCalls {
			toolResult, updatedDraft := executeProfileWizardTool(ctx, input.Mode, input.OriginalDraft, draft, input.AvailableFleets, toolCall)
			draft = updatedDraft
			messages = append(messages, openRouterChatMessage{
				Role:       "tool",
				ToolCallID: toolCall.ID,
				Name:       toolCall.Function.Name,
				Content:    encodeProfileWizardToolResult(toolResult),
			})
		}
	}

	return profileWizardDraft{}, "", fmt.Errorf("profile wizard tool loop exceeded limit")
}

func (ai *ProfileWizardAI) StreamChat(ctx context.Context, input profileWizardAIInput, onChunk func(string) error) (profileWizardAIResult, error) {
	draft, guidance, err := ai.ResolveDraft(ctx, input)
	if err != nil {
		return profileWizardAIResult{}, err
	}

	message, err := ai.StreamReply(ctx, input, draft, guidance, onChunk)
	if err != nil {
		return profileWizardAIResult{}, err
	}

	return profileWizardAIResult{Message: message, Draft: draft}, nil
}

func (ai *ProfileWizardAI) StreamReply(ctx context.Context, input profileWizardAIInput, draft profileWizardDraft, guidance string, onChunk func(string) error) (string, error) {
	if ai == nil || !ai.Enabled() {
		return "", fmt.Errorf("profile wizard ai is disabled")
	}

	messages := []openRouterChatMessage{{Role: "system", Content: buildProfileWizardReplyPrompt(input.Mode, draft, input.AvailableFleets, guidance)}}
	for _, entry := range normalizeProfileWizardConversation(input.Conversation) {
		messages = append(messages, openRouterChatMessage{Role: entry.Role, Content: entry.Content})
	}
	messages = append(messages, openRouterChatMessage{Role: "user", Content: strings.TrimSpace(input.UserMessage)})

	return ai.streamChatCompletion(ctx, openRouterChatRequest{
		Model:       ai.model,
		Messages:    messages,
		Stream:      true,
		Temperature: 0.2,
	}, onChunk)
}

func (ai *ProfileWizardAI) createChatCompletion(ctx context.Context, payload openRouterChatRequest) (openRouterChatResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return openRouterChatResponse{}, fmt.Errorf("failed to encode openrouter request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ai.endpoint, bytes.NewReader(body))
	if err != nil {
		return openRouterChatResponse{}, fmt.Errorf("failed to build openrouter request: %w", err)
	}

	request.Header.Set("Authorization", "Bearer "+ai.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Title", ai.title)
	if ai.referer != "" {
		request.Header.Set("HTTP-Referer", ai.referer)
	}

	response, err := ai.httpClient.Do(request)
	if err != nil {
		return openRouterChatResponse{}, fmt.Errorf("openrouter request failed: %w", err)
	}
	defer response.Body.Close()

	var decoded openRouterChatResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return openRouterChatResponse{}, fmt.Errorf("failed to decode openrouter response: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest {
		message := response.Status
		if decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
			message = decoded.Error.Message
		}

		return openRouterChatResponse{}, fmt.Errorf("openrouter error: %s", message)
	}

	return decoded, nil
}

func (ai *ProfileWizardAI) streamChatCompletion(ctx context.Context, payload openRouterChatRequest, onChunk func(string) error) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to encode openrouter stream request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, ai.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("failed to build openrouter stream request: %w", err)
	}

	request.Header.Set("Authorization", "Bearer "+ai.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Title", ai.title)
	if ai.referer != "" {
		request.Header.Set("HTTP-Referer", ai.referer)
	}

	response, err := ai.httpClient.Do(request)
	if err != nil {
		return "", fmt.Errorf("openrouter stream request failed: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		var decoded openRouterChatResponse
		if err := json.NewDecoder(response.Body).Decode(&decoded); err == nil && decoded.Error != nil && strings.TrimSpace(decoded.Error.Message) != "" {
			return "", fmt.Errorf("openrouter error: %s", decoded.Error.Message)
		}

		bodyText, _ := io.ReadAll(response.Body)
		return "", fmt.Errorf("openrouter error: %s", strings.TrimSpace(string(bodyText)))
	}

	return streamOpenRouterResponse(response.Body, onChunk)
}

func buildProfileWizardSystemPrompt(mode string, draft profileWizardDraft, fleets []db.Fleet) string {
	summary := summarizeProfileWizardDraft(profileWizardState{Mode: mode, Draft: draft}, fleets)
	encodedDraft, _ := json.Marshal(summary)
	encodedFleets, _ := json.Marshal(profileWizardFleetOptions(fleets))

	modeDescription := "creating a new profile"
	if strings.TrimSpace(mode) == profileWizardModeAdapt {
		modeDescription = "adapting an existing profile"
	}

	return strings.TrimSpace("You are Fleeti's profile wizard assistant. You are " + modeDescription + ". " +
		"Collect the user's requirements conversationally and keep the draft accurate. " +
		"Only work within Fleeti's supported profile fields: name, description, assigned fleets, packages, kernel selection, OpenClaw MicroVM toggle, and raw Nix. " +
		"Use tools whenever you need to inspect or update the draft, search packages, inspect fleets, list kernels, validate the draft, validate raw Nix, or inspect pinned NixOS options. " +
		"If the user asks to start over, reset, discard changes, or revert to the original profile state, use the reset_profile_draft tool. " +
		"Important: never clear existing fields implicitly. Only use clear_description, clear_fleet_ids, clear_packages, clear_kernel, or clear_raw_nix when the user explicitly asked to remove something. " +
		"Do not claim anything has been saved. The draft is only persisted when the user presses Apply. " +
		"When package names, kernel choices, or raw Nix options are uncertain, use the discovery and evaluation tools instead of guessing. " +
		"Keep replies concise and action-oriented, and end with the next useful question when more information is needed. " +
		"Unknown config keys from an existing profile are preserved automatically, so do not mention or alter them unless the user asks for supported changes. " +
		"Current draft JSON: " + string(encodedDraft) + ". " +
		"Available fleets JSON: " + string(encodedFleets) + ".")
}

func buildProfileWizardReplyPrompt(mode string, draft profileWizardDraft, fleets []db.Fleet, guidance string) string {
	summary := summarizeProfileWizardDraft(profileWizardState{Mode: mode, Draft: draft}, fleets)
	validation := validateProfileWizardDraft(context.Background(), draft, fleets)
	encodedDraft, _ := json.Marshal(summary)
	encodedValidation, _ := json.Marshal(validation)

	prompt := "You are Fleeti's profile wizard assistant. The profile draft has already been updated server-side. " +
		"Write the next assistant message to the user based on the current draft. " +
		"Be concise, practical, and do not mention internal tools or hidden reasoning. " +
		"If the draft is ready, tell the user they can press Apply. If important details are still missing, ask one short follow-up question. " +
		"Current draft JSON: " + string(encodedDraft) + ". Validation JSON: " + string(encodedValidation) + "."

	guidance = strings.TrimSpace(guidance)
	if guidance != "" {
		prompt += " Planning note: " + guidance + "."
	}

	return prompt
}

func profileWizardToolDefinitions() []openRouterToolDefinition {
	return []openRouterToolDefinition{
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "get_profile_draft",
				Description: "Return the current draft summary.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "get_available_fleets",
				Description: "Return the fleets the user can assign to this profile.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "reset_profile_draft",
				Description: "Reset the working draft back to the original state for this wizard session.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "search_nix_packages",
				Description: "Search nix packages when the user mentions software or services.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{"type": "string", "description": "Short package search query."},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "list_available_kernel_options",
				Description: "Return the pinned kernel options available in Fleeti.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "evaluate_profile_draft_nix",
				Description: "Evaluate the current draft, including raw Nix, against Fleeti's pinned NixOS flake without building an image.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "list_nixos_option_children",
				Description: "List child option names under a pinned NixOS option path like services, programs, or services.openssh.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path":  map[string]any{"type": "string", "description": "Option path to inspect, such as services or services.openssh."},
						"limit": map[string]any{"type": "integer", "description": "Optional maximum number of children to return."},
					},
				},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "describe_nixos_option",
				Description: "Describe a specific pinned NixOS option path like services.openssh.enable.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]any{"type": "string", "description": "Exact NixOS option path to describe."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "update_profile_draft",
				Description: "Update one or more supported profile draft fields without saving to the database.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":                     map[string]any{"type": "string"},
						"description":              map[string]any{"type": "string"},
						"clear_description":        map[string]any{"type": "boolean", "description": "Set true only when the user explicitly asked to remove the description."},
						"fleet_ids":                map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"clear_fleet_ids":          map[string]any{"type": "boolean", "description": "Set true only when the user explicitly asked to remove all fleet assignments."},
						"packages":                 map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"clear_packages":           map[string]any{"type": "boolean", "description": "Set true only when the user explicitly asked to remove all packages."},
						"add_packages":             map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"remove_packages":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"kernel_attr":              map[string]any{"type": "string", "description": "Pinned kernel attr like linux_6_19."},
						"clear_kernel":             map[string]any{"type": "boolean"},
						"openclaw_microvm_enabled": map[string]any{"type": "boolean"},
						"raw_nix":                  map[string]any{"type": "string"},
						"clear_raw_nix":            map[string]any{"type": "boolean"},
					},
				},
			},
		},
		{
			Type: "function",
			Function: openRouterToolDefinitionFunction{
				Name:        "validate_profile_draft",
				Description: "Validate whether the current draft is ready to apply.",
				Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			},
		},
	}
}

func executeProfileWizardTool(ctx context.Context, mode string, originalDraft, draft profileWizardDraft, fleets []db.Fleet, toolCall openRouterToolCall) (map[string]any, profileWizardDraft) {
	originalDraft = normalizeProfileWizardDraft(originalDraft)
	draft = normalizeProfileWizardDraft(draft)
	name := strings.TrimSpace(toolCall.Function.Name)

	switch name {
	case "get_profile_draft":
		return map[string]any{
			"ok":    true,
			"draft": summarizeProfileWizardDraft(profileWizardState{Mode: mode, OriginalDraft: originalDraft, Draft: draft}, fleets),
		}, draft
	case "get_available_fleets":
		return map[string]any{
			"ok":     true,
			"fleets": profileWizardFleetOptions(fleets),
		}, draft
	case "reset_profile_draft":
		resetDraft := originalDraft
		return map[string]any{
			"ok":    true,
			"draft": summarizeProfileWizardDraft(profileWizardState{Mode: mode, OriginalDraft: originalDraft, Draft: resetDraft}, fleets),
		}, resetDraft
	case "search_nix_packages":
		var input profileWizardPackageToolInput
		if err := decodeProfileWizardToolInput(toolCall.Function.Arguments, &input); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		packages, err := packagesFromProfileConfig(draft.ConfigJSON)
		if err != nil {
			return map[string]any{"ok": false, "error": mutationErrorMessage(err)}, draft
		}

		existing := make(map[string]struct{}, len(packages))
		for _, pkg := range packages {
			existing[pkg] = struct{}{}
		}

		results, total, err := searchNixPackages(ctx, input.Query, existing)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		rows := make([]map[string]any, 0, len(results))
		for _, result := range results {
			rows = append(rows, map[string]any{
				"attr_name":     result.AttrName,
				"package_name":  result.PackageName,
				"version":       result.Version,
				"description":   result.Description,
				"already_added": result.AlreadyAdded,
				"addable":       result.Addable,
			})
		}

		return map[string]any{"ok": true, "total": total, "results": rows}, draft
	case "list_available_kernel_options":
		options, err := listAvailableKernelOptions(ctx)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		return map[string]any{"ok": true, "kernels": options}, draft
	case "evaluate_profile_draft_nix":
		return map[string]any{"ok": true, "evaluation": evaluateProfileDraftAgainstPinnedNix(ctx, draft)}, draft
	case "list_nixos_option_children":
		var input profileWizardNixOptionListInput
		if err := decodeProfileWizardToolInput(toolCall.Function.Arguments, &input); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		result := listPinnedNixOSOptionChildren(ctx, input.Path, input.Limit)
		return map[string]any{"ok": result.Error == "", "result": result}, draft
	case "describe_nixos_option":
		var input profileWizardNixOptionDescribeInput
		if err := decodeProfileWizardToolInput(toolCall.Function.Arguments, &input); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		result := describePinnedNixOSOption(ctx, input.Path)
		return map[string]any{"ok": result.Error == "", "result": result}, draft
	case "update_profile_draft":
		var input profileWizardDraftUpdateInput
		if err := decodeProfileWizardToolInput(toolCall.Function.Arguments, &input); err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		updatedDraft, err := applyProfileWizardDraftUpdate(draft, input)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}, draft
		}

		return map[string]any{
			"ok":    true,
			"draft": summarizeProfileWizardDraft(profileWizardState{Mode: mode, OriginalDraft: originalDraft, Draft: updatedDraft}, fleets),
		}, updatedDraft
	case "validate_profile_draft":
		return map[string]any{
			"ok":         true,
			"validation": validateProfileWizardDraft(ctx, draft, fleets),
		}, draft
	default:
		return map[string]any{"ok": false, "error": "Unsupported tool call"}, draft
	}
}

func applyProfileWizardDraftUpdate(draft profileWizardDraft, input profileWizardDraftUpdateInput) (profileWizardDraft, error) {
	draft = normalizeProfileWizardDraft(draft)

	if input.Name != nil {
		trimmedName := strings.TrimSpace(*input.Name)
		if trimmedName != "" {
			draft.Name = trimmedName
		}
	}

	if input.ClearDescription {
		draft.Description = ""
	} else if input.Description != nil {
		trimmedDescription := strings.TrimSpace(*input.Description)
		if trimmedDescription != "" {
			draft.Description = trimmedDescription
		}
	}

	if input.ClearFleetIDs {
		draft.FleetIDs = []string{}
	} else if input.FleetIDs != nil {
		normalizedFleetIDs := normalizeProfileWizardFleetIDs(*input.FleetIDs)
		if len(normalizedFleetIDs) > 0 {
			draft.FleetIDs = normalizedFleetIDs
		}
	}

	configJSON := draft.ConfigJSON
	if input.ClearPackages {
		updatedConfigJSON, err := profileConfigWithPackages(configJSON, []string{})
		if err != nil {
			return draft, err
		}

		configJSON = updatedConfigJSON
	} else if input.Packages != nil {
		normalizedPackages := limitProfileWizardPackages(*input.Packages)
		if len(normalizedPackages) > 0 {
			updatedConfigJSON, err := profileConfigWithPackages(configJSON, normalizedPackages)
			if err != nil {
				return draft, err
			}

			configJSON = updatedConfigJSON
		}
	}

	if len(input.AddPackages) > 0 || len(input.RemovePackages) > 0 {
		packages, err := packagesFromProfileConfig(configJSON)
		if err != nil {
			return draft, err
		}

		packages = append(packages, limitProfileWizardPackages(input.AddPackages)...)
		for _, pkg := range input.RemovePackages {
			packages = removeString(packages, pkg)
		}

		updatedConfigJSON, err := profileConfigWithPackages(configJSON, limitProfileWizardPackages(packages))
		if err != nil {
			return draft, err
		}

		configJSON = updatedConfigJSON
	}

	if input.ClearKernel {
		updatedConfigJSON, err := profileConfigWithKernel(configJSON, ProfileKernelConfig{})
		if err != nil {
			return draft, err
		}

		configJSON = updatedConfigJSON
	}

	if input.KernelAttr != nil {
		kernelConfig, err := profileKernelConfigFromProfileConfig(configJSON)
		if err != nil {
			return draft, err
		}

		kernelConfig.Attr = strings.TrimSpace(*input.KernelAttr)
		if kernelConfig.Attr == "" {
			kernelConfig = ProfileKernelConfig{}
		}

		updatedConfigJSON, err := profileConfigWithKernel(configJSON, kernelConfig)
		if err != nil {
			return draft, err
		}

		configJSON = updatedConfigJSON
	}

	if input.OpenClawMicroVMEnabled != nil {
		updatedConfigJSON, err := profileConfigWithOpenclawMicrovmEnabled(configJSON, *input.OpenClawMicroVMEnabled)
		if err != nil {
			return draft, err
		}

		configJSON = updatedConfigJSON
	}

	draft.ConfigJSON = configJSON
	if input.ClearRawNix {
		draft.RawNix = ""
	}

	if input.RawNix != nil {
		draft.RawNix = strings.TrimSpace(*input.RawNix)
	}

	return normalizeProfileWizardDraft(draft), nil
}

func limitProfileWizardPackages(packages []string) []string {
	normalized := normalizePackageList(packages)
	if len(normalized) > maxProfileWizardPackages {
		return append([]string(nil), normalized[:maxProfileWizardPackages]...)
	}

	return normalized
}

func decodeProfileWizardToolInput(arguments string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(arguments)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid tool arguments")
	}

	return nil
}

func encodeProfileWizardToolResult(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `{"ok":false,"error":"failed to encode tool result"}`
	}

	return string(encoded)
}

func extractOpenRouterMessageText(content any) string {
	switch value := content.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(value)
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}

			if text, ok := part["text"].(string); ok {
				parts = append(parts, text)
			}
		}

		return strings.TrimSpace(strings.Join(parts, "\n"))
	default:
		return ""
	}
}

func streamOpenRouterResponse(body io.Reader, onChunk func(string) error) (string, error) {
	reader := bufio.NewReader(body)
	var content strings.Builder

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return content.String(), fmt.Errorf("failed to read stream: %w", err)
		}

		chunk, done, err := processOpenRouterStreamLine(line)
		if err != nil {
			return content.String(), err
		}

		if chunk != "" {
			content.WriteString(chunk)
			if onChunk != nil {
				if err := onChunk(chunk); err != nil {
					return content.String(), err
				}
			}
		}

		if done {
			break
		}
	}

	return content.String(), nil
}

func processOpenRouterStreamLine(line []byte) (string, bool, error) {
	lineStr := strings.TrimSpace(string(line))
	if lineStr == "" || !strings.HasPrefix(lineStr, "data: ") {
		return "", false, nil
	}

	data := strings.TrimPrefix(lineStr, "data: ")
	if data == "[DONE]" {
		return "", true, nil
	}

	var response openRouterStreamResponse
	if err := json.Unmarshal([]byte(data), &response); err != nil {
		return "", false, fmt.Errorf("failed to decode stream response: %w", err)
	}

	if response.Error != nil {
		return "", false, fmt.Errorf("openrouter error: %s", response.Error.Message)
	}

	if len(response.Choices) == 0 {
		return "", false, nil
	}

	return response.Choices[0].Delta.Content, false, nil
}
