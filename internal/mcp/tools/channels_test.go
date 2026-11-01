package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

func TestChannelTools(t *testing.T) {
	blRunCases(t, []blCase{
		{
			tool: "discord_create_text_channel", required: []string{"guild_id", "name"},
			args: map[string]any{"guild_id": "Test Guild", "name": "dev", "category_id": "#Community", "topic": "Dev talk", "announcement": true},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateChannel").Args[0].(dto.CreateChannelReq)
				if r.GuildID != discord.MockGuildID || r.ParentID != discord.MockCategoryID || r.Type != dto.ChannelAnnouncement || r.Topic != "Dev talk" {
					t.Errorf("CreateChannel req = %+v", r)
				}
			},
		},
		{
			tool: "discord_create_voice_channel", required: []string{"guild_id", "name"},
			args: map[string]any{"guild_id": discord.MockGuildID, "name": "Stage", "stage": true, "bitrate": 64000, "user_limit": 10},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateChannel").Args[0].(dto.CreateChannelReq)
				if r.Type != dto.ChannelStage || r.Bitrate != 64000 || r.UserLimit != 10 {
					t.Errorf("CreateChannel req = %+v", r)
				}
			},
		},
		{
			tool: "discord_create_category", required: []string{"guild_id", "name"},
			args: map[string]any{"guild_id": discord.MockGuildID, "name": "Ops", "position": 4},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateChannel").Args[0].(dto.CreateChannelReq)
				if r.Type != dto.ChannelCategory || r.Position != 4 {
					t.Errorf("CreateChannel req = %+v", r)
				}
			},
		},
		{
			tool: "discord_edit_channel", required: []string{"channel_id"}, dryRun: true,
			args: map[string]any{"channel_id": "#general", "name": "chat", "topic": ""},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "EditChannel").Args[0].(dto.EditChannelReq)
				if r.ChannelID != discord.MockGeneralID || r.Name == nil || *r.Name != "chat" || r.Topic == nil || *r.Topic != "" || r.ParentID != nil {
					t.Errorf("EditChannel req = %+v", r)
				}
			},
		},
		{
			tool: "discord_delete_channel", required: []string{"channel_id"}, dryRun: true,
			args: map[string]any{"channel_id": "<#" + discord.MockGeneralID + ">", "reason": "cleanup"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "DeleteChannel"); c.Args[0] != discord.MockGeneralID || c.Args[1] != "cleanup" {
					t.Errorf("DeleteChannel args = %v", c.Args)
				}
			},
		},
		{
			tool: "discord_set_channel_permissions", required: []string{"channel_id", "target_id", "target_type"}, dryRun: true,
			enums: map[string][]string{"target_type": {"role", "member"}},
			args: map[string]any{"channel_id": "#general", "target_id": "@Moderator", "target_type": "role",
				"allow_permissions": []any{"ManageMessages", "ViewChannel"}, "deny_permissions": "2048"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "SetChannelPermissions").Args[0].(dto.ChannelPermissionReq)
				if r.ChannelID != discord.MockGeneralID || r.TargetID != discord.MockModeratorRole || r.Allow != 1<<13|1<<10 || r.Deny != 2048 {
					t.Errorf("SetChannelPermissions req = %+v", r)
				}
			},
		},
	})
}

func TestEditChannelPreviewShowsOldToNew(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	var p dto.DryRunPreview
	decodeResult(t, callTool(t, s, "discord_edit_channel", map[string]any{"channel_id": "general", "name": "chat", "topic": "New", "dry_run": true}), &p)
	joined := strings.Join(p.Changes, "\n")
	if !strings.Contains(joined, "name: general → chat") || !strings.Contains(joined, `topic: "General chat" → "New"`) {
		t.Fatalf("changes = %v", p.Changes)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("mutating calls in dry run")
	}
}

func TestEditChannelValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_edit_channel", map[string]any{"channel_id": "#general"}), "nothing to change")
	blWantErr(t, callTool(t, s, "discord_edit_channel", map[string]any{"channel_id": "#general", "archived": true}), "only to threads")
	blWantErr(t, callTool(t, s, "discord_edit_channel", map[string]any{"channel_id": "#general", "category_id": "#announcements"}), "not a category")
	blWantErr(t, callTool(t, s, "discord_edit_channel", map[string]any{"channel_id": "#nope", "name": "x"}), "not found")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("validation failures must not mutate")
	}
}

func TestDeleteCategoryPreviewWarnsAboutChildren(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	var p dto.DryRunPreview
	decodeResult(t, callTool(t, s, "discord_delete_channel", map[string]any{"channel_id": discord.MockCategoryID, "dry_run": true}), &p)
	if len(p.Warnings) == 0 || !strings.Contains(p.Warnings[0], "child channel") || !strings.Contains(p.Warnings[0], "#general") {
		t.Fatalf("warnings = %v", p.Warnings)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("mutating calls in dry run")
	}
}

func TestSetPermissionsPreviewAndValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	var p dto.DryRunPreview
	decodeResult(t, callTool(t, s, "discord_set_channel_permissions", map[string]any{
		"channel_id": "#general", "target_id": "@Moderator", "target_type": "role", "allow_permissions": []any{"ManageMessages"}, "dry_run": true}), &p)
	if len(p.Changes) != 1 || !strings.Contains(p.Changes[0], "#general: role @Moderator") || !strings.Contains(p.Changes[0], "allow +ManageMessages") {
		t.Fatalf("changes = %v", p.Changes)
	}
	blWantErr(t, callTool(t, s, "discord_set_channel_permissions", map[string]any{
		"channel_id": "#general", "target_id": "@alice", "target_type": "member", "allow_permissions": "Bogus"}), "unknown permission")
	blWantErr(t, callTool(t, s, "discord_set_channel_permissions", map[string]any{
		"channel_id": "#general", "target_id": "@alice", "target_type": "member"}), "remove: true")
	blWantErr(t, callTool(t, s, "discord_set_channel_permissions", map[string]any{
		"channel_id": "#general", "target_id": "@alice", "target_type": "member", "allow_permissions": "SendMessages", "deny_permissions": "SendMessages"}), "both allowed and denied")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("unexpected mutation")
	}
	// Member overwrite removal resolves @alice.
	blWantOK(t, callTool(t, s, "discord_set_channel_permissions", map[string]any{
		"channel_id": "#general", "target_id": "@alice", "target_type": "member", "remove": true}))
	r := blLastCall(t, m, "SetChannelPermissions").Args[0].(dto.ChannelPermissionReq)
	if r.TargetID != discord.MockAliceID || !r.Remove {
		t.Fatalf("req = %+v", r)
	}
}

func TestPermsParamSchemaAcceptsArrayOrString(t *testing.T) {
	s, _, _ := blSetup(t, nil)
	b, _ := json.Marshal(s.GetTool("discord_set_channel_permissions").Tool.InputSchema.Properties["allow_permissions"])
	if !strings.Contains(string(b), `"type":["array","string"]`) {
		t.Fatalf("schema = %s", b)
	}
}

func TestChannelAllowlistDenies(t *testing.T) {
	s, m, _ := blSetup(t, &config.Config{AllowedChannels: []string{discord.MockAnnouncementID}})
	blWantErr(t, callTool(t, s, "discord_delete_channel", map[string]any{"channel_id": "#general"}), "not in the allowlist")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("unexpected mutation")
	}
}

func TestAmbiguousChannelError(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	m.Channels["100000000000000012"] = dto.ChannelInfo{ID: "100000000000000012", GuildID: discord.MockGuildID, Name: "general", Type: dto.ChannelText}
	blWantErr(t, callTool(t, s, "discord_delete_channel", map[string]any{"channel_id": "#general"}), "pass the Snowflake ID instead")
}
