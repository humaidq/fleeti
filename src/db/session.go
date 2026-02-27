/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package db

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/flamego/session"
	"github.com/jackc/pgx/v5"
)

// PostgresSessionConfig contains options for the PostgreSQL session store.
type PostgresSessionConfig struct {
	// Lifetime is the absolute duration a session remains valid after it is first
	// persisted. Session activity does not extend this value.
	// Default is 14 days (336 hours).
	Lifetime time.Duration
	// TableName is the name of the session table. Default is "flamego_sessions".
	TableName string
	// Encoder is the encoder to encode session data. Default is session.GobEncoder.
	Encoder session.Encoder
	// Decoder is the decoder to decode session data. Default is session.GobDecoder.
	Decoder session.Decoder
}

// PostgresSessionStore implements session.Store interface for PostgreSQL.
type PostgresSessionStore struct {
	config   PostgresSessionConfig
	encoder  session.Encoder
	decoder  session.Decoder
	idWriter session.IDWriter
}

// PostgresSessionIniter returns the Initer for the PostgreSQL session store.
func PostgresSessionIniter() session.Initer {
	return func(_ context.Context, args ...interface{}) (session.Store, error) {
		if pool == nil {
			return nil, ErrDatabaseConnectionNotInitialized
		}

		var (
			config   PostgresSessionConfig
			idWriter session.IDWriter
		)

		for _, arg := range args {
			switch value := arg.(type) {
			case PostgresSessionConfig:
				config = value
			case session.IDWriter:
				idWriter = value
			case nil:
				continue
			default:
				return nil, ErrInvalidPostgresSessionIniterArgument
			}
		}

		if config.Lifetime == 0 {
			config.Lifetime = 14 * 24 * time.Hour
		}

		if config.TableName == "" {
			config.TableName = "flamego_sessions"
		}

		if config.Encoder == nil {
			config.Encoder = session.GobEncoder
		}

		if config.Decoder == nil {
			config.Decoder = session.GobDecoder
		}

		if idWriter == nil {
			idWriter = func(http.ResponseWriter, *http.Request, string) {}
		}

		store := &PostgresSessionStore{
			config:   config,
			encoder:  config.Encoder,
			decoder:  config.Decoder,
			idWriter: idWriter,
		}

		return store, nil
	}
}

// Exist returns true if the session with given ID exists and has not expired.
func (s *PostgresSessionStore) Exist(ctx context.Context, sid string) bool {
	var exists bool

	err := pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM `+s.config.TableName+` WHERE id = $1 AND expires_at > NOW())`,
		sid,
	).Scan(&exists)

	return err == nil && exists
}

// Read returns the session with given ID. If no session exists, it returns a
// new empty session with the same ID.
func (s *PostgresSessionStore) Read(ctx context.Context, sid string) (session.Session, error) {
	var data []byte

	err := pool.QueryRow(ctx,
		`SELECT data FROM `+s.config.TableName+` WHERE id = $1 AND expires_at > NOW()`,
		sid,
	).Scan(&data)

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("failed to read session %q: %w", sid, err)
	}

	if errors.Is(err, pgx.ErrNoRows) || len(data) == 0 {
		return session.NewBaseSession(sid, s.encoder, s.idWriter), nil
	}

	sessionData, decodeErr := s.decoder(data)
	if decodeErr == nil {
		return session.NewBaseSessionWithData(sid, s.encoder, s.idWriter, sessionData), nil
	}

	return session.NewBaseSession(sid, s.encoder, s.idWriter), nil
}

// Destroy deletes session with given ID from the session store.
func (s *PostgresSessionStore) Destroy(ctx context.Context, sid string) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM `+s.config.TableName+` WHERE id = $1`,
		sid,
	)
	if err != nil {
		return fmt.Errorf("failed to delete session %q: %w", sid, err)
	}

	return nil
}

// Touch is a no-op to enforce absolute session expiry.
func (s *PostgresSessionStore) Touch(_ context.Context, _ string) error {
	return nil
}

// Save persists session data to the session store.
func (s *PostgresSessionStore) Save(ctx context.Context, sess session.Session) error {
	data, err := sess.Encode()
	if err != nil {
		return fmt.Errorf("failed to encode session %q: %w", sess.ID(), err)
	}

	expiresAt := time.Now().Add(s.config.Lifetime)

	_, err = pool.Exec(ctx,
		`INSERT INTO `+s.config.TableName+` (id, data, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET
			data = EXCLUDED.data,
			expires_at = CASE
				WHEN `+s.config.TableName+`.expires_at <= NOW() THEN EXCLUDED.expires_at
				ELSE `+s.config.TableName+`.expires_at
			END`,
		sess.ID(),
		data,
		expiresAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save session %q: %w", sess.ID(), err)
	}

	return nil
}

// GC performs a garbage collection operation on the session store.
func (s *PostgresSessionStore) GC(ctx context.Context) error {
	_, err := pool.Exec(ctx,
		`DELETE FROM `+s.config.TableName+` WHERE expires_at < NOW()`,
	)
	if err != nil {
		return fmt.Errorf("failed to garbage collect sessions: %w", err)
	}

	return nil
}
