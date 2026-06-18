/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"

	"github.com/humaidq/fleeti/v2/cmd"
	"github.com/humaidq/fleeti/v2/logging"
)

func main() {
	logging.Init()

	logger := logging.Logger(logging.SourceApp)

	app := &cli.Command{
		Name:  "fleeti",
		Usage: "Fleet management control plane",
		Commands: []*cli.Command{
			cmd.CmdStart,
			cmd.CmdMigrate,
		},
	}

	if err := app.Run(context.Background(), os.Args); err != nil {
		logger.Fatal("app run failed", "error", err)
	}
}
