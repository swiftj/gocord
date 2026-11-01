package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

func newDeps(t *testing.T, cfg *config.Config) (deps.Deps, *discord.MockClient) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	m := discord.NewMockClient()
	st, err := index.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return deps.Deps{Discord: m, Store: st, Guard: policy.New(cfg), Config: cfg}, m
}

// readURI sends a JSON-RPC resources/read through the server and returns the text or error.
func readURI(t *testing.T, s *server.MCPServer, uri string) (text, mime string, rpcErr string) {
	t.Helper()
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":%q}}`, uri)
	resp := s.HandleMessage(context.Background(), json.RawMessage(msg))
	b, _ := json.Marshal(resp)
	var out struct {
		Result struct {
			Contents []struct {
				Text     string `json:"text"`
				MIMEType string `json:"mimeType"`
			} `json:"contents"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	if out.Error != nil {
		return "", "", out.Error.Message
	}
	if len(out.Result.Contents) != 1 {
		t.Fatalf("contents = %s", b)
	}
	return out.Result.Contents[0].Text, out.Result.Contents[0].MIMEType, ""
}

func newServer(d deps.Deps) *server.MCPServer {
	s := server.NewMCPServer("test", "0", server.WithResourceCapabilities(true, true))
	Register(s, d)
	return s
}

func TestGuildMapGolden(t *testing.T) {
	d, m := newDeps(t, nil)
	m.Guilds[discord.MockGuildID] = dto.GuildDetail{
		GuildSummary: dto.GuildSummary{ID: discord.MockGuildID, Name: "Test Guild", MemberCount: 1234},
		BoostTier:    2,
	}
	m.Channels["100000000000000070"] = dto.ChannelInfo{ID: "100000000000000070", GuildID: discord.MockGuildID, Name: "Lounge", Type: dto.ChannelVoice, Position: 9}
	m.Channels[discord.MockGeneralID] = dto.ChannelInfo{ID: discord.MockGeneralID, GuildID: discord.MockGuildID, Name: "general", Type: dto.ChannelText,
		ParentID: discord.MockCategoryID, Position: 1, Topic: strings.Repeat("a", 100)}
	m.Channels["100000000000000061"] = dto.ChannelInfo{ID: "100000000000000061", GuildID: discord.MockGuildID, Name: "Release planning", Type: dto.ChannelThread, ParentID: discord.MockGeneralID}
	m.Threads["100000000000000061"] = dto.ThreadInfo{ID: "100000000000000061", ParentID: discord.MockGeneralID, Name: "Release planning"}
	m.Threads["100000000000000062"] = dto.ThreadInfo{ID: "100000000000000062", ParentID: discord.MockGeneralID, Name: "Old", Archived: true}
	m.Roles[discord.MockGuildID][1].PermissionNames = []string{"ManageMessages", "KickMembers"}

	text, mime, errMsg := readURI(t, newServer(d), "discord://guilds/"+discord.MockGuildID+"/map")
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	want := `# Test Guild (100000000000000001) · 1,234 members · tier 2
## Roles
- @MCP Bot (100000000000000041) pos 10 · ManageRoles · managed
- @Moderator (100000000000000040) pos 5 · ManageMessages, KickMembers
## Channels
- 📁 Community (100000000000000020)
  - #general (100000000000000010) text · "` + strings.Repeat("a", 79) + `…"
    - 🧵 Release planning (100000000000000061) active
  - #announcements (100000000000000011) announcement
  - 🗂 #help-forum (100000000000000030) forum · tags: question
    - 🧵 How do I search? (100000000000000060) active
- 🔊 Lounge (100000000000000070) voice
`
	if text != want {
		t.Fatalf("map mismatch:\n--- got ---\n%s\n--- want ---\n%s", text, want)
	}
	if mime != "text/markdown" {
		t.Fatalf("mime = %q", mime)
	}
}

func TestGuildMapByNameAndPolicy(t *testing.T) {
	d, _ := newDeps(t, nil)
	text, _, errMsg := readURI(t, newServer(d), "discord://guilds/Test%20Guild/map")
	if errMsg != "" || !strings.HasPrefix(text, "# Test Guild (100000000000000001)") {
		t.Fatalf("by name: %q %q", text, errMsg)
	}

	d2, _ := newDeps(t, &config.Config{BlockedGuilds: []string{discord.MockGuildID}})
	if _, _, errMsg := readURI(t, newServer(d2), "discord://guilds/"+discord.MockGuildID+"/map"); !strings.Contains(errMsg, "blocked") {
		t.Fatalf("blocked guild not denied: %q", errMsg)
	}

	d3, _ := newDeps(t, &config.Config{BlockedChannels: []string{discord.MockForumID}})
	text, _, _ = readURI(t, newServer(d3), "discord://guilds/"+discord.MockGuildID+"/map")
	if strings.Contains(text, "help-forum") {
		t.Fatalf("blocked channel listed:\n%s", text)
	}
}

func TestTranscriptGolden(t *testing.T) {
	d, m := newDeps(t, nil)
	msgs := m.Messages[discord.MockGeneralID]
	msgs[0].Content = "Shipping v1.2 tomorrow — <@&" + discord.MockModeratorRole + "> please review <#" + discord.MockAnnouncementID + ">"
	msgs = append(msgs, dto.MessageItem{ID: "100000000000000102", ChannelID: discord.MockGeneralID, AuthorID: "100000000000000051", Author: "bob",
		Content: "LGTM <@" + discord.MockAliceID + "> 👍", Timestamp: "2026-11-01T14:57:00Z", ReplyToID: discord.MockFirstMessageID,
		Attachments: []dto.AttachmentRef{{Filename: "a.png"}, {Filename: "b.png"}}})
	m.Messages[discord.MockGeneralID] = msgs

	text, mime, errMsg := readURI(t, newServer(d), "discord://channels/"+discord.MockGeneralID+"/transcript")
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	want := `[2026-11-01 12:00] @alice: Shipping v1.2 tomorrow — @Moderator please review #announcements
[2026-11-01 12:01] @discord-mcp: Release checklist posted
[2026-11-01 14:57] @bob ↪ reply to alice: LGTM @alice 👍  [2 attachments]
`
	if text != want {
		t.Fatalf("transcript mismatch:\n--- got ---\n%s\n--- want ---\n%s", text, want)
	}
	if mime != "text/plain" {
		t.Fatalf("mime = %q", mime)
	}
	// Transcript reads feed the index.
	hits, err := d.Store.Search(context.Background(), dto.SearchQuery{GuildID: discord.MockGuildID, Query: "LGTM"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("index: %v %v", hits, err)
	}
}

func TestTranscriptQueryParams(t *testing.T) {
	d, m := newDeps(t, nil)
	s := newServer(d)
	text, _, errMsg := readURI(t, s, "discord://channels/"+discord.MockGeneralID+"/transcript?limit=1")
	if errMsg != "" {
		t.Fatal(errMsg)
	}
	if strings.Count(text, "\n") != 1 || !strings.Contains(text, "Release checklist") {
		t.Fatalf("limit=1 transcript = %q", text)
	}
	text, _, errMsg = readURI(t, s, "discord://channels/"+discord.MockGeneralID+"/transcript?before=100000000000000101")
	if errMsg != "" || strings.Count(text, "\n") != 1 || !strings.Contains(text, "Hello world") {
		t.Fatalf("before transcript = %q %q", text, errMsg)
	}
	r := m.CallsTo("ReadMessages")
	if got := r[len(r)-1].Args[0].(dto.ReadMessagesReq).Before; got != "100000000000000101" {
		t.Fatalf("before not passed: %q", got)
	}
	// limit is capped at 500 and read in pages of ≤100.
	if _, _, errMsg := readURI(t, s, "discord://channels/"+discord.MockGeneralID+"/transcript?limit=9999"); errMsg != "" {
		t.Fatal(errMsg)
	}
	if _, _, errMsg := readURI(t, s, "discord://channels/"+discord.MockGeneralID+"/transcript?limit=abc"); !strings.Contains(errMsg, "limit") {
		t.Fatalf("bad limit not rejected: %q", errMsg)
	}
}

func TestTranscriptPaging(t *testing.T) {
	d, m := newDeps(t, nil)
	var msgs []dto.MessageItem
	for i := range 250 {
		id := fmt.Sprintf("1000000000000%05d", 10000+i)
		msgs = append(msgs, dto.MessageItem{ID: id, ChannelID: discord.MockGeneralID, Author: "alice", Content: fmt.Sprintf("m%d", i), Timestamp: "2026-11-01T12:00:00Z"})
	}
	m.Messages[discord.MockGeneralID] = msgs
	ch, _ := m.GetChannel(context.Background(), discord.MockGeneralID)
	text, err := RenderTranscript(context.Background(), d, ch, 230, "")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	if len(lines) != 230 || !strings.HasSuffix(lines[0], ": m20") || !strings.HasSuffix(lines[229], ": m249") {
		t.Fatalf("paging: %d lines, first %q last %q", len(lines), lines[0], lines[len(lines)-1])
	}
	if n := len(m.CallsTo("ReadMessages")); n != 3 {
		t.Fatalf("ReadMessages calls = %d, want 3", n)
	}
}

func TestTranscriptPolicy(t *testing.T) {
	d, _ := newDeps(t, &config.Config{BlockedChannels: []string{discord.MockForumID}})
	// A thread inherits its parent's block.
	if _, _, errMsg := readURI(t, newServer(d), "discord://channels/"+discord.MockForumThreadID+"/transcript"); !strings.Contains(errMsg, "blocked") {
		t.Fatalf("thread of blocked forum not denied: %q", errMsg)
	}
}

func TestGuildsResource(t *testing.T) {
	d, _ := newDeps(t, nil)
	text, mime, errMsg := readURI(t, newServer(d), "discord://guilds")
	if errMsg != "" || mime != "application/json" {
		t.Fatal(errMsg, mime)
	}
	var gs []dto.GuildSummary
	if err := json.Unmarshal([]byte(text), &gs); err != nil || len(gs) != 1 || gs[0].ID != discord.MockGuildID {
		t.Fatalf("guilds = %s", text)
	}
	d2, _ := newDeps(t, &config.Config{AllowedGuilds: []string{"100000000000000009"}})
	text, _, _ = readURI(t, newServer(d2), "discord://guilds")
	if text != "[]" {
		t.Fatalf("allowlist not applied: %s", text)
	}
}

func TestRegisteredTemplates(t *testing.T) {
	d, _ := newDeps(t, nil)
	s := newServer(d)
	resp := s.HandleMessage(context.Background(), json.RawMessage(`{"jsonrpc":"2.0","id":2,"method":"resources/templates/list"}`))
	b, _ := json.Marshal(resp)
	for _, want := range []string{"discord://guilds/{guild_id}/map", "discord://channels/{channel_id}/transcript"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("templates missing %s: %s", want, b)
		}
	}
	var _ mcp.ResourceContents = mcp.TextResourceContents{}
}

func TestMentionResolverLazyAndUnknown(t *testing.T) {
	_, m := newDeps(t, nil)
	r := NewMentionResolver(m, discord.MockGuildID)
	if got := r.Resolve(context.Background(), "no mentions"); got != "no mentions" || len(m.Calls) != 0 {
		t.Fatalf("resolver not lazy: %q %d calls", got, len(m.Calls))
	}
	in := "<@!" + discord.MockAliceID + "> <@100000000000000999> <#" + discord.MockForumThreadID + ">"
	if got := r.Resolve(context.Background(), in); got != "@alice <@100000000000000999> #How do I search?" {
		t.Fatalf("resolve = %q", got)
	}
}

func TestThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 1234567: "1,234,567"} {
		if got := thousands(n); got != want {
			t.Errorf("thousands(%d) = %q", n, got)
		}
	}
}
