package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// msgFreshIDs returns n Snowflakes created just now (inside the 14-day bulk window).
func msgFreshIDs(n int) []any {
	base, _ := strconv.ParseUint(dto.SnowflakeFromTime(time.Now().Add(-time.Hour)), 10, 64)
	out := make([]any, n)
	for i := range out {
		out[i] = strconv.FormatUint(base+uint64(i), 10)
	}
	return out
}

func TestMessageTools(t *testing.T) {
	fresh := msgFreshIDs(3)
	blRunCases(t, []blCase{
		{
			tool: "discord_send", required: []string{"channel_id", "content"},
			args: map[string]any{"channel_id": "#general", "content": "hello", "reply_to_message_id": discord.MockFirstMessageID,
				"embed": map[string]any{"title": "T", "description": "D", "color": 255}},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "SendMessage").Args[0].(dto.SendMessageReq)
				if r.ChannelID != discord.MockGeneralID || r.Content != "hello" || r.Embed == nil || r.Embed.Title != "T" || r.ReplyToMessageID != discord.MockFirstMessageID {
					t.Errorf("SendMessage req = %+v", r)
				}
				if !strings.Contains(txt, `"content":"hello"`) {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			tool: "discord_read_messages", required: []string{"channel_id"}, read: true,
			args: map[string]any{"channel_id": "#general", "limit": 10},
			check: func(t *testing.T, txt string, m *discord.MockClient, d deps.Deps) {
				var out struct {
					Count int               `json:"count"`
					Items []dto.MessageItem `json:"items"`
				}
				_ = json.Unmarshal([]byte(txt), &out)
				if out.Count != 2 || out.Items[0].ID != "100000000000000101" {
					t.Errorf("result = %s", txt)
				}
				// Opportunistic index upsert.
				hits, err := d.Store.Search(context.Background(), dto.SearchQuery{GuildID: discord.MockGuildID, Query: "shipping"})
				if err != nil || len(hits) != 1 {
					t.Errorf("index not populated: %v %v", hits, err)
				}
			},
		},
		{
			tool: "discord_edit_message", required: []string{"channel_id", "message_id", "content"},
			args: map[string]any{"channel_id": "#general", "message_id": "100000000000000101", "content": "edited"},
			check: func(t *testing.T, txt string, _ *discord.MockClient, _ deps.Deps) {
				if !strings.Contains(txt, `"content":"edited"`) {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			tool: "discord_delete_message", required: []string{"channel_id", "message_id"}, dryRun: true,
			args: map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "DeleteMessage"); c.Args[0] != discord.MockGeneralID || c.Args[1] != discord.MockFirstMessageID {
					t.Errorf("DeleteMessage args = %v", c.Args)
				}
			},
		},
		{
			tool: "discord_bulk_delete_messages", required: []string{"channel_id", "message_ids"}, dryRun: true,
			args: map[string]any{"channel_id": "#general", "message_ids": fresh},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				ids := blLastCall(t, m, "BulkDeleteMessages").Args[1].([]string)
				if len(ids) != 3 {
					t.Errorf("ids = %v", ids)
				}
			},
		},
	})
}

func TestSendValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "content": strings.Repeat("x", 2001)}), "at most 2000")
	blWantErr(t, callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "content": ""}), "content is required")
	blWantErr(t, callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "content": "x", "file_path": "rel/file.txt"}), "must be absolute")
	dir := t.TempDir()
	blWantErr(t, callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "content": "x", "file_path": dir}), "not a regular file")
	blWantErr(t, callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "content": "x", "file_path": filepath.Join(dir, "missing")}), "file_path")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("validation failures must not mutate")
	}
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	blWantOK(t, callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "content": "", "file_path": f}))
	if r := blLastCall(t, m, "SendMessage").Args[0].(dto.SendMessageReq); r.FilePath != f {
		t.Fatalf("file path = %q", r.FilePath)
	}
}

func TestSendToThreadChecksParentPolicy(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantOK(t, callTool(t, s, "discord_send", map[string]any{"channel_id": discord.MockForumThreadID, "content": "hi"}))
	if len(m.CallsTo("SendMessage")) != 1 {
		t.Fatal("send to thread failed")
	}
}

func TestReadMessagesCursorsAndMentions(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_read_messages", map[string]any{"channel_id": "#general", "before": discord.MockFirstMessageID, "after": discord.MockFirstMessageID}), "at most one")
	blWantErr(t, callTool(t, s, "discord_read_messages", map[string]any{"channel_id": "#general", "before": "yesterday"}), "before")

	// ISO-8601 cursor is converted to a Snowflake.
	blWantOK(t, callTool(t, s, "discord_read_messages", map[string]any{"channel_id": "#general", "before": "2026-11-01T12:00:30Z"}))
	r := blLastCall(t, m, "ReadMessages").Args[0].(dto.ReadMessagesReq)
	if r.Before != dto.SnowflakeFromTime(time.Date(2026, 11, 1, 12, 0, 30, 0, time.UTC)) {
		t.Fatalf("before = %q", r.Before)
	}

	// Mentions are resolved; next_cursor is the oldest ID when the page is full.
	m.Messages[discord.MockGeneralID][1].Content = "ping <@" + discord.MockAliceID + "> and <@&" + discord.MockModeratorRole + "> in <#" + discord.MockAnnouncementID + ">"
	txt := blWantOK(t, callTool(t, s, "discord_read_messages", map[string]any{"channel_id": "#general", "limit": 1}))
	if !strings.Contains(txt, "ping @alice and @Moderator in #announcements") {
		t.Fatalf("mentions not resolved: %s", txt)
	}
	if !strings.Contains(txt, `"next_cursor":"100000000000000101"`) {
		t.Fatalf("next_cursor missing: %s", txt)
	}
}

func TestEditMessageNotBotAuthored(t *testing.T) {
	s, _, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_edit_message", map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID, "content": "x"}),
		"only messages authored by the bot")
}

func TestBulkDeleteLimits(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	// Fixture messages are from 2015 (synthetic Snowflakes) → older than 14 days.
	blWantErr(t, callTool(t, s, "discord_bulk_delete_messages", map[string]any{"channel_id": "#general",
		"message_ids": []any{discord.MockFirstMessageID, "100000000000000101"}}), "older than 14 days")
	blWantErr(t, callTool(t, s, "discord_bulk_delete_messages", map[string]any{"channel_id": "#general",
		"message_ids": msgFreshIDs(1)}), "2-100")
	blWantErr(t, callTool(t, s, "discord_bulk_delete_messages", map[string]any{"channel_id": "#general",
		"message_ids": []any{"abc", "def"}}), "not a message Snowflake")
	// Even a dry run reports the age denial.
	blWantErr(t, callTool(t, s, "discord_bulk_delete_messages", map[string]any{"channel_id": "#general",
		"message_ids": []any{discord.MockFirstMessageID, "100000000000000101"}, "dry_run": true}), "older than 14 days")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("unexpected mutation")
	}
}
