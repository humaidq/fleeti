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
	"path/filepath"
	"sort"
	"strings"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

const profileKernelMultipartMemoryLimit = 32 << 20

func writePlain(c flamego.Context, value string) {
	if _, err := c.ResponseWriter().Write([]byte(value)); err != nil {
		logger.Error("failed to write response", "error", err)
	}
}

// Connectivity returns a tiny endpoint for online checks.
func Connectivity(c flamego.Context) {
	writePlain(c, "1")
}

// Healthz returns a simple health endpoint.
func Healthz(c flamego.Context) {
	writePlain(c, "ok")
}

// Dashboard renders the landing page with fleet stats.
func Dashboard(c flamego.Context, t template.Template, data template.Data) {
	setPage(data, "Dashboard")
	data["IsDashboard"] = true

	counts, err := db.GetDashboardCounts(c.Request().Context())
	if err != nil {
		logger.Error("failed to load dashboard counts", "error", err)
		setPageErrorFlash(data, "Failed to load dashboard stats")
		counts = db.DashboardCounts{}
	}

	data["Counts"] = counts

	t.HTML(http.StatusOK, "dashboard")
}

// FleetsPage renders fleets list and create form.
func FleetsPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Fleets")
	data["IsFleets"] = true

	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		logger.Error("failed to resolve session user for fleets", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")
		data["Fleets"] = []db.Fleet{}

		t.HTML(http.StatusOK, "fleets")

		return
	}

	fleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list fleets", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")
		fleets = []db.Fleet{}
	}

	data["Fleets"] = fleets

	t.HTML(http.StatusOK, "fleets")
}

// FleetPage renders fleet summary and management actions.
func FleetPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Fleet Summary")
	data["IsFleets"] = true

	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		redirectWithMessage(c, s, "/fleets", FlashError, "Access restricted")

		return
	}

	fleetID := strings.TrimSpace(c.Param("id"))
	if fleetID == "" {
		redirectWithMessage(c, s, "/fleets", FlashError, "Fleet not found")

		return
	}

	fleet, err := db.GetFleetByID(c.Request().Context(), fleetID)
	if err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	canView, err := db.UserCanViewFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	if !canView {
		redirectWithMessage(c, s, "/fleets", FlashError, "Access restricted")

		return
	}

	canManage, err := db.UserCanManageFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	data["Fleet"] = fleet
	data["CanManageFleet"] = canManage
	data["FleetNavActive"] = "summary"
	setBreadcrumbs(data, fleetSectionBreadcrumbs(fleet, ""))

	t.HTML(http.StatusOK, "fleet_view")
}

// FleetAccessPage renders fleet access controls.
func FleetAccessPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Fleet Access")
	data["IsFleets"] = true

	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		redirectWithMessage(c, s, "/fleets", FlashError, "Access restricted")

		return
	}

	fleetID := strings.TrimSpace(c.Param("id"))
	if fleetID == "" {
		redirectWithMessage(c, s, "/fleets", FlashError, "Fleet not found")

		return
	}

	fleet, err := db.GetFleetByID(c.Request().Context(), fleetID)
	if err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	canManage, err := db.UserCanManageFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	if !canManage {
		redirectWithMessage(c, s, fleetViewPath(fleetID), FlashError, "Access restricted")

		return
	}

	fleetViewers := []db.ViewerUser{}
	fleetUsersLoaded := false

	loadedFleetUsers, err := db.ListFleetViewerUsers(c.Request().Context(), fleetID)
	if err != nil {
		logger.Error("failed to list fleet users", "fleet_id", fleetID, "error", err)
		setPageErrorFlash(data, "Failed to load fleet users")
	} else {
		fleetViewers = loadedFleetUsers
		fleetUsersLoaded = true
	}

	viewerUsers := []db.ViewerUser{}
	if fleetUsersLoaded {
		allUsers, err := db.ListViewerUsers(c.Request().Context())
		if err != nil {
			logger.Error("failed to list users for fleet access", "fleet_id", fleetID, "error", err)
			setPageErrorFlash(data, "Failed to load user access controls")
		} else {
			viewerUsers = filterAccessCandidateUsers(allUsers, fleetViewers)
		}
	}

	data["ViewerUsers"] = viewerUsers
	data["FleetViewers"] = fleetViewers

	data["Fleet"] = fleet
	data["CanManageFleet"] = true
	data["FleetNavActive"] = "access"
	setBreadcrumbs(data, fleetSectionBreadcrumbs(fleet, "Access"))

	t.HTML(http.StatusOK, "fleet_access")
}

// CreateFleet handles fleet creation.
func CreateFleet(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/fleets", db.ErrAccessDenied)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, "/fleets", FlashError, "Failed to parse form")

		return
	}

	name := strings.TrimSpace(c.Request().Form.Get("name"))
	description := strings.TrimSpace(c.Request().Form.Get("description"))

	if err := db.CreateFleet(c.Request().Context(), name, description, user.ID.String()); err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	redirectWithMessage(c, s, "/fleets", FlashSuccess, "Fleet created")
}

// UpdateFleet handles fleet metadata updates.
func UpdateFleet(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/fleets", db.ErrAccessDenied)

		return
	}

	fleetID := strings.TrimSpace(c.Param("id"))
	path := fleetViewPath(fleetID)
	if path == "/fleets/" {
		path = "/fleets"
	}

	if fleetID == "" {
		handleMutationError(c, s, path, db.ErrFleetRequired)

		return
	}

	canManage, err := db.UserCanManageFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	name := strings.TrimSpace(c.Request().Form.Get("name"))
	description := strings.TrimSpace(c.Request().Form.Get("description"))

	if err := db.UpdateFleet(c.Request().Context(), fleetID, name, description); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Fleet updated")
}

// DeleteFleet permanently deletes a fleet and all dependent records.
func DeleteFleet(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/fleets", db.ErrAccessDenied)

		return
	}

	fleetID := strings.TrimSpace(c.Param("id"))
	if fleetID == "" {
		handleMutationError(c, s, "/fleets", db.ErrFleetRequired)

		return
	}

	canManage, err := db.UserCanManageFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, "/fleets", db.ErrAccessDenied)

		return
	}

	if err := deleteFleetCascade(c.Request().Context(), fleetID); err != nil {
		handleMutationError(c, s, "/fleets", err)

		return
	}

	redirectWithMessage(c, s, "/fleets", FlashSuccess, "Fleet permanently deleted")
}

// AddFleetUser grants a user full access to a fleet.
func AddFleetUser(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/fleets", db.ErrAccessDenied)

		return
	}

	fleetID := strings.TrimSpace(c.Param("id"))
	path := fleetAccessPath(fleetID)
	if path == "/fleets//access" {
		path = "/fleets"
	}

	if fleetID == "" {
		handleMutationError(c, s, path, db.ErrFleetRequired)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	canManage, err := db.UserCanManageFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	accessUserID := strings.TrimSpace(c.Request().Form.Get("user_id"))
	if err := db.GrantFleetViewer(c.Request().Context(), fleetID, accessUserID, user.ID.String()); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Fleet user added")
}

// RemoveFleetUser revokes a user's fleet access.
func RemoveFleetUser(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/fleets", db.ErrAccessDenied)

		return
	}

	fleetID := strings.TrimSpace(c.Param("id"))
	path := fleetAccessPath(fleetID)
	if path == "/fleets//access" {
		path = "/fleets"
	}

	if fleetID == "" {
		handleMutationError(c, s, path, db.ErrFleetRequired)

		return
	}

	canManage, err := db.UserCanManageFleet(c.Request().Context(), user.ID.String(), user.IsAdmin, fleetID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	accessUserID := strings.TrimSpace(c.Param("user_id"))
	if err := db.RevokeFleetViewer(c.Request().Context(), fleetID, accessUserID); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Fleet user removed")
}

// ProfilesPage renders the profiles list.
func ProfilesPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profiles")
	data["IsProfiles"] = true

	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		logger.Error("failed to resolve session user for profiles", "error", err)
		setPageErrorFlash(data, "Failed to load profiles")
		data["Profiles"] = []db.Profile{}

		t.HTML(http.StatusOK, "profiles")

		return
	}

	profiles, err := db.ListProfilesForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list profiles", "error", err)
		setPageErrorFlash(data, "Failed to load profiles")
		profiles = []db.Profile{}
	}

	data["Profiles"] = profiles

	t.HTML(http.StatusOK, "profiles")
}

// NewProfilePage renders the profile create form.
func NewProfilePage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Create Profile")
	data["IsProfiles"] = true

	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		logger.Error("failed to resolve session user for profile form", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")
		data["Fleets"] = []db.Fleet{}

		t.HTML(http.StatusOK, "profile_new")

		return
	}

	fleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list fleets for profile form", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")

		fleets = []db.Fleet{}
	}

	data["Fleets"] = manageableFleetsForUser(user, fleets)

	t.HTML(http.StatusOK, "profile_new")
}

// AddProfilePackage handles package additions for profile package management.
func AddProfilePackage(c flamego.Context, s session.Session) {
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

	path := profilePackagesPath(profileID)

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	path = profilePackagesPathWithQuery(profileID, c.Request().Form.Get("q"))

	packageName := strings.TrimSpace(c.Request().Form.Get("package"))
	if packageName == "" {
		redirectWithMessage(c, s, path, FlashError, "Package name is required")

		return
	}

	if _, err := packageNameToNixExpression(packageName); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Package name is invalid")

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

	packages, err := packagesFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	for _, existing := range packages {
		if existing == packageName {
			redirectWithMessage(c, s, path, FlashInfo, "Package already added")

			return
		}
	}

	packages = append(packages, packageName)
	configJSON, err := profileConfigWithPackages(profile.ConfigJSON, packages)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	input := db.CreateProfileInput{
		FleetIDs:    profile.FleetIDs,
		Name:        profile.Name,
		Description: profile.Description,
		ConfigJSON:  configJSON,
		RawNix:      profile.RawNix,
	}

	if _, err := db.UpdateProfile(c.Request().Context(), profileID, input); err != nil {
		if errors.Is(err, db.ErrProfileNotFound) {
			path = "/profiles"
		}

		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Package added")
}

// RemoveProfilePackage handles package removal for profile package management.
func RemoveProfilePackage(c flamego.Context, s session.Session) {
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

	path := profilePackagesPath(profileID)

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	path = profilePackagesPathWithQuery(profileID, c.Request().Form.Get("q"))

	packageName := strings.TrimSpace(c.Request().Form.Get("package"))
	if packageName == "" {
		redirectWithMessage(c, s, path, FlashError, "Package name is required")

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

	packages, err := packagesFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	filtered := make([]string, 0, len(packages))
	removed := false

	for _, existing := range packages {
		if existing == packageName {
			removed = true

			continue
		}

		filtered = append(filtered, existing)
	}

	if !removed {
		redirectWithMessage(c, s, path, FlashInfo, "Package not found")

		return
	}

	configJSON, err := profileConfigWithPackages(profile.ConfigJSON, filtered)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	input := db.CreateProfileInput{
		FleetIDs:    profile.FleetIDs,
		Name:        profile.Name,
		Description: profile.Description,
		ConfigJSON:  configJSON,
		RawNix:      profile.RawNix,
	}

	if _, err := db.UpdateProfile(c.Request().Context(), profileID, input); err != nil {
		if errors.Is(err, db.ErrProfileNotFound) {
			path = "/profiles"
		}

		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Package removed")
}

// CreateProfile handles profile creation.
func CreateProfile(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles/new", db.ErrAccessDenied)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, "/profiles/new", FlashError, "Failed to parse form")

		return
	}

	input := db.CreateProfileInput{
		FleetIDs:    selectedFleetIDsFromForm(c.Request().Form),
		Name:        strings.TrimSpace(c.Request().Form.Get("name")),
		Description: strings.TrimSpace(c.Request().Form.Get("description")),
		ConfigJSON:  `{"packages":[]}`,
	}

	if err := ensureUserCanManageFleetIDs(c.Request().Context(), user, input.FleetIDs); err != nil {
		handleMutationError(c, s, "/profiles/new", err)

		return
	}

	if err := db.CreateProfile(c.Request().Context(), input, user.ID.String()); err != nil {
		handleMutationError(c, s, "/profiles/new", err)

		return
	}

	redirectWithMessage(c, s, "/profiles", FlashSuccess, "Profile created")
}

// ProfilePage renders profile summary.
func ProfilePage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Summary")
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

	canView, err := db.UserCanViewProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profile.ID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canView {
		redirectWithMessage(c, s, "/profiles", FlashError, "Access restricted")

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profile.ID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	packages, err := packagesFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		logger.Warn("failed to parse profile packages", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, mutationErrorMessage(err))
		packages = []string{}
	}

	kernelConfig, err := profileKernelConfigFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		logger.Warn("failed to parse profile kernel config", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, mutationErrorMessage(err))
		kernelConfig = ProfileKernelConfig{}
	}

	data["Profile"] = profile
	data["Packages"] = packages
	data["PackageCount"] = len(packages)
	data["KernelSummary"] = profileKernelSummary(kernelConfig)
	data["HasRawNix"] = strings.TrimSpace(profile.RawNix) != ""
	data["ProfileNavActive"] = "summary"
	data["CanManageProfile"] = canManage
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, ""))

	t.HTML(http.StatusOK, "profile_view")
}

// EditProfilePage renders profile metadata settings.
func EditProfilePage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Settings")
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

	fleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list fleets for profile settings form", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load fleets")
		fleets = []db.Fleet{}
	}

	data["Profile"] = profile
	data["Fleets"] = manageableFleetsForUser(user, fleets)
	data["SelectedFleetIDs"] = fleetSelectionMap(profile.FleetIDs)
	data["ProfileNavActive"] = "settings"
	data["CanManageProfile"] = true

	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Settings"))

	t.HTML(http.StatusOK, "profile_edit")
}

// ProfileAccessPage renders profile access controls.
func ProfileAccessPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Access")
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
		redirectWithMessage(c, s, profileViewPath(profileID), FlashError, "Access restricted")

		return
	}

	profileViewers := []db.ViewerUser{}
	profileUsersLoaded := false

	loadedProfileUsers, err := db.ListProfileViewerUsers(c.Request().Context(), profileID)
	if err != nil {
		logger.Error("failed to list profile users", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile users")
	} else {
		profileViewers = loadedProfileUsers
		profileUsersLoaded = true
	}

	viewerUsers := []db.ViewerUser{}
	if profileUsersLoaded {
		allUsers, err := db.ListViewerUsers(c.Request().Context())
		if err != nil {
			logger.Error("failed to list users for profile access", "profile_id", profileID, "error", err)
			setPageErrorFlash(data, "Failed to load user access controls")
		} else {
			viewerUsers = filterAccessCandidateUsers(allUsers, profileViewers)
		}
	}

	data["ViewerUsers"] = viewerUsers
	data["ProfileViewers"] = profileViewers

	data["Profile"] = profile
	data["ProfileNavActive"] = "access"
	data["CanManageProfile"] = true
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Access"))

	t.HTML(http.StatusOK, "profile_access")
}

// ProfilePackagesPage renders package search and package assignments for a profile.
func ProfilePackagesPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Packages")
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

	packages, err := packagesFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		logger.Warn("failed to parse profile packages", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, mutationErrorMessage(err))
		packages = []string{}
	}

	data["Profile"] = profile
	data["Packages"] = packages
	data["ProfileNavActive"] = "packages"
	data["CanManageProfile"] = true
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Packages"))

	searchQuery := strings.TrimSpace(c.Request().URL.Query().Get("q"))
	data["PackageSearchQuery"] = searchQuery

	searchResults := []PackageSearchResult{}
	if searchQuery != "" {
		existingPackages := make(map[string]struct{}, len(packages))
		for _, item := range packages {
			existingPackages[item] = struct{}{}
		}

		results, total, err := searchNixPackages(c.Request().Context(), searchQuery, existingPackages)
		if err != nil {
			logger.Warn("package search failed", "profile_id", profileID, "query", searchQuery, "error", err)
			data["PackageSearchError"] = "Package search failed: " + err.Error()
		} else {
			searchResults = results
			data["PackageSearchTotal"] = total
			data["PackageSearchTruncated"] = total > len(results)
		}
	}

	data["PackageSearchResults"] = searchResults

	t.HTML(http.StatusOK, "profile_packages")
}

// ProfileKernelPage renders kernel settings for a profile.
func ProfileKernelPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Kernel")
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

	kernelConfig, err := profileKernelConfigFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		logger.Warn("failed to parse profile kernel config", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, mutationErrorMessage(err))
		kernelConfig = ProfileKernelConfig{}
	}

	kernelOptions, err := listAvailableKernelOptions(c.Request().Context())
	if err != nil {
		logger.Warn("failed to load available kernels", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load available kernels")
		kernelOptions = []KernelOption{}
	}

	kernelOptions = ensureKernelOption(kernelOptions, kernelConfig.Attr)

	data["Profile"] = profile
	data["KernelOptions"] = kernelOptions
	data["SelectedKernelAttr"] = kernelConfig.Attr
	data["KernelSourceOverrideEnabled"] = kernelConfig.SourceOverride.Enabled
	data["KernelSourceURL"] = kernelConfig.SourceOverride.URL
	data["KernelSourceRef"] = kernelConfig.SourceOverride.Ref
	data["KernelSourceRev"] = kernelConfig.SourceOverride.Rev
	data["KernelSourcePatches"] = kernelConfig.SourceOverride.Patches
	data["ProfileNavActive"] = "kernel"
	data["CanManageProfile"] = true
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Kernel"))

	t.HTML(http.StatusOK, "profile_kernel")
}

// ProfileRawNixPage renders raw nix settings for a profile.
func ProfileRawNixPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Raw Nix")
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
	data["ProfileNavActive"] = "raw_nix"
	data["CanManageProfile"] = true
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Raw Nix"))

	t.HTML(http.StatusOK, "profile_raw_nix")
}

// ProfileDeploymentsPage renders build/release/rollout workflows for a profile.
func ProfileDeploymentsPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Profile Deployments")
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

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	builds := []db.Build{}
	allBuilds, err := db.ListBuilds(c.Request().Context())
	if err != nil {
		logger.Error("failed to list builds for profile deployments", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile builds")
	} else {
		builds = filterBuildsByProfileID(allBuilds, profile.ID)
	}

	buildIDs := buildIDSet(builds)

	releases := []db.Release{}
	allReleases, err := db.ListReleases(c.Request().Context())
	if err != nil {
		logger.Error("failed to list releases for profile deployments", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile releases")
	} else {
		releases = filterReleasesByBuildIDs(allReleases, buildIDs)
	}

	releaseIDs := releaseIDSet(releases)

	rollouts := []db.Rollout{}
	allRollouts, err := db.ListRollouts(c.Request().Context())
	if err != nil {
		logger.Error("failed to list rollouts for profile deployments", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile rollouts")
	} else {
		rollouts = filterRolloutsByReleaseIDs(allRollouts, releaseIDs)
	}

	fleets := []db.Fleet{}
	allFleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list fleets for profile deployments", "profile_id", profileID, "error", err)
		setPageErrorFlash(data, "Failed to load profile fleets")
	} else {
		fleets = fleetsAssignedToProfile(profile, manageableFleetsForUser(user, allFleets))
	}

	data["Profile"] = profile
	data["CanManageProfile"] = canManage
	data["ProfileNavActive"] = "deployments"
	data["ProfileBuilds"] = builds
	data["ProfileReleases"] = releases
	data["ProfileRollouts"] = rollouts
	data["DeploymentFleets"] = fleets
	setBreadcrumbs(data, profileSectionBreadcrumbs(profile, "Deployments"))

	t.HTML(http.StatusOK, "profile_deployments")
}

// UpdateProfile handles profile settings updates.
func UpdateProfile(c flamego.Context, s session.Session) {
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

	path := profileEditPath(profileID)

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

	input := db.CreateProfileInput{
		FleetIDs:            selectedFleetIDsFromForm(c.Request().Form),
		Name:                strings.TrimSpace(c.Request().Form.Get("name")),
		Description:         strings.TrimSpace(c.Request().Form.Get("description")),
		ConfigJSON:          profile.ConfigJSON,
		RawNix:              profile.RawNix,
		ConfigSchemaVersion: profile.ConfigSchemaVersion,
	}

	if err := ensureUserCanManageFleetIDs(c.Request().Context(), user, input.FleetIDs); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	createdNewRevision, err := db.UpdateProfile(c.Request().Context(), profileID, input)
	if err != nil {
		if errors.Is(err, db.ErrProfileNotFound) {
			path = "/profiles"
		}

		handleMutationError(c, s, path, err)

		return
	}

	message := "Profile settings updated"
	if createdNewRevision {
		message = "Profile settings updated with new revision"
	}

	redirectWithMessage(c, s, path, FlashSuccess, message)
}

// AddProfileUser grants a user full access to a profile.
func AddProfileUser(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	path := profileAccessPath(profileID)
	if path == "/profiles//access" {
		path = "/profiles"
	}

	if profileID == "" {
		handleMutationError(c, s, path, db.ErrProfileNotFound)

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profileID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	accessUserID := strings.TrimSpace(c.Request().Form.Get("user_id"))
	if err := db.GrantProfileViewer(c.Request().Context(), profileID, accessUserID, user.ID.String()); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Profile user added")
}

// RemoveProfileUser revokes a user's profile access.
func RemoveProfileUser(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	path := profileAccessPath(profileID)
	if path == "/profiles//access" {
		path = "/profiles"
	}

	if profileID == "" {
		handleMutationError(c, s, path, db.ErrProfileNotFound)

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profileID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	accessUserID := strings.TrimSpace(c.Param("user_id"))
	if err := db.RevokeProfileViewer(c.Request().Context(), profileID, accessUserID); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Profile user removed")
}

// UpdateProfileKernel handles profile kernel updates.
func UpdateProfileKernel(c flamego.Context, s session.Session) {
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

	path := profileKernelPath(profileID)

	if err := c.Request().ParseMultipartForm(profileKernelMultipartMemoryLimit); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	if c.Request().MultipartForm != nil {
		defer func() {
			_ = c.Request().MultipartForm.RemoveAll()
		}()
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

	existingKernelConfig, err := profileKernelConfigFromProfileConfig(profile.ConfigJSON)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	kernelConfig := profileKernelConfigFromForm(c.Request().Form)
	patches, err := profileKernelSourcePatchesFromMultipartForm(c.Request().Form, c.Request().MultipartForm, existingKernelConfig.SourceOverride.Patches)
	if err != nil {
		redirectWithMessage(c, s, path, FlashError, err.Error())

		return
	}

	kernelConfig.SourceOverride.Patches = patches
	kernelAttrs := map[string]struct{}{}
	if kernelConfig.Attr != "" {
		kernelOptions, err := listAvailableKernelOptions(c.Request().Context())
		if err != nil {
			logger.Warn("failed to load available kernels for validation", "profile_id", profileID, "error", err)
			redirectWithMessage(c, s, path, FlashError, "Failed to validate selected kernel against pinned nixpkgs")

			return
		}

		kernelAttrs = kernelOptionSet(kernelOptions)
	}

	if err := validateProfileKernelConfig(kernelConfig, kernelAttrs); err != nil {
		redirectWithMessage(c, s, path, FlashError, err.Error())

		return
	}

	configJSON, err := profileConfigWithKernel(profile.ConfigJSON, kernelConfig)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	input := db.CreateProfileInput{
		FleetIDs:            profile.FleetIDs,
		Name:                profile.Name,
		Description:         profile.Description,
		ConfigJSON:          configJSON,
		RawNix:              profile.RawNix,
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

	message := "Kernel settings updated"
	if createdNewRevision {
		message = "Kernel settings updated with new revision"
	}

	redirectWithMessage(c, s, path, FlashSuccess, message)
}

// UpdateProfileRawNix handles profile raw nix updates.
func UpdateProfileRawNix(c flamego.Context, s session.Session) {
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

	path := profileRawNixPath(profileID)

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

	input := db.CreateProfileInput{
		FleetIDs:            profile.FleetIDs,
		Name:                profile.Name,
		Description:         profile.Description,
		ConfigJSON:          profile.ConfigJSON,
		RawNix:              strings.TrimSpace(c.Request().Form.Get("raw_nix")),
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

	message := "Raw Nix updated"
	if createdNewRevision {
		message = "Raw Nix updated with new revision"
	}

	redirectWithMessage(c, s, path, FlashSuccess, message)
}

// DeleteProfile permanently deletes a profile and dependent builds/releases/rollouts.
func DeleteProfile(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	canManage, err := db.UserCanManageProfile(c.Request().Context(), user.ID.String(), user.IsAdmin, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	if err := deleteProfileCascade(c.Request().Context(), profileID); err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	redirectWithMessage(c, s, "/profiles", FlashSuccess, "Profile permanently deleted")
}

// ProfileBuildPage renders details for a profile-scoped build.
func ProfileBuildPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Build Details")
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

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	buildID := strings.TrimSpace(c.Param("build_id"))
	path := profileDeploymentsBuildsPath(profileID)
	if buildID == "" {
		redirectWithMessage(c, s, path, FlashError, "Build not found")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Build not found")

		return
	}

	updateArtifactLinks, err := listBuildUpdateArtifactLinks(build.ID)
	if err != nil {
		logger.Warn("failed to list profile build update artifacts", "profile_id", profileID, "build_id", build.ID, "error", err)
		setPageErrorFlash(data, "Failed to load update artifacts")
		updateArtifactLinks = []BuildArtifactLink{}
	}

	installerArtifactLinks, err := listBuildInstallerArtifactLinks(build.ID)
	if err != nil {
		logger.Warn("failed to list profile build installer artifacts", "profile_id", profileID, "build_id", build.ID, "error", err)
		setPageErrorFlash(data, "Failed to load installer artifacts")
		installerArtifactLinks = []BuildArtifactLink{}
	}

	buildName := strings.TrimSpace(build.Version)
	if buildName == "" {
		buildName = "Build"
	}

	data["Build"] = build
	data["CanManageBuild"] = canManage
	data["BuildBackPath"] = path
	data["BuildDeletePath"] = profileBuildDeletePath(profileID, buildID)
	data["UpdateArtifactLinks"] = updateArtifactLinks
	data["InstallerArtifactLinks"] = installerArtifactLinks
	setBreadcrumbs(data, profileDeploymentsBreadcrumbs(profile, buildName))

	t.HTML(http.StatusOK, "build_view")
}

// CreateProfileBuild handles profile-scoped build creation.
func CreateProfileBuild(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	path := profileDeploymentsBuildsPath(profileID)

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	fleetID := strings.TrimSpace(c.Request().Form.Get("fleet_id"))
	if err := ensureUserCanManageFleetIDs(c.Request().Context(), user, []string{fleetID}); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	input := db.CreateBuildInput{
		ProfileID: profile.ID,
		FleetID:   fleetID,
		Version:   strings.TrimSpace(c.Request().Form.Get("version")),
	}

	buildID, err := db.CreateBuild(c.Request().Context(), input)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	queueBuildExecution(buildID, input.Version)

	redirectWithMessage(c, s, path, FlashSuccess, "Build queued")
}

// DeleteProfileBuild permanently deletes a profile-scoped build.
func DeleteProfileBuild(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	path := profileDeploymentsBuildsPath(profileID)
	buildID := strings.TrimSpace(c.Param("build_id"))
	if buildID == "" {
		handleMutationError(c, s, path, db.ErrBuildRequired)

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Build not found")

		return
	}

	if err := deleteBuildCascade(c.Request().Context(), buildID); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Build permanently deleted")
}

// ProfileReleasePage renders details for a profile-scoped release.
func ProfileReleasePage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Release Summary")
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

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	releaseID := strings.TrimSpace(c.Param("release_id"))
	path := profileDeploymentsReleasesPath(profileID)
	if releaseID == "" {
		redirectWithMessage(c, s, path, FlashError, "Release not found")

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), releaseID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Release not found")

		return
	}

	rollouts, err := db.ListRollouts(c.Request().Context())
	if err != nil {
		logger.Error("failed to list rollouts for profile release summary", "profile_id", profileID, "release_id", releaseID, "error", err)
		setPageErrorFlash(data, "Failed to load release rollouts")
		rollouts = []db.Rollout{}
	}

	relatedRollouts := make([]db.Rollout, 0)
	for _, rollout := range rollouts {
		if rollout.ReleaseID != release.ID {
			continue
		}

		relatedRollouts = append(relatedRollouts, rollout)
	}

	releaseName := strings.TrimSpace(release.Version)
	if releaseName == "" {
		releaseName = "Release"
	}

	data["Release"] = release
	data["RelatedRollouts"] = relatedRollouts
	data["CanManageRelease"] = canManage
	data["ReleaseDeletePath"] = profileReleaseDeletePath(profileID, releaseID)
	data["ReleaseBackPath"] = path
	data["ReleaseRolloutsPath"] = profileDeploymentsRolloutsPath(profileID)
	data["ReleaseBuildPath"] = profileBuildPath(profileID, release.BuildID)
	data["ReleaseRolloutPathPrefix"] = profileRolloutsPathPrefix(profileID)
	setBreadcrumbs(data, profileDeploymentsBreadcrumbs(profile, releaseName))

	t.HTML(http.StatusOK, "release_view")
}

// CreateProfileRelease handles profile-scoped release creation.
func CreateProfileRelease(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	path := profileDeploymentsReleasesPath(profileID)

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	input := db.CreateReleaseInput{
		BuildID: strings.TrimSpace(c.Request().Form.Get("build_id")),
		Channel: strings.TrimSpace(c.Request().Form.Get("channel")),
		Version: strings.TrimSpace(c.Request().Form.Get("version")),
		Notes:   strings.TrimSpace(c.Request().Form.Get("notes")),
	}

	build, err := db.GetBuildByID(c.Request().Context(), input.BuildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Build not found")

		return
	}

	if input.Version == "" {
		input.Version = build.Version
	}

	if err := db.CreateRelease(c.Request().Context(), input); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Release created")
}

// DeleteProfileRelease permanently deletes a profile-scoped release.
func DeleteProfileRelease(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	path := profileDeploymentsReleasesPath(profileID)
	releaseID := strings.TrimSpace(c.Param("release_id"))
	if releaseID == "" {
		handleMutationError(c, s, path, db.ErrReleaseRequired)

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), releaseID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Release not found")

		return
	}

	if err := deleteReleaseCascade(c.Request().Context(), releaseID); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Release permanently deleted")
}

// ProfileRolloutPage renders details for a profile-scoped rollout.
func ProfileRolloutPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Rollout Summary")
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

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	rolloutID := strings.TrimSpace(c.Param("rollout_id"))
	path := profileDeploymentsRolloutsPath(profileID)
	if rolloutID == "" {
		redirectWithMessage(c, s, path, FlashError, "Rollout not found")

		return
	}

	rollout, err := db.GetRolloutByID(c.Request().Context(), rolloutID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), rollout.ReleaseID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Rollout not found")

		return
	}

	rolloutName := strings.TrimSpace(rollout.ReleaseVersion)
	if rolloutName == "" {
		rolloutName = strings.TrimSpace(rollout.ID)
	}

	if rolloutName == "" {
		rolloutName = "Rollout"
	}

	data["Rollout"] = rollout
	data["Release"] = release
	data["HasRelease"] = true
	data["CanManageRollout"] = canManage
	data["RolloutDeletePath"] = profileRolloutDeletePath(profileID, rolloutID)
	data["RolloutBackPath"] = path
	data["RolloutReleasePath"] = profileReleasePath(profileID, rollout.ReleaseID)
	data["RolloutReleasesPath"] = profileDeploymentsReleasesPath(profileID)
	setBreadcrumbs(data, profileDeploymentsBreadcrumbs(profile, rolloutName))

	t.HTML(http.StatusOK, "rollout_view")
}

// CreateProfileRollout handles profile-scoped rollout creation.
func CreateProfileRollout(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	path := profileDeploymentsRolloutsPath(profileID)

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, path, FlashError, "Failed to parse form")

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	fleetID := strings.TrimSpace(c.Request().Form.Get("fleet_id"))
	releaseID := strings.TrimSpace(c.Request().Form.Get("release_id"))
	if err := ensureUserCanManageFleetIDs(c.Request().Context(), user, []string{fleetID}); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), releaseID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Release not found")

		return
	}

	if err := createAndActivateRollout(c.Request().Context(), fleetID, releaseID); err != nil {
		if errors.Is(err, errRolloutArtifactActivationFailed) {
			redirectWithMessage(c, s, path, FlashError, "Failed to activate rollout artifacts")

			return
		}

		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Rollout created and activated")
}

// DeleteProfileRollout permanently deletes a profile-scoped rollout.
func DeleteProfileRollout(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, "/profiles", db.ErrAccessDenied)

		return
	}

	profileID := strings.TrimSpace(c.Param("id"))
	if profileID == "" {
		handleMutationError(c, s, "/profiles", db.ErrProfileNotFound)

		return
	}

	path := profileDeploymentsRolloutsPath(profileID)
	rolloutID := strings.TrimSpace(c.Param("rollout_id"))
	if rolloutID == "" {
		handleMutationError(c, s, path, db.ErrRolloutNotFound)

		return
	}

	profile, canManage, err := resolveProfileAccessContext(c.Request().Context(), user, profileID)
	if err != nil {
		handleMutationError(c, s, "/profiles", err)

		return
	}

	if !canManage {
		handleMutationError(c, s, path, db.ErrAccessDenied)

		return
	}

	rollout, err := db.GetRolloutByID(c.Request().Context(), rolloutID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), rollout.ReleaseID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	if strings.TrimSpace(build.ProfileID) != profile.ID {
		redirectWithMessage(c, s, path, FlashError, "Rollout not found")

		return
	}

	if err := db.DeleteRollout(c.Request().Context(), rolloutID); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	redirectWithMessage(c, s, path, FlashSuccess, "Rollout permanently deleted")
}

// BuildsPage renders builds list and create form.
func BuildsPage(c flamego.Context, t template.Template, data template.Data) {
	setPage(data, "Builds")
	data["IsBuilds"] = true

	builds, err := db.ListBuilds(c.Request().Context())
	if err != nil {
		logger.Error("failed to list builds", "error", err)
		setPageErrorFlash(data, "Failed to load builds")
		builds = []db.Build{}
	}

	profiles, err := db.ListProfiles(c.Request().Context())
	if err != nil {
		logger.Error("failed to list profiles for build form", "error", err)
		setPageErrorFlash(data, "Failed to load profiles")

		profiles = []db.Profile{}
	}

	fleets, err := db.ListFleets(c.Request().Context())
	if err != nil {
		logger.Error("failed to list fleets for build form", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")

		fleets = []db.Fleet{}
	}

	data["Builds"] = builds
	data["Profiles"] = profiles
	data["Fleets"] = fleets

	t.HTML(http.StatusOK, "builds")
}

// BuildPage renders details for a single build.
func BuildPage(c flamego.Context, s session.Session, _ template.Template, _ template.Data) {
	buildID := strings.TrimSpace(c.Param("id"))
	if buildID == "" {
		redirectWithMessage(c, s, "/builds", FlashError, "Build not found")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err != nil {
		handleMutationError(c, s, "/builds", err)

		return
	}

	if strings.TrimSpace(build.ProfileID) == "" {
		handleMutationError(c, s, "/builds", db.ErrProfileRequired)

		return
	}

	c.Redirect(profileBuildPath(build.ProfileID, build.ID), http.StatusSeeOther)
}

// CreateBuild handles build creation.
func CreateBuild(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, "/builds", FlashError, "Failed to parse form")

		return
	}

	input := db.CreateBuildInput{
		ProfileID: strings.TrimSpace(c.Request().Form.Get("profile_id")),
		FleetID:   strings.TrimSpace(c.Request().Form.Get("fleet_id")),
		Version:   strings.TrimSpace(c.Request().Form.Get("version")),
	}

	buildID, err := db.CreateBuild(c.Request().Context(), input)
	if err != nil {
		handleMutationError(c, s, "/builds", err)

		return
	}

	queueBuildExecution(buildID, input.Version)

	redirectWithMessage(c, s, profileDeploymentsBuildsPath(input.ProfileID), FlashSuccess, "Build queued")
}

// CreateBuildInstaller handles installer image generation for a build.
func CreateBuildInstaller(c flamego.Context, s session.Session) {
	buildID := strings.TrimSpace(c.Param("id"))
	path := buildViewPath(buildID)
	if path == "/builds/" {
		path = "/builds"
	}

	if buildID == "" {
		handleMutationError(c, s, path, db.ErrBuildRequired)

		return
	}

	if err := db.QueueBuildInstaller(c.Request().Context(), buildID); err != nil {
		handleMutationError(c, s, path, err)

		return
	}

	queueInstallerBuildExecution(buildID)

	redirectWithMessage(c, s, path, FlashSuccess, "Installer build queued")
}

// DeleteBuild permanently deletes a build and dependent releases/rollouts.
func DeleteBuild(c flamego.Context, s session.Session) {
	buildID := strings.TrimSpace(c.Param("id"))
	if buildID == "" {
		handleMutationError(c, s, "/builds", db.ErrBuildRequired)

		return
	}

	profileID := ""
	build, err := db.GetBuildByID(c.Request().Context(), buildID)
	if err == nil {
		profileID = strings.TrimSpace(build.ProfileID)
	}

	if err := deleteBuildCascade(c.Request().Context(), buildID); err != nil {
		handleMutationError(c, s, "/builds", err)

		return
	}

	if profileID != "" {
		redirectWithMessage(c, s, profileDeploymentsBuildsPath(profileID), FlashSuccess, "Build permanently deleted")

		return
	}

	redirectWithMessage(c, s, "/builds", FlashSuccess, "Build permanently deleted")
}

// ReleasesPage renders releases list and create form.
func ReleasesPage(c flamego.Context, t template.Template, data template.Data) {
	setPage(data, "Releases")
	data["IsReleases"] = true

	releases, err := db.ListReleases(c.Request().Context())
	if err != nil {
		logger.Error("failed to list releases", "error", err)
		setPageErrorFlash(data, "Failed to load releases")
		releases = []db.Release{}
	}

	builds, err := db.ListBuilds(c.Request().Context())
	if err != nil {
		logger.Error("failed to list builds for release form", "error", err)
		setPageErrorFlash(data, "Failed to load builds")

		builds = []db.Build{}
	}

	data["Releases"] = releases
	data["Builds"] = builds

	t.HTML(http.StatusOK, "releases")
}

// ReleasePage renders release summary.
func ReleasePage(c flamego.Context, s session.Session, _ template.Template, _ template.Data) {
	releaseID := strings.TrimSpace(c.Param("id"))
	if releaseID == "" {
		redirectWithMessage(c, s, "/releases", FlashError, "Release not found")

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), releaseID)
	if err != nil {
		handleMutationError(c, s, "/releases", err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, "/releases", err)

		return
	}

	if strings.TrimSpace(build.ProfileID) == "" {
		handleMutationError(c, s, "/releases", db.ErrProfileRequired)

		return
	}

	c.Redirect(profileReleasePath(build.ProfileID, release.ID), http.StatusSeeOther)
}

// CreateRelease handles release creation.
func CreateRelease(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, "/releases", FlashError, "Failed to parse form")

		return
	}

	input := db.CreateReleaseInput{
		BuildID: strings.TrimSpace(c.Request().Form.Get("build_id")),
		Channel: strings.TrimSpace(c.Request().Form.Get("channel")),
		Version: strings.TrimSpace(c.Request().Form.Get("version")),
		Notes:   strings.TrimSpace(c.Request().Form.Get("notes")),
	}

	if input.Version == "" {
		build, err := db.GetBuildByID(c.Request().Context(), input.BuildID)
		if err != nil {
			handleMutationError(c, s, "/releases", err)

			return
		}

		input.Version = build.Version
	}

	if err := db.CreateRelease(c.Request().Context(), input); err != nil {
		handleMutationError(c, s, "/releases", err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), input.BuildID)
	if err != nil {
		logger.Warn("release created but failed to load build for redirect", "build_id", input.BuildID, "error", err)
		redirectWithMessage(c, s, "/releases", FlashSuccess, "Release created")

		return
	}

	redirectWithMessage(c, s, profileDeploymentsReleasesPath(build.ProfileID), FlashSuccess, "Release created")
}

// WithdrawRelease marks a release as withdrawn and removes active fleet artifacts when needed.
func WithdrawRelease(c flamego.Context, s session.Session) {
	releaseID := strings.TrimSpace(c.Param("id"))
	if releaseID == "" {
		handleMutationError(c, s, "/releases", db.ErrReleaseRequired)

		return
	}

	info, err := db.GetReleaseTakedownInfo(c.Request().Context(), releaseID)
	if err != nil {
		handleMutationError(c, s, "/releases", err)

		return
	}

	if info.IsCurrentlyLive {
		updatesDir, err := resolveUpdatesDirectory()
		if err != nil {
			logger.Error("failed to resolve updates directory for release takedown", "release_id", releaseID, "fleet_id", info.FleetID, "error", err)
			redirectWithMessage(c, s, "/releases", FlashError, "Failed to remove active release artifacts")

			return
		}

		if err := deactivateFleetReleaseArtifacts(updatesDir, info.FleetID); err != nil {
			logger.Error("failed to remove active release artifacts", "release_id", releaseID, "fleet_id", info.FleetID, "error", err)
			redirectWithMessage(c, s, "/releases", FlashError, "Failed to remove active release artifacts")

			return
		}
	}

	if err := db.SetReleaseStatus(c.Request().Context(), releaseID, db.ReleaseStatusWithdrawn); err != nil {
		handleMutationError(c, s, "/releases", err)

		return
	}

	redirectWithMessage(c, s, "/releases", FlashSuccess, "Release taken down")
}

// DeleteRelease permanently deletes a release and dependent rollouts.
func DeleteRelease(c flamego.Context, s session.Session) {
	releaseID := strings.TrimSpace(c.Param("id"))
	if releaseID == "" {
		handleMutationError(c, s, "/releases", db.ErrReleaseRequired)

		return
	}

	profileID := ""
	release, err := db.GetReleaseByID(c.Request().Context(), releaseID)
	if err == nil {
		build, buildErr := db.GetBuildByID(c.Request().Context(), release.BuildID)
		if buildErr == nil {
			profileID = strings.TrimSpace(build.ProfileID)
		}
	}

	if err := deleteReleaseCascade(c.Request().Context(), releaseID); err != nil {
		handleMutationError(c, s, "/releases", err)

		return
	}

	if profileID != "" {
		redirectWithMessage(c, s, profileDeploymentsReleasesPath(profileID), FlashSuccess, "Release permanently deleted")

		return
	}

	redirectWithMessage(c, s, "/releases", FlashSuccess, "Release permanently deleted")
}

// DevicesPage renders devices list and create form.
func DevicesPage(c flamego.Context, t template.Template, data template.Data) {
	setPage(data, "Devices")
	data["IsDevices"] = true

	devices, err := db.ListDevices(c.Request().Context())
	if err != nil {
		logger.Error("failed to list devices", "error", err)
		setPageErrorFlash(data, "Failed to load devices")
		devices = []db.Device{}
	}

	fleets, err := db.ListFleets(c.Request().Context())
	if err != nil {
		logger.Error("failed to list fleets for device form", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")

		fleets = []db.Fleet{}
	}

	data["Devices"] = devices
	data["Fleets"] = fleets

	t.HTML(http.StatusOK, "devices")
}

// CreateDevice handles device creation.
func CreateDevice(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, "/devices", FlashError, "Failed to parse form")

		return
	}

	input := db.CreateDeviceInput{
		FleetID:          strings.TrimSpace(c.Request().Form.Get("fleet_id")),
		Hostname:         strings.TrimSpace(c.Request().Form.Get("hostname")),
		SerialNumber:     strings.TrimSpace(c.Request().Form.Get("serial_number")),
		UpdateState:      strings.TrimSpace(c.Request().Form.Get("update_state")),
		AttestationLevel: strings.TrimSpace(c.Request().Form.Get("attestation_level")),
	}

	if err := db.CreateDevice(c.Request().Context(), input); err != nil {
		handleMutationError(c, s, "/devices", err)

		return
	}

	redirectWithMessage(c, s, "/devices", FlashSuccess, "Device created")
}

// RolloutsPage renders rollouts list and create form.
func RolloutsPage(c flamego.Context, t template.Template, data template.Data) {
	setPage(data, "Rollouts")
	data["IsRollouts"] = true

	rollouts, err := db.ListRollouts(c.Request().Context())
	if err != nil {
		logger.Error("failed to list rollouts", "error", err)
		setPageErrorFlash(data, "Failed to load rollouts")
		rollouts = []db.Rollout{}
	}

	fleets, err := db.ListFleets(c.Request().Context())
	if err != nil {
		logger.Error("failed to list fleets for rollout form", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")

		fleets = []db.Fleet{}
	}

	releases, err := db.ListReleases(c.Request().Context())
	if err != nil {
		logger.Error("failed to list releases for rollout form", "error", err)
		setPageErrorFlash(data, "Failed to load releases")

		releases = []db.Release{}
	}

	data["Rollouts"] = rollouts
	data["Fleets"] = fleets
	data["Releases"] = releases

	t.HTML(http.StatusOK, "rollouts")
}

// RolloutPage renders rollout summary.
func RolloutPage(c flamego.Context, s session.Session, _ template.Template, _ template.Data) {
	rolloutID := strings.TrimSpace(c.Param("id"))
	if rolloutID == "" {
		redirectWithMessage(c, s, "/rollouts", FlashError, "Rollout not found")

		return
	}

	rollout, err := db.GetRolloutByID(c.Request().Context(), rolloutID)
	if err != nil {
		handleMutationError(c, s, "/rollouts", err)

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), rollout.ReleaseID)
	if err != nil {
		handleMutationError(c, s, "/rollouts", err)

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		handleMutationError(c, s, "/rollouts", err)

		return
	}

	if strings.TrimSpace(build.ProfileID) == "" {
		handleMutationError(c, s, "/rollouts", db.ErrProfileRequired)

		return
	}

	c.Redirect(profileRolloutPath(build.ProfileID, rollout.ID), http.StatusSeeOther)
}

// CreateRollout handles rollout creation.
func CreateRollout(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, "/rollouts", FlashError, "Failed to parse form")

		return
	}

	fleetID := strings.TrimSpace(c.Request().Form.Get("fleet_id"))
	releaseID := strings.TrimSpace(c.Request().Form.Get("release_id"))
	if fleetID == "" {
		handleMutationError(c, s, "/rollouts", db.ErrFleetRequired)

		return
	}

	if err := createAndActivateRollout(c.Request().Context(), fleetID, releaseID); err != nil {
		if errors.Is(err, errRolloutArtifactActivationFailed) {
			redirectWithMessage(c, s, "/rollouts", FlashError, "Failed to activate rollout artifacts")

			return
		}

		handleMutationError(c, s, "/rollouts", err)

		return
	}

	release, err := db.GetReleaseByID(c.Request().Context(), releaseID)
	if err != nil {
		logger.Warn("rollout created but failed to load release for redirect", "release_id", releaseID, "error", err)
		redirectWithMessage(c, s, "/rollouts", FlashSuccess, "Rollout created and activated")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), release.BuildID)
	if err != nil {
		logger.Warn("rollout created but failed to load build for redirect", "release_id", releaseID, "build_id", release.BuildID, "error", err)
		redirectWithMessage(c, s, "/rollouts", FlashSuccess, "Rollout created and activated")

		return
	}

	if strings.TrimSpace(build.ProfileID) == "" {
		redirectWithMessage(c, s, "/rollouts", FlashSuccess, "Rollout created and activated")

		return
	}

	redirectWithMessage(c, s, profileDeploymentsRolloutsPath(build.ProfileID), FlashSuccess, "Rollout created and activated")
}

// DeleteRollout permanently deletes a rollout.
func DeleteRollout(c flamego.Context, s session.Session) {
	rolloutID := strings.TrimSpace(c.Param("id"))
	if rolloutID == "" {
		handleMutationError(c, s, "/rollouts", db.ErrRolloutNotFound)

		return
	}

	profileID := ""
	rollout, err := db.GetRolloutByID(c.Request().Context(), rolloutID)
	if err == nil {
		release, releaseErr := db.GetReleaseByID(c.Request().Context(), rollout.ReleaseID)
		if releaseErr == nil {
			build, buildErr := db.GetBuildByID(c.Request().Context(), release.BuildID)
			if buildErr == nil {
				profileID = strings.TrimSpace(build.ProfileID)
			}
		}
	}

	if err := db.DeleteRollout(c.Request().Context(), rolloutID); err != nil {
		handleMutationError(c, s, "/rollouts", err)

		return
	}

	if profileID != "" {
		redirectWithMessage(c, s, profileDeploymentsRolloutsPath(profileID), FlashSuccess, "Rollout permanently deleted")

		return
	}

	redirectWithMessage(c, s, "/rollouts", FlashSuccess, "Rollout permanently deleted")
}

func markRolloutFailed(ctx context.Context, rolloutID string) {
	if err := db.UpdateRolloutStatus(ctx, rolloutID, db.RolloutStatusFailed); err != nil {
		logger.Error("failed to mark rollout as failed", "rollout_id", rolloutID, "error", err)
	}
}

type BuildArtifactLink struct {
	Name string
	URL  string
}

func normalizePackageList(packages []string) []string {
	if len(packages) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(packages))
	seen := make(map[string]struct{}, len(packages))

	for _, item := range packages {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}

		if _, exists := seen[trimmed]; exists {
			continue
		}

		normalized = append(normalized, trimmed)
		seen[trimmed] = struct{}{}
	}

	return normalized
}

func resolveProfileAccessContext(ctx context.Context, user *db.User, profileID string) (db.ProfileEdit, bool, error) {
	if user == nil {
		return db.ProfileEdit{}, false, errSessionUserMissing
	}

	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return db.ProfileEdit{}, false, db.ErrProfileNotFound
	}

	profile, err := db.GetProfileForEdit(ctx, profileID)
	if err != nil {
		return db.ProfileEdit{}, false, err
	}

	canView, err := db.UserCanViewProfile(ctx, user.ID.String(), user.IsAdmin, profile.ID)
	if err != nil {
		return db.ProfileEdit{}, false, err
	}

	if !canView {
		return db.ProfileEdit{}, false, db.ErrAccessDenied
	}

	canManage, err := db.UserCanManageProfile(ctx, user.ID.String(), user.IsAdmin, profile.ID)
	if err != nil {
		return db.ProfileEdit{}, false, err
	}

	return profile, canManage, nil
}

func filterBuildsByProfileID(builds []db.Build, profileID string) []db.Build {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return []db.Build{}
	}

	filtered := make([]db.Build, 0, len(builds))
	for _, item := range builds {
		if strings.TrimSpace(item.ProfileID) != profileID {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func buildIDSet(builds []db.Build) map[string]struct{} {
	ids := make(map[string]struct{}, len(builds))
	for _, item := range builds {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}

		ids[id] = struct{}{}
	}

	return ids
}

func filterReleasesByBuildIDs(releases []db.Release, buildIDs map[string]struct{}) []db.Release {
	if len(buildIDs) == 0 {
		return []db.Release{}
	}

	filtered := make([]db.Release, 0, len(releases))
	for _, item := range releases {
		if _, ok := buildIDs[strings.TrimSpace(item.BuildID)]; !ok {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func releaseIDSet(releases []db.Release) map[string]struct{} {
	ids := make(map[string]struct{}, len(releases))
	for _, item := range releases {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}

		ids[id] = struct{}{}
	}

	return ids
}

func filterRolloutsByReleaseIDs(rollouts []db.Rollout, releaseIDs map[string]struct{}) []db.Rollout {
	if len(releaseIDs) == 0 {
		return []db.Rollout{}
	}

	filtered := make([]db.Rollout, 0, len(rollouts))
	for _, item := range rollouts {
		if _, ok := releaseIDs[strings.TrimSpace(item.ReleaseID)]; !ok {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func fleetsAssignedToProfile(profile db.ProfileEdit, fleets []db.Fleet) []db.Fleet {
	assignedFleetIDs := make(map[string]struct{})

	legacyFleetID := strings.TrimSpace(profile.FleetID)
	if legacyFleetID != "" {
		assignedFleetIDs[legacyFleetID] = struct{}{}
	}

	for _, fleetID := range profile.FleetIDs {
		trimmedFleetID := strings.TrimSpace(fleetID)
		if trimmedFleetID == "" {
			continue
		}

		assignedFleetIDs[trimmedFleetID] = struct{}{}
	}

	if len(assignedFleetIDs) == 0 {
		return []db.Fleet{}
	}

	filtered := make([]db.Fleet, 0, len(fleets))
	for _, item := range fleets {
		if _, ok := assignedFleetIDs[strings.TrimSpace(item.ID)]; !ok {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func profileDeploymentsPath(profileID string) string {
	return "/profiles/" + profileID + "/deployments"
}

func profileDeploymentsBuildsPath(profileID string) string {
	return profileDeploymentsPath(profileID) + "#profile-deployments-builds"
}

func profileDeploymentsReleasesPath(profileID string) string {
	return profileDeploymentsPath(profileID) + "#profile-deployments-releases"
}

func profileDeploymentsRolloutsPath(profileID string) string {
	return profileDeploymentsPath(profileID) + "#profile-deployments-rollouts"
}

func profileBuildPath(profileID, buildID string) string {
	return "/profiles/" + profileID + "/builds/" + buildID
}

func profileBuildDeletePath(profileID, buildID string) string {
	return "/profiles/" + profileID + "/builds/" + buildID + "/delete"
}

func profileReleasePath(profileID, releaseID string) string {
	return "/profiles/" + profileID + "/releases/" + releaseID
}

func profileReleaseDeletePath(profileID, releaseID string) string {
	return "/profiles/" + profileID + "/releases/" + releaseID + "/delete"
}

func profileRolloutPath(profileID, rolloutID string) string {
	return "/profiles/" + profileID + "/rollouts/" + rolloutID
}

func profileRolloutDeletePath(profileID, rolloutID string) string {
	return "/profiles/" + profileID + "/rollouts/" + rolloutID + "/delete"
}

func profileRolloutsPathPrefix(profileID string) string {
	return "/profiles/" + profileID + "/rollouts"
}

func profileDeploymentsBreadcrumbs(profile db.ProfileEdit, current string) []BreadcrumbItem {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = "Profile"
	}

	items := []BreadcrumbItem{
		{Name: "Profiles", URL: "/profiles"},
		{Name: name, URL: profileViewPath(profile.ID)},
		{Name: "Deployments", URL: profileDeploymentsPath(profile.ID)},
	}

	currentName := strings.TrimSpace(current)
	if currentName == "" {
		items[len(items)-1].IsCurrent = true

		return items
	}

	items = append(items, BreadcrumbItem{Name: currentName, IsCurrent: true})

	return items
}

func profileViewPath(profileID string) string {
	return "/profiles/" + profileID
}

func fleetViewPath(fleetID string) string {
	return "/fleets/" + fleetID
}

func fleetAccessPath(fleetID string) string {
	return "/fleets/" + fleetID + "/access"
}

func buildViewPath(buildID string) string {
	return "/builds/" + buildID
}

func profileEditPath(profileID string) string {
	return "/profiles/" + profileID + "/edit"
}

func profileAccessPath(profileID string) string {
	return "/profiles/" + profileID + "/access"
}

func profilePackagesPath(profileID string) string {
	return "/profiles/" + profileID + "/packages"
}

func profilePackagesPathWithQuery(profileID, query string) string {
	path := profilePackagesPath(profileID)
	trimmedQuery := strings.TrimSpace(query)
	if trimmedQuery == "" {
		return path
	}

	return path + "?q=" + url.QueryEscape(trimmedQuery)
}

func profileKernelPath(profileID string) string {
	return "/profiles/" + profileID + "/kernel"
}

func profileRawNixPath(profileID string) string {
	return "/profiles/" + profileID + "/raw-nix"
}

func profileSectionBreadcrumbs(profile db.ProfileEdit, section string) []BreadcrumbItem {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = "Profile"
	}

	items := []BreadcrumbItem{
		{Name: "Profiles", URL: "/profiles"},
		{Name: name, URL: profileViewPath(profile.ID)},
	}

	sectionName := strings.TrimSpace(section)
	if sectionName != "" {
		items = append(items, BreadcrumbItem{Name: sectionName, IsCurrent: true})
	} else {
		items[len(items)-1].IsCurrent = true
	}

	return items
}

func fleetSectionBreadcrumbs(fleet db.Fleet, section string) []BreadcrumbItem {
	name := strings.TrimSpace(fleet.Name)
	if name == "" {
		name = "Fleet"
	}

	items := []BreadcrumbItem{
		{Name: "Fleets", URL: "/fleets"},
		{Name: name, URL: fleetViewPath(fleet.ID)},
	}

	sectionName := strings.TrimSpace(section)
	if sectionName != "" {
		items = append(items, BreadcrumbItem{Name: sectionName, IsCurrent: true})
	} else {
		items[len(items)-1].IsCurrent = true
	}

	return items
}

func buildBreadcrumbs(build db.Build) []BreadcrumbItem {
	name := strings.TrimSpace(build.Version)
	if name == "" {
		name = "Build"
	}

	return []BreadcrumbItem{
		{Name: "Builds", URL: "/builds"},
		{Name: name, IsCurrent: true},
	}
}

func releaseBreadcrumbs(release db.Release) []BreadcrumbItem {
	name := strings.TrimSpace(release.Version)
	if name == "" {
		name = "Release"
	}

	return []BreadcrumbItem{
		{Name: "Releases", URL: "/releases"},
		{Name: name, IsCurrent: true},
	}
}

func rolloutBreadcrumbs(rollout db.Rollout) []BreadcrumbItem {
	name := strings.TrimSpace(rollout.ID)
	if name == "" {
		name = "Rollout"
	}

	return []BreadcrumbItem{
		{Name: "Rollouts", URL: "/rollouts"},
		{Name: name, IsCurrent: true},
	}
}

func filterAccessCandidateUsers(users, existingUsers []db.ViewerUser) []db.ViewerUser {
	if len(users) == 0 {
		return []db.ViewerUser{}
	}

	existingUserIDs := make(map[string]struct{}, len(existingUsers))
	for _, user := range existingUsers {
		existingUserIDs[user.ID] = struct{}{}
	}

	filtered := make([]db.ViewerUser, 0, len(users))
	for _, user := range users {
		if user.IsAdmin {
			continue
		}

		if _, exists := existingUserIDs[user.ID]; exists {
			continue
		}

		filtered = append(filtered, user)
	}

	return filtered
}

func listBuildUpdateArtifactLinks(buildID string) ([]BuildArtifactLink, error) {
	prefix := "/update/" + url.PathEscape(updatesArtifactsDirName) + "/" + url.PathEscape(strings.TrimSpace(buildID)) + "/"

	return listBuildArtifactLinks(buildID, "", prefix, isUpdateArtifactFileName)
}

func listBuildInstallerArtifactLinks(buildID string) ([]BuildArtifactLink, error) {
	prefix := "/update/" + url.PathEscape(updatesArtifactsDirName) + "/" + url.PathEscape(strings.TrimSpace(buildID)) + "/" + url.PathEscape(installerArtifactsDir) + "/"

	return listBuildArtifactLinks(buildID, installerArtifactsDir, prefix, isInstallerArtifactFileName)
}

func listBuildArtifactLinks(buildID, relativeDir, urlPrefix string, allowFile func(string) bool) ([]BuildArtifactLink, error) {
	normalizedBuildID := strings.TrimSpace(buildID)
	if !isSafeUpdatePathSegment(normalizedBuildID) {
		return nil, fmt.Errorf("invalid build identifier")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve working directory: %w", err)
	}

	directoryPath := filepath.Join(workingDir, updatesDirName, updatesArtifactsDirName, normalizedBuildID, relativeDir)
	entries, err := os.ReadDir(directoryPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []BuildArtifactLink{}, nil
		}

		return nil, fmt.Errorf("failed to read build artifact directory: %w", err)
	}

	links := make([]BuildArtifactLink, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !allowFile(name) {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("failed to read build artifact metadata for %s: %w", name, err)
		}

		if !info.Mode().IsRegular() {
			continue
		}

		links = append(links, BuildArtifactLink{
			Name: name,
			URL:  urlPrefix + url.PathEscape(name),
		})
	}

	sort.Slice(links, func(i, j int) bool {
		return links[i].Name < links[j].Name
	})

	return links, nil
}

func profileKernelSummary(kernelConfig ProfileKernelConfig) string {
	summary := "Default from pinned nixpkgs"
	if kernelConfig.Attr == "" {
		return summary
	}

	summary = kernelConfig.Attr
	if kernelConfig.SourceOverride.Enabled {
		summary += " with source override"

		patchCount := len(kernelConfig.SourceOverride.Patches)
		if patchCount > 0 {
			summary += fmt.Sprintf(" (%d patch", patchCount)
			if patchCount != 1 {
				summary += "es"
			}

			summary += ")"
		}
	}

	return summary
}

func selectedFleetIDsFromForm(values url.Values) []string {
	selected := values["fleet_ids"]
	if len(selected) > 0 {
		return selected
	}

	fleetID := strings.TrimSpace(values.Get("fleet_id"))
	if fleetID == "" {
		return []string{}
	}

	return []string{fleetID}
}

func deleteFleetCascade(ctx context.Context, fleetID string) error {
	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return db.ErrFleetRequired
	}

	rollouts, err := db.ListRollouts(ctx)
	if err != nil {
		return err
	}

	for _, rollout := range rollouts {
		if rollout.FleetID != fleetID {
			continue
		}

		if err := db.DeleteRollout(ctx, rollout.ID); err != nil {
			return err
		}
	}

	builds, err := db.ListBuilds(ctx)
	if err != nil {
		return err
	}

	for _, build := range builds {
		if build.FleetID != fleetID {
			continue
		}

		if err := deleteBuildCascade(ctx, build.ID); err != nil {
			return err
		}
	}

	profiles, err := db.ListProfiles(ctx)
	if err != nil {
		return err
	}

	for _, profile := range profiles {
		if !containsValue(profile.FleetIDs, fleetID) {
			continue
		}

		if len(profile.FleetIDs) <= 1 {
			if err := deleteProfileCascade(ctx, profile.ID); err != nil {
				return err
			}

			continue
		}

		profileEdit, err := db.GetProfileForEdit(ctx, profile.ID)
		if err != nil {
			return err
		}

		remainingFleetIDs := removeString(profileEdit.FleetIDs, fleetID)
		if len(remainingFleetIDs) == 0 {
			if err := deleteProfileCascade(ctx, profile.ID); err != nil {
				return err
			}

			continue
		}

		_, err = db.UpdateProfile(ctx, profile.ID, db.CreateProfileInput{
			FleetIDs:            remainingFleetIDs,
			Name:                profileEdit.Name,
			Description:         profileEdit.Description,
			ConfigJSON:          profileEdit.ConfigJSON,
			RawNix:              profileEdit.RawNix,
			ConfigSchemaVersion: profileEdit.ConfigSchemaVersion,
		})
		if err != nil {
			return err
		}
	}

	if _, err := db.DeleteDevicesByFleet(ctx, fleetID); err != nil {
		return err
	}

	return db.DeleteFleet(ctx, fleetID)
}

func deleteProfileCascade(ctx context.Context, profileID string) error {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return db.ErrProfileNotFound
	}

	builds, err := db.ListBuilds(ctx)
	if err != nil {
		return err
	}

	for _, build := range builds {
		if build.ProfileID != profileID {
			continue
		}

		if err := deleteBuildCascade(ctx, build.ID); err != nil {
			return err
		}
	}

	return db.DeleteProfile(ctx, profileID)
}

func deleteBuildCascade(ctx context.Context, buildID string) error {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return db.ErrBuildRequired
	}

	releases, err := db.ListReleases(ctx)
	if err != nil {
		return err
	}

	for _, release := range releases {
		if release.BuildID != buildID {
			continue
		}

		if err := deleteReleaseCascade(ctx, release.ID); err != nil {
			return err
		}
	}

	return db.DeleteBuild(ctx, buildID)
}

func deleteReleaseCascade(ctx context.Context, releaseID string) error {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return db.ErrReleaseRequired
	}

	rollouts, err := db.ListRollouts(ctx)
	if err != nil {
		return err
	}

	for _, rollout := range rollouts {
		if rollout.ReleaseID != releaseID {
			continue
		}

		if err := db.DeleteRollout(ctx, rollout.ID); err != nil {
			return err
		}
	}

	return db.DeleteRelease(ctx, releaseID)
}

func removeString(values []string, target string) []string {
	trimmedTarget := strings.TrimSpace(target)
	if len(values) == 0 {
		return []string{}
	}

	filtered := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || trimmed == trimmedTarget {
			continue
		}

		filtered = append(filtered, trimmed)
	}

	return filtered
}

func containsValue(values []string, target string) bool {
	trimmedTarget := strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == trimmedTarget {
			return true
		}
	}

	return false
}

func fleetSelectionMap(fleetIDs []string) map[string]bool {
	selected := make(map[string]bool, len(fleetIDs))

	for _, fleetID := range fleetIDs {
		trimmed := strings.TrimSpace(fleetID)
		if trimmed == "" {
			continue
		}

		selected[trimmed] = true
	}

	return selected
}

func parseProfileConfig(configJSON string) (map[string]any, error) {
	trimmed := strings.TrimSpace(configJSON)
	if trimmed == "" {
		trimmed = "{}"
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil, db.ErrInvalidProfileConfigJSON
	}

	config, ok := decoded.(map[string]any)
	if !ok {
		return nil, db.ErrProfileConfigMustBeObject
	}

	return config, nil
}

func packagesFromProfileConfig(configJSON string) ([]string, error) {
	config, err := parseProfileConfig(configJSON)
	if err != nil {
		return nil, err
	}

	rawPackages, exists := config["packages"]
	if !exists || rawPackages == nil {
		return []string{}, nil
	}

	rawList, ok := rawPackages.([]any)
	if !ok {
		return nil, db.ErrInvalidProfileConfigJSON
	}

	packages := make([]string, 0, len(rawList))
	for _, raw := range rawList {
		value, ok := raw.(string)
		if !ok {
			return nil, db.ErrInvalidProfileConfigJSON
		}

		packages = append(packages, value)
	}

	return normalizePackageList(packages), nil
}

func profileConfigWithPackages(configJSON string, packages []string) (string, error) {
	config, err := parseProfileConfig(configJSON)
	if err != nil {
		return "", err
	}

	config["packages"] = normalizePackageList(packages)

	encoded, err := json.Marshal(config)
	if err != nil {
		return "", err
	}

	return string(encoded), nil
}

func handleMutationError(c flamego.Context, s session.Session, path string, err error) {
	message := mutationErrorMessage(err)
	logger.Warn("mutation failed", "path", path, "error", err)
	redirectWithMessage(c, s, path, FlashError, message)
}

func mutationErrorMessage(err error) string {
	switch {
	case errors.Is(err, db.ErrNameRequired):
		return "Name is required"
	case errors.Is(err, db.ErrAccessDenied):
		return "Access restricted"
	case errors.Is(err, db.ErrAdminRequired):
		return "Admin permissions required"
	case errors.Is(err, db.ErrUserNotFound):
		return "User not found"
	case errors.Is(err, db.ErrProfileRequired):
		return "Profile is required"
	case errors.Is(err, db.ErrBuildRequired):
		return "Build is required"
	case errors.Is(err, db.ErrBuildNotFound):
		return "Build not found"
	case errors.Is(err, db.ErrBuildNotReadyForInstaller):
		return "Build must succeed before installer can be built"
	case errors.Is(err, db.ErrBuildInstallerAlreadyQueued):
		return "Installer build is already queued or running"
	case errors.Is(err, db.ErrFleetRequired):
		return "Fleet is required"
	case errors.Is(err, db.ErrFleetNotFound):
		return "Fleet not found"
	case errors.Is(err, db.ErrReleaseRequired):
		return "Release is required"
	case errors.Is(err, db.ErrReleaseNotFound):
		return "Release not found"
	case errors.Is(err, db.ErrReleaseWithdrawn):
		return "Release is taken down"
	case errors.Is(err, db.ErrReleaseFleetNotConfigured):
		return "Release build must be assigned to a fleet"
	case errors.Is(err, db.ErrVersionRequired):
		return "Version is required"
	case errors.Is(err, db.ErrVersionMustBeSemver):
		return "Version must use semver format like v1.1.0"
	case errors.Is(err, db.ErrHostnameRequired):
		return "Hostname is required"
	case errors.Is(err, db.ErrSerialRequired):
		return "Serial number is required"
	case errors.Is(err, db.ErrInvalidStatus):
		return "Status value is invalid"
	case errors.Is(err, db.ErrInvalidStrategy):
		return "Rollout strategy is invalid"
	case errors.Is(err, db.ErrInvalidStageValue):
		return "Stage percent must be 100 for all-at-once rollouts"
	case errors.Is(err, db.ErrFleetAlreadyExists):
		return "Fleet already exists"
	case errors.Is(err, db.ErrProfileAlreadyExists):
		return "Profile already exists"
	case errors.Is(err, db.ErrProfileNotFound):
		return "Profile not found"
	case errors.Is(err, db.ErrProfileHasNoRevisions):
		return "Profile has no revisions"
	case errors.Is(err, db.ErrProfileFleetRequired):
		return "Profile must be assigned to a fleet"
	case errors.Is(err, db.ErrProfileNotAssignedToFleet):
		return "Profile is not assigned to the selected fleet"
	case errors.Is(err, db.ErrInvalidProfileConfigJSON):
		return "Profile config must be valid JSON"
	case errors.Is(err, db.ErrProfileConfigMustBeObject):
		return "Profile config JSON must be an object"
	case errors.Is(err, db.ErrInvalidConfigSchemaVersion):
		return "Config schema version must be a positive number"
	case errors.Is(err, db.ErrBuildVersionAlreadyExists):
		return "Build version already exists for this profile and fleet"
	case errors.Is(err, db.ErrReleaseVersionAlreadyExists):
		return "Release version already exists"
	case errors.Is(err, db.ErrDeviceHostnameAlreadyExists):
		return "Hostname already exists in this fleet"
	case errors.Is(err, db.ErrDeviceSerialAlreadyExists):
		return "Serial number already exists"
	case errors.Is(err, db.ErrRolloutNotFound):
		return "Rollout not found"
	case errors.Is(err, db.ErrRolloutFleetReleaseMismatch):
		return "Release does not belong to the selected fleet"
	default:
		return "Operation failed"
	}
}
