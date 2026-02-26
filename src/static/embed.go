/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package static

import "embed"

// Static contains embedded files from the static directory.
//
//go:embed *
var Static embed.FS
