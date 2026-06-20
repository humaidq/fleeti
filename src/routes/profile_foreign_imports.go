/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	foreignFlakeCommandTimeout = 120 * time.Second
	foreignNetrcFileName       = "fleeti-foreign-netrc"
)

// foreignFlakeModuleResult is the JSON payload returned to the Foreign Imports
// page when a flake's modules are loaded.
type foreignFlakeModuleResult struct {
	Modules []string `json:"modules"`
	Rev     string   `json:"rev"`
	Error   string   `json:"error,omitempty"`
}

// foreignImportView is the template-facing representation of a stored foreign
// import. The access token is never exposed; only whether one is set.
type foreignImportView struct {
	FlakeRef string
	Rev      string
	Modules  []string
	HasToken bool
	Host     string
	Username string
}

func foreignImportViews(imports []db.ForeignImport) []foreignImportView {
	views := make([]foreignImportView, 0, len(imports))
	for _, item := range imports {
		view := foreignImportView{
			FlakeRef: item.FlakeRef,
			Rev:      item.Rev,
			Modules:  item.Modules,
		}
		if item.Auth != nil && strings.TrimSpace(item.Auth.Token) != "" {
			view.HasToken = true
			view.Host = item.Auth.Host
			view.Username = item.Auth.Username
		}
		views = append(views, view)
	}

	return views
}

// ProfileForeignImportsPage renders the external flake module configuration for
// a profile.
func ProfileForeignImportsPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Foreign Imports")
	data["IsProfiles"] = true

	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		redirectWithMessage(c, s, "/profiles", FlashError, "Access restricted")

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		redirectWithMessage(c, s, "/profiles", FlashError, "Profile not found")

		return
	}

	profile, err := db.GetProfileForEdit(c.Request().Context(), profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		redirectWithMessage(c, s, "/profiles", FlashError, "Access restricted")

		return
	}

	data["Profile"] = profile
	data["ForeignImports"] = foreignImportViews(profile.ForeignImports)
	data["ProfileNavActive"] = "foreign_imports"
	data["CanManageProfile"] = true
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Foreign Imports"))

	t.HTML(http.StatusOK, "profile_foreign_imports")
}

// UpdateProfileForeignImports persists the external flakes and selected modules
// for a profile, preserving the rest of the profile configuration.
func UpdateProfileForeignImports(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		redirectWithMessage(c, s, "/profiles", FlashError, "Profile not found")

		return
	}

	path := profileForeignImportsPath(profileID)

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	profile, err := db.GetProfileForEdit(c.Request().Context(), profileID)
	if err != nil {
		if errors.Is(err, db.ErrProfileNotFound) {
			path = "/profiles"
		}

		handleMutationError(c, s, path, err)

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profileID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if !canManage {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	foreignImports, err := foreignImportsFromForm(c.Request().Form, profile.ForeignImports)
	if err != nil {
		redirectWithMessage(c, s, path, FlashError, err.Error())

		return
	}

	input := db.CreateProfileInput{
		FleetIDs:            profile.FleetIDs,
		Name:                profile.Name,
		Description:         profile.Description,
		ConfigJSON:          profile.ConfigJSON,
		RawNix:              profile.RawNix,
		ForeignImports:      foreignImports,
		ConfigSchemaVersion: profile.ConfigSchemaVersion,
	}

	createdNewRevision, err := db.UpdateProfile(c.Request().Context(), profileID, input)
	if err != nil {
		if errors.Is(err, db.ErrProfileNotFound) {
			path = "/profiles"
		}

		handleMutationError(c, s, path, err)

		return
	}

	message := "Foreign imports updated"
	if createdNewRevision {
		message = "Foreign imports updated and a new profile revision was created"
	}

	redirectWithMessage(c, s, path, FlashSuccess, message)
}

// ForeignImportModulesJSON resolves a flake reference and returns its importable
// nixosModules names as JSON for the Foreign Imports page.
func ForeignImportModulesJSON(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		writeJSONError(c, http.StatusNotFound, "Profile not found")

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profileID)
	if err != nil || !canManage {
		writeJSONError(c, http.StatusForbidden, "Access restricted")

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		writeJSONError(c, http.StatusBadRequest, "Failed to parse request")

		return
	}

	ref := strings.TrimSpace(c.Request().Form.Get("flake_ref"))
	token := strings.TrimSpace(c.Request().Form.Get("flake_token"))

	// "Update" re-resolves an already-stored private flake without re-entering
	// the token: reuse the persisted token when none is supplied.
	if token == "" {
		if profile, profileErr := db.GetProfileForEdit(c.Request().Context(), profileID); profileErr == nil {
			if existing := findForeignImportByRef(profile.ForeignImports, ref); existing != nil && existing.Auth != nil {
				token = existing.Auth.Token
			}
		}
	}

	modules, lockedRev, err := listForeignFlakeModules(c.Request().Context(), ref, token)
	if err != nil {
		writeJSON(c, foreignFlakeModuleResult{Modules: []string{}, Error: err.Error()})

		return
	}

	writeJSON(c, foreignFlakeModuleResult{Modules: modules, Rev: lockedRev})
}

func findForeignImportByRef(imports []db.ForeignImport, ref string) *db.ForeignImport {
	ref = strings.TrimSpace(ref)
	for i := range imports {
		if imports[i].FlakeRef == ref {
			return &imports[i]
		}
	}

	return nil
}

// foreignImportsFromForm reconstructs the foreign import list from the submitted
// form. Each row is identified by an index suffix (e.g. flake_ref_0). Empty
// token fields reuse the previously stored token for that flake reference, so
// tokens are never round-tripped through the browser.
func foreignImportsFromForm(form url.Values, existing []db.ForeignImport) ([]db.ForeignImport, error) {
	indices := map[string]struct{}{}
	for key := range form {
		if strings.HasPrefix(key, "flake_ref_") {
			indices[strings.TrimPrefix(key, "flake_ref_")] = struct{}{}
		}
	}

	imports := make([]db.ForeignImport, 0, len(indices))

	for idx := range indices {
		ref := strings.TrimSpace(form.Get("flake_ref_" + idx))
		if ref == "" {
			continue
		}

		entry := db.ForeignImport{
			FlakeRef: ref,
			Rev:      strings.TrimSpace(form.Get("flake_rev_" + idx)),
			Modules:  form["flake_modules_"+idx],
		}

		token := strings.TrimSpace(form.Get("flake_token_" + idx))
		username := strings.TrimSpace(form.Get("flake_username_" + idx))

		switch {
		case token != "":
			auth, err := foreignImportAuthForRef(ref, username, token)
			if err != nil {
				return nil, err
			}
			entry.Auth = auth
		default:
			if prev := findForeignImportByRef(existing, ref); prev != nil && prev.Auth != nil {
				authCopy := *prev.Auth
				if username != "" {
					authCopy.Username = username
				}
				entry.Auth = &authCopy
			}
		}

		imports = append(imports, entry)
	}

	return imports, nil
}

var (
	foreignFlakeListMu       sync.Mutex
	foreignFlakeListInflight = map[string]chan struct{}{}
)

// pinnedForeignFlakeRef builds a fully-pinned flake reference from a base ref
// and a resolved commit so that builtins.getFlake stays pure.
func pinnedForeignFlakeRef(ref, rev string) (string, error) {
	ref = strings.TrimSpace(ref)
	rev = strings.ToLower(strings.TrimSpace(rev))

	if ref == "" {
		return "", fmt.Errorf("external flake reference is required")
	}

	if !foreignRevLooksResolved(rev) {
		return "", fmt.Errorf("external flake %q is missing a pinned commit", ref)
	}

	switch {
	case strings.HasPrefix(ref, "github:"), strings.HasPrefix(ref, "gitlab:"):
		scheme := ref[:strings.IndexByte(ref, ':')+1]
		rest := ref[len(scheme):]

		// Split off any query string (e.g. ?dir=nixos for subdirectory flakes)
		// before segmenting so the pinned revision is inserted as the owner/repo
		// commit segment, not appended after the query.
		query := ""
		if i := strings.IndexByte(rest, '?'); i >= 0 {
			query = rest[i:]
			rest = rest[:i]
		}

		segments := strings.Split(strings.Trim(rest, "/"), "/")
		if len(segments) < 2 {
			return "", fmt.Errorf("invalid external flake reference %q", ref)
		}

		return fmt.Sprintf("%s%s/%s/%s%s", scheme, segments[0], segments[1], rev, query), nil
	case strings.HasPrefix(ref, "git+https://"):
		separator := "?"
		if strings.Contains(ref, "?") {
			separator = "&"
		}

		return fmt.Sprintf("%s%srev=%s", ref, separator, rev), nil
	default:
		return "", fmt.Errorf("unsupported external flake reference %q", ref)
	}
}

func foreignRevLooksResolved(rev string) bool {
	if len(rev) != 40 {
		return false
	}

	for _, r := range rev {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}

	return true
}

// nixAuthArgs builds the extra `nix` arguments needed to fetch the private
// flakes referenced by the supplied imports. GitHub/GitLab style references use
// `--option access-tokens`; generic git+https references use a temporary netrc
// file (mode 0600) written under scratchDir and removed by the returned cleanup.
func nixAuthArgs(imports []db.ForeignImport, scratchDir string) ([]string, func(), error) {
	noop := func() {}

	accessTokens := make([]string, 0)
	netrcLines := make([]string, 0)

	for _, item := range imports {
		if item.Auth == nil || strings.TrimSpace(item.Auth.Token) == "" {
			continue
		}

		switch item.Auth.Type {
		case db.ForeignImportAuthGitHubToken:
			accessTokens = append(accessTokens, fmt.Sprintf("%s=%s", item.Auth.Host, item.Auth.Token))
		case db.ForeignImportAuthNetrcPassword:
			username := strings.TrimSpace(item.Auth.Username)
			if username == "" {
				username = "git"
			}
			netrcLines = append(netrcLines, fmt.Sprintf("machine %s login %s password %s", item.Auth.Host, username, item.Auth.Token))
		}
	}

	args := make([]string, 0, 4)

	if len(accessTokens) > 0 {
		args = append(args, "--option", "access-tokens", strings.Join(accessTokens, " "))
	}

	if len(netrcLines) == 0 {
		return args, noop, nil
	}

	netrcPath := filepath.Join(scratchDir, foreignNetrcFileName)
	content := strings.Join(netrcLines, "\n") + "\n"
	if err := os.WriteFile(netrcPath, []byte(content), 0o600); err != nil {
		return nil, noop, fmt.Errorf("failed to write flake credentials file: %w", err)
	}

	cleanup := func() {
		if err := os.Remove(netrcPath); err != nil && !os.IsNotExist(err) {
			logger.Warn("failed to remove temporary flake credentials file", "error", err)
		}
	}

	args = append(args, "--netrc-file", netrcPath)

	return args, cleanup, nil
}

// foreignImportAuthForRef derives the auth descriptor for a freshly entered
// flake reference + token. github:/gitlab: references authenticate via Nix
// access-tokens; git+https:// references authenticate via netrc.
func foreignImportAuthForRef(ref, username, token string) (*db.ForeignImportAuth, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil
	}

	host, authType, err := foreignFlakeAuthTarget(ref)
	if err != nil {
		return nil, err
	}

	return &db.ForeignImportAuth{
		Type:     authType,
		Host:     host,
		Username: strings.TrimSpace(username),
		Token:    token,
	}, nil
}

// foreignFlakeAuthTarget returns the host and auth mechanism for a flake ref.
func foreignFlakeAuthTarget(ref string) (string, string, error) {
	ref = strings.TrimSpace(ref)

	switch {
	case strings.HasPrefix(ref, "github:"):
		return "github.com", db.ForeignImportAuthGitHubToken, nil
	case strings.HasPrefix(ref, "gitlab:"):
		return "gitlab.com", db.ForeignImportAuthGitHubToken, nil
	case strings.HasPrefix(ref, "git+https://"):
		rest := strings.TrimPrefix(ref, "git+https://")
		rest = strings.TrimPrefix(rest, "//")
		host := rest
		if idx := strings.IndexAny(rest, "/?#"); idx >= 0 {
			host = rest[:idx]
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		host = strings.TrimSpace(host)
		if host == "" {
			return "", "", fmt.Errorf("invalid external flake reference %q", ref)
		}

		return strings.ToLower(host), db.ForeignImportAuthNetrcPassword, nil
	default:
		return "", "", fmt.Errorf("unsupported external flake reference %q", ref)
	}
}

// listForeignFlakeModules resolves the flake to a pinned commit and returns the
// attribute names of its `nixosModules` output. A token may be supplied for
// private flakes; it is never logged.
func listForeignFlakeModules(ctx context.Context, ref, token string) (modules []string, lockedRev string, err error) {
	ref = strings.TrimSpace(ref)
	if err := db.ValidateForeignFlakeRef(ref); err != nil {
		return nil, "", err
	}

	auth, err := foreignImportAuthForRef(ref, "", token)
	if err != nil {
		return nil, "", err
	}

	var authImports []db.ForeignImport
	if auth != nil {
		authImports = []db.ForeignImport{{Auth: auth}}
	}

	scratch, err := os.MkdirTemp("", "fleeti-flake-list-*")
	if err != nil {
		return nil, "", fmt.Errorf("failed to prepare flake listing workspace: %w", err)
	}
	defer func() {
		if removeErr := os.RemoveAll(scratch); removeErr != nil {
			logger.Warn("failed to clean flake listing workspace", "error", removeErr)
		}
	}()

	authArgs, cleanup, err := nixAuthArgs(authImports, scratch)
	if err != nil {
		return nil, "", err
	}
	defer cleanup()

	// Single-flight by reference so repeated clicks don't spawn parallel evals.
	release := acquireForeignFlakeListSlot(ref)
	defer release()

	lockedRev, err = resolveForeignFlakeRev(ctx, ref, authArgs)
	if err != nil {
		return nil, "", err
	}

	pinnedRef, err := pinnedForeignFlakeRef(ref, lockedRev)
	if err != nil {
		return nil, "", err
	}

	modules, err = evalForeignFlakeModules(ctx, pinnedRef, authArgs)
	if err != nil {
		return nil, "", err
	}

	return modules, lockedRev, nil
}

func acquireForeignFlakeListSlot(ref string) func() {
	for {
		foreignFlakeListMu.Lock()
		existing, ok := foreignFlakeListInflight[ref]
		if !ok {
			done := make(chan struct{})
			foreignFlakeListInflight[ref] = done
			foreignFlakeListMu.Unlock()

			return func() {
				foreignFlakeListMu.Lock()
				delete(foreignFlakeListInflight, ref)
				close(done)
				foreignFlakeListMu.Unlock()
			}
		}
		foreignFlakeListMu.Unlock()

		<-existing
	}
}

func resolveForeignFlakeRev(ctx context.Context, ref string, authArgs []string) (string, error) {
	evalCtx, cancel := context.WithTimeout(ctx, foreignFlakeCommandTimeout)
	defer cancel()

	args := []string{"flake", "metadata", "--json", "--no-write-lock-file"}
	args = append(args, authArgs...)
	args = append(args, ref)

	cmd := exec.CommandContext(evalCtx, "nix", args...)

	output, err := cmd.Output()
	if err != nil {
		return "", foreignFlakeCommandError(evalCtx, err, "resolve")
	}

	var metadata struct {
		Locked struct {
			Rev string `json:"rev"`
		} `json:"locked"`
	}
	if err := json.Unmarshal(output, &metadata); err != nil {
		return "", fmt.Errorf("failed to parse flake metadata: %w", err)
	}

	rev := strings.ToLower(strings.TrimSpace(metadata.Locked.Rev))
	if !foreignRevLooksResolved(rev) {
		return "", fmt.Errorf("flake did not resolve to a commit revision")
	}

	return rev, nil
}

func evalForeignFlakeModules(ctx context.Context, pinnedRef string, authArgs []string) ([]string, error) {
	evalCtx, cancel := context.WithTimeout(ctx, foreignFlakeCommandTimeout)
	defer cancel()

	expr := fmt.Sprintf(`let f = builtins.getFlake "%s"; in if f ? nixosModules then builtins.attrNames f.nixosModules else []`, escapeNixString(pinnedRef))

	args := []string{"eval", "--json", "--no-write-lock-file"}
	args = append(args, authArgs...)
	args = append(args, "--expr", expr)

	cmd := exec.CommandContext(evalCtx, "nix", args...)

	output, err := cmd.Output()
	if err != nil {
		return nil, foreignFlakeCommandError(evalCtx, err, "evaluate")
	}

	var names []string
	if err := json.Unmarshal(output, &names); err != nil {
		return nil, fmt.Errorf("failed to parse flake modules: %w", err)
	}

	cleaned := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		cleaned = append(cleaned, name)
	}

	sort.Strings(cleaned)

	return cleaned, nil
}

// foreignFlakeCommandError converts a nix command failure into a user-facing
// message, collapsing the common authentication/not-found cases. The token is
// never included.
func foreignFlakeCommandError(ctx context.Context, err error, action string) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("flake %s timed out", action)
	}

	stderr := ""
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = strings.ToLower(string(exitErr.Stderr))
	}

	if strings.Contains(stderr, "401") || strings.Contains(stderr, "403") ||
		strings.Contains(stderr, "404") || strings.Contains(stderr, "authentication") ||
		strings.Contains(stderr, "could not read username") || strings.Contains(stderr, "permission denied") {
		return fmt.Errorf("authentication failed or flake not found")
	}

	messages := cleanNixEvalErrors(stderr)
	if len(messages) > 0 && messages[0] != "" {
		return fmt.Errorf("failed to %s flake: %s", action, messages[0])
	}

	return fmt.Errorf("failed to %s flake", action)
}
