package tools

// Wave 3 (Lead): policy checks on webhook tools are scoped to the webhook's
// real channel and guild via Service.GetWebhook.

import (
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
)

const scopeHookID = "100000000000000800"

func scopeSetup(t *testing.T, cfg *config.Config) (*discord.MockClient, func(string, map[string]any) (string, bool)) {
	t.Helper()
	d, mock := newTestDeps(t, cfg)
	mock.Webhooks[scopeHookID] = dto.WebhookInfo{ID: scopeHookID, ChannelID: discord.MockGeneralID, GuildID: discord.MockGuildID, Name: "ops"}
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	return mock, func(tool string, args map[string]any) (string, bool) {
		res := callTool(t, s, tool, args)
		return resultText(t, res), res.IsError
	}
}

func TestWebhookToolsScopedToWebhookChannel(t *testing.T) {
	// #general is blocked: edits, deletes, and sends to a webhook in #general must be denied
	// even though the caller never names the channel.
	cfg := &config.Config{BlockedChannels: []string{discord.MockGeneralID}}
	mock, call := scopeSetup(t, cfg)

	cases := []struct {
		tool string
		args map[string]any
	}{
		{"discord_edit_webhook", map[string]any{"webhook_id": scopeHookID, "name": "renamed"}},
		{"discord_delete_webhook", map[string]any{"webhook_id": scopeHookID}},
		{"discord_send_webhook_message", map[string]any{"webhook_id": scopeHookID, "content": "hi"}},
	}
	for _, c := range cases {
		txt, isErr := call(c.tool, c.args)
		if !isErr || !strings.Contains(txt, "denied by policy") {
			t.Errorf("%s: want policy denial, got %q", c.tool, txt)
		}
	}
	if n := len(mock.MutatingCalls()); n != 0 {
		t.Fatalf("denied calls still mutated: %+v", mock.MutatingCalls())
	}
}

func TestWebhookEditDestinationAlsoChecked(t *testing.T) {
	cfg := &config.Config{BlockedChannels: []string{discord.MockAnnouncementID}}
	mock, call := scopeSetup(t, cfg)
	txt, isErr := call("discord_edit_webhook", map[string]any{"webhook_id": scopeHookID, "channel_id": "#announcements"})
	if !isErr || !strings.Contains(txt, "denied by policy") {
		t.Fatalf("moving into a blocked channel: got %q", txt)
	}
	txt, _ = call("discord_edit_webhook", map[string]any{"webhook_id": scopeHookID, "channel_id": "#announcements", "dry_run": true})
	if !strings.Contains(txt, `"allowed":false`) {
		t.Fatalf("dry run should report denial: %s", txt)
	}
	if n := len(mock.MutatingCalls()); n != 0 {
		t.Fatalf("mutated: %+v", mock.MutatingCalls())
	}
}

func TestWebhookURLSendUnverifiableUnderAllowlist(t *testing.T) {
	cfg := &config.Config{AllowedGuilds: []string{discord.MockGuildID}}
	mock, call := scopeSetup(t, cfg)
	url := "https://discord.com/api/webhooks/100000000000000777/secret-token-0123456789abcdef"
	txt, isErr := call("discord_send_webhook_message", map[string]any{"webhook_url": url, "content": "hi"})
	if !isErr || !strings.Contains(txt, "cannot verify") {
		t.Fatalf("want unverifiable denial, got %q", txt)
	}
	if strings.Contains(txt, "secret-token") {
		t.Fatal("token leaked in error")
	}
	if n := len(mock.MutatingCalls()); n != 0 {
		t.Fatal("send happened")
	}
	// The same webhook URL is allowed when no allowlists are configured.
	_, call = scopeSetup(t, nil)
	if txt, isErr := call("discord_send_webhook_message", map[string]any{"webhook_url": url, "content": "hi"}); isErr {
		t.Fatalf("unrestricted send failed: %s", txt)
	}
}

func TestWebhookReadOnlyFailsFastWithoutLookup(t *testing.T) {
	mock, call := scopeSetup(t, &config.Config{ReadOnly: true})
	if _, isErr := call("discord_delete_webhook", map[string]any{"webhook_id": scopeHookID}); !isErr {
		t.Fatal("read-only delete allowed")
	}
	if n := len(mock.CallsTo("GetWebhook")); n != 0 {
		t.Fatalf("lookup happened before read-only check (%d calls)", n)
	}
}
