/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

// Init initializes the database connection pool.
func Init(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return ErrDatabaseURLEnvVarNotSet
	}

	if err := ensureDatabaseExists(ctx, databaseURL); err != nil {
		return fmt.Errorf("failed to ensure database exists: %w", err)
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	config.MaxConns = 20
	config.MinConns = 2

	pool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// GetPool returns the shared database connection pool.
func GetPool() *pgxpool.Pool {
	return pool
}

// Close closes the database connection pool.
func Close() {
	if pool != nil {
		pool.Close()
	}
}

func ensureDatabaseExists(ctx context.Context, databaseURL string) error {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	dbName := config.Database
	if dbName == "" {
		return ErrDatabaseNameNotSpecified
	}

	config.Database = "postgres"

	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return fmt.Errorf("failed to connect to postgres database: %w", err)
	}

	defer func() {
		if err := conn.Close(ctx); err != nil {
			logger.Warn("failed to close bootstrap database connection", "error", err)
		}
	}()

	var exists bool

	err = conn.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)", dbName).Scan(&exists)
	if err != nil {
		return fmt.Errorf("failed to check if database exists: %w", err)
	}

	if !exists {
		sql := "CREATE DATABASE " + pgx.Identifier{dbName}.Sanitize()

		_, err = conn.Exec(ctx, sql)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			return fmt.Errorf("failed to create database: %w", err)
		}
	}

	return nil
}
