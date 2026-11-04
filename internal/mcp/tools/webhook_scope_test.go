package tools

// Wave 3 (Lead): policy checks on webhook tools are scoped to the webhook's
// real channel and guild via Service.GetWebhook.

import (
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

func TestWebhookURLSendWithoutLookup(t *testing.T) {
	// A webhook_url the bot cannot inspect is sent as given.
	_, call := scopeSetup(t, nil)
	url := "https://discord.com/api/webhooks/100000000000000777/secret-token-0123456789abcdef"
	if txt, isErr := call("discord_send_webhook_message", map[string]any{"webhook_url": url, "content": "hi"}); isErr {
		t.Fatalf("send failed: %s", txt)
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
