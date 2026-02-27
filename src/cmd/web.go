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

	if err := os.Setenv("DATABASE_URL", databaseURL); err != nil {
		return fmt.Errorf("failed to set DATABASE_URL: %w", err)
	}

	webAuthn, err := routes.NewWebAuthnFromEnv()
	if err != nil {
		return fmt.Errorf("failed to configure WebAuthn: %w", err)
	}

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

	f := flamego.New()
	configureEmptyNotFoundHandler(f)
	f.Use(flamego.Recovery())
	f.Map(webAuthn)
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
	f.Use(routes.NoCacheHeaders())

	fs, err := flamegoTemplate.EmbedFS(templates.Templates, ".", []string{".html"})
	if err != nil {
		return fmt.Errorf("failed to load templates: %w", err)
	}

	f.Use(flamegoTemplate.Templater(flamegoTemplate.Options{
		FileSystem: fs,
	}))
	f.Use(func(data flamegoTemplate.Data, flash session.Flash) {
		if msg, ok := flash.(routes.FlashMessage); ok {
			data["Flash"] = msg
		}
	})
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

	f.Get("/login", routes.LoginForm)
	f.Get("/setup", routes.SetupForm)
	f.Post("/webauthn/login/start", routes.PasskeyLoginStart)
	f.Post("/webauthn/login/finish", routes.PasskeyLoginFinish)
	f.Post("/webauthn/setup/start", routes.SetupStart)
	f.Post("/webauthn/setup/finish", routes.SetupFinish)

	f.Group("", func() {
		f.Post("/logout", routes.Logout)

		f.Get("/", routes.Dashboard)
		f.Get("/deployments/wizard", routes.DeploymentWizardPage)
		f.Get("/deployments/wizard/reset", routes.DeploymentWizardReset)
		f.Get("/deployments/wizard/restart/fleet", routes.DeploymentWizardRestartFromFleet)
		f.Get("/deployments/wizard/restart/profile", routes.DeploymentWizardRestartFromProfile)
		f.Get("/deployments/wizard/restart/build", routes.DeploymentWizardRestartFromBuild)
		f.Post("/deployments/wizard/fleet", routes.DeploymentWizardFleet)
		f.Post("/deployments/wizard/profile", routes.DeploymentWizardProfile)
		f.Post("/deployments/wizard/build", routes.DeploymentWizardBuild)
		f.Post("/deployments/wizard/release", routes.DeploymentWizardRelease)
		f.Post("/deployments/wizard/rollout", routes.DeploymentWizardRollout)

		f.Get("/security", routes.Security)
		f.Post("/webauthn/passkey/start", routes.PasskeyRegistrationStart)
		f.Post("/webauthn/passkey/finish", routes.PasskeyRegistrationFinish)
		f.Post("/security/passkeys/{id}/delete", routes.DeletePasskey)
		f.Post("/security/invites", routes.CreateUserInvite)
		f.Post("/security/invites/{id}/regenerate", routes.RegenerateUserInvite)
		f.Post("/security/invites/{id}/delete", routes.DeleteUserInvite)

		f.Get("/fleets", routes.FleetsPage)
		f.Post("/fleets", routes.CreateFleet)

		f.Get("/profiles", routes.ProfilesPage)
		f.Get("/profiles/new", routes.NewProfilePage)
		f.Post("/profiles", routes.CreateProfile)
		f.Get("/profiles/{id}", routes.ProfilePage)
		f.Get("/profiles/{id}/edit", routes.EditProfilePage)
		f.Get("/profiles/{id}/packages", routes.ProfilePackagesPage)
		f.Post("/profiles/{id}/packages", routes.AddProfilePackage)
		f.Post("/profiles/{id}/packages/remove", routes.RemoveProfilePackage)
		f.Get("/profiles/{id}/kernel", routes.ProfileKernelPage)
		f.Post("/profiles/{id}/kernel", routes.UpdateProfileKernel)
		f.Get("/profiles/{id}/raw-nix", routes.ProfileRawNixPage)
		f.Post("/profiles/{id}/raw-nix", routes.UpdateProfileRawNix)
		f.Post("/profiles/{id}/edit", routes.UpdateProfile)

		f.Get("/builds", routes.BuildsPage)
		f.Post("/builds", routes.CreateBuild)
		f.Get("/builds/{id}", routes.BuildPage)
		f.Post("/builds/{id}/installer", routes.CreateBuildInstaller)
		f.Get("/builds/{id}/installer/logs", routes.BuildInstallerLogPage)
		f.Get("/builds/{id}/installer/logs/live", routes.BuildInstallerLogLive)
		f.Get("/builds/{id}/logs", routes.BuildLogPage)
		f.Get("/builds/{id}/logs/live", routes.BuildLogLive)

		f.Get("/releases", routes.ReleasesPage)
		f.Post("/releases", routes.CreateRelease)
		f.Get("/releases/{id}", routes.ReleasePage)
		f.Post("/releases/{id}/withdraw", routes.WithdrawRelease)

		f.Get("/devices", routes.DevicesPage)
		f.Post("/devices", routes.CreateDevice)

		f.Get("/rollouts", routes.RolloutsPage)
		f.Post("/rollouts", routes.CreateRollout)
		f.Get("/rollouts/{id}", routes.RolloutPage)
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
