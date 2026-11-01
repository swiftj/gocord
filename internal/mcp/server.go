// Package mcp bootstraps the MCP server and wires every tool, resource, and
// prompt registration. Owned by the Lead Agent.
package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/mcp/prompts"
	"github.com/local/discord-mcp/internal/mcp/resources"
	"github.com/local/discord-mcp/internal/mcp/tools"
)

// Deps is re-exported for callers (cmd/discord-mcp).
type Deps = deps.Deps

// ServerName is the MCP implementation name advertised in initialize.
const ServerName = "discord-mcp"

const instructions = `Discord MCP server. Read discord://guilds/{guild_id}/map before acting on a server.
Any guild/channel/user/role parameter accepts a Snowflake, #name, @name, or mention.
Destructive tools accept dry_run: true — preview first, then apply.
discord_search_messages uses a local FTS5 index with automatic backfill (Discord's search API is unavailable to bots).`

// registrations lists every registration hook in a stable order.
var registrations = []func(*server.MCPServer, deps.Deps){
	tools.RegisterGuildTools,
	tools.RegisterChannelTools,
	tools.RegisterMessageTools,
	tools.RegisterSearchTools,
	tools.RegisterForumThreadTools,
	tools.RegisterReactionPollTools,
	tools.RegisterMemberRoleTools,
	tools.RegisterWebhookPersonaTools,
	tools.RegisterEventTools,
	tools.RegisterInteractionTools,
	resources.Register,
	prompts.Register,
}

// NewServer constructs the MCP server with all tools, resources, and prompts.
func NewServer(d Deps, version string) *server.MCPServer {
	s, _ := New(d, version)
	return s
}

// New constructs the MCP server and the resource-subscription Notifier.
// Feed Gateway events to Notifier.HandleEvent (e.g. via discord.Client.OnEvent)
// so subscribed sessions receive notifications/resources/updated.
func New(d Deps, version string) (*server.MCPServer, *Notifier) {
	var s *server.MCPServer
	n := newNotifier(func(sessionID, uri string) {
		if err := s.SendNotificationToSpecificClient(sessionID, mcp.MethodNotificationResourceUpdated, map[string]any{"uri": uri}); err != nil {
			d.Log().Debug("resource update notification not delivered", "uri", uri, "err", err)
		}
	})
	s = server.NewMCPServer(ServerName, version,
		server.WithToolCapabilities(false),
		server.WithResourceCapabilities(true, true),
		server.WithPromptCapabilities(false),
		server.WithRecovery(),
		server.WithInstructions(instructions),
		server.WithHooks(n.hooks()),
	)
	for _, register := range registrations {
		register(s, d)
	}
	return s, n
}
