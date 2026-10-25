package opencode

import "ai-worker-mcp/internal/config"

func legacyFixtureConfig() config.Config {
	c := config.Default()
	c.Runtime = config.Runtime{Type: "opencode", Mode: "managed", Binary: "opencode", Host: "127.0.0.1"}
	return c
}
