// Package deps holds the dependency bundle passed to every tool, resource, and
// prompt registration function. It is a leaf package so that internal/mcp can
// import internal/mcp/tools without an import cycle.
package deps

import (
	"log/slog"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/policy"
)

// Deps bundles shared services. All fields are required except Logger
// (nil means slog.Default()).
type Deps struct {
	Discord discord.Service
	Store   *index.Store
	Guard   *policy.Guard
	Config  *config.Config
	Logger  *slog.Logger
}

// Log returns the configured logger or slog.Default().
func (d Deps) Log() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return slog.Default()
}
