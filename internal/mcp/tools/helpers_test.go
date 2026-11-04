package tools

// Shared test harness (Lead Agent owned). Wave 2 teammates reuse newTestDeps,
// callTool, and resultText rather than defining their own.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

// newTestDeps returns Deps backed by a fresh MockClient and an in-memory store.
// Pass a non-nil cfg to exercise policy (read-only).
func newTestDeps(t *testing.T, cfg *config.Config) (deps.Deps, *discord.MockClient) {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	mock := discord.NewMockClient()
	store, err := index.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return deps.Deps{Discord: mock, Store: store, Guard: policy.New(cfg), Config: cfg}, mock
}

// newTestServer registers tools via register and returns the server.
func newTestServer(t *testing.T, d deps.Deps, register ...func(*server.MCPServer, deps.Deps)) *server.MCPServer {
	t.Helper()
	s := server.NewMCPServer("test", "0.0.0", server.WithToolCapabilities(false))
	for _, r := range register {
		r(s, d)
	}
	return s
}

// callTool invokes a registered tool by name with args.
func callTool(t *testing.T, s *server.MCPServer, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	tool := s.GetTool(name)
	if tool == nil {
		t.Fatalf("tool %q not registered", name)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	res, err := tool.Handler(context.Background(), req)
	if err != nil {
		t.Fatalf("%s returned Go error: %v", name, err)
	}
	return res
}

// resultText returns the concatenated text content of a result.
func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var out string
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			out += tc.Text
		}
	}
	return out
}

// decodeResult unmarshals a JSON text result into v, failing on tool errors.
func decodeResult(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	txt := resultText(t, res)
	if res.IsError {
		t.Fatalf("tool error: %s", txt)
	}
	if err := json.Unmarshal([]byte(txt), v); err != nil {
		t.Fatalf("decode %q: %v", txt, err)
	}
}

func TestParsePermissions(t *testing.T) {
	cases := []struct {
		in   any
		want int64
		err  bool
	}{
		{"1024", 1024, false},
		{float64(2048), 2048, false},
		{"ViewChannel,sendmessages", 1<<10 | 1<<11, false},
		{[]any{"ManageMessages"}, 1 << 13, false},
		{"Bogus", 0, true},
	}
	for _, c := range cases {
		got, err := parsePermissions(c.in)
		if (err != nil) != c.err || got != c.want {
			t.Errorf("parsePermissions(%v) = %d, %v", c.in, got, err)
		}
	}
	if names := permissionNames(1<<10 | 1<<13); len(names) != 2 || names[0] != "ManageMessages" {
		t.Errorf("permissionNames = %v", names)
	}
}

func TestParseColor(t *testing.T) {
	for in, want := range map[any]int{"#3498DB": 0x3498DB, "3498db": 0x3498DB, float64(255): 255} {
		if got, err := parseColor(in); err != nil || got != want {
			t.Errorf("parseColor(%v) = %d, %v", in, got, err)
		}
	}
	if _, err := parseColor("#GGGGGG"); err == nil {
		t.Error("expected error")
	}
}

func TestResolveGuildFallbacks(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	ctx := context.Background()
	if id, err := resolveGuildRef(ctx, d, ""); err != nil || id != discord.MockGuildID {
		t.Fatalf("single-guild fallback: %q %v", id, err)
	}
	d.Config.DefaultGuildID = "100000000000000009"
	if id, _ := resolveGuildRef(ctx, d, ""); id != "100000000000000009" {
		t.Fatalf("default guild not used: %q", id)
	}
}
