package prompts

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

func testServer(t *testing.T, cfg *config.Config) (*server.MCPServer, *discord.MockClient) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	mock := discord.NewMockClient()
	store, err := index.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := server.NewMCPServer("test", "0", server.WithPromptCapabilities(false))
	Register(s, deps.Deps{Discord: mock, Store: store, Guard: policy.New(cfg), Config: cfg})
	old := now
	now = func() time.Time { return time.Date(2026, 11, 1, 13, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = old })
	return s, mock
}

func getPrompt(t *testing.T, s *server.MCPServer, name string, args map[string]string) (*mcp.GetPromptResult, error) {
	t.Helper()
	p, ok := s.ListPrompts()[name]
	if !ok {
		t.Fatalf("prompt %q not registered", name)
	}
	req := mcp.GetPromptRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	return p.Handler(context.Background(), req)
}

// promptText concatenates all text and embedded-resource text.
func promptText(r *mcp.GetPromptResult) string {
	var b strings.Builder
	for _, m := range r.Messages {
		if tc, ok := mcp.AsTextContent(m.Content); ok {
			b.WriteString(tc.Text)
		}
		if er, ok := m.Content.(mcp.EmbeddedResource); ok {
			if tr, ok := er.Resource.(mcp.TextResourceContents); ok {
				b.WriteString(tr.URI + "\n" + tr.Text)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestPromptsRegisteredWithRequiredArgs(t *testing.T) {
	s, _ := testServer(t, nil)
	want := map[string][]string{
		ChannelDigest:   {"channel_id"},
		ServerBlueprint: {"guild_id", "goal"},
		HITLApproval:    {"channel_id", "decision"},
	}
	ps := s.ListPrompts()
	if len(ps) != len(want) {
		t.Fatalf("got %d prompts", len(ps))
	}
	for name, req := range want {
		p, ok := ps[name]
		if !ok {
			t.Fatalf("missing prompt %s", name)
		}
		var got []string
		for _, a := range p.Prompt.Arguments {
			if a.Required {
				got = append(got, a.Name)
			}
		}
		if !slices.Equal(got, req) {
			t.Errorf("%s required args = %v, want %v", name, got, req)
		}
		if p.Prompt.Description == "" {
			t.Errorf("%s has no description", name)
		}
		if _, err := getPrompt(t, s, name, map[string]string{}); err == nil {
			t.Errorf("%s: missing required args should error", name)
		}
	}
}

func TestChannelDigestRenders(t *testing.T) {
	s, mock := testServer(t, nil)
	r, err := getPrompt(t, s, ChannelDigest, map[string]string{"channel_id": "#general", "since": "24h", "audience": "executives"})
	if err != nil {
		t.Fatal(err)
	}
	txt := promptText(r)
	for _, want := range []string{
		"discord://channels/" + discord.MockGeneralID + "/transcript",
		"[2026-11-01 12:00] @alice: Hello world, shipping the release tomorrow",
		"[2026-11-01 12:01] @discord-mcp: Release checklist posted",
		"Summary", "Decisions", "Open Questions", "Action Items (owner → task)", "Notable Links", "executives",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("digest lacks %q:\n%s", want, txt)
		}
	}
	if strings.Index(txt, "Hello world") > strings.Index(txt, "Release checklist") {
		t.Error("transcript not chronological")
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatal("prompt mutated state")
	}
	// A window that excludes everything.
	r, err = getPrompt(t, s, ChannelDigest, map[string]string{"channel_id": discord.MockGeneralID, "since": "2026-11-01T12:30:00Z"})
	if err != nil || !strings.Contains(promptText(r), "no messages in this window") {
		t.Fatalf("empty window: %v %s", err, promptText(r))
	}
	if _, err := getPrompt(t, s, ChannelDigest, map[string]string{"channel_id": "#general", "since": "fortnight"}); err == nil {
		t.Fatal("bad since must error")
	}
}

func TestChannelDigestPolicy(t *testing.T) {
	s, _ := testServer(t, &config.Config{BlockedChannels: []string{discord.MockGeneralID}})
	if _, err := getPrompt(t, s, ChannelDigest, map[string]string{"channel_id": "#general"}); err == nil {
		t.Fatal("blocked channel must not be digested")
	}
}

func TestServerBlueprintRenders(t *testing.T) {
	s, mock := testServer(t, &config.Config{ReadOnly: true})
	r, err := getPrompt(t, s, ServerBlueprint, map[string]string{"guild_id": "Test Guild", "goal": "open-source project with support forum"})
	if err != nil {
		t.Fatal(err)
	}
	txt := promptText(r)
	for _, want := range []string{
		"discord://guilds/" + discord.MockGuildID + "/map",
		"# Test Guild (" + discord.MockGuildID + ")",
		"@Moderator (" + discord.MockModeratorRole + ")",
		"#general (" + discord.MockGeneralID + ") text",
		"dry_run: true", "open-source project with support forum", "read-only mode",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("blueprint lacks %q:\n%s", want, txt)
		}
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatal("prompt mutated state")
	}
}

func TestHITLApprovalRenders(t *testing.T) {
	s, mock := testServer(t, nil)
	r, err := getPrompt(t, s, HITLApproval, map[string]string{
		"channel_id": "#general", "decision": "Delete #old-logs", "context": "Disk is full",
		"options": "Ship it, Hold", "approver": "@Moderator", "timeout_seconds": "120",
	})
	if err != nil {
		t.Fatal(err)
	}
	txt := promptText(r)
	for _, want := range []string{
		"discord_await_interaction", `"` + discord.MockGeneralID + `"`, `{"label": "Ship it"}, {"label": "Hold"}`,
		`allowed_role_ids: ["` + discord.MockModeratorRole + `"]`, "timeout_seconds: 120", `"ship-it"`,
		"What", "Why", "Risk", "Rollback", "Delete #old-logs", "Disk is full",
	} {
		if !strings.Contains(txt, want) {
			t.Errorf("approval lacks %q:\n%s", want, txt)
		}
	}
	r, err = getPrompt(t, s, HITLApproval, map[string]string{"channel_id": "#general", "decision": "Deploy", "approver": "@alice"})
	if err != nil {
		t.Fatal(err)
	}
	txt = promptText(r)
	if !strings.Contains(txt, `allowed_user_ids: ["`+discord.MockAliceID+`"]`) || !strings.Contains(txt, `"approve"`) ||
		!strings.Contains(txt, "timeout_seconds: 300") {
		t.Errorf("defaults/user approver:\n%s", txt)
	}
	if _, err := getPrompt(t, s, HITLApproval, map[string]string{"channel_id": "#general", "decision": "x", "timeout_seconds": "5"}); err == nil {
		t.Fatal("out-of-range timeout must error")
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatal("prompt mutated state")
	}
}
