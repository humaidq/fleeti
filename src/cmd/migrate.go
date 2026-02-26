/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package cmd

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/pressly/goose/v3"
	"github.com/urfave/cli/v3"

	"github.com/humaidq/fleeti/v2/db"

	// Register pgx with database/sql for goose migrations.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// CmdMigrate defines database migration subcommands.
var CmdMigrate = &cli.Command{
	Name:  "migrate",
	Usage: "Database migration commands",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "database-url",
			Sources: cli.EnvVars("DATABASE_URL"),
			Usage:   "PostgreSQL connection string (e.g., postgres://user:pass@localhost/dbname)",
		},
	},
	Commands: []*cli.Command{
		{
			Name:   "up",
			Usage:  "Run all pending migrations",
			Action: migrateUp,
		},
		{
			Name:   "down",
			Usage:  "Roll back the last migration",
			Action: migrateDown,
		},
		{
			Name:   "status",
			Usage:  "Show migration status",
			Action: migrateStatus,
		},
		{
			Name:  "create",
			Usage: "Create a new migration file <name>",
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "sql",
					Usage: "Create a SQL migration (default)",
					Value: true,
				},
			},
			Action: migrateCreate,
		},
		{
			Name:   "version",
			Usage:  "Print the current version of the database",
			Action: migrateVersion,
		},
	},
}

func getDB(ctx context.Context, cmd *cli.Command) (*sql.DB, error) {
	databaseURL := cmd.String("database-url")
	if databaseURL == "" {
		return nil, errDatabaseURLRequired
	}

	if err := os.Setenv("DATABASE_URL", databaseURL); err != nil {
		return nil, fmt.Errorf("failed to set DATABASE_URL: %w", err)
	}

	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := sqlDB.PingContext(ctx); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to ping database: %w (close error: %w)", err, closeErr)
		}

		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	goose.SetBaseFS(db.GetEmbeddedMigrations())

	if err := goose.SetDialect("postgres"); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			return nil, fmt.Errorf("failed to set dialect: %w (close error: %w)", err, closeErr)
		}

		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}

	return sqlDB, nil
}

func migrateUp(ctx context.Context, cmd *cli.Command) error {
	sqlDB, err := getDB(ctx, cmd)
	if err != nil {
		return err
	}

	defer func() {
		if err := sqlDB.Close(); err != nil {
			appLogger.Warn("failed to close migration database", "error", err)
		}
	}()

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	fmt.Println("Migrations completed successfully")

	return nil
}

func migrateDown(ctx context.Context, cmd *cli.Command) error {
	sqlDB, err := getDB(ctx, cmd)
	if err != nil {
		return err
	}

	defer func() {
		if err := sqlDB.Close(); err != nil {
			appLogger.Warn("failed to close migration database", "error", err)
		}
	}()

	if err := goose.Down(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("failed to roll back migration: %w", err)
	}

	fmt.Println("Migration rolled back successfully")

	return nil
}

func migrateStatus(ctx context.Context, cmd *cli.Command) error {
	sqlDB, err := getDB(ctx, cmd)
	if err != nil {
		return err
	}

	defer func() {
		if err := sqlDB.Close(); err != nil {
			appLogger.Warn("failed to close migration database", "error", err)
		}
	}()

	if err := goose.Status(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("failed to get migration status: %w", err)
	}

	return nil
}

func migrateVersion(ctx context.Context, cmd *cli.Command) error {
	sqlDB, err := getDB(ctx, cmd)
	if err != nil {
		return err
	}

	defer func() {
		if err := sqlDB.Close(); err != nil {
			appLogger.Warn("failed to close migration database", "error", err)
		}
	}()

	version, err := goose.GetDBVersion(sqlDB)
	if err != nil {
		return fmt.Errorf("failed to get database version: %w", err)
	}

	fmt.Printf("Database version: %d\n", version)

	return nil
}

func migrateCreate(_ context.Context, cmd *cli.Command) error {
	args := cmd.Args()
	if args.Len() < 1 {
		return errMigrationNameRequired
	}

	name := args.First()

	migrationsDir := "db/migrations"
	if err := os.MkdirAll(migrationsDir, 0o750); err != nil {
		return fmt.Errorf("failed to create migrations directory: %w", err)
	}

	if err := goose.Create(nil, migrationsDir, name, "sql"); err != nil {
		return fmt.Errorf("failed to create migration: %w", err)
	}

	fmt.Printf("Created new migration in %s/\n", migrationsDir)

	return nil
}
