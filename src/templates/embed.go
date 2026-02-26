/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package templates

import "embed"

// Templates contains embedded HTML templates from this directory.
//
//go:embed *.html
var Templates embed.FS
