package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// chanPermsParam declares a permissions parameter accepting names or a bitfield string.
func chanPermsParam(name, desc string) mcp.ToolOption {
	return mcp.WithAny(name, mcp.Description(desc), func(m map[string]any) {
		m["type"] = []string{"array", "string"}
		m["items"] = map[string]any{"type": "string"}
	})
}

// RegisterChannelTools registers channel and category CRUD plus permission overwrites.
func RegisterChannelTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool("discord_create_text_channel",
		"Create a text channel, or an announcement channel when announcement is true.",
		blMutating(false, false),
		blGuildParam(),
		mcp.WithString("name", mcp.Required(), mcp.MaxLength(100), mcp.Description("Channel name.")),
		mcp.WithString("category_id", mcp.Description("Parent category (Snowflake or name).")),
		mcp.WithString("topic", mcp.MaxLength(1024), mcp.Description("Channel topic.")),
		mcp.WithBoolean("announcement", mcp.DefaultBool(false), mcp.Description("Create an announcement channel.")),
	), chanCreateHandler(d, "discord_create_text_channel"))

	s.AddTool(blTool("discord_create_voice_channel",
		"Create a voice channel, or a stage channel when stage is true.",
		blMutating(false, false),
		blGuildParam(),
		mcp.WithString("name", mcp.Required(), mcp.MaxLength(100), mcp.Description("Channel name.")),
		mcp.WithString("category_id", mcp.Description("Parent category (Snowflake or name).")),
		mcp.WithInteger("bitrate", mcp.Min(8000), mcp.Max(384000), mcp.Description("Bitrate in bits per second.")),
		mcp.WithInteger("user_limit", mcp.Min(0), mcp.Max(99), mcp.Description("Max connected users (0 = unlimited).")),
		mcp.WithBoolean("stage", mcp.DefaultBool(false), mcp.Description("Create a stage channel.")),
	), chanCreateHandler(d, "discord_create_voice_channel"))

	s.AddTool(blTool("discord_create_category",
		"Create a channel category.",
		blMutating(false, false),
		blGuildParam(),
		mcp.WithString("name", mcp.Required(), mcp.MaxLength(100), mcp.Description("Category name.")),
		mcp.WithInteger("position", mcp.Min(0), mcp.Description("Sort position.")),
	), chanCreateHandler(d, "discord_create_category"))

	s.AddTool(blTool("discord_edit_channel",
		"Change a channel's name, topic, parent category, or (threads) archive status. Only provided fields change.",
		blMutating(true, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel Snowflake, #name, or mention.")),
		mcp.WithString("name", mcp.MaxLength(100), mcp.Description("New name.")),
		mcp.WithString("topic", mcp.MaxLength(1024), mcp.Description("New topic (empty string clears it).")),
		mcp.WithString("category_id", mcp.Description("New parent category (empty string removes it).")),
		mcp.WithBoolean("archived", mcp.Description("Archive or unarchive (threads only).")),
		blDryRunParam(),
	), chanEditHandler(d))

	s.AddTool(blTool("discord_delete_channel",
		"Permanently delete a channel, category, or thread. Preview with dry_run first.",
		blMutating(true, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel Snowflake, #name, or mention.")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		blDryRunParam(),
	), chanDeleteHandler(d))

	s.AddTool(blTool("discord_set_channel_permissions",
		"Set or remove a role/member permission overwrite on a channel. Permissions accept names or a decimal bitfield string.",
		blMutating(true, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel Snowflake, #name, or mention.")),
		mcp.WithString("target_id", mcp.Required(), mcp.Description("Role or member (Snowflake, @name, or mention).")),
		mcp.WithString("target_type", mcp.Required(), mcp.Enum(dto.TargetRole, dto.TargetMember), mcp.Description("Overwrite target type.")),
		chanPermsParam("allow_permissions", `Permissions to allow, e.g. ["ViewChannel","SendMessages"] or "3072".`),
		chanPermsParam("deny_permissions", `Permissions to deny, e.g. ["SendMessages"] or "2048".`),
		mcp.WithBoolean("remove", mcp.DefaultBool(false), mcp.Description("Remove the overwrite entirely.")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		blDryRunParam(),
	), chanPermsHandler(d))
}

func chanCreateHandler(d deps.Deps, tool string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, res := blRequired(req, "name")
		if res != nil {
			return res, nil
		}
		gid, res := blGuild(ctx, d, req, tool, opWrite, false)
		if res != nil {
			return res, nil
		}
		cr := dto.CreateChannelReq{GuildID: gid, Name: name}
		switch tool {
		case "discord_create_text_channel":
			cr.Type = dto.ChannelText
			if req.GetBool("announcement", false) {
				cr.Type = dto.ChannelAnnouncement
			}
			cr.Topic = req.GetString("topic", "")
		case "discord_create_voice_channel":
			cr.Type = dto.ChannelVoice
			if req.GetBool("stage", false) {
				cr.Type = dto.ChannelStage
			}
			if hasArg(req, "bitrate") {
				b := req.GetInt("bitrate", 0)
				if b < 8000 || b > 384000 {
					return errorf("bitrate must be 8000-384000, got %d", b), nil
				}
				cr.Bitrate = b
			}
			if hasArg(req, "user_limit") {
				u := req.GetInt("user_limit", 0)
				if u < 0 || u > 99 {
					return errorf("user_limit must be 0-99, got %d", u), nil
				}
				cr.UserLimit = u
			}
		case "discord_create_category":
			cr.Type = dto.ChannelCategory
			cr.Position = max(0, req.GetInt("position", 0))
		}
		if cr.Type != dto.ChannelCategory {
			parent, res := blCategory(ctx, d, req, gid)
			if res != nil {
				return res, nil
			}
			cr.ParentID = parent
		}
		ch, err := d.Discord.CreateChannel(ctx, cr)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(ch), nil
	}
}

func chanEditHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_edit_channel"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dry := dryRun(req)
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opDestructive, dry)
		if res != nil {
			return res, nil
		}
		er := dto.EditChannelReq{ChannelID: ch.ID, Reason: req.GetString("reason", "")}
		var changes []string
		if n := optString(req, "name"); n != nil {
			if strings.TrimSpace(*n) == "" {
				return errorf("name cannot be empty"), nil
			}
			er.Name = n
			changes = append(changes, fmt.Sprintf("name: %s → %s", ch.Name, *n))
		}
		if t := optString(req, "topic"); t != nil {
			er.Topic = t
			changes = append(changes, fmt.Sprintf("topic: %q → %q", ch.Topic, *t))
		}
		if c := optString(req, "category_id"); c != nil {
			newParent := ""
			if strings.TrimSpace(*c) != "" {
				id, res := blCategory(ctx, d, req, ch.GuildID)
				if res != nil {
					return res, nil
				}
				newParent = id
			}
			er.ParentID = &newParent
			changes = append(changes, fmt.Sprintf("category: %s → %s", chanOrNone(ch.ParentID), chanOrNone(newParent)))
		}
		var warnings []string
		if a := optBool(req, "archived"); a != nil {
			if ch.Type != dto.ChannelThread {
				return errorf("archived applies only to threads; %s is a %s channel", blLabel(ch), ch.Type), nil
			}
			er.Archived = a
			changes = append(changes, fmt.Sprintf("archived: → %t", *a))
		}
		if len(changes) == 0 {
			return errorf("nothing to change: pass at least one of name, topic, category_id, archived"), nil
		}
		if dry {
			return dryRunResult(d, channelOp(tool, ch, opDestructive, true), blLabel(ch), changes, warnings...), nil
		}
		out, err := d.Discord.EditChannel(ctx, er)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(out), nil
	}
}

func chanOrNone(id string) string {
	if id == "" {
		return "(none)"
	}
	return id
}

func chanDeleteHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_delete_channel"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dry := dryRun(req)
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opDestructive, dry)
		if res != nil {
			return res, nil
		}
		reason := req.GetString("reason", "")
		if dry {
			var warnings []string
			if ch.Type == dto.ChannelCategory && ch.GuildID != "" {
				if all, err := d.Discord.ListChannels(ctx, ch.GuildID); err == nil {
					var kids []string
					for _, c := range all {
						if c.ParentID == ch.ID {
							kids = append(kids, "#"+c.Name)
						}
					}
					if len(kids) > 0 {
						warnings = append(warnings, fmt.Sprintf("category has %d child channel(s) that will become uncategorised: %s",
							len(kids), strings.Join(kids, ", ")))
					}
				}
			}
			warnings = append(warnings, "deletion is permanent; message history cannot be recovered")
			change := fmt.Sprintf("delete %s channel %s", ch.Type, blLabel(ch))
			if reason != "" {
				change += " (reason: " + reason + ")"
			}
			return dryRunResult(d, channelOp(tool, ch, opDestructive, true), blLabel(ch), []string{change}, warnings...), nil
		}
		if err := d.Discord.DeleteChannel(ctx, ch.ID, reason); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"ok": true, "deleted": ch.ID, "name": ch.Name}), nil
	}
}

func chanPermsHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_set_channel_permissions"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dry := dryRun(req)
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opDestructive, dry)
		if res != nil {
			return res, nil
		}
		targetRef, res := blRequired(req, "target_id")
		if res != nil {
			return res, nil
		}
		ttype := req.GetString("target_type", "")
		var targetID string
		var err error
		switch ttype {
		case dto.TargetRole:
			targetID, err = resolveRole(ctx, d, ch.GuildID, targetRef)
		case dto.TargetMember:
			targetID, err = resolveUser(ctx, d, ch.GuildID, targetRef)
		default:
			return errorf("target_type must be %q or %q, got %q", dto.TargetRole, dto.TargetMember, ttype), nil
		}
		if err != nil {
			return errorResult(err), nil
		}
		targetLabel := fmt.Sprintf("%s %s (%s)", ttype, targetRef, targetID)

		allow, err := parsePermissions(blRaw(req, "allow_permissions"))
		if err != nil {
			return errorf("allow_permissions: %v", err), nil
		}
		deny, err := parsePermissions(blRaw(req, "deny_permissions"))
		if err != nil {
			return errorf("deny_permissions: %v", err), nil
		}
		if allow&deny != 0 {
			return errorf("permissions cannot be both allowed and denied: %s", strings.Join(permissionNames(allow&deny), ", ")), nil
		}
		remove := req.GetBool("remove", false)
		if !remove && allow == 0 && deny == 0 {
			return errorf("pass allow_permissions and/or deny_permissions, or remove: true"), nil
		}
		pr := dto.ChannelPermissionReq{ChannelID: ch.ID, TargetID: targetID, TargetType: ttype,
			Allow: allow, Deny: deny, Remove: remove, Reason: req.GetString("reason", "")}

		if dry {
			var changes []string
			if remove {
				changes = append(changes, fmt.Sprintf("#%s: remove overwrite for %s", ch.Name, targetLabel))
			} else {
				if allow != 0 {
					changes = append(changes, fmt.Sprintf("#%s: %s allow %s", ch.Name, targetLabel, chanPlus(allow)))
				}
				if deny != 0 {
					changes = append(changes, fmt.Sprintf("#%s: %s deny %s", ch.Name, targetLabel, chanPlus(deny)))
				}
			}
			warnings := []string{"the overwrite replaces any existing allow/deny bits for this target"}
			if ttype == dto.TargetRole && targetID == ch.GuildID {
				warnings = append(warnings, "target is @everyone: affects every member")
			}
			return dryRunResult(d, channelOp(tool, ch, opDestructive, true), blLabel(ch), changes, warnings...), nil
		}
		if err := d.Discord.SetChannelPermissions(ctx, pr); err != nil {
			return errorResult(err), nil
		}
		out := map[string]any{"ok": true, "channel_id": ch.ID, "target_id": targetID, "target_type": ttype}
		if remove {
			out["removed"] = true
		} else {
			out["allow"] = strconv.FormatInt(allow, 10)
			out["deny"] = strconv.FormatInt(deny, 10)
			if n := permissionNames(allow); len(n) > 0 {
				out["allow_names"] = n
			}
			if n := permissionNames(deny); len(n) > 0 {
				out["deny_names"] = n
			}
		}
		return jsonResult(out), nil
	}
}

// chanPlus renders a bitfield as "+A +B".
func chanPlus(bits int64) string {
	names := permissionNames(bits)
	for i, n := range names {
		names[i] = "+" + n
	}
	return strings.Join(names, " ")
}
