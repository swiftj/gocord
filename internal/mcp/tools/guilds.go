package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// ---------- Baseline-tool shared helpers (prefix "bl", owned by mcp-baseline-tools) ----------

// blMaxContent is Discord's message content limit.
const blMaxContent = 2000

// blReadOnly returns annotations for read-only tools.
func blReadOnly() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithOpenWorldHintAnnotation(true),
	}
}

// blMutating returns annotations for write or destructive tools.
func blMutating(destructive, idempotent bool) []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(destructive),
		mcp.WithIdempotentHintAnnotation(idempotent),
		mcp.WithOpenWorldHintAnnotation(true),
	}
}

// blTool builds a tool from a description, annotations, and parameter options.
func blTool(name, desc string, ann []mcp.ToolOption, params ...mcp.ToolOption) mcp.Tool {
	opts := append([]mcp.ToolOption{mcp.WithDescription(desc)}, ann...)
	return mcp.NewTool(name, append(opts, params...)...)
}

// blDryRunParam is the standard dry_run parameter.
func blDryRunParam() mcp.ToolOption {
	return mcp.WithBoolean("dry_run", mcp.DefaultBool(false),
		mcp.Description("Preview the change without applying it."))
}

// blGuildParam is the standard guild_id parameter.
func blGuildParam() mcp.ToolOption {
	return mcp.WithString("guild_id", mcp.Required(),
		mcp.Description("Guild Snowflake or name. Optional when a default guild is configured or the bot is in one guild."))
}

// blRequired reads a non-empty string argument.
func blRequired(req mcp.CallToolRequest, key string) (string, *mcp.CallToolResult) {
	s := strings.TrimSpace(req.GetString(key, ""))
	if s == "" {
		return "", errorf("%s is required", key)
	}
	return s, nil
}

// blGuild resolves guild_id and runs the policy check for a guild-scoped op.
func blGuild(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string, kind int, dry bool) (string, *mcp.CallToolResult) {
	gid, err := resolveGuild(ctx, d, req)
	if err != nil {
		return "", errorResult(err)
	}
	if err := d.Guard.Check(guildOp(tool, gid, kind, dry)); err != nil {
		return "", errorResult(err)
	}
	return gid, nil
}

// blChannel resolves the channel argument key and runs the policy check.
func blChannel(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, key, tool string, kind int, dry bool) (*dto.ChannelInfo, *mcp.CallToolResult) {
	ref, res := blRequired(req, key)
	if res != nil {
		return nil, res
	}
	ch, err := resolveChannel(ctx, d, "", ref)
	if err != nil {
		return nil, errorResult(err)
	}
	if err := d.Guard.Check(channelOp(tool, ch, kind, dry)); err != nil {
		return nil, errorResult(err)
	}
	return ch, nil
}

// blCategory resolves an optional category_id within guildID ("" when absent).
func blCategory(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, guildID string) (string, *mcp.CallToolResult) {
	ref := strings.TrimSpace(req.GetString("category_id", ""))
	if ref == "" {
		return "", nil
	}
	ch, err := resolveChannel(ctx, d, guildID, ref)
	if err != nil {
		return "", errorResult(err)
	}
	if ch.Type != dto.ChannelCategory {
		return "", errorf("category_id %q is a %s channel, not a category", ref, ch.Type)
	}
	return ch.ID, nil
}

// blLabel renders a channel for previews, e.g. "#general (100000000000000010)".
func blLabel(ch *dto.ChannelInfo) string {
	if ch == nil {
		return ""
	}
	return fmt.Sprintf("#%s (%s)", ch.Name, ch.ID)
}

// blCheckContent validates message content length.
func blCheckContent(content string) *mcp.CallToolResult {
	if n := utf8.RuneCountInString(content); n > blMaxContent {
		return errorf("content is %d characters; Discord allows at most %d", n, blMaxContent)
	}
	return nil
}

// blRaw returns the raw argument value for key (nil if absent).
func blRaw(req mcp.CallToolRequest, key string) any { return req.GetArguments()[key] }

// ---------- Guild tools ----------

// RegisterGuildTools registers discord_login, discord_list_servers,
// discord_get_server_info, and discord_governance_audit.
func RegisterGuildTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool("discord_login",
		"Authenticate or switch the active bot profile. Pass a token, or omit it and name a configured profile.",
		blMutating(false, true),
		mcp.WithString("token", mcp.Description("Bot token. Optional when profile names a configured profile; never echoed back.")),
		mcp.WithString("profile", mcp.Description("Named bot profile to activate.")),
	), guildLoginHandler(d))

	s.AddTool(blTool("discord_list_servers",
		"List the guilds the bot belongs to with member counts and owners.",
		blReadOnly(),
	), guildListHandler(d))

	s.AddTool(blTool("discord_get_server_info",
		"Get guild metadata: channels, roles, boost tier, and features.",
		blReadOnly(),
		blGuildParam(),
	), guildInfoHandler(d))

	s.AddTool(blTool("discord_governance_audit",
		"Query audit logs, list AutoMod rules, or time out a member (timeout_member supports dry_run; duration 0 clears).",
		blMutating(true, false),
		mcp.WithString("action", mcp.Required(), mcp.Enum("audit_logs", "automod_rules", "timeout_member"),
			mcp.Description("Operation to perform.")),
		blGuildParam(),
		mcp.WithString("user_id", mcp.Description("Member to time out (timeout_member). Snowflake, @name, or mention.")),
		mcp.WithInteger("action_type", mcp.Description("Audit log action type filter (audit_logs).")),
		mcp.WithInteger("limit", mcp.Min(1), mcp.Max(100), mcp.DefaultNumber(50), mcp.Description("Max audit log entries (1-100).")),
		mcp.WithInteger("duration_seconds", mcp.Min(0), mcp.Max(2419200),
			mcp.Description("Timeout length in seconds (max 28 days); 0 clears an existing timeout.")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		blDryRunParam(),
	), guildAuditHandler(d))
}

func guildLoginHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		token := strings.TrimSpace(req.GetString("token", ""))
		profile := strings.TrimSpace(req.GetString("profile", ""))
		if token == "" {
			if profile == "" {
				return errorf("token is required (or pass profile naming a configured bot profile)"), nil
			}
			if d.Config == nil {
				return errorf("profile %q is not configured; pass token instead", profile), nil
			}
			t, err := d.Config.TokenForProfile(profile, nil)
			if err != nil {
				return errorf("%v; pass token instead", err), nil
			}
			token = t
		}
		info, err := d.Discord.Login(ctx, token, profile)
		if err != nil {
			msg := strings.ReplaceAll(err.Error(), token, "***")
			return errorResult(errors.New(msg)), nil
		}
		return jsonResult(info), nil
	}
}

func guildListHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		gs, err := d.Discord.ListGuilds(ctx)
		if err != nil {
			return errorResult(err), nil
		}
		return listResult(gs, ""), nil
	}
}

func guildInfoHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		gid, res := blGuild(ctx, d, req, "discord_get_server_info", opRead, false)
		if res != nil {
			return res, nil
		}
		g, err := d.Discord.GetGuildInfo(ctx, gid)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(g), nil
	}
}

func guildAuditHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_governance_audit"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action := req.GetString("action", "")
		switch action {
		case "audit_logs":
			gid, res := blGuild(ctx, d, req, tool, opRead, false)
			if res != nil {
				return res, nil
			}
			limit := clamp(req.GetInt("limit", 50), 1, 100)
			entries, err := d.Discord.GetAuditLogs(ctx, gid, req.GetInt("action_type", 0), limit)
			if err != nil {
				return errorResult(err), nil
			}
			return listResult(entries, ""), nil

		case "automod_rules":
			gid, res := blGuild(ctx, d, req, tool, opRead, false)
			if res != nil {
				return res, nil
			}
			rules, err := d.Discord.ListAutoModRules(ctx, gid)
			if err != nil {
				return errorResult(err), nil
			}
			return listResult(rules, ""), nil

		case "timeout_member":
			dry := dryRun(req)
			gid, res := blGuild(ctx, d, req, tool, opDestructive, dry)
			if res != nil {
				return res, nil
			}
			userRef, res := blRequired(req, "user_id")
			if res != nil {
				return res, nil
			}
			if !hasArg(req, "duration_seconds") {
				return errorf("duration_seconds is required for timeout_member (0 clears a timeout)"), nil
			}
			secs := req.GetInt("duration_seconds", 0)
			if secs < 0 || secs > 2419200 {
				return errorf("duration_seconds must be 0-2419200 (28 days), got %d", secs), nil
			}
			uid, err := resolveUser(ctx, d, gid, userRef)
			if err != nil {
				return errorResult(err), nil
			}
			reason := req.GetString("reason", "")
			if dry {
				change := fmt.Sprintf("user %s: clear communication timeout", uid)
				if secs > 0 {
					change = fmt.Sprintf("user %s: timeout for %s", uid, time.Duration(secs)*time.Second)
				}
				if reason != "" {
					change += fmt.Sprintf(" (reason: %s)", reason)
				}
				return dryRunResult(d, guildOp(tool, gid, opDestructive, true), "user "+uid, []string{change}), nil
			}
			if err := d.Discord.TimeoutMember(ctx, gid, uid, secs, reason); err != nil {
				if errors.Is(err, discord.ErrForbidden) {
					return errorf("cannot time out %s: the bot needs ModerateMembers and a role above the member (owners/admins cannot be timed out)", uid), nil
				}
				return errorResult(err), nil
			}
			out := map[string]any{"ok": true, "guild_id": gid, "user_id": uid, "duration_seconds": secs}
			if secs == 0 {
				out["cleared"] = true
			}
			return jsonResult(out), nil
		}
		return errorf("action must be one of audit_logs, automod_rules, timeout_member; got %q", action), nil
	}
}
