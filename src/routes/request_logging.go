/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"net/http"
	"strings"
	"time"

	"github.com/flamego/flamego"

	"github.com/humaidq/fleeti/v2/logging"
)

var requestLogger = logging.Logger(logging.SourceWebRequest)

// RequestLogger logs request metadata and timing for each HTTP request.
func RequestLogger(c flamego.Context) {
	start := time.Now()

	c.Next()

	status := c.ResponseWriter().Status()
	if status == 0 {
		status = http.StatusOK
	}

	fields := []interface{}{
		"event", "request",
		"status", status,
		"duration_ms", time.Since(start).Milliseconds(),
		"method", c.Request().Method,
		"path", c.Request().URL.Path,
		"ip", clientIP(c),
		"user_agent", c.Request().UserAgent(),
	}

	requestLogger.Info("request", fields...)
}

func clientIP(c flamego.Context) string {
	forwardedFor := c.Request().Header.Get("X-Forwarded-For")
	if forwardedFor != "" {
		if idx := strings.Index(forwardedFor, ","); idx != -1 {
			forwardedFor = forwardedFor[:idx]
		}

		if ip := strings.TrimSpace(forwardedFor); ip != "" {
			return ip
		}
	}

	if realIP := strings.TrimSpace(c.Request().Header.Get("X-Real-IP")); realIP != "" {
		return realIP
	}

	return c.RemoteAddr()
}
