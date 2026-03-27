/*
 * Copyright 2026 Humaid Alqasimi
 * SPDX-License-Identifier: Apache-2.0
 */
package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/flamego/flamego"
	"github.com/google/uuid"

	"github.com/humaidq/fleeti/v2/db"
)

func TestParseAPIBearerTokenAcceptsBearerToken(t *testing.T) {
	t.Parallel()

	token, err := parseAPIBearerToken("Bearer flt_test_key")
	if err != nil {
		t.Fatalf("parseAPIBearerToken returned error: %v", err)
	}

	if token != "flt_test_key" {
		t.Fatalf("expected parsed token to match input, got %q", token)
	}
}

func TestParseAPIBearerTokenAcceptsCaseInsensitivePrefix(t *testing.T) {
	t.Parallel()

	token, err := parseAPIBearerToken("bearer   flt_test_key  ")
	if err != nil {
		t.Fatalf("parseAPIBearerToken returned error: %v", err)
	}

	if token != "flt_test_key" {
		t.Fatalf("expected trimmed token, got %q", token)
	}
}

func TestParseAPIBearerTokenRejectsMissingHeader(t *testing.T) {
	t.Parallel()

	_, err := parseAPIBearerToken("")
	if !errors.Is(err, errAPIAuthorizationMissing) {
		t.Fatalf("expected missing authorization error, got %v", err)
	}
}

func TestParseAPIBearerTokenRejectsMalformedHeader(t *testing.T) {
	t.Parallel()

	_, err := parseAPIBearerToken("Token flt_test_key")
	if !errors.Is(err, errAPIAuthorizationInvalid) {
		t.Fatalf("expected invalid authorization error, got %v", err)
	}
}

func TestRequireAPIUserRejectsMissingAuthorizationHeader(t *testing.T) {
	originalAuthenticate := authenticateUserAPIKey
	t.Cleanup(func() {
		authenticateUserAPIKey = originalAuthenticate
	})

	authenticateUserAPIKey = func(ctx context.Context, rawKey string) (*db.User, error) {
		t.Fatalf("authenticateUserAPIKey should not be called for missing header")
		return nil, nil
	}

	app := flamego.New()
	handlerCalled := false
	app.Group("/api/v1", func() {
		app.Get("/protected", func(user *db.User) {
			handlerCalled = true
		})
	}, RequireAPIUser())

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	app.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d", http.StatusUnauthorized, recorder.Code)
	}

	if handlerCalled {
		t.Fatal("expected protected handler not to run")
	}

	if got := recorder.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("expected WWW-Authenticate header %q, got %q", "Bearer", got)
	}

	if body := recorder.Body.String(); body != "{\"error\":\"API key required\"}\n" {
		t.Fatalf("unexpected response body: %q", body)
	}
}

func TestRequireAPIUserInjectsAuthenticatedUser(t *testing.T) {
	originalAuthenticate := authenticateUserAPIKey
	t.Cleanup(func() {
		authenticateUserAPIKey = originalAuthenticate
	})

	expectedUser := &db.User{
		ID:          uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		DisplayName: "API User",
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}

	authenticateUserAPIKey = func(ctx context.Context, rawKey string) (*db.User, error) {
		if rawKey != "flt_test_key" {
			t.Fatalf("expected bearer token to be parsed, got %q", rawKey)
		}

		return expectedUser, nil
	}

	app := flamego.New()
	app.Group("/api/v1", func() {
		app.Get("/protected", func(user *db.User) {
			if user != expectedUser {
				t.Fatalf("expected injected user pointer to match authenticated user")
			}
		})
	}, RequireAPIUser())

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	req.Header.Set("Authorization", "Bearer flt_test_key")
	app.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, recorder.Code)
	}
}
