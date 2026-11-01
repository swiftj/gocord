package tools

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// mrBotIdentity is an optional capability of a discord.Service implementation that
// knows the logged-in bot's user ID. When available, role mutations are checked
// against the bot's highest role before calling Discord.
type mrBotIdentity interface {
	BotUserID() string
}

// RegisterMemberRoleTools registers discord_list_members and discord_manage_roles.
func RegisterMemberRoleTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool("discord_list_members",
		"List guild members with roles and join dates, paginated by user ID. Requires the privileged GuildMembers intent.",
		blReadOnly(),
		blGuildParam(),
		mcp.WithInteger("limit", mcp.Min(1), mcp.Max(1000), mcp.DefaultNumber(100), mcp.Description("Members to return (1-1000).")),
		mcp.WithString("after", mcp.Description("Return members with user IDs after this Snowflake (use next_cursor).")),
	), mrListMembersHandler(d))

	s.AddTool(blTool("discord_manage_roles",
		"List, create, edit, delete, assign, or revoke guild roles. Mutations support dry_run; roles at or above the bot's highest role cannot be changed.",
		blMutating(true, false),
		mcp.WithString("action", mcp.Required(), mcp.Enum("list", "create", "edit", "delete", "assign", "revoke"), mcp.Description("Operation to perform.")),
		blGuildParam(),
		mcp.WithString("role_id", mcp.Description("Role (Snowflake, @name, or mention) for edit/delete/assign/revoke.")),
		mcp.WithString("user_id", mcp.Description("Member (Snowflake, @name, or mention) for assign/revoke.")),
		mcp.WithString("name", mcp.MaxLength(100), mcp.Description("Role name (create/edit).")),
		mcp.WithAny("color", mcp.Description(`Role colour as "#RRGGBB" or an integer.`), func(m map[string]any) {
			m["type"] = []string{"string", "integer"}
		}),
		chanPermsParam("permissions", `Role permissions as names (["ManageMessages"]) or a decimal bitfield string.`),
		mcp.WithBoolean("hoist", mcp.Description("Display role members separately.")),
		mcp.WithBoolean("mentionable", mcp.Description("Allow anyone to @mention the role.")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		blDryRunParam(),
	), mrManageRolesHandler(d))
}

func mrListMembersHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_list_members"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		gid, res := blGuild(ctx, d, req, tool, opRead, false)
		if res != nil {
			return res, nil
		}
		limit := clamp(req.GetInt("limit", 100), 1, 1000)
		after := strings.TrimSpace(req.GetString("after", ""))
		if after != "" && !dto.IsSnowflake(after) {
			id, err := resolveUser(ctx, d, gid, after)
			if err != nil {
				return errorf("after must be a user Snowflake: %v", err), nil
			}
			after = id
		}
		ms, err := d.Discord.ListMembers(ctx, gid, limit, after)
		if err != nil {
			if errors.Is(err, discord.ErrForbidden) || strings.Contains(strings.ToLower(err.Error()), "intent") {
				return errorf("cannot list members: %v — enable the privileged Server Members (GuildMembers) intent in the Discord developer portal", err), nil
			}
			return errorResult(err), nil
		}
		next := ""
		if len(ms) == limit {
			next = ms[len(ms)-1].UserID
		}
		return listResult(ms, next), nil
	}
}

// mrRoleChanges holds parsed role mutation fields.
type mrRoleChanges struct {
	req     dto.RoleMutationReq
	changed bool
}

func mrParseMutation(req mcp.CallToolRequest, gid string) (mrRoleChanges, *mcp.CallToolResult) {
	out := mrRoleChanges{req: dto.RoleMutationReq{GuildID: gid, Reason: req.GetString("reason", "")}}
	if n := optString(req, "name"); n != nil {
		if strings.TrimSpace(*n) == "" {
			return out, errorf("name cannot be empty")
		}
		v := strings.TrimSpace(*n)
		out.req.Name, out.changed = &v, true
	}
	if hasArg(req, "color") {
		c, err := parseColor(blRaw(req, "color"))
		if err != nil {
			return out, errorf("color: %v", err)
		}
		out.req.Color, out.changed = &c, true
	}
	if hasArg(req, "permissions") {
		p, err := parsePermissions(blRaw(req, "permissions"))
		if err != nil {
			return out, errorf("permissions: %v", err)
		}
		out.req.Permissions, out.changed = &p, true
	}
	if b := optBool(req, "hoist"); b != nil {
		out.req.Hoist, out.changed = b, true
	}
	if b := optBool(req, "mentionable"); b != nil {
		out.req.Mentionable, out.changed = b, true
	}
	return out, nil
}

// mrDescribe renders "old → new" lines for a role mutation (old may be nil for create).
func mrDescribe(old *dto.RoleInfo, m dto.RoleMutationReq) []string {
	var lines []string
	oldOr := func(f func(*dto.RoleInfo) string) string {
		if old == nil {
			return "(new)"
		}
		return f(old)
	}
	if m.Name != nil {
		lines = append(lines, fmt.Sprintf("name: %s → %s", oldOr(func(r *dto.RoleInfo) string { return r.Name }), *m.Name))
	}
	if m.Color != nil {
		lines = append(lines, fmt.Sprintf("color: %s → #%06X", oldOr(func(r *dto.RoleInfo) string {
			if r.Color == "" {
				return "(none)"
			}
			return r.Color
		}), *m.Color))
	}
	if m.Permissions != nil {
		newBits := *m.Permissions
		if old == nil {
			lines = append(lines, fmt.Sprintf("permissions: %s", mrPermList(newBits)))
		} else {
			oldBits, _ := strconv.ParseInt(old.Permissions, 10, 64)
			var diff []string
			for _, n := range permissionNames(newBits &^ oldBits) {
				diff = append(diff, "+"+n)
			}
			for _, n := range permissionNames(oldBits &^ newBits) {
				diff = append(diff, "-"+n)
			}
			if len(diff) == 0 {
				diff = []string{"(unchanged)"}
			}
			lines = append(lines, "permissions: "+strings.Join(diff, " "))
		}
	}
	if m.Hoist != nil {
		lines = append(lines, fmt.Sprintf("hoist: %s → %t", oldOr(func(r *dto.RoleInfo) string { return strconv.FormatBool(r.Hoist) }), *m.Hoist))
	}
	if m.Mentionable != nil {
		lines = append(lines, fmt.Sprintf("mentionable: %s → %t", oldOr(func(r *dto.RoleInfo) string { return strconv.FormatBool(r.Mentionable) }), *m.Mentionable))
	}
	return lines
}

func mrPermList(bits int64) string {
	if bits == 0 {
		return "(none)"
	}
	return strings.Join(permissionNames(bits), ", ")
}

// mrBotTopPosition returns the bot's highest role position, or -1 when unknown.
func mrBotTopPosition(ctx context.Context, d deps.Deps, gid string, roles []dto.RoleInfo) int {
	bi, ok := d.Discord.(mrBotIdentity)
	if !ok || bi.BotUserID() == "" {
		return -1
	}
	botID := bi.BotUserID()
	ms, err := d.Discord.ListMembers(ctx, gid, 1000, "")
	if err != nil {
		return -1
	}
	for _, m := range ms {
		if m.UserID != botID {
			continue
		}
		top := 0
		for _, name := range m.Roles {
			for _, r := range roles {
				if r.Name == name || r.ID == name {
					top = max(top, r.Position)
				}
			}
		}
		return top
	}
	return -1
}

func mrManageRolesHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_manage_roles"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action := req.GetString("action", "")
		dry := dryRun(req)
		switch action {
		case "list":
			gid, res := blGuild(ctx, d, req, tool, opRead, false)
			if res != nil {
				return res, nil
			}
			roles, err := d.Discord.ListRoles(ctx, gid)
			if err != nil {
				return errorResult(err), nil
			}
			return listResult(roles, ""), nil
		case "create", "edit", "delete", "assign", "revoke":
		default:
			return errorf("action must be one of list, create, edit, delete, assign, revoke; got %q", action), nil
		}

		// Per-action required params, validated before any Discord call.
		needRole := action != "create"
		needUser := action == "assign" || action == "revoke"
		if needRole && strings.TrimSpace(req.GetString("role_id", "")) == "" {
			return errorf("role_id is required for %s", action), nil
		}
		if needUser && strings.TrimSpace(req.GetString("user_id", "")) == "" {
			return errorf("user_id is required for %s", action), nil
		}

		gid, res := blGuild(ctx, d, req, tool, opDestructive, dry)
		if res != nil {
			return res, nil
		}
		mut, res := mrParseMutation(req, gid)
		if res != nil {
			return res, nil
		}
		if action == "create" && mut.req.Name == nil {
			return errorf("name is required for create"), nil
		}
		if action == "edit" && !mut.changed {
			return errorf("nothing to change: pass at least one of name, color, permissions, hoist, mentionable"), nil
		}

		roles, err := d.Discord.ListRoles(ctx, gid)
		if err != nil {
			return errorResult(err), nil
		}
		var role *dto.RoleInfo
		if needRole {
			rid, err := resolveRole(ctx, d, gid, req.GetString("role_id", ""))
			if err != nil {
				return errorResult(err), nil
			}
			for i := range roles {
				if roles[i].ID == rid {
					role = &roles[i]
				}
			}
			if role == nil {
				return errorf("role %s not found in guild %s", rid, gid), nil
			}
			if role.ID == gid && action != "edit" {
				return errorf("@everyone cannot be %s", mrPast(action)), nil
			}
			if role.Managed && action != "edit" {
				return errorf("role @%s is managed by an integration and cannot be %s manually", role.Name, mrPast(action)), nil
			}
			if top := mrBotTopPosition(ctx, d, gid, roles); top >= 0 && role.Position >= top {
				return errorf("role @%s (position %d) is at or above the bot's highest role (position %d); move the bot's role higher in Server Settings → Roles", role.Name, role.Position, top), nil
			}
		}
		var uid string
		if needUser {
			uid, err = resolveUser(ctx, d, gid, req.GetString("user_id", ""))
			if err != nil {
				return errorResult(err), nil
			}
		}

		if dry {
			var target string
			var changes []string
			switch action {
			case "create":
				target = "new role"
				changes = append([]string{"create role @" + *mut.req.Name}, mrDescribe(nil, mut.req)...)
			case "edit":
				target = fmt.Sprintf("role @%s (%s)", role.Name, role.ID)
				changes = mrDescribe(role, mut.req)
			case "delete":
				target = fmt.Sprintf("role @%s (%s)", role.Name, role.ID)
				changes = []string{fmt.Sprintf("delete role @%s (%s)", role.Name, role.ID)}
			case "assign":
				target = "user " + uid
				changes = []string{fmt.Sprintf("user %s: +@%s", uid, role.Name)}
			case "revoke":
				target = "user " + uid
				changes = []string{fmt.Sprintf("user %s: -@%s", uid, role.Name)}
			}
			var warnings []string
			if mut.req.Permissions != nil && *mut.req.Permissions&(1<<3) != 0 {
				warnings = append(warnings, "grants Administrator: bypasses all channel overwrites")
			}
			if action == "delete" {
				warnings = append(warnings, "deleting a role removes it from every member and cannot be undone")
			}
			return dryRunResult(d, guildOp(tool, gid, opDestructive, true), target, changes, warnings...), nil
		}

		var out any
		switch action {
		case "create":
			out, err = d.Discord.CreateRole(ctx, mut.req)
		case "edit":
			out, err = d.Discord.EditRole(ctx, role.ID, mut.req)
		case "delete":
			err = d.Discord.DeleteRole(ctx, gid, role.ID, mut.req.Reason)
			out = map[string]any{"ok": true, "deleted": role.ID, "name": role.Name}
		case "assign":
			err = d.Discord.AssignRole(ctx, gid, uid, role.ID)
			out = map[string]any{"ok": true, "user_id": uid, "role_id": role.ID, "assigned": true}
		case "revoke":
			err = d.Discord.RemoveRole(ctx, gid, uid, role.ID)
			out = map[string]any{"ok": true, "user_id": uid, "role_id": role.ID, "revoked": true}
		}
		if err != nil {
			if errors.Is(err, discord.ErrForbidden) && role != nil {
				return errorf("cannot %s role @%s: the bot needs ManageRoles and its highest role must be above @%s", action, role.Name, role.Name), nil
			}
			return errorResult(err), nil
		}
		return jsonResult(out), nil
	}
}

func mrPast(action string) string {
	switch action {
	case "assign":
		return "assigned"
	case "revoke":
		return "revoked"
	case "delete":
		return "deleted"
	}
	return action + "ed"
}
