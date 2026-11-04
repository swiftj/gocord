package tools

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// ---------- Shared baseline test runner (prefix "bl") ----------

// blRegisters lists the registration hooks owned by mcp-baseline-tools.
var blRegisters = []func(*server.MCPServer, deps.Deps){
	RegisterGuildTools, RegisterChannelTools, RegisterMessageTools,
	RegisterForumThreadTools, RegisterReactionPollTools, RegisterMemberRoleTools,
}

// blCase describes one tool scenario for the table-driven runner.
type blCase struct {
	label    string // defaults to tool
	tool     string
	required []string
	enums    map[string][]string
	args     map[string]any // happy-path args; prefer #name refs to exercise resolution
	setup    func(m *discord.MockClient)
	check    func(t *testing.T, text string, m *discord.MockClient, d deps.Deps)
	read     bool // read-only tool (readOnlyHint): read-only mode must still allow it
	roOK     bool // allowed in read-only mode although the tool is not read-only (read actions)
	dryRun   bool // DR scenario: dry_run must make zero mutating calls
	noGuild  bool // policy check is not guild-scoped (e.g. login)
}

func blSetup(t *testing.T, cfg *config.Config) (*server.MCPServer, *discord.MockClient, deps.Deps) {
	t.Helper()
	d, m := newTestDeps(t, cfg)
	return newTestServer(t, d, blRegisters...), m, d
}

func blWantErr(t *testing.T, res *mcp.CallToolResult, substr string) {
	t.Helper()
	txt := resultText(t, res)
	if !res.IsError {
		t.Fatalf("expected error containing %q, got success: %s", substr, txt)
	}
	if !strings.Contains(txt, substr) {
		t.Fatalf("error %q does not contain %q", txt, substr)
	}
}

func blWantOK(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	txt := resultText(t, res)
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", txt)
	}
	return txt
}

func blWithArgs(base map[string]any, extra map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

func blRunCases(t *testing.T, cases []blCase) {
	for _, c := range cases {
		name := c.label
		if name == "" {
			name = c.tool
		}
		t.Run(name, func(t *testing.T) {
			t.Run("schema", func(t *testing.T) {
				s, _, _ := blSetup(t, nil)
				tool := s.GetTool(c.tool)
				if tool == nil {
					t.Fatalf("tool %s not registered", c.tool)
				}
				got := slices.Clone(tool.Tool.InputSchema.Required)
				want := slices.Clone(c.required)
				sort.Strings(got)
				sort.Strings(want)
				if !slices.Equal(got, want) {
					t.Errorf("required = %v, want %v", got, want)
				}
				for param, vals := range c.enums {
					prop, _ := tool.Tool.InputSchema.Properties[param].(map[string]any)
					enum, _ := prop["enum"].([]string)
					if !slices.Equal(enum, vals) {
						t.Errorf("%s enum = %v, want %v", param, prop["enum"], vals)
					}
				}
				if tool.Tool.Description == "" {
					t.Error("missing description")
				}
				ro := tool.Tool.Annotations.ReadOnlyHint
				if ro == nil || *ro != c.read {
					t.Errorf("readOnlyHint = %v, want %v", ro, c.read)
				}
				if c.dryRun {
					if _, ok := tool.Tool.InputSchema.Properties["dry_run"]; !ok {
						t.Error("DR tool lacks dry_run param")
					}
					if dh := tool.Tool.Annotations.DestructiveHint; dh == nil || !*dh {
						t.Error("DR tool must set destructiveHint")
					}
				}
			})

			t.Run("happy", func(t *testing.T) {
				s, m, d := blSetup(t, nil)
				if c.setup != nil {
					c.setup(m)
				}
				txt := blWantOK(t, callTool(t, s, c.tool, c.args))
				if c.check != nil {
					c.check(t, txt, m, d)
				}
				if c.read && len(m.MutatingCalls()) != 0 {
					t.Errorf("read tool made mutating calls: %+v", m.MutatingCalls())
				}
			})

			t.Run("read_only", func(t *testing.T) {
				s, m, _ := blSetup(t, &config.Config{ReadOnly: true})
				if c.setup != nil {
					c.setup(m)
				}
				res := callTool(t, s, c.tool, c.args)
				if c.read || c.roOK || c.noGuild {
					blWantOK(t, res)
				} else {
					blWantErr(t, res, "read-only mode")
				}
				if !c.noGuild && len(m.MutatingCalls()) != 0 {
					t.Errorf("mutating calls in read-only mode: %+v", m.MutatingCalls())
				}
			})

			if c.dryRun {
				t.Run("dry_run", func(t *testing.T) {
					s, m, _ := blSetup(t, nil)
					if c.setup != nil {
						c.setup(m)
					}
					var p dto.DryRunPreview
					decodeResult(t, callTool(t, s, c.tool, blWithArgs(c.args, map[string]any{"dry_run": true})), &p)
					if !p.DryRun || !p.Allowed || len(p.Changes) == 0 || p.Action != c.tool {
						t.Errorf("bad preview: %+v", p)
					}
					if n := len(m.MutatingCalls()); n != 0 {
						t.Errorf("dry_run made %d mutating calls: %+v", n, m.MutatingCalls())
					}
				})
				t.Run("dry_run_read_only", func(t *testing.T) {
					s, m, _ := blSetup(t, &config.Config{ReadOnly: true})
					if c.setup != nil {
						c.setup(m)
					}
					var p dto.DryRunPreview
					decodeResult(t, callTool(t, s, c.tool, blWithArgs(c.args, map[string]any{"dry_run": true})), &p)
					if p.Allowed || !strings.Contains(p.DeniedReason, "read-only") {
						t.Errorf("preview should report read-only denial: %+v", p)
					}
					if len(m.MutatingCalls()) != 0 {
						t.Errorf("dry_run made mutating calls")
					}
				})
			}
		})
	}
}

// blLastCall returns the last recorded call to method.
func blLastCall(t *testing.T, m *discord.MockClient, method string) discord.MockCall {
	t.Helper()
	calls := m.CallsTo(method)
	if len(calls) == 0 {
		t.Fatalf("no %s call recorded", method)
	}
	return calls[len(calls)-1]
}

// ---------- Registration ----------

func TestBaselineRegistersExactly28Tools(t *testing.T) {
	s, _, _ := blSetup(t, nil)
	want := []string{
		"discord_login", "discord_list_servers", "discord_get_server_info", "discord_governance_audit",
		"discord_create_text_channel", "discord_create_voice_channel", "discord_create_category",
		"discord_edit_channel", "discord_delete_channel", "discord_set_channel_permissions",
		"discord_send", "discord_read_messages", "discord_edit_message", "discord_delete_message", "discord_bulk_delete_messages",
		"discord_create_forum_channel", "discord_create_forum_post", "discord_get_forum_post",
		"discord_reply_to_forum", "discord_list_forum_threads", "discord_manage_threads",
		"discord_add_reaction", "discord_add_multiple_reactions", "discord_remove_reaction",
		"discord_create_poll", "discord_get_poll_results",
		"discord_list_members", "discord_manage_roles",
	}
	var got []string
	for name := range s.ListTools() {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(want) != 28 || !slices.Equal(got, want) {
		t.Fatalf("registered tools:\n got  %v\n want %v", got, want)
	}
}

// ---------- Guild tools ----------

func TestGuildTools(t *testing.T) {
	blRunCases(t, []blCase{
		{
			tool: "discord_login", noGuild: true,
			args: map[string]any{"token": "super-secret-token", "profile": "ops"},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				if strings.Contains(txt, "super-secret-token") {
					t.Fatal("token echoed")
				}
				var s dto.SessionInfo
				_ = json.Unmarshal([]byte(txt), &s)
				if s.Profile != "ops" || s.BotUserID != discord.MockBotID {
					t.Errorf("session = %+v", s)
				}
			},
		},
		{
			tool: "discord_list_servers", read: true, noGuild: true, args: map[string]any{},
			check: func(t *testing.T, txt string, _ *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, `"count":1`) || !strings.Contains(txt, discord.MockGuildID) {
					t.Errorf("list = %s", txt)
				}
			},
		},
		{
			tool: "discord_get_server_info", read: true, required: []string{"guild_id"},
			args: map[string]any{"guild_id": "Test Guild"},
			check: func(t *testing.T, txt string, _ *discord.MockClient, _ deps.Deps) {
				var g dto.GuildDetail
				_ = json.Unmarshal([]byte(txt), &g)
				if g.ID != discord.MockGuildID || len(g.Channels) == 0 || len(g.Roles) == 0 || g.BoostTier != 1 {
					t.Errorf("guild = %+v", g)
				}
			},
		},
		{
			label: "governance_audit_logs", tool: "discord_governance_audit",
			required: []string{"action", "guild_id"},
			enums:    map[string][]string{"action": {"audit_logs", "automod_rules", "timeout_member"}},
			roOK:     true, // tool can mutate (timeout_member) but audit_logs is a read
			args:     map[string]any{"action": "audit_logs", "guild_id": discord.MockGuildID, "action_type": 22},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, "MEMBER_BAN_ADD") {
					t.Errorf("audit = %s", txt)
				}
				if c := blLastCall(t, m, "GetAuditLogs"); c.Args[1] != 22 || c.Args[2] != 50 {
					t.Errorf("GetAuditLogs args = %v", c.Args)
				}
			},
		},
		{
			label: "governance_timeout", tool: "discord_governance_audit", dryRun: true,
			required: []string{"action", "guild_id"},
			args:     map[string]any{"action": "timeout_member", "guild_id": "Test Guild", "user_id": "@alice", "duration_seconds": 600, "reason": "spam"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				c := blLastCall(t, m, "TimeoutMember")
				if c.Args[1] != discord.MockAliceID || c.Args[2] != 600 {
					t.Errorf("TimeoutMember args = %v", c.Args)
				}
			},
		},
	})
}

// governance_audit's read actions must work in read-only mode even though the tool can mutate.
func TestGovernanceAuditReadActionsInReadOnly(t *testing.T) {
	s, _, _ := blSetup(t, &config.Config{ReadOnly: true})
	blWantOK(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "automod_rules", "guild_id": discord.MockGuildID}))
	blWantOK(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "audit_logs", "guild_id": discord.MockGuildID}))
	blWantErr(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "timeout_member", "guild_id": discord.MockGuildID, "user_id": "alice", "duration_seconds": 60}), "read-only")
}

func TestGovernanceTimeoutValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "timeout_member", "guild_id": discord.MockGuildID, "user_id": "alice"}), "duration_seconds is required")
	blWantErr(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "timeout_member", "guild_id": discord.MockGuildID, "duration_seconds": 5}), "user_id is required")
	blWantErr(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "timeout_member", "guild_id": discord.MockGuildID, "user_id": "alice", "duration_seconds": 9999999}), "2419200")
	blWantErr(t, callTool(t, s, "discord_governance_audit", map[string]any{"action": "bogus", "guild_id": discord.MockGuildID}), "action must be")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("validation failures must not mutate")
	}
}

func TestLoginProfileAndErrors(t *testing.T) {
	cfg := &config.Config{Profiles: map[string]config.Profile{"ops": {TokenEnv: "DMCP_TEST_OPS_TOKEN"}}}
	s, m, _ := blSetup(t, cfg)
	blWantErr(t, callTool(t, s, "discord_login", map[string]any{}), "token is required")
	blWantErr(t, callTool(t, s, "discord_login", map[string]any{"profile": "nope"}), "unknown profile")

	t.Setenv("DMCP_TEST_OPS_TOKEN", "profile-token-xyz")
	txt := blWantOK(t, callTool(t, s, "discord_login", map[string]any{"profile": "ops"}))
	if strings.Contains(txt, "profile-token-xyz") || !strings.Contains(txt, `"profile":"ops"`) {
		t.Fatalf("login = %s", txt)
	}

	m.Err["Login"] = errors.New("401 Unauthorized for token bad-token-123")
	res := callTool(t, s, "discord_login", map[string]any{"token": "bad-token-123"})
	blWantErr(t, res, "***")
	if strings.Contains(resultText(t, res), "bad-token-123") {
		t.Fatal("token leaked in error")
	}
}

func TestGetServerInfoListsAllChannels(t *testing.T) {
	s, _, _ := blSetup(t, nil)
	txt := blWantOK(t, callTool(t, s, "discord_get_server_info", map[string]any{"guild_id": discord.MockGuildID}))
	if !strings.Contains(txt, discord.MockAnnouncementID) || !strings.Contains(txt, discord.MockGeneralID) {
		t.Fatalf("channels missing: %s", txt)
	}
}

func TestGuildFallbackWhenOmitted(t *testing.T) {
	s, _, _ := blSetup(t, nil)
	txt := blWantOK(t, callTool(t, s, "discord_get_server_info", map[string]any{}))
	if !strings.Contains(txt, discord.MockGuildID) {
		t.Fatalf("single-guild fallback failed: %s", txt)
	}
}
