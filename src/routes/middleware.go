/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"net/http"

	"github.com/flamego/flamego"
)

// NoCacheHeaders disables caching for GET responses.
func NoCacheHeaders() flamego.Handler {
	return func(c flamego.Context) {
		if c.Request().Method == http.MethodGet {
			header := c.ResponseWriter().Header()
			header.Set("Cache-Control", "no-store, max-age=0")
			header.Set("Pragma", "no-cache")
			header.Set("Expires", "0")
		}

		c.Next()
	}
}
