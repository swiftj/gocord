package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
)

const personaTestToken = "abcdefghijklmnopqrstuvwxyz0123456789ABCD"

func TestWebhookPersonaSchemas(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	nextgenSchema(t, s, webhookCreateTool, "channel_id", "name")
	nextgenSchema(t, s, webhookSendTool, "content")
	nextgenSchema(t, s, webhookEditTool, "webhook_id")
	nextgenSchema(t, s, webhookDeleteTool, "webhook_id")
	nextgenSchema(t, s, personaRegisterTool, "persona_name", "display_name")
	nextgenSchema(t, s, personaSendTool, "persona_name", "content")
	for _, name := range []string{webhookEditTool, webhookDeleteTool} {
		tool := s.GetTool(name).Tool
		if tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint {
			t.Errorf("%s must be destructive", name)
		}
		if _, ok := tool.InputSchema.Properties["dry_run"]; !ok {
			t.Errorf("%s lacks dry_run", name)
		}
	}
}

func TestCreateWebhookResolvesChannel(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	var w dto.WebhookInfo
	decodeResult(t, callTool(t, s, webhookCreateTool, map[string]any{"channel_id": "#general", "name": "Release Bot"}), &w)
	if w.ChannelID != discord.MockGeneralID || w.ID == "" {
		t.Fatalf("webhook = %+v", w)
	}
	if c := mock.CallsTo("CreateWebhook"); len(c) != 1 || c[0].Args[0] != discord.MockGeneralID {
		t.Fatalf("calls = %+v", c)
	}
	res := callTool(t, s, webhookCreateTool, map[string]any{"channel_id": "#general", "name": "My Discord Hook"})
	if !res.IsError {
		t.Fatal("name containing 'discord' must be rejected")
	}
}

func TestWebhookPolicyDenials(t *testing.T) {
	d, mock := newTestDeps(t, &config.Config{ReadOnly: true})
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	for tool, args := range map[string]map[string]any{
		webhookCreateTool:   {"channel_id": "#general", "name": "x"},
		webhookSendTool:     {"webhook_id": "100000000000000999", "content": "hi"},
		webhookEditTool:     {"webhook_id": "100000000000000999", "name": "y"},
		webhookDeleteTool:   {"webhook_id": "100000000000000999"},
		personaRegisterTool: {"persona_name": "bot", "display_name": "Bot"},
	} {
		res := callTool(t, s, tool, args)
		if !res.IsError || !strings.Contains(resultText(t, res), "read-only") {
			t.Errorf("%s: want read-only denial, got %s", tool, resultText(t, res))
		}
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatalf("mutating calls under read-only: %+v", mock.MutatingCalls())
	}
}

func TestSendWebhookMessage(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	w, _ := mock.CreateWebhook(context.Background(), discord.MockGeneralID, "hook")
	mock.Reset()

	var msg dto.MessageItem
	decodeResult(t, callTool(t, s, webhookSendTool, map[string]any{
		"webhook_id": w.ID, "content": "hi", "username": "Herald", "embed": map[string]any{"title": "T"},
	}), &msg)
	if msg.ChannelID != discord.MockGeneralID || msg.Author != "Herald" {
		t.Fatalf("msg = %+v", msg)
	}
	req := mock.CallsTo("SendWebhookMessage")[0].Args[0].(dto.WebhookMessageReq)
	if req.Embed == nil || req.Embed.Title != "T" {
		t.Fatalf("embed not passed: %+v", req)
	}

	url := "https://discord.com/api/webhooks/100000000000000777/" + personaTestToken
	res := callTool(t, s, webhookSendTool, map[string]any{"webhook_url": url, "content": "via url"})
	if res.IsError || strings.Contains(resultText(t, res), personaTestToken) {
		t.Fatalf("url send: %s", resultText(t, res))
	}

	for _, args := range []map[string]any{
		{"content": "no target"},
		{"webhook_id": w.ID, "webhook_url": url, "content": "both"},
		{"webhook_url": "https://evil.example.com/api/webhooks/1/" + personaTestToken, "content": "x"},
		{"webhook_id": w.ID, "content": strings.Repeat("a", 2001)},
		{"webhook_id": w.ID, "content": "x", "avatar_url": "http://insecure.example.com/a.png"},
		{"webhook_id": w.ID, "content": "x", "thread_id": "#general"},
	} {
		res := callTool(t, s, webhookSendTool, args)
		if !res.IsError {
			t.Errorf("%v: expected error", args)
		}
		if strings.Contains(resultText(t, res), personaTestToken) {
			t.Errorf("token leaked: %s", resultText(t, res))
		}
	}
}

func TestEditDeleteWebhookDryRun(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	w, _ := mock.CreateWebhook(context.Background(), discord.MockGeneralID, "hook")
	mock.Reset()

	var p dto.DryRunPreview
	decodeResult(t, callTool(t, s, webhookEditTool, map[string]any{"webhook_id": w.ID, "name": "New", "channel_id": "#announcements", "dry_run": true}), &p)
	if !p.DryRun || !p.Allowed || len(p.Changes) != 2 {
		t.Fatalf("preview = %+v", p)
	}
	decodeResult(t, callTool(t, s, webhookDeleteTool, map[string]any{"webhook_id": w.ID, "dry_run": true}), &p)
	if !p.DryRun || !p.Allowed {
		t.Fatalf("preview = %+v", p)
	}
	if n := len(mock.MutatingCalls()); n != 0 {
		t.Fatalf("dry run made %d mutating calls", n)
	}

	var out dto.WebhookInfo
	decodeResult(t, callTool(t, s, webhookEditTool, map[string]any{"webhook_id": w.ID, "channel_id": "#announcements"}), &out)
	if out.ChannelID != discord.MockAnnouncementID {
		t.Fatalf("edit = %+v", out)
	}
	if res := callTool(t, s, webhookEditTool, map[string]any{"webhook_id": w.ID}); !res.IsError {
		t.Fatal("edit with nothing to change must fail")
	}
}

func TestDeleteWebhookForgetsPersonaMapping(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	ctx := context.Background()
	w, _ := mock.CreateWebhook(ctx, discord.MockGeneralID, "MCP Personas")
	if err := d.Store.PutPersonaWebhook(ctx, discord.MockGeneralID, w.ID, ""); err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, webhookDeleteTool, map[string]any{"webhook_id": w.ID, "reason": "cleanup"})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	if _, _, err := d.Store.GetPersonaWebhook(ctx, discord.MockGeneralID); err == nil {
		t.Fatal("persona webhook row not removed")
	}
	if c := mock.CallsTo("DeleteWebhook"); len(c) != 1 || c[0].Args[1] != "cleanup" {
		t.Fatalf("calls = %+v", c)
	}
}

func TestPersonaRegisterValidation(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	for _, args := range []map[string]any{
		{"persona_name": "Bad Name", "display_name": "X"},
		{"persona_name": strings.Repeat("a", 33), "display_name": "X"},
		{"persona_name": "ok", "display_name": "X", "avatar_url": "http://a.example.com/x.png"},
		{"persona_name": "ok", "display_name": "X", "avatar_url": "not a url"},
		{"persona_name": "ok", "display_name": ""},
	} {
		if res := callTool(t, s, personaRegisterTool, args); !res.IsError {
			t.Errorf("%v: expected error", args)
		}
	}

	var out struct {
		Created bool        `json:"created"`
		Persona dto.Persona `json:"persona"`
	}
	decodeResult(t, callTool(t, s, personaRegisterTool, map[string]any{
		"persona_name": "release-bot", "display_name": "Release Bot", "avatar_url": "https://example.com/a.png",
		"default_channel_id": "<#" + discord.MockGeneralID + ">",
	}), &out)
	if !out.Created || out.Persona.DefaultChannelID != discord.MockGeneralID || out.Persona.AvatarURL == "" {
		t.Fatalf("register = %+v", out)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("register with a Snowflake mention must not call Discord: %+v", mock.Calls)
	}
	decodeResult(t, callTool(t, s, personaRegisterTool, map[string]any{"persona_name": "release-bot", "display_name": "Release Bot 2", "default_channel_id": "#general"}), &out)
	if out.Created || out.Persona.DisplayName != "Release Bot 2" || out.Persona.DefaultChannelID != discord.MockGeneralID {
		t.Fatalf("update = %+v", out)
	}
}

func personaSetup(t *testing.T) (*personaHarness, *discord.MockClient) {
	t.Helper()
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	res := callTool(t, s, personaRegisterTool, map[string]any{
		"persona_name": "scribe", "display_name": "The Scribe", "avatar_url": "https://example.com/s.png", "default_channel_id": discord.MockGeneralID,
	})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	return &personaHarness{call: func(args map[string]any) personaSendResult {
		var out personaSendResult
		decodeResult(t, callTool(t, s, personaSendTool, args), &out)
		return out
	}, raw: func(args map[string]any) (bool, string) {
		r := callTool(t, s, personaSendTool, args)
		return r.IsError, resultText(t, r)
	}}, mock
}

type personaHarness struct {
	call func(map[string]any) personaSendResult
	raw  func(map[string]any) (bool, string)
}

func TestPersonaSendCreatesThenReusesWebhook(t *testing.T) {
	h, mock := personaSetup(t)
	first := h.call(map[string]any{"persona_name": "scribe", "content": "hello"})
	if !first.WebhookCreated || first.ChannelID != discord.MockGeneralID || first.Message == nil {
		t.Fatalf("first = %+v", first)
	}
	second := h.call(map[string]any{"persona_name": "scribe", "content": "again", "channel_id": "#general"})
	if second.WebhookCreated || second.WebhookID != first.WebhookID {
		t.Fatalf("second = %+v", second)
	}
	if n := len(mock.CallsTo("CreateWebhook")); n != 1 {
		t.Fatalf("CreateWebhook called %d times", n)
	}
	req := mock.CallsTo("SendWebhookMessage")[1].Args[0].(dto.WebhookMessageReq)
	if req.Username != "The Scribe" || req.AvatarURL != "https://example.com/s.png" || req.WebhookID != first.WebhookID {
		t.Fatalf("send req = %+v", req)
	}
	if name := mock.CallsTo("CreateWebhook")[0].Args[1].(string); strings.Contains(strings.ToLower(name), "discord") {
		t.Fatalf("persona webhook name %q would be rejected by Discord", name)
	}
}

func TestPersonaSendThreadUsesParentWebhook(t *testing.T) {
	h, mock := personaSetup(t)
	out := h.call(map[string]any{"persona_name": "scribe", "content": "in thread", "thread_id": discord.MockForumThreadID})
	if out.ChannelID != discord.MockForumID || out.ThreadID != discord.MockForumThreadID {
		t.Fatalf("out = %+v", out)
	}
	if c := mock.CallsTo("CreateWebhook"); len(c) != 1 || c[0].Args[0] != discord.MockForumID {
		t.Fatalf("webhook not created on parent: %+v", c)
	}
	// A thread passed as channel_id behaves the same and reuses the parent webhook.
	out2 := h.call(map[string]any{"persona_name": "scribe", "content": "again", "channel_id": discord.MockForumThreadID})
	if out2.ThreadID != discord.MockForumThreadID || out2.WebhookID != out.WebhookID {
		t.Fatalf("out2 = %+v", out2)
	}
}

func TestPersonaSendRecreatesDeletedWebhook(t *testing.T) {
	h, mock := personaSetup(t)
	first := h.call(map[string]any{"persona_name": "scribe", "content": "hello"})
	delete(mock.Webhooks, first.WebhookID) // deleted out-of-band → Unknown Webhook
	second := h.call(map[string]any{"persona_name": "scribe", "content": "after delete"})
	if !second.WebhookCreated || second.WebhookID == first.WebhookID {
		t.Fatalf("second = %+v", second)
	}
	if n := len(mock.CallsTo("CreateWebhook")); n != 2 {
		t.Fatalf("CreateWebhook called %d times, want 2", n)
	}
}

func TestPersonaSendInjectedNotFoundRecreatesOnce(t *testing.T) {
	h, mock := personaSetup(t)
	h.call(map[string]any{"persona_name": "scribe", "content": "hello"})
	mock.Err["SendWebhookMessage"] = fmt.Errorf("Unknown Webhook: %w", discord.ErrNotFound)
	isErr, txt := h.raw(map[string]any{"persona_name": "scribe", "content": "boom"})
	if !isErr {
		t.Fatalf("expected error, got %s", txt)
	}
	if n := len(mock.CallsTo("CreateWebhook")); n != 2 {
		t.Fatalf("CreateWebhook called %d times, want exactly one recreate", n)
	}
	if n := len(mock.CallsTo("SendWebhookMessage")); n != 3 {
		t.Fatalf("SendWebhookMessage called %d times, want 3 (ok, 404, retry)", n)
	}
}

func TestPersonaSendErrors(t *testing.T) {
	h, mock := personaSetup(t)
	isErr, txt := h.raw(map[string]any{"persona_name": "ghost", "content": "x"})
	if !isErr || !strings.Contains(txt, "scribe") {
		t.Fatalf("unknown persona should list registered ones: %s", txt)
	}
	if isErr, _ := h.raw(map[string]any{"persona_name": "scribe", "content": "x", "channel_id": discord.MockForumID}); !isErr {
		t.Fatal("forum without thread must fail")
	}
	if isErr, _ := h.raw(map[string]any{"persona_name": "scribe", "content": ""}); !isErr {
		t.Fatal("empty content must fail")
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatalf("errors made mutating calls: %+v", mock.MutatingCalls())
	}
}

func TestPersonaSendPolicy(t *testing.T) {
	d, mock := newTestDeps(t, &config.Config{ReadOnly: true})
	if err := d.Store.UpsertPersona(context.Background(), dto.Persona{Name: "scribe", DisplayName: "Scribe"}); err != nil {
		t.Fatal(err)
	}
	s := newTestServer(t, d, RegisterWebhookPersonaTools)
	res := callTool(t, s, personaSendTool, map[string]any{"persona_name": "scribe", "content": "x", "channel_id": "#general"})
	if !res.IsError || !strings.Contains(resultText(t, res), "read-only") {
		t.Fatalf("read-only send: %s", resultText(t, res))
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatal("mutating call under read-only")
	}
}
