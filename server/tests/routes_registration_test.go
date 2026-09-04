package tests

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/toji339/online-judge/internal/config"
	"github.com/toji339/online-judge/internal/routes"
)

// Route registration had no test until the assist routes were added, and
// it is the one part of the wiring that fails at startup rather than at
// request time: Gin panics when two different wildcard names share a
// path position, which is why the admin problem routes live under
// /api/admin/problems in the first place. Every other suite in this
// package builds its own small router, so nothing was calling Setup at
// all — the first time anyone found out would have been a crash loop on
// a deploy.
//
// It doubles as the check that the API still starts with no optional
// dependency wired: no broker, no Redis, no sandbox, no assist provider.

func testConfig() *config.Config {
	return &config.Config{
		DBName:    "online_judge_test",
		JWTSecret: strings.Repeat("k", 48),
		Port:      "8080",
		ClientURL: "http://localhost:5173",
	}
}

// TestSetupRegistersEveryRouteWithoutConflict is the panic test. Setup
// either returns a router or brings the process down.
func TestSetupRegistersEveryRouteWithoutConflict(t *testing.T) {
	router := routes.Setup(testDB, testConfig(), routes.Deps{})
	require.NotNil(t, router)

	registered := map[string]bool{}
	for _, r := range router.Routes() {
		registered[r.Method+" "+r.Path] = true
	}

	// The assist surface, and one pre-existing route from the same
	// wildcard position, which is what a conflict would have broken.
	for _, route := range []string{
		"GET /api/problems/:slug",
		"GET /api/problems/:slug/assist/state",
		"POST /api/assist/hint",
		"POST /api/assist/explain",
		"POST /api/assist/review",
	} {
		assert.True(t, registered[route], "route not registered: %s", route)
	}
}

// TestSetupStartsWithNoAssistProviderConfigured pins the optional
// dependency rule for the new feature: an API with no ANTHROPIC_API_KEY
// starts and serves exactly as it did before assist existed.
func TestSetupStartsWithNoAssistProviderConfigured(t *testing.T) {
	cfg := testConfig()
	cfg.AnthropicAPIKey = ""
	cfg.AssistEnabled = true

	assert.NotPanics(t, func() {
		require.NotNil(t, routes.Setup(testDB, cfg, routes.Deps{}))
	})
}

// TestSetupStartsWithAssistSwitchedOff covers the other way the feature
// can be absent — the kill switch rather than a missing credential.
func TestSetupStartsWithAssistSwitchedOff(t *testing.T) {
	cfg := testConfig()
	// A non-empty placeholder: the point is that the switch wins
	// even when a credential is present.
	cfg.AnthropicAPIKey = "placeholder-credential"
	cfg.AssistEnabled = false

	assert.NotPanics(t, func() {
		require.NotNil(t, routes.Setup(testDB, cfg, routes.Deps{}))
	})
}
