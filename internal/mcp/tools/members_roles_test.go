package tools

import (
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

func TestMemberRoleTools(t *testing.T) {
	roleEnums := map[string][]string{"action": {"list", "create", "edit", "delete", "assign", "revoke"}}
	blRunCases(t, []blCase{
		{
			tool: "discord_list_members", required: []string{"guild_id"}, read: true,
			args: map[string]any{"guild_id": "Test Guild", "limit": 2},
			check: func(t *testing.T, txt string, _ *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, `"count":2`) || !strings.Contains(txt, `"next_cursor":"100000000000000051"`) {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			label: "manage_roles_list", tool: "discord_manage_roles", required: []string{"action", "guild_id"}, enums: roleEnums, roOK: true,
			args: map[string]any{"action": "list", "guild_id": discord.MockGuildID},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, "Moderator") || len(m.MutatingCalls()) != 0 {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			label: "manage_roles_create", tool: "discord_manage_roles", required: []string{"action", "guild_id"}, dryRun: true,
			args: map[string]any{"action": "create", "guild_id": "Test Guild", "name": "Helpers", "color": "#FF0000", "permissions": []any{"ManageMessages"}, "hoist": true},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateRole").Args[0].(dto.RoleMutationReq)
				if r.GuildID != discord.MockGuildID || *r.Name != "Helpers" || *r.Color != 0xFF0000 || *r.Permissions != 1<<13 || !*r.Hoist {
					t.Errorf("CreateRole req = %+v", r)
				}
			},
		},
		{
			label: "manage_roles_edit", tool: "discord_manage_roles", required: []string{"action", "guild_id"}, dryRun: true,
			args: map[string]any{"action": "edit", "guild_id": discord.MockGuildID, "role_id": "@Moderator", "name": "Mods", "permissions": "ManageMessages,KickMembers"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				c := blLastCall(t, m, "EditRole")
				if c.Args[0] != discord.MockModeratorRole || *c.Args[1].(dto.RoleMutationReq).Name != "Mods" {
					t.Errorf("EditRole args = %v", c.Args)
				}
			},
		},
		{
			label: "manage_roles_delete", tool: "discord_manage_roles", required: []string{"action", "guild_id"}, dryRun: true,
			args: map[string]any{"action": "delete", "guild_id": discord.MockGuildID, "role_id": "<@&" + discord.MockModeratorRole + ">"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "DeleteRole"); c.Args[1] != discord.MockModeratorRole {
					t.Errorf("DeleteRole args = %v", c.Args)
				}
			},
		},
		{
			label: "manage_roles_assign", tool: "discord_manage_roles", required: []string{"action", "guild_id"}, dryRun: true,
			args: map[string]any{"action": "assign", "guild_id": discord.MockGuildID, "role_id": "Moderator", "user_id": "@bob"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "AssignRole"); c.Args[1] != "100000000000000051" || c.Args[2] != discord.MockModeratorRole {
					t.Errorf("AssignRole args = %v", c.Args)
				}
			},
		},
		{
			label: "manage_roles_revoke", tool: "discord_manage_roles", required: []string{"action", "guild_id"}, dryRun: true,
			args: map[string]any{"action": "revoke", "guild_id": discord.MockGuildID, "role_id": "@Moderator", "user_id": "<@" + discord.MockAliceID + ">"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "RemoveRole"); c.Args[1] != discord.MockAliceID {
					t.Errorf("RemoveRole args = %v", c.Args)
				}
			},
		},
	})
}

func TestManageRolesEditPreviewDiff(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	var p dto.DryRunPreview
	decodeResult(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "edit", "guild_id": discord.MockGuildID,
		"role_id": "@Moderator", "name": "Mods", "color": "#FF0000", "permissions": []any{"KickMembers"}, "dry_run": true}), &p)
	joined := strings.Join(p.Changes, "\n")
	for _, want := range []string{"name: Moderator → Mods", "color: #3498DB → #FF0000", "permissions: +KickMembers -ManageMessages"} {
		if !strings.Contains(joined, want) {
			t.Errorf("changes %v missing %q", p.Changes, want)
		}
	}
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("mutating calls in dry run")
	}
}

func TestManageRolesValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	g := discord.MockGuildID
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "edit", "guild_id": g}), "role_id is required")
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "assign", "guild_id": g, "role_id": "Moderator"}), "user_id is required")
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "create", "guild_id": g}), "name is required")
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "edit", "guild_id": g, "role_id": "Moderator"}), "nothing to change")
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "edit", "guild_id": g, "role_id": "Moderator", "color": "#GGGGGG"}), "color")
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "delete", "guild_id": g, "role_id": "Ghost"}), "not found")
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "delete", "guild_id": g, "role_id": g}), "@everyone cannot be deleted")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("validation failures must not mutate")
	}
}

// mrBotMock adds the optional bot-identity capability to the mock.
type mrBotMock struct{ *discord.MockClient }

func (mrBotMock) BotUserID() string { return discord.MockBotID }

func TestManageRolesRefusesRolesAtOrAboveBot(t *testing.T) {
	d, m := newTestDeps(t, nil)
	d.Discord = mrBotMock{m}
	// Bot holds a "Bot" role at position 3; Moderator is at 5.
	m.Roles[discord.MockGuildID] = append(m.Roles[discord.MockGuildID], dto.RoleInfo{ID: "100000000000000041", Name: "Bot", Position: 3})
	m.Members[discord.MockGuildID][2].Roles = []string{"Bot"}
	s := newTestServer(t, d, RegisterMemberRoleTools)
	blWantErr(t, callTool(t, s, "discord_manage_roles", map[string]any{"action": "edit", "guild_id": discord.MockGuildID, "role_id": "@Moderator", "name": "x"}),
		"at or above the bot's highest role")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("refused edit must not mutate")
	}
}

func TestListMembersIntentError(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	m.Err["ListMembers"] = discord.ErrForbidden
	blWantErr(t, callTool(t, s, "discord_list_members", map[string]any{"guild_id": discord.MockGuildID}), "GuildMembers")
}
