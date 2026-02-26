/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"os"

	"github.com/pressly/goose/v3"

	// Register pgx with database/sql for goose migrations.
	_ "github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var embedMigrations embed.FS

// GetEmbeddedMigrations returns the embedded migrations filesystem.
func GetEmbeddedMigrations() embed.FS {
	return embedMigrations
}

// SyncSchema runs database migrations using goose.
func SyncSchema(_ context.Context) error {
	if pool == nil {
		return ErrDatabaseConnectionNotInitialized
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return ErrDatabaseURLEnvVarNotSet
	}

	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("failed to open database for migrations: %w", err)
	}

	defer func() {
		if err := sqlDB.Close(); err != nil {
			logger.Warn("failed to close migration connection", "error", err)
		}
	}()

	goose.SetBaseFS(embedMigrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("failed to set dialect: %w", err)
	}

	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	return nil
}
