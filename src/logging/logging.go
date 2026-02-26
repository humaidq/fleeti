/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package logging

import (
	stdlog "log"
	"os"
	"sync"
	"time"

	"github.com/charmbracelet/log"
)

const (
	SourceApp        = "app"
	SourceWeb        = "web"
	SourceWebRequest = "web_request"
	SourceDB         = "db"
)

var (
	initOnce   sync.Once
	baseLogger *log.Logger
)

// Init configures the shared application logger.
func Init() {
	initOnce.Do(func() {
		baseLogger = log.NewWithOptions(os.Stdout, log.Options{
			TimeFunction:    log.NowUTC,
			TimeFormat:      time.RFC3339Nano,
			Level:           log.DebugLevel,
			ReportTimestamp: true,
			Formatter:       log.LogfmtFormatter,
		})

		stdLogger := baseLogger.With("source", SourceApp).StandardLog(log.StandardLogOptions{ForceLevel: log.InfoLevel})

		stdlog.SetFlags(0)
		stdlog.SetOutput(stdLogger.Writer())
	})
}

// Logger returns a structured logger tagged with source.
func Logger(source string) *log.Logger {
	Init()

	return baseLogger.With("source", source)
}
