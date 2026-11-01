// Package tools implements every discord_* MCP tool.
//
// helpers.go is owned by the Lead Agent and shared by all tool files. Wave 2
// teammates MUST reuse these helpers and MUST NOT redefine them. Name any
// file-private helper with a file-specific prefix (e.g. searchBackfill,
// personaWebhookFor) to avoid collisions inside this shared package.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

// ---------- Results ----------

// jsonResult marshals v as compact JSON into a text tool result.
func jsonResult(v any) *mcp.CallToolResult {
	b, err := json.Marshal(v)
	if err != nil {
		return mcp.NewToolResultError("internal: marshal result: " + err.Error())
	}
	return mcp.NewToolResultText(string(b))
}

// listResult wraps a slice with a count and optional next cursor.
func listResult[T any](items []T, nextCursor string) *mcp.CallToolResult {
	if items == nil {
		items = []T{}
	}
	return jsonResult(struct {
		Count      int    `json:"count"`
		Items      []T    `json:"items"`
		NextCursor string `json:"next_cursor,omitempty"`
	}{len(items), items, nextCursor})
}

// errorResult converts err into an actionable tool error result.
func errorResult(err error) *mcp.CallToolResult {
	var de *policy.DeniedError
	switch {
	case errors.As(err, &de):
		return mcp.NewToolResultError("denied by policy: " + de.Reason)
	case errors.Is(err, discord.ErrAmbiguous):
		return mcp.NewToolResultError(err.Error() + " — pass the Snowflake ID instead")
	case errors.Is(err, discord.ErrNotLoggedIn):
		return mcp.NewToolResultError("not logged in: set DISCORD_BOT_TOKEN or call discord_login")
	case errors.Is(err, discord.ErrForbidden):
		return mcp.NewToolResultError(err.Error() + " — check the bot's role permissions and hierarchy")
	case errors.Is(err, discord.ErrRateLimited):
		return mcp.NewToolResultError(err.Error() + " — retry shortly")
	}
	return mcp.NewToolResultError(err.Error())
}

// errorf returns a formatted tool error result (for validation failures).
func errorf(format string, a ...any) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, a...))
}

// ---------- Arguments ----------

// hasArg reports whether key was supplied by the caller (even if empty/false).
func hasArg(req mcp.CallToolRequest, key string) bool {
	_, ok := req.GetArguments()[key]
	return ok
}

// optString returns a pointer to the string arg, or nil if absent.
func optString(req mcp.CallToolRequest, key string) *string {
	if !hasArg(req, key) {
		return nil
	}
	s := req.GetString(key, "")
	return &s
}

// optBool returns a pointer to the bool arg, or nil if absent.
func optBool(req mcp.CallToolRequest, key string) *bool {
	if !hasArg(req, key) {
		return nil
	}
	b := req.GetBool(key, false)
	return &b
}

// dryRun reports whether dry_run=true was passed.
func dryRun(req mcp.CallToolRequest) bool { return req.GetBool("dry_run", false) }

// objectArg decodes an object argument (e.g. "embed") into out. Absent → (false, nil).
func objectArg(req mcp.CallToolRequest, key string, out any) (bool, error) {
	v, ok := req.GetArguments()[key]
	if !ok || v == nil {
		return false, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		return false, fmt.Errorf("%s: invalid object: %w", key, err)
	}
	return true, nil
}

// cursorArg reads an optional Snowflake-or-ISO-8601 argument and returns a Snowflake.
func cursorArg(req mcp.CallToolRequest, key string) (string, error) {
	s, err := dto.NormalizeCursor(req.GetString(key, ""))
	if err != nil {
		return "", fmt.Errorf("%s: %w", key, err)
	}
	return s, nil
}

// clamp bounds n to [lo, hi].
func clamp(n, lo, hi int) int { return max(lo, min(n, hi)) }

// ---------- Resolution ----------

// resolveGuild resolves the "guild_id" argument (Snowflake or name). When it is
// absent it falls back to DISCORD_MCP_DEFAULT_GUILD_ID, then to the bot's only guild.
func resolveGuild(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) (string, error) {
	return resolveGuildRef(ctx, d, req.GetString("guild_id", ""))
}

func resolveGuildRef(ctx context.Context, d deps.Deps, ref string) (string, error) {
	if ref != "" {
		return d.Discord.ResolveGuildID(ctx, ref)
	}
	if d.Config != nil && d.Config.DefaultGuildID != "" {
		return d.Config.DefaultGuildID, nil
	}
	gs, err := d.Discord.ListGuilds(ctx)
	if err != nil {
		return "", err
	}
	if len(gs) == 1 {
		return gs[0].ID, nil
	}
	return "", fmt.Errorf("guild_id is required (bot is in %d guilds and no default guild is configured)", len(gs))
}

// resolveChannel resolves a channel/thread reference (Snowflake, #name, <#id>, name)
// within guildID ("" searches all guilds) and returns its full info.
func resolveChannel(ctx context.Context, d deps.Deps, guildID, ref string) (*dto.ChannelInfo, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, errors.New("channel reference is required")
	}
	id, err := d.Discord.ResolveChannelID(ctx, guildID, ref)
	if err != nil {
		return nil, err
	}
	return d.Discord.GetChannel(ctx, id)
}

// resolveUser resolves a user reference (Snowflake, @name, <@id>, name).
func resolveUser(ctx context.Context, d deps.Deps, guildID, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", errors.New("user reference is required")
	}
	return d.Discord.ResolveUserID(ctx, guildID, ref)
}

// resolveRole resolves a role reference (Snowflake, @name, <@&id>, name) via ListRoles.
func resolveRole(ctx context.Context, d deps.Deps, guildID, ref string) (string, error) {
	r := discord.ParseRef(ref)
	roles, err := d.Discord.ListRoles(ctx, guildID)
	if err != nil {
		return "", err
	}
	var hits []string
	for _, role := range roles {
		if (r.ID != "" && role.ID == r.ID) || (r.ID == "" && discord.NameMatches(r.Name, role.Name)) {
			hits = append(hits, role.ID)
		}
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("role %q: %w", ref, discord.ErrNotFound)
	case 1:
		return hits[0], nil
	}
	return "", fmt.Errorf("role %q matches %v: %w", ref, hits, discord.ErrAmbiguous)
}

// ---------- Policy ----------

// Op kinds for channelOp / guildOp.
const (
	opRead = iota
	opWrite
	opDestructive
)

// channelOp builds a policy.Op scoped to a resolved channel (thread parent included).
func channelOp(tool string, ch *dto.ChannelInfo, kind int, dry bool) policy.Op {
	op := policy.Op{Tool: tool, Write: kind >= opWrite, Destructive: kind == opDestructive, DryRun: dry}
	if ch != nil {
		op.GuildID = ch.GuildID
		op.ChannelID = ch.ID
		if ch.Type == dto.ChannelThread {
			op.ParentChannelID = ch.ParentID
		}
	}
	return op
}

// guildOp builds a policy.Op scoped to a guild.
func guildOp(tool, guildID string, kind int, dry bool) policy.Op {
	return policy.Op{Tool: tool, GuildID: guildID, Write: kind >= opWrite, Destructive: kind == opDestructive, DryRun: dry}
}

// dryRunResult returns the DryRunPreview for op without mutating anything.
func dryRunResult(d deps.Deps, op policy.Op, target string, changes []string, warnings ...string) *mcp.CallToolResult {
	return jsonResult(d.Guard.Preview(op, target, changes, warnings...))
}

// ---------- Permissions & colours ----------

// permissionBits maps Discord permission names to bit flags.
var permissionBits = map[string]int64{
	"CreateInstantInvite": 1 << 0, "KickMembers": 1 << 1, "BanMembers": 1 << 2, "Administrator": 1 << 3,
	"ManageChannels": 1 << 4, "ManageGuild": 1 << 5, "AddReactions": 1 << 6, "ViewAuditLog": 1 << 7,
	"PrioritySpeaker": 1 << 8, "Stream": 1 << 9, "ViewChannel": 1 << 10, "SendMessages": 1 << 11,
	"SendTTSMessages": 1 << 12, "ManageMessages": 1 << 13, "EmbedLinks": 1 << 14, "AttachFiles": 1 << 15,
	"ReadMessageHistory": 1 << 16, "MentionEveryone": 1 << 17, "UseExternalEmojis": 1 << 18, "ViewGuildInsights": 1 << 19,
	"Connect": 1 << 20, "Speak": 1 << 21, "MuteMembers": 1 << 22, "DeafenMembers": 1 << 23,
	"MoveMembers": 1 << 24, "UseVAD": 1 << 25, "ChangeNickname": 1 << 26, "ManageNicknames": 1 << 27,
	"ManageRoles": 1 << 28, "ManageWebhooks": 1 << 29, "ManageGuildExpressions": 1 << 30, "UseApplicationCommands": 1 << 31,
	"RequestToSpeak": 1 << 32, "ManageEvents": 1 << 33, "ManageThreads": 1 << 34, "CreatePublicThreads": 1 << 35,
	"CreatePrivateThreads": 1 << 36, "UseExternalStickers": 1 << 37, "SendMessagesInThreads": 1 << 38, "UseEmbeddedActivities": 1 << 39,
	"ModerateMembers": 1 << 40, "ViewCreatorMonetizationAnalytics": 1 << 41, "UseSoundboard": 1 << 42, "CreateGuildExpressions": 1 << 43,
	"CreateEvents": 1 << 44, "UseExternalSounds": 1 << 45, "SendVoiceMessages": 1 << 46, "SendPolls": 1 << 49,
	"UseExternalApps": 1 << 50, "PinMessages": 1 << 51,
}

// parsePermissions accepts a decimal bitfield string, a number, a single name,
// a comma-separated list of names, or an array of names (case-insensitive).
func parsePermissions(v any) (int64, error) {
	switch t := v.(type) {
	case nil:
		return 0, nil
	case float64:
		return int64(t), nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, nil
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, nil
		}
		return parsePermissionNames(strings.Split(s, ","))
	case []any:
		names := make([]string, 0, len(t))
		for _, x := range t {
			s, ok := x.(string)
			if !ok {
				return 0, fmt.Errorf("permission list must contain strings, got %T", x)
			}
			names = append(names, s)
		}
		return parsePermissionNames(names)
	case []string:
		return parsePermissionNames(t)
	}
	return 0, fmt.Errorf("unsupported permissions value %T", v)
}

func parsePermissionNames(names []string) (int64, error) {
	var bits int64
	for _, raw := range names {
		n := strings.TrimSpace(raw)
		if n == "" {
			continue
		}
		found := false
		for name, bit := range permissionBits {
			if strings.EqualFold(name, n) {
				bits |= bit
				found = true
				break
			}
		}
		if !found {
			return 0, fmt.Errorf("unknown permission %q", n)
		}
	}
	return bits, nil
}

// permissionNames returns the sorted names of set bits.
func permissionNames(bits int64) []string {
	var out []string
	for name, bit := range permissionBits {
		if bits&bit != 0 {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// parseColor accepts "#RRGGBB", "RRGGBB", "0xRRGGBB", or a number.
func parseColor(v any) (int, error) {
	switch t := v.(type) {
	case float64:
		return int(t), nil
	case string:
		s := strings.TrimSpace(t)
		s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(s, "#"), "0x"), "0X")
		n, err := strconv.ParseInt(s, 16, 32)
		if err != nil || n < 0 || n > 0xFFFFFF {
			if d, derr := strconv.Atoi(strings.TrimSpace(t)); derr == nil && d >= 0 && d <= 0xFFFFFF {
				return d, nil
			}
			return 0, fmt.Errorf("invalid color %q (want #RRGGBB)", t)
		}
		return int(n), nil
	}
	return 0, fmt.Errorf("unsupported color value %T", v)
}

// containsFold reports whether list contains s case-insensitively.
func containsFold(list []string, s string) bool {
	return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, s) })
}
