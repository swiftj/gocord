package tools

import (
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

func TestForumThreadTools(t *testing.T) {
	blRunCases(t, []blCase{
		{
			tool: "discord_create_forum_channel", required: []string{"guild_id", "name"},
			args: map[string]any{"guild_id": "Test Guild", "name": "ideas", "tags": []any{"bug", "idea", "Bug"}, "category_id": "Community", "default_reaction": "👍"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateForumChannel").Args[0].(dto.CreateForumReq)
				if r.GuildID != discord.MockGuildID || r.ParentID != discord.MockCategoryID || len(r.Tags) != 2 || r.DefaultReaction != "👍" {
					t.Errorf("CreateForumChannel req = %+v", r)
				}
			},
		},
		{
			tool: "discord_create_forum_post", required: []string{"forum_channel_id", "title", "content"},
			args: map[string]any{"forum_channel_id": "#help-forum", "title": "Crash on start", "content": "Stack trace…", "tag_names": []any{"bug"}},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateForumPost").Args[0].(dto.CreateForumPostReq)
				if r.ForumChannelID != discord.MockForumID || r.Title != "Crash on start" || len(r.TagNames) != 1 {
					t.Errorf("CreateForumPost req = %+v", r)
				}
				if !strings.Contains(txt, `"name":"Crash on start"`) {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			tool: "discord_get_forum_post", required: []string{"thread_id"}, read: true,
			args: map[string]any{"thread_id": "How do I search?", "limit": 10},
			check: func(t *testing.T, txt string, _ *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, `"starter"`) || !strings.Contains(txt, "How do I search old messages?") {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			tool: "discord_reply_to_forum", required: []string{"thread_id", "content"},
			args: map[string]any{"thread_id": discord.MockForumThreadID, "content": "Use discord_search_messages"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "ReplyToForum"); c.Args[0] != discord.MockForumThreadID {
					t.Errorf("ReplyToForum args = %v", c.Args)
				}
			},
		},
		{
			tool: "discord_list_forum_threads", required: []string{"forum_channel_id"}, read: true,
			args: map[string]any{"forum_channel_id": "#help-forum", "include_archived": true},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, `"count":1`) || !strings.Contains(txt, discord.MockForumThreadID) {
					t.Errorf("result = %s", txt)
				}
				if c := blLastCall(t, m, "ListForumThreads"); c.Args[1] != true {
					t.Errorf("include_archived not passed: %v", c.Args)
				}
			},
		},
		{
			label: "manage_threads_create", tool: "discord_manage_threads", required: []string{"action"},
			enums: map[string][]string{"action": {"create", "archive", "unarchive", "lock", "unlock"}},
			args:  map[string]any{"action": "create", "channel_id": "#general", "name": "Release planning", "message_id": discord.MockFirstMessageID, "auto_archive_minutes": 1440},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreateThread").Args[0].(dto.CreateThreadReq)
				if r.ChannelID != discord.MockGeneralID || r.MessageID != discord.MockFirstMessageID || r.AutoArchiveMinutes != 1440 {
					t.Errorf("CreateThread req = %+v", r)
				}
			},
		},
		{
			label: "manage_threads_archive", tool: "discord_manage_threads", required: []string{"action"}, dryRun: true,
			args: map[string]any{"action": "archive", "thread_id": discord.MockForumThreadID},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				c := blLastCall(t, m, "ModifyThread")
				if a := c.Args[1].(*bool); a == nil || !*a {
					t.Errorf("ModifyThread args = %v", c.Args)
				}
			},
		},
		{
			label: "manage_threads_lock", tool: "discord_manage_threads", required: []string{"action"}, dryRun: true,
			args: map[string]any{"action": "lock", "thread_id": "How do I search?"},
		},
	})
}

func TestManageThreadsPreviewShowsCurrentState(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	var p dto.DryRunPreview
	decodeResult(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "archive", "thread_id": discord.MockForumThreadID, "dry_run": true}), &p)
	if len(p.Changes) != 1 || !strings.Contains(p.Changes[0], "archived: false → true") {
		t.Fatalf("changes = %v", p.Changes)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("mutating calls in dry run")
	}
}

func TestManageThreadsValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "create", "channel_id": "#general", "name": "x", "auto_archive_minutes": 30}), "auto_archive_minutes")
	blWantErr(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "create", "channel_id": "#general"}), "name is required")
	blWantErr(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "create", "channel_id": "#help-forum", "name": "x"}), "discord_create_forum_post")
	blWantErr(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "archive", "thread_id": "#general"}), "not a thread")
	blWantErr(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "lock", "channel_id": "#general"}), "needs thread_id")
	blWantErr(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "explode"}), "action must be")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("validation failures must not mutate")
	}
	// unarchive is a plain write: no dry run required, executes directly.
	blWantOK(t, callTool(t, s, "discord_manage_threads", map[string]any{"action": "unarchive", "thread_id": discord.MockForumThreadID}))
}

func TestForumPostRequiresForum(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_create_forum_post", map[string]any{"forum_channel_id": "#general", "title": "x", "content": "y"}), "not a forum")
	blWantErr(t, callTool(t, s, "discord_list_forum_threads", map[string]any{"forum_channel_id": "#general"}), "not a forum")
	blWantErr(t, callTool(t, s, "discord_reply_to_forum", map[string]any{"thread_id": "#general", "content": "y"}), "not a thread")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("unexpected mutation")
	}
}
