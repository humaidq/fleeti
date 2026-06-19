/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/flamego/csrf"
	"github.com/flamego/flamego"
	"github.com/flamego/session"
	flamegoTemplate "github.com/flamego/template"
	"github.com/urfave/cli/v3"

	"github.com/humaidq/fleeti/v2/db"
	"github.com/humaidq/fleeti/v2/routes"
	"github.com/humaidq/fleeti/v2/static"
	"github.com/humaidq/fleeti/v2/templates"
)

// CmdStart defines the command that starts the web server.
var CmdStart = &cli.Command{
	Name:    "start",
	Aliases: []string{"run"},
	Usage:   "Start the web server",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "port",
			Value: "8080",
			Usage: "the web server port",
		},
		&cli.StringFlag{
			Name:    "database-url",
			Sources: cli.EnvVars("DATABASE_URL"),
			Usage:   "PostgreSQL connection string (e.g., postgres://user:pass@localhost/dbname)",
		},
	},
	Action: start,
}

func start(ctx context.Context, cmd *cli.Command) error {
	databaseURL := cmd.String("database-url")
	if databaseURL == "" {
		return errDatabaseURLRequired
	}

	csrfSecret := os.Getenv("CSRF_SECRET")
	if csrfSecret == "" {
		return errCSRFSecretRequired
	}

	if err := os.Setenv("DATABASE_URL", databaseURL); err != nil {
		return fmt.Errorf("failed to set DATABASE_URL: %w", err)
	}

	webAuthn, err := routes.NewWebAuthnFromEnv()
	if err != nil {
		return fmt.Errorf("failed to configure WebAuthn: %w", err)
	}

	profileWizardAI := routes.NewProfileWizardAIFromEnv()

	appLogger.Info("connecting to database")

	if err := db.Init(ctx); err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	defer db.Close()

	appLogger.Info("syncing database schema")

	if err := db.SyncSchema(ctx); err != nil {
		return fmt.Errorf("failed to sync schema: %w", err)
	}

	recoveredBuilds, err := db.FailRunningBuilds(ctx)
	if err != nil {
		return fmt.Errorf("failed to recover interrupted builds: %w", err)
	}

	if recoveredBuilds > 0 {
		appLogger.Warn("recovered interrupted builds", "count", recoveredBuilds)
	}

	recoveredInstallerBuilds, err := db.FailRunningBuildInstallers(ctx)
	if err != nil {
		return fmt.Errorf("failed to recover interrupted installer builds: %w", err)
	}

	if recoveredInstallerBuilds > 0 {
		appLogger.Warn("recovered interrupted installer builds", "count", recoveredInstallerBuilds)
	}

	if err := routes.RecoverQueuedBuildExecutions(ctx); err != nil {
		return fmt.Errorf("failed to recover queued builds: %w", err)
	}

	if err := routes.InitializeKernelOptionsCache(ctx); err != nil {
		appLogger.Warn("failed to initialize kernel options cache", "error", err)
	}

	f := flamego.New()
	configureEmptyNotFoundHandler(f)
	f.Use(flamego.Recovery())
	f.Map(webAuthn)
	f.Map(profileWizardAI)
	f.Use(session.Sessioner(session.Options{
		Initer: db.PostgresSessionIniter(),
		Config: db.PostgresSessionConfig{
			Lifetime:  14 * 24 * time.Hour,
			TableName: "flamego_sessions",
		},
		Cookie: session.CookieOptions{
			MaxAge:   14 * 24 * 60 * 60,
			HTTPOnly: true,
			SameSite: http.SameSiteLaxMode,
		},
	}))
	f.Use(routes.RequestLogger)
	f.Use(csrf.Csrfer(csrf.Options{Secret: csrfSecret}))
	f.Use(routes.NoCacheHeaders())

	fs, err := flamegoTemplate.EmbedFS(templates.Templates, ".", []string{".html"})
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	f.Use(flamegoTemplate.Templater(flamegoTemplate.Options{
		FileSystem: fs,
	}))
	appVersion := BuildDisplayVersion()
	f.Use(func(data flamegoTemplate.Data, flash session.Flash) {
		data["AppVersion"] = appVersion

		if msg, ok := flash.(routes.FlashMessage); ok {
			data["Flash"] = msg
		}
	})
	f.Use(routes.CSRFInjector())
	f.Use(routes.UserContextInjector())

	f.Use(flamego.Static(flamego.StaticOptions{
		FileSystem: http.FS(static.Static),
	}))

	workingDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to resolve working directory: %w", err)
	}

	updatesDir := filepath.Join(workingDir, "updates")
	if err := os.MkdirAll(updatesDir, 0o750); err != nil {
		return fmt.Errorf("failed to create updates directory: %w", err)
	}

	f.Use(routes.DynamicSHA256SUMS(updatesDir))

	f.Use(flamego.Static(flamego.StaticOptions{
		Directory: updatesDir,
		Prefix:    "/update",
	}))

	appLogger.Info("serving update artifacts", "directory", updatesDir, "prefix", "/update")

	f.Get("/connectivity", routes.Connectivity)
	f.Get("/healthz", routes.Healthz)
	f.Group("/api/v1", func() {
		f.Get("/profiles", routes.APIProfiles)
		f.Get("/profiles/{id}", routes.APIProfile)
		f.Get("/profiles/{id}/builds", routes.APIProfileBuilds)
		f.Get("/profiles/{id}/builds/{buildId}", routes.APIProfileBuild)
		f.Get("/profiles/{id}/builds/{buildId}/logs", routes.APIProfileBuildLogs)
		f.Post("/profiles/{id}/builds", routes.APICreateProfileBuild)
		f.Put("/profiles/{id}", routes.APIReplaceProfile)
		f.Patch("/profiles/{id}", routes.APIPatchProfile)
	}, routes.RequireAPIUser())

	// Unauthenticated device bootstrap endpoints: a pending enrollment grants
	// nothing until an administrator claims its code.
	f.Group("/api/v1/device", func() {
		f.Post("/enroll/start", routes.AgentEnrollStart)
		f.Post("/enroll/poll", routes.AgentEnrollPoll)
	})

	// Device-token authenticated agent endpoints.
	f.Group("/api/v1/device", func() {
		f.Post("/telemetry", routes.AgentTelemetry)
		f.Get("/commands", routes.AgentCommands)
		f.Post("/commands/{id}/result", routes.AgentCommandResult)
	}, routes.RequireDeviceAuth())

	f.Get("/login", routes.LoginForm)
	f.Get("/setup", routes.SetupForm)
	f.Post("/webauthn/login/start", csrf.Validate, routes.PasskeyLoginStart)
	f.Post("/webauthn/login/finish", csrf.Validate, routes.PasskeyLoginFinish)
	f.Post("/webauthn/setup/start", csrf.Validate, routes.SetupStart)
	f.Post("/webauthn/setup/finish", csrf.Validate, routes.SetupFinish)

	f.Group("", func() {
		f.Post("/logout", csrf.Validate, routes.Logout)

		f.Get("/", routes.Dashboard)

		f.Get("/security", routes.Security)
		f.Post("/webauthn/passkey/start", csrf.Validate, routes.PasskeyRegistrationStart)
		f.Post("/webauthn/passkey/finish", csrf.Validate, routes.PasskeyRegistrationFinish)
		f.Post("/security/passkeys/{id}/delete", csrf.Validate, routes.DeletePasskey)
		f.Post("/security/api-keys", csrf.Validate, routes.CreateAPIKey)
		f.Post("/security/api-keys/{id}/delete", csrf.Validate, routes.DeleteAPIKey)
		f.Get("/users", routes.UsersPage)
		f.Post("/users/{id}/reset", csrf.Validate, routes.CreateUserResetLink)
		f.Post("/users/{id}/delete", csrf.Validate, routes.DeleteUser)
		f.Post("/users/invites", csrf.Validate, routes.CreateUserInvite)
		f.Post("/users/invites/{id}/regenerate", csrf.Validate, routes.RegenerateUserInvite)
		f.Post("/users/invites/{id}/delete", csrf.Validate, routes.DeleteUserInvite)

		f.Get("/fleets", routes.FleetsPage)
		f.Post("/fleets", csrf.Validate, routes.CreateFleet)
		f.Get("/fleets/{id}", routes.FleetPage)
		f.Get("/fleets/{id}/access", routes.FleetAccessPage)
		f.Post("/fleets/{id}/edit", csrf.Validate, routes.UpdateFleet)
		f.Post("/fleets/{id}/users", csrf.Validate, routes.AddFleetUser)
		f.Post("/fleets/{id}/users/{user_id}/delete", csrf.Validate, routes.RemoveFleetUser)
		f.Post("/fleets/{id}/delete", csrf.Validate, routes.DeleteFleet)

		f.Get("/profiles", routes.ProfilesPage)
		f.Get("/profiles/new", routes.NewProfilePage)
		f.Get("/profiles/wizard", routes.ProfileWizardPage)
		f.Post("/profiles", csrf.Validate, routes.CreateProfile)
		f.Post("/profiles/wizard/chat", csrf.Validate, routes.ProfileWizardChat)
		f.Post("/profiles/wizard/apply", csrf.Validate, routes.ProfileWizardApply)
		f.Post("/profiles/wizard/discard", csrf.Validate, routes.ProfileWizardDiscard)
		f.Get("/profiles/{id}", routes.ProfilePage)
		f.Get("/profiles/{id}/wizard", routes.ProfileWizardPage)
		f.Post("/profiles/{id}/wizard/chat", csrf.Validate, routes.ProfileWizardChat)
		f.Post("/profiles/{id}/wizard/apply", csrf.Validate, routes.ProfileWizardApply)
		f.Post("/profiles/{id}/wizard/discard", csrf.Validate, routes.ProfileWizardDiscard)
		f.Get("/profiles/{id}/deployments", routes.ProfileDeploymentsPage)
		f.Get("/profiles/{id}/edit", routes.EditProfilePage)
		f.Get("/profiles/{id}/security", routes.ProfileSecurityPage)
		f.Get("/profiles/{id}/secure-boot", routes.ProfileSecureBootPage)
		f.Get("/profiles/{id}/secure-boot/certificate", routes.ProfileSecureBootCertificate)
		f.Get("/profiles/{id}/packages", routes.ProfilePackagesPage)
		f.Post("/profiles/{id}/security", csrf.Validate, routes.UpdateProfileSecurity)
		f.Post("/profiles/{id}/packages", csrf.Validate, routes.AddProfilePackage)
		f.Post("/profiles/{id}/packages/remove", csrf.Validate, routes.RemoveProfilePackage)
		f.Get("/profiles/{id}/kernel", routes.ProfileKernelPage)
		f.Post("/profiles/{id}/kernel", csrf.Validate, routes.UpdateProfileKernel)
		f.Get("/profiles/{id}/openclaw", routes.ProfileOpenClawPage)
		f.Post("/profiles/{id}/openclaw", csrf.Validate, routes.UpdateProfileOpenClaw)
		f.Get("/profiles/{id}/raw-nix", routes.ProfileRawNixPage)
		f.Post("/profiles/{id}/raw-nix", csrf.Validate, routes.UpdateProfileRawNix)
		f.Get("/profiles/{id}/builds/{build_id}", routes.ProfileBuildPage)
		f.Post("/profiles/{id}/builds", csrf.Validate, routes.CreateProfileBuild)
		f.Post("/profiles/{id}/builds/{build_id}/delete", csrf.Validate, routes.DeleteProfileBuild)
		f.Get("/profiles/{id}/releases/{release_id}", routes.ProfileReleasePage)
		f.Post("/profiles/{id}/releases", csrf.Validate, routes.CreateProfileRelease)
		f.Post("/profiles/{id}/releases/{release_id}/delete", csrf.Validate, routes.DeleteProfileRelease)
		f.Get("/profiles/{id}/rollouts/{rollout_id}", routes.ProfileRolloutPage)
		f.Post("/profiles/{id}/rollouts", csrf.Validate, routes.CreateProfileRollout)
		f.Post("/profiles/{id}/rollouts/{rollout_id}/delete", csrf.Validate, routes.DeleteProfileRollout)
		f.Post("/profiles/{id}/edit", csrf.Validate, routes.UpdateProfile)
		f.Post("/profiles/{id}/users", csrf.Validate, routes.AddProfileUser)
		f.Post("/profiles/{id}/users/{user_id}/delete", csrf.Validate, routes.RemoveProfileUser)
		f.Post("/profiles/{id}/delete", csrf.Validate, routes.DeleteProfile)

		f.Get("/builds", routes.BuildsPage)
		f.Post("/builds", csrf.Validate, routes.CreateBuild)
		f.Get("/builds/{id}", routes.BuildPage)
		f.Post("/builds/{id}/installer", csrf.Validate, routes.CreateBuildInstaller)
		f.Get("/builds/{id}/installer/logs", routes.BuildInstallerLogPage)
		f.Get("/builds/{id}/installer/logs/live", routes.BuildInstallerLogLive)
		f.Get("/builds/{id}/logs", routes.BuildLogPage)
		f.Get("/builds/{id}/logs/live", routes.BuildLogLive)
		f.Post("/builds/{id}/delete", csrf.Validate, routes.DeleteBuild)

		f.Get("/devices", routes.DevicesPage)
		f.Post("/devices/pair", csrf.Validate, routes.PairDevice)
		f.Get("/devices/{id}", routes.DeviceDetailPage)
		f.Post("/devices/{id}/edit", csrf.Validate, routes.UpdateDevice)
		f.Post("/devices/{id}/force-update", csrf.Validate, routes.DeviceForceUpdate)
		f.Post("/devices/{id}/reboot", csrf.Validate, routes.DeviceReboot)
		f.Post("/devices/{id}/delete", csrf.Validate, routes.DeleteDevice)
	}, routes.RequireAuth)

	port := cmd.String("port")

	appLogger.Info("starting web server", "port", port)

	srv := &http.Server{
		Addr:              "0.0.0.0:" + port,
		Handler:           f,
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web server failed: %w", err)
	}

	return nil
}

func configureEmptyNotFoundHandler(f *flamego.Flame) {
	f.NotFound(func(c flamego.Context) {
		c.ResponseWriter().WriteHeader(http.StatusNotFound)
	})
}
