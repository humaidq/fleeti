/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/flamego/flamego"
	"github.com/flamego/session"
	"github.com/flamego/template"

	"github.com/humaidq/fleeti/v2/db"
)

const (
	deploymentWizardBasePath = "/deployments/wizard"
	wizardStepFleetID        = "wizard-step-fleet"
	wizardStepProfileID      = "wizard-step-profile"
	wizardStepBuildID        = "wizard-step-build"
	wizardStepReleaseID      = "wizard-step-release"
	wizardStepRolloutID      = "wizard-step-rollout"

	deploymentWizardFleetSessionKey   = "deployment_wizard_fleet_id"
	deploymentWizardProfileSessionKey = "deployment_wizard_profile_id"
	deploymentWizardBuildSessionKey   = "deployment_wizard_build_id"
	deploymentWizardReleaseSessionKey = "deployment_wizard_release_id"
)

var errRolloutArtifactActivationFailed = errors.New("failed to activate rollout artifacts")

type deploymentWizardState struct {
	FleetID   string
	ProfileID string
	BuildID   string
	ReleaseID string
}

// DeploymentWizardPage renders the guided deployment workflow.
func DeploymentWizardPage(c flamego.Context, s session.Session, t template.Template, data template.Data) {
	setPage(data, "Deployment Wizard")
	data["IsWizard"] = true

	state := getDeploymentWizardState(s)
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		logger.Error("failed to resolve session user for deployment wizard", "error", err)
		setPageErrorFlash(data, "Failed to load deployment wizard")
		data["WizardFleets"] = []db.Fleet{}
		data["WizardProfiles"] = []db.Profile{}
		data["WizardBuilds"] = []db.Build{}
		data["WizardReleases"] = []db.Release{}

		t.HTML(http.StatusOK, "deployment_wizard")

		return
	}

	fleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list fleets for deployment wizard", "error", err)
		setPageErrorFlash(data, "Failed to load fleets")
		fleets = []db.Fleet{}
	}

	selectedFleet, fleetSelected := fleetByID(fleets, state.FleetID)
	if !fleetSelected {
		state = deploymentWizardState{}
	}

	profiles := []db.Profile{}
	selectedProfile := db.Profile{}
	profileSelected := false
	if fleetSelected {
		allProfiles, listErr := db.ListProfilesForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
		if listErr != nil {
			logger.Error("failed to list profiles for deployment wizard", "fleet_id", state.FleetID, "error", listErr)
			setPageErrorFlash(data, "Failed to load profiles")
		} else {
			profiles = profilesForFleet(allProfiles, state.FleetID)
			selectedProfile, profileSelected = profileByID(profiles, state.ProfileID)
			if !profileSelected {
				state.ProfileID = ""
				state.BuildID = ""
				state.ReleaseID = ""
			}
		}
	}

	builds := []db.Build{}
	selectedBuild := db.Build{}
	buildSelected := false
	if profileSelected {
		allBuilds, listErr := db.ListBuilds(c.Request().Context())
		if listErr != nil {
			logger.Error("failed to list builds for deployment wizard", "fleet_id", state.FleetID, "profile_id", state.ProfileID, "error", listErr)
			setPageErrorFlash(data, "Failed to load builds")
		} else {
			builds = buildsForProfileAndFleet(allBuilds, state.ProfileID, state.FleetID)
			selectedBuild, buildSelected = buildByID(builds, state.BuildID)
			if !buildSelected {
				state.BuildID = ""
				state.ReleaseID = ""
			}
		}
	}

	releases := []db.Release{}
	selectedRelease := db.Release{}
	releaseSelected := false
	if buildSelected {
		allReleases, listErr := db.ListReleases(c.Request().Context())
		if listErr != nil {
			logger.Error("failed to list releases for deployment wizard", "fleet_id", state.FleetID, "build_id", state.BuildID, "error", listErr)
			setPageErrorFlash(data, "Failed to load releases")
		} else {
			releases = releasesForBuild(allReleases, state.BuildID)
			selectedRelease, releaseSelected = releaseByID(releases, state.ReleaseID)
			if !releaseSelected {
				state.ReleaseID = ""
			}
		}
	}

	buildReadyForRelease := buildSelected && selectedBuild.Status == db.BuildStatusSucceeded
	canRollout := releaseSelected &&
		fleetSelected &&
		selectedRelease.FleetID == state.FleetID &&
		selectedRelease.Status == db.ReleaseStatusActive
	setDeploymentWizardState(s, state)

	data["WizardFleets"] = fleets
	data["WizardProfiles"] = profiles
	data["WizardBuilds"] = builds
	data["WizardReleases"] = releases

	data["WizardFleetID"] = state.FleetID
	data["WizardProfileID"] = state.ProfileID
	data["WizardBuildID"] = state.BuildID
	data["WizardReleaseID"] = state.ReleaseID

	data["WizardHasFleet"] = fleetSelected
	data["WizardHasProfile"] = profileSelected
	data["WizardHasBuild"] = buildSelected
	data["WizardHasRelease"] = releaseSelected

	data["WizardSelectedFleetName"] = selectedFleet.Name
	data["WizardSelectedProfileName"] = selectedProfile.Name
	data["WizardSelectedBuildVersion"] = selectedBuild.Version
	data["WizardSelectedBuildStatus"] = selectedBuild.Status
	data["WizardSelectedBuildCreatedAt"] = selectedBuild.CreatedAt
	data["WizardSelectedReleaseVersion"] = selectedRelease.Version
	data["WizardSelectedReleaseChannel"] = selectedRelease.Channel
	data["WizardSelectedReleaseStatus"] = selectedRelease.Status
	data["WizardSelectedReleasePublishedAt"] = selectedRelease.PublishedAt

	data["WizardBuildReadyForRelease"] = buildReadyForRelease
	data["WizardCanRollout"] = canRollout
	data["WizardBuildLogsPath"] = "/builds/" + state.BuildID + "/logs"
	data["WizardResetPath"] = deploymentWizardBasePath + "/reset"
	data["WizardKeepFleetPath"] = deploymentWizardBasePath + "/restart/fleet"
	data["WizardKeepProfilePath"] = deploymentWizardBasePath + "/restart/profile"
	data["WizardKeepBuildPath"] = deploymentWizardBasePath + "/restart/build"

	t.HTML(http.StatusOK, "deployment_wizard")
}

// DeploymentWizardReset clears wizard progress from the current session.
func DeploymentWizardReset(c flamego.Context, s session.Session) {
	clearDeploymentWizardState(s)
	redirectWithMessage(c, s, deploymentWizardPathWithAnchor(deploymentWizardState{}, wizardStepFleetID), FlashSuccess, "Wizard progress reset")
}

// DeploymentWizardRestartFromFleet keeps fleet selection and clears downstream wizard state.
func DeploymentWizardRestartFromFleet(c flamego.Context, s session.Session) {
	state := getDeploymentWizardState(s)
	state.ProfileID = ""
	state.BuildID = ""
	state.ReleaseID = ""
	setDeploymentWizardState(s, state)

	c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepProfileID), http.StatusSeeOther)
}

// DeploymentWizardRestartFromProfile keeps fleet/profile and clears build and release state.
func DeploymentWizardRestartFromProfile(c flamego.Context, s session.Session) {
	state := getDeploymentWizardState(s)
	state.BuildID = ""
	state.ReleaseID = ""
	setDeploymentWizardState(s, state)

	c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepBuildID), http.StatusSeeOther)
}

// DeploymentWizardRestartFromBuild keeps fleet/profile/build and clears release state.
func DeploymentWizardRestartFromBuild(c flamego.Context, s session.Session) {
	state := getDeploymentWizardState(s)
	state.ReleaseID = ""
	setDeploymentWizardState(s, state)

	c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepReleaseID), http.StatusSeeOther)
}

// DeploymentWizardFleet handles fleet selection and creation for the wizard.
func DeploymentWizardFleet(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, deploymentWizardBasePath, db.ErrAccessDenied)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Failed to parse form")

		return
	}

	intent := strings.TrimSpace(c.Request().Form.Get("intent"))

	switch intent {
	case "select":
		fleetID := strings.TrimSpace(c.Request().Form.Get("fleet_id"))
		if fleetID == "" {
			redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Fleet is required")

			return
		}

		fleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
		if err != nil {
			logger.Error("failed to list fleets for wizard fleet selection", "error", err)
			redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Failed to load fleets")

			return
		}

		if _, ok := fleetByID(fleets, fleetID); !ok {
			redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Fleet not found")

			return
		}

		state := deploymentWizardState{FleetID: fleetID}
		setDeploymentWizardState(s, state)
		c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepProfileID), http.StatusSeeOther)

		return
	case "create":
		name := strings.TrimSpace(c.Request().Form.Get("name"))
		description := strings.TrimSpace(c.Request().Form.Get("description"))

		if err := db.CreateFleet(c.Request().Context(), name, description, user.ID.String()); err != nil {
			handleMutationError(c, s, deploymentWizardBasePath, err)

			return
		}

		fleets, err := db.ListFleetsForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
		if err != nil {
			logger.Error("failed to list fleets after wizard fleet creation", "fleet_name", name, "error", err)
			redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Fleet created but failed to reload wizard")

			return
		}

		fleet, ok := fleetByName(fleets, name)
		if !ok {
			logger.Warn("created fleet not found in follow-up list", "fleet_name", name)
			redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Fleet created but failed to select it")

			return
		}

		state := deploymentWizardState{FleetID: fleet.ID}
		setDeploymentWizardState(s, state)
		redirectWithMessage(c, s, deploymentWizardPathWithAnchor(state, wizardStepProfileID), FlashSuccess, "Fleet created. Continue to profile step")

		return
	default:
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Invalid wizard action")

		return
	}
}

// DeploymentWizardProfile handles profile selection and creation for the wizard.
func DeploymentWizardProfile(c flamego.Context, s session.Session) {
	user, err := resolveSessionUser(c.Request().Context(), s)
	if err != nil {
		handleMutationError(c, s, deploymentWizardBasePath, db.ErrAccessDenied)

		return
	}

	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Failed to parse form")

		return
	}

	state := getDeploymentWizardState(s)
	if state.FleetID == "" {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Select a fleet first")

		return
	}

	intent := strings.TrimSpace(c.Request().Form.Get("intent"))

	allProfiles, err := db.ListProfilesForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
	if err != nil {
		logger.Error("failed to list profiles for wizard profile step", "fleet_id", state.FleetID, "error", err)
		redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), FlashError, "Failed to load profiles")

		return
	}

	profiles := profilesForFleet(allProfiles, state.FleetID)

	switch intent {
	case "select":
		profileID := strings.TrimSpace(c.Request().Form.Get("profile_id"))
		if profileID == "" {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), FlashError, "Profile is required")

			return
		}

		if _, ok := profileByID(profiles, profileID); !ok {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), FlashError, "Profile is not assigned to the selected fleet")

			return
		}

		state.ProfileID = profileID
		state.BuildID = ""
		state.ReleaseID = ""
		setDeploymentWizardState(s, state)

		c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepBuildID), http.StatusSeeOther)

		return
	case "create":
		name := strings.TrimSpace(c.Request().Form.Get("name"))
		description := strings.TrimSpace(c.Request().Form.Get("description"))

		if err := ensureUserCanManageFleetIDs(c.Request().Context(), user, []string{state.FleetID}); err != nil {
			handleMutationError(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), err)

			return
		}

		input := db.CreateProfileInput{
			FleetIDs:    []string{state.FleetID},
			Name:        name,
			Description: description,
			ConfigJSON:  `{"packages":[]}`,
		}

		if err := db.CreateProfile(c.Request().Context(), input, user.ID.String()); err != nil {
			handleMutationError(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), err)

			return
		}

		allProfiles, err = db.ListProfilesForUser(c.Request().Context(), user.ID.String(), user.IsAdmin)
		if err != nil {
			logger.Error("failed to list profiles after wizard profile creation", "fleet_id", state.FleetID, "profile_name", name, "error", err)
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), FlashError, "Profile created but failed to reload wizard")

			return
		}

		profiles = profilesForFleet(allProfiles, state.FleetID)
		profile, ok := profileByName(profiles, name)
		if !ok {
			logger.Warn("created profile not found in follow-up list", "fleet_id", state.FleetID, "profile_name", name)
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), FlashError, "Profile created but failed to select it")

			return
		}

		state.ProfileID = profile.ID
		state.BuildID = ""
		state.ReleaseID = ""
		setDeploymentWizardState(s, state)

		redirectWithMessage(c, s, deploymentWizardPathWithAnchor(state, wizardStepBuildID), FlashSuccess, "Profile created. You can edit packages and kernel before building")

		return
	default:
		redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID}), FlashError, "Invalid wizard action")

		return
	}
}

// DeploymentWizardBuild handles build selection and creation for the wizard.
func DeploymentWizardBuild(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Failed to parse form")

		return
	}

	state := getDeploymentWizardState(s)
	if state.FleetID == "" || state.ProfileID == "" {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Select fleet and profile first")

		return
	}

	intent := strings.TrimSpace(c.Request().Form.Get("intent"))

	switch intent {
	case "select":
		buildID := strings.TrimSpace(c.Request().Form.Get("build_id"))
		if buildID == "" {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID}), FlashError, "Build is required")

			return
		}

		build, err := db.GetBuildByID(c.Request().Context(), buildID)
		if err != nil {
			handleMutationError(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID}), err)

			return
		}

		if build.ProfileID != state.ProfileID || build.FleetID != state.FleetID {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID}), FlashError, "Build does not match selected profile and fleet")

			return
		}

		state.BuildID = buildID
		state.ReleaseID = ""
		setDeploymentWizardState(s, state)

		c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepReleaseID), http.StatusSeeOther)

		return
	case "create":
		version := strings.TrimSpace(c.Request().Form.Get("version"))

		input := db.CreateBuildInput{
			ProfileID: state.ProfileID,
			FleetID:   state.FleetID,
			Version:   version,
		}

		buildID, err := db.CreateBuild(c.Request().Context(), input)
		if err != nil {
			handleMutationError(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID}), err)

			return
		}

		queueBuildExecution(buildID, version)

		state.BuildID = buildID
		state.ReleaseID = ""
		setDeploymentWizardState(s, state)

		redirectWithMessage(c, s, deploymentWizardPathWithAnchor(state, wizardStepReleaseID), FlashSuccess, "Build queued. Continue once it succeeds")

		return
	default:
		redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID}), FlashError, "Invalid wizard action")

		return
	}
}

// DeploymentWizardRelease handles release selection and creation for the wizard.
func DeploymentWizardRelease(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Failed to parse form")

		return
	}

	state := getDeploymentWizardState(s)
	if state.FleetID == "" || state.ProfileID == "" || state.BuildID == "" {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Select fleet, profile, and build first")

		return
	}

	intent := strings.TrimSpace(c.Request().Form.Get("intent"))

	allReleases, err := db.ListReleases(c.Request().Context())
	if err != nil {
		logger.Error("failed to list releases for wizard release step", "build_id", state.BuildID, "error", err)
		redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Failed to load releases")

		return
	}

	releases := releasesForBuild(allReleases, state.BuildID)

	switch intent {
	case "select":
		releaseID := strings.TrimSpace(c.Request().Form.Get("release_id"))
		if releaseID == "" {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Release is required")

			return
		}

		release, ok := releaseByID(releases, releaseID)
		if !ok {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Release does not match selected build")

			return
		}

		if release.FleetID != state.FleetID {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Release does not belong to the selected fleet")

			return
		}

		if release.Status == db.ReleaseStatusWithdrawn {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Release is taken down")

			return
		}

		state.ReleaseID = releaseID
		setDeploymentWizardState(s, state)

		c.Redirect(deploymentWizardPathWithAnchor(state, wizardStepRolloutID), http.StatusSeeOther)

		return
	case "create":
		qaConfirmed := strings.TrimSpace(c.Request().Form.Get("qa_confirmed")) == "1"
		if !qaConfirmed {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "QA confirmation is required before release")

			return
		}

		build, err := db.GetBuildByID(c.Request().Context(), state.BuildID)
		if err != nil {
			handleMutationError(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), err)

			return
		}

		if build.ProfileID != state.ProfileID || build.FleetID != state.FleetID {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Build does not match selected profile and fleet")

			return
		}

		if build.Status != db.BuildStatusSucceeded {
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Build must succeed before creating a release")

			return
		}

		version := strings.TrimSpace(c.Request().Form.Get("version"))
		channel := strings.TrimSpace(c.Request().Form.Get("channel"))
		notes := strings.TrimSpace(c.Request().Form.Get("notes"))
		if version == "" {
			version = build.Version
		}

		input := db.CreateReleaseInput{
			BuildID: state.BuildID,
			Channel: channel,
			Version: version,
			Notes:   notes,
		}

		if err := db.CreateRelease(c.Request().Context(), input); err != nil {
			handleMutationError(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), err)

			return
		}

		allReleases, err = db.ListReleases(c.Request().Context())
		if err != nil {
			logger.Error("failed to list releases after wizard release creation", "build_id", state.BuildID, "release_version", version, "error", err)
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Release created but failed to reload wizard")

			return
		}

		releases = releasesForBuild(allReleases, state.BuildID)
		release, ok := releaseByVersion(releases, version)
		if !ok {
			logger.Warn("created release not found in follow-up list", "build_id", state.BuildID, "release_version", version)
			redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Release created but failed to select it")

			return
		}

		state.ReleaseID = release.ID
		setDeploymentWizardState(s, state)

		redirectWithMessage(c, s, deploymentWizardPathWithAnchor(state, wizardStepRolloutID), FlashSuccess, "Release created. Continue to rollout")

		return
	default:
		redirectWithMessage(c, s, deploymentWizardPath(deploymentWizardState{FleetID: state.FleetID, ProfileID: state.ProfileID, BuildID: state.BuildID}), FlashError, "Invalid wizard action")

		return
	}
}

// DeploymentWizardRollout creates and activates a rollout from the selected wizard chain.
func DeploymentWizardRollout(c flamego.Context, s session.Session) {
	if err := c.Request().ParseForm(); err != nil {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Failed to parse form")

		return
	}

	state := getDeploymentWizardState(s)
	if state.FleetID == "" || state.ProfileID == "" || state.BuildID == "" || state.ReleaseID == "" {
		redirectWithMessage(c, s, deploymentWizardBasePath, FlashError, "Complete all deployment steps before rollout")

		return
	}

	confirm := strings.TrimSpace(c.Request().Form.Get("confirm_rollout")) == "1"
	if !confirm {
		redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Confirm rollout target before continuing")

		return
	}

	build, err := db.GetBuildByID(c.Request().Context(), state.BuildID)
	if err != nil {
		handleMutationError(c, s, deploymentWizardPath(state), err)

		return
	}

	if build.ProfileID != state.ProfileID || build.FleetID != state.FleetID {
		redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Build does not match selected profile and fleet")

		return
	}

	allReleases, err := db.ListReleases(c.Request().Context())
	if err != nil {
		logger.Error("failed to list releases for rollout validation", "build_id", state.BuildID, "release_id", state.ReleaseID, "error", err)
		redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Failed to validate release for rollout")

		return
	}

	release, ok := releaseByID(allReleases, state.ReleaseID)
	if !ok {
		redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Release not found")

		return
	}

	if release.BuildID != state.BuildID {
		redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Release does not match selected build")

		return
	}

	if release.FleetID != state.FleetID {
		redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Release does not belong to the selected fleet")

		return
	}

	if err := createAndActivateRollout(c.Request().Context(), state.FleetID, state.ReleaseID); err != nil {
		if errors.Is(err, errRolloutArtifactActivationFailed) {
			redirectWithMessage(c, s, deploymentWizardPath(state), FlashError, "Failed to activate rollout artifacts")

			return
		}

		handleMutationError(c, s, deploymentWizardPath(state), err)

		return
	}

	redirectPath := profileDeploymentsRolloutsPath(state.ProfileID)
	clearDeploymentWizardState(s)
	redirectWithMessage(c, s, redirectPath, FlashSuccess, "Rollout created and activated")
}

func createAndActivateRollout(ctx context.Context, fleetID, releaseID string) error {
	fleetID = strings.TrimSpace(fleetID)
	releaseID = strings.TrimSpace(releaseID)

	if fleetID == "" {
		return db.ErrFleetRequired
	}

	if releaseID == "" {
		return db.ErrReleaseRequired
	}

	deploymentInfo, err := db.GetReleaseDeploymentInfo(ctx, releaseID)
	if err != nil {
		return err
	}

	if deploymentInfo.FleetID != fleetID {
		return db.ErrRolloutFleetReleaseMismatch
	}

	input := db.CreateRolloutInput{
		FleetID:      fleetID,
		ReleaseID:    releaseID,
		Strategy:     db.RolloutStrategyAllAtOnce,
		StagePercent: 100,
		Status:       db.RolloutStatusInProgress,
	}

	rolloutID, err := db.CreateRollout(ctx, input)
	if err != nil {
		return err
	}

	updatesDir, err := resolveUpdatesDirectory()
	if err != nil {
		logger.Error("failed to resolve updates directory for rollout", "fleet_id", fleetID, "release_id", releaseID, "error", err)
		markRolloutFailed(ctx, rolloutID)

		return fmt.Errorf("%w: %v", errRolloutArtifactActivationFailed, err)
	}

	if err := activateFleetReleaseArtifacts(updatesDir, fleetID, deploymentInfo.BuildID); err != nil {
		logger.Error("failed to activate fleet rollout artifacts", "fleet_id", fleetID, "release_id", releaseID, "build_id", deploymentInfo.BuildID, "error", err)
		markRolloutFailed(ctx, rolloutID)

		return fmt.Errorf("%w: %v", errRolloutArtifactActivationFailed, err)
	}

	if _, err := db.SetFleetDesiredRelease(ctx, fleetID, releaseID); err != nil {
		logger.Error("failed to update fleet desired release", "fleet_id", fleetID, "release_id", releaseID, "rollout_id", rolloutID, "error", err)
		markRolloutFailed(ctx, rolloutID)

		return err
	}

	if err := db.UpdateRolloutStatus(ctx, rolloutID, db.RolloutStatusCompleted); err != nil {
		logger.Error("failed to mark rollout as completed", "rollout_id", rolloutID, "error", err)

		return err
	}

	return nil
}

func getDeploymentWizardState(s session.Session) deploymentWizardState {
	state := deploymentWizardState{}

	if value, ok := s.Get(deploymentWizardFleetSessionKey).(string); ok {
		state.FleetID = strings.TrimSpace(value)
	}

	if value, ok := s.Get(deploymentWizardProfileSessionKey).(string); ok {
		state.ProfileID = strings.TrimSpace(value)
	}

	if value, ok := s.Get(deploymentWizardBuildSessionKey).(string); ok {
		state.BuildID = strings.TrimSpace(value)
	}

	if value, ok := s.Get(deploymentWizardReleaseSessionKey).(string); ok {
		state.ReleaseID = strings.TrimSpace(value)
	}

	return state
}

func setDeploymentWizardState(s session.Session, state deploymentWizardState) {
	s.Set(deploymentWizardFleetSessionKey, strings.TrimSpace(state.FleetID))
	s.Set(deploymentWizardProfileSessionKey, strings.TrimSpace(state.ProfileID))
	s.Set(deploymentWizardBuildSessionKey, strings.TrimSpace(state.BuildID))
	s.Set(deploymentWizardReleaseSessionKey, strings.TrimSpace(state.ReleaseID))
}

func clearDeploymentWizardState(s session.Session) {
	s.Delete(deploymentWizardFleetSessionKey)
	s.Delete(deploymentWizardProfileSessionKey)
	s.Delete(deploymentWizardBuildSessionKey)
	s.Delete(deploymentWizardReleaseSessionKey)
}

func deploymentWizardPath(state deploymentWizardState) string {
	_ = state

	return deploymentWizardBasePath
}

func deploymentWizardPathWithAnchor(state deploymentWizardState, stepID string) string {
	path := deploymentWizardPath(state)
	trimmed := strings.TrimSpace(stepID)
	if trimmed == "" {
		return path
	}

	if strings.HasPrefix(trimmed, "#") {
		return path + trimmed
	}

	return path + "#" + trimmed
}

func fleetByID(fleets []db.Fleet, fleetID string) (db.Fleet, bool) {
	for _, item := range fleets {
		if item.ID == fleetID {
			return item, true
		}
	}

	return db.Fleet{}, false
}

func fleetByName(fleets []db.Fleet, name string) (db.Fleet, bool) {
	for _, item := range fleets {
		if item.Name == name {
			return item, true
		}
	}

	return db.Fleet{}, false
}

func profilesForFleet(profiles []db.Profile, fleetID string) []db.Profile {
	filtered := make([]db.Profile, 0, len(profiles))

	for _, item := range profiles {
		if profileAssignedToFleet(item, fleetID) {
			filtered = append(filtered, item)
		}
	}

	return filtered
}

func profileAssignedToFleet(profile db.Profile, fleetID string) bool {
	fleetID = strings.TrimSpace(fleetID)
	if fleetID == "" {
		return false
	}

	if strings.TrimSpace(profile.FleetID) == fleetID {
		return true
	}

	for _, assignedFleetID := range profile.FleetIDs {
		if strings.TrimSpace(assignedFleetID) == fleetID {
			return true
		}
	}

	return false
}

func profileByID(profiles []db.Profile, profileID string) (db.Profile, bool) {
	for _, item := range profiles {
		if item.ID == profileID {
			return item, true
		}
	}

	return db.Profile{}, false
}

func profileByName(profiles []db.Profile, name string) (db.Profile, bool) {
	for _, item := range profiles {
		if item.Name == name {
			return item, true
		}
	}

	return db.Profile{}, false
}

func buildsForProfileAndFleet(builds []db.Build, profileID, fleetID string) []db.Build {
	filtered := make([]db.Build, 0, len(builds))

	for _, item := range builds {
		if item.ProfileID != profileID {
			continue
		}

		if item.FleetID != fleetID {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func buildByID(builds []db.Build, buildID string) (db.Build, bool) {
	for _, item := range builds {
		if item.ID == buildID {
			return item, true
		}
	}

	return db.Build{}, false
}

func releasesForBuild(releases []db.Release, buildID string) []db.Release {
	filtered := make([]db.Release, 0, len(releases))

	for _, item := range releases {
		if item.BuildID != buildID {
			continue
		}

		filtered = append(filtered, item)
	}

	return filtered
}

func releaseByID(releases []db.Release, releaseID string) (db.Release, bool) {
	for _, item := range releases {
		if item.ID == releaseID {
			return item, true
		}
	}

	return db.Release{}, false
}

func releaseByVersion(releases []db.Release, version string) (db.Release, bool) {
	for _, item := range releases {
		if item.Version == version {
			return item, true
		}
	}

	return db.Release{}, false
}
