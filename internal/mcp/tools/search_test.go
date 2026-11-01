package tools

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// nextgenRegisterAll registers every tool owned by the mcp-nextgen-tools teammate.
var nextgenRegisterAll = []func(*server.MCPServer, deps.Deps){
	RegisterSearchTools, RegisterWebhookPersonaTools, RegisterInteractionTools, RegisterEventTools,
}

func TestNextgenRegistersExactlyNineTools(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, nextgenRegisterAll...)
	var got []string
	for name := range s.ListTools() {
		got = append(got, name)
	}
	slices.Sort(got)
	want := []string{
		"discord_await_interaction", "discord_create_webhook", "discord_delete_webhook", "discord_edit_webhook",
		"discord_manage_events", "discord_persona_register", "discord_persona_send", "discord_search_messages",
		"discord_send_webhook_message",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("tools = %v\nwant %v", got, want)
	}
}

// nextgenSchema asserts required params and returns the tool definition.
func nextgenSchema(t *testing.T, s *server.MCPServer, name string, required ...string) mcp.Tool {
	t.Helper()
	st := s.GetTool(name)
	if st == nil {
		t.Fatalf("tool %q not registered", name)
	}
	for _, r := range required {
		if !slices.Contains(st.Tool.InputSchema.Required, r) {
			t.Errorf("%s: %q not required (required=%v)", name, r, st.Tool.InputSchema.Required)
		}
		if _, ok := st.Tool.InputSchema.Properties[r]; !ok {
			t.Errorf("%s: missing property %q", name, r)
		}
	}
	if st.Tool.Description == "" {
		t.Errorf("%s: empty description", name)
	}
	return st.Tool
}

func nextgenEnum(t *testing.T, tool mcp.Tool, prop string, want ...string) {
	t.Helper()
	p, ok := tool.InputSchema.Properties[prop].(map[string]any)
	if !ok {
		t.Fatalf("%s: property %q missing", tool.Name, prop)
	}
	got, _ := p["enum"].([]string)
	for _, w := range want {
		if !slices.Contains(got, w) {
			t.Errorf("%s.%s enum %v lacks %q", tool.Name, prop, got, w)
		}
	}
}

func searchCall(t *testing.T, s *server.MCPServer, args map[string]any) searchResponse {
	t.Helper()
	var out searchResponse
	decodeResult(t, callTool(t, s, searchToolName, args), &out)
	return out
}

func TestSearchSchema(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterSearchTools)
	tool := nextgenSchema(t, s, searchToolName, "guild_id", "query")
	if tool.Annotations.ReadOnlyHint == nil || !*tool.Annotations.ReadOnlyHint {
		t.Error("search must be read-only")
	}
	for _, p := range []string{"channel_id", "author_id", "before", "after", "has_attachments", "limit", "auto_backfill_limit"} {
		if _, ok := tool.InputSchema.Properties[p]; !ok {
			t.Errorf("missing param %s", p)
		}
	}
}

func TestSearchBackfillThenFind(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterSearchTools)

	out := searchCall(t, s, map[string]any{"guild_id": "Test Guild", "query": "release"})
	if out.Count != 2 {
		t.Fatalf("want 2 hits for 'release', got %+v", out)
	}
	// general (2) + forum thread (1); announcements is empty.
	if out.Backfilled != 3 || out.ChannelsSearched != 3 || out.IndexedChannels != 3 {
		t.Fatalf("counts: %+v", out)
	}
	if out.Warning != "" {
		t.Fatalf("unexpected warning %q", out.Warning)
	}
	for _, id := range []string{discord.MockGeneralID, discord.MockAnnouncementID, discord.MockForumThreadID} {
		if _, _, full, _ := d.Store.Coverage(context.Background(), id); !full {
			t.Errorf("channel %s not marked fully backfilled after empty page", id)
		}
	}
	// Forum thread messages are indexed and searchable through the forum's ID.
	forum := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "search", "channel_id": discord.MockForumID})
	if forum.Count != 1 || forum.Items[0].ChannelID != discord.MockForumThreadID {
		t.Fatalf("forum search: %+v", forum)
	}

	// A second call must not hit the REST API again.
	mock.Reset()
	again := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "hello"})
	if again.Count != 1 || again.Backfilled != 0 {
		t.Fatalf("second search: %+v", again)
	}
	if n := len(mock.CallsTo("ReadMessages")); n != 0 {
		t.Fatalf("second call re-backfilled (%d ReadMessages)", n)
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatal("search made mutating calls")
	}
}

func TestSearchChannelResolutionAndAuthor(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterSearchTools)
	out := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "release OR search", "channel_id": "#general", "author_id": "@alice"})
	if out.Count != 1 || out.Items[0].ID != discord.MockFirstMessageID {
		t.Fatalf("got %+v", out)
	}
	if out.ChannelsSearched != 1 {
		t.Fatalf("channel scope not applied: %+v", out)
	}
	for _, c := range mock.CallsTo("ReadMessages") {
		if c.Args[0].(dto.ReadMessagesReq).ChannelID != discord.MockGeneralID {
			t.Fatalf("backfilled the wrong channel: %+v", c.Args)
		}
	}
}

func TestSearchBudgetTruncationWarns(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	for i := range 150 {
		mock.Messages[discord.MockGeneralID] = append(mock.Messages[discord.MockGeneralID], dto.MessageItem{
			ID: strconv.Itoa(100000000000001000 + i), ChannelID: discord.MockGeneralID,
			AuthorID: discord.MockAliceID, Author: "alice", Content: "filler message " + strconv.Itoa(i),
		})
	}
	s := newTestServer(t, d, RegisterSearchTools)
	out := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "filler", "auto_backfill_limit": 50})
	if out.Backfilled != 50 {
		t.Fatalf("budget not respected: %+v", out)
	}
	if !strings.Contains(out.Warning, "partial index: 3 channels not fully backfilled") {
		t.Fatalf("warning = %q", out.Warning)
	}
	if _, _, full, _ := d.Store.Coverage(context.Background(), discord.MockGeneralID); full {
		t.Fatal("truncated channel marked full")
	}
	// Continue from the oldest indexed message on the next call.
	out = searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "filler", "auto_backfill_limit": 5000, "limit": 100})
	if out.Backfilled != 103 || out.Warning != "" || out.Count != 100 {
		t.Fatalf("resume: %+v", out)
	}
}

func TestSearchZeroBudgetUsesExistingIndex(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterSearchTools)
	out := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "release", "auto_backfill_limit": 0})
	if out.Count != 0 || !strings.Contains(out.Warning, "partial index") {
		t.Fatalf("got %+v", out)
	}
	if len(mock.CallsTo("ReadMessages")) != 0 {
		t.Fatal("backfill ran with zero budget")
	}
}

func TestSearchAfterBoundStopsBackfill(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	for i := range 250 {
		mock.Messages[discord.MockGeneralID] = append(mock.Messages[discord.MockGeneralID], dto.MessageItem{
			ID: strconv.Itoa(100000000000001000 + i), ChannelID: discord.MockGeneralID, AuthorID: discord.MockAliceID, Author: "alice", Content: "x" + strconv.Itoa(i),
		})
	}
	s := newTestServer(t, d, RegisterSearchTools)
	out := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "x", "channel_id": discord.MockGeneralID, "after": "100000000000001200"})
	if out.Backfilled != 100 || out.Warning != "" {
		t.Fatalf("after bound not honoured: %+v", out)
	}
}

func TestSearchInvalidFTSRewriteWarning(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterSearchTools)
	out := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": `"hello world`})
	if !strings.Contains(out.Warning, "exact phrase") || out.Count != 1 {
		t.Fatalf("got %+v", out)
	}
}

func TestSearchPolicy(t *testing.T) {
	d, _ := newTestDeps(t, &config.Config{ReadOnly: true})
	s := newTestServer(t, d, RegisterSearchTools)
	if out := searchCall(t, s, map[string]any{"guild_id": discord.MockGuildID, "query": "release"}); out.Count != 2 {
		t.Fatalf("read-only must still allow search: %+v", out)
	}

	d2, mock := newTestDeps(t, &config.Config{BlockedGuilds: []string{discord.MockGuildID}})
	s2 := newTestServer(t, d2, RegisterSearchTools)
	res := callTool(t, s2, searchToolName, map[string]any{"guild_id": discord.MockGuildID, "query": "release"})
	if !res.IsError || !strings.Contains(resultText(t, res), "blocked") {
		t.Fatalf("blocked guild: %s", resultText(t, res))
	}
	if len(mock.CallsTo("ReadMessages")) != 0 {
		t.Fatal("blocked guild was backfilled")
	}

	d3, mock3 := newTestDeps(t, &config.Config{BlockedChannels: []string{discord.MockGeneralID}})
	s3 := newTestServer(t, d3, RegisterSearchTools)
	out := searchCall(t, s3, map[string]any{"guild_id": discord.MockGuildID, "query": "release OR search"})
	if out.Count != 1 || out.Items[0].ChannelID != discord.MockForumThreadID {
		t.Fatalf("blocked channel leaked: %+v", out)
	}
	for _, c := range mock3.CallsTo("ReadMessages") {
		if c.Args[0].(dto.ReadMessagesReq).ChannelID == discord.MockGeneralID {
			t.Fatal("blocked channel was backfilled")
		}
	}
}

func TestSearchValidation(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterSearchTools)
	for _, args := range []map[string]any{
		{"guild_id": discord.MockGuildID, "query": "  "},
		{"guild_id": "No Such Guild", "query": "x"},
		{"guild_id": discord.MockGuildID, "query": "x", "before": "yesterday"},
		{"guild_id": discord.MockGuildID, "query": "x", "channel_id": "#nope"},
	} {
		if res := callTool(t, s, searchToolName, args); !res.IsError {
			t.Errorf("%v: expected error, got %s", args, resultText(t, res))
		}
	}
}
