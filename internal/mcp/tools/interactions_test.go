package tools

import (
	"slices"
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
)

func TestAwaitInteractionSchema(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterInteractionTools)
	tool := nextgenSchema(t, s, interactionTool, "channel_id", "prompt_text")
	nextgenEnum(t, tool, "mode", "buttons", "select", "modal")
	ts := tool.InputSchema.Properties["timeout_seconds"].(map[string]any)
	if ts["minimum"] != float64(10) && ts["minimum"] != 10 {
		t.Errorf("timeout_seconds minimum = %v", ts["minimum"])
	}
}

func TestAwaitInteractionTimeoutDefaults(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterInteractionTools)
	var res dto.InteractionResult
	decodeResult(t, callTool(t, s, interactionTool, map[string]any{"channel_id": "#general", "prompt_text": "Deploy v1.2?"}), &res)
	if !res.TimedOut || res.MessageID == "" {
		t.Fatalf("result = %+v", res)
	}
	req := mock.CallsTo("AwaitInteraction")[0].Args[0].(dto.AwaitInteractionReq)
	if req.ChannelID != discord.MockGeneralID || req.Mode != dto.ModeButtons || req.TimeoutSeconds != dto.DefaultInteractionTimeout {
		t.Fatalf("req = %+v", req)
	}
	if len(req.Options) != 2 || req.Options[0].Style != "success" || req.Options[1].Style != "danger" || req.Options[0].Value != "approve" {
		t.Fatalf("default options = %+v", req.Options)
	}
}

func TestAwaitInteractionApproval(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	mock.NextInteraction = &dto.InteractionResult{Selected: []string{"ship"}, UserID: discord.MockAliceID, Username: "alice"}
	s := newTestServer(t, d, RegisterInteractionTools)
	var res dto.InteractionResult
	decodeResult(t, callTool(t, s, interactionTool, map[string]any{
		"channel_id": discord.MockGeneralID, "prompt_text": "Ship?",
		"options":          []any{map[string]any{"label": "Ship it", "value": "ship"}, map[string]any{"label": "Hold"}},
		"allowed_user_ids": []any{"@alice"}, "allowed_role_ids": []any{"Moderator"}, "timeout_seconds": 60,
	}), &res)
	if res.TimedOut || !slices.Equal(res.Selected, []string{"ship"}) || res.Username != "alice" {
		t.Fatalf("result = %+v", res)
	}
	req := mock.CallsTo("AwaitInteraction")[0].Args[0].(dto.AwaitInteractionReq)
	if !slices.Equal(req.AllowedUserIDs, []string{discord.MockAliceID}) || !slices.Equal(req.AllowedRoleIDs, []string{discord.MockModeratorRole}) {
		t.Fatalf("allowed = %v %v", req.AllowedUserIDs, req.AllowedRoleIDs)
	}
	if req.Options[1].Value != "hold" || req.Options[1].Style != "primary" || req.TimeoutSeconds != 60 {
		t.Fatalf("options = %+v", req.Options)
	}
}

func TestAwaitInteractionSelectAndModal(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterInteractionTools)
	opts := []any{map[string]any{"label": "A"}, map[string]any{"label": "B"}, map[string]any{"label": "C"}}
	res := callTool(t, s, interactionTool, map[string]any{"channel_id": "#general", "prompt_text": "Pick", "mode": "select", "options": opts, "max_values": 2})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	req := mock.CallsTo("AwaitInteraction")[0].Args[0].(dto.AwaitInteractionReq)
	if req.MinValues != 1 || req.MaxValues != 2 || req.Options[0].Style != "" {
		t.Fatalf("select req = %+v", req)
	}
	res = callTool(t, s, interactionTool, map[string]any{"channel_id": "#general", "prompt_text": "Tell me", "mode": "modal"})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	req = mock.CallsTo("AwaitInteraction")[1].Args[0].(dto.AwaitInteractionReq)
	if req.Mode != dto.ModeModal || len(req.Options) != 1 || req.Options[0].Value != "response" {
		t.Fatalf("modal req = %+v", req)
	}
}

func TestAwaitInteractionValidation(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterInteractionTools)
	many := make([]any, 26)
	for i := range many {
		many[i] = map[string]any{"label": "opt" + strings.Repeat("x", i)}
	}
	six := many[:6]
	for _, args := range []map[string]any{
		{"channel_id": "#general", "prompt_text": ""},
		{"channel_id": "#general", "prompt_text": "x", "mode": "carousel"},
		{"channel_id": "#general", "prompt_text": "x", "timeout_seconds": 5},
		{"channel_id": "#general", "prompt_text": "x", "timeout_seconds": 901},
		{"channel_id": "#general", "prompt_text": "x", "options": many},
		{"channel_id": "#general", "prompt_text": "x", "mode": "modal", "options": six},
		{"channel_id": "#general", "prompt_text": "x", "options": []any{map[string]any{"label": "A", "style": "blurple"}}},
		{"channel_id": "#general", "prompt_text": "x", "options": []any{map[string]any{"label": "A"}, map[string]any{"label": "a"}}},
		{"channel_id": "#general", "prompt_text": "x", "min_values": 1},
		{"channel_id": "#general", "prompt_text": "x", "mode": "select", "max_values": 3},
		{"channel_id": "#general", "prompt_text": "x", "allowed_role_ids": []any{"Nonexistent"}},
		{"channel_id": "#general", "prompt_text": "x", "allowed_user_ids": []any{"@nobody"}},
		{"channel_id": discord.MockCategoryID, "prompt_text": "x"},
	} {
		if res := callTool(t, s, interactionTool, args); !res.IsError {
			t.Errorf("%v: expected error", args)
		}
	}
	if n := len(mock.CallsTo("AwaitInteraction")); n != 0 {
		t.Fatalf("invalid input reached AwaitInteraction %d times", n)
	}
}

func TestAwaitInteractionPolicy(t *testing.T) {
	for _, cfg := range []*config.Config{{ReadOnly: true}} {
		d, mock := newTestDeps(t, cfg)
		s := newTestServer(t, d, RegisterInteractionTools)
		res := callTool(t, s, interactionTool, map[string]any{"channel_id": "#general", "prompt_text": "x"})
		if !res.IsError || !strings.Contains(resultText(t, res), "denied by policy") {
			t.Errorf("cfg %+v: %s", cfg, resultText(t, res))
		}
		if len(mock.MutatingCalls()) != 0 {
			t.Errorf("cfg %+v: mutating calls", cfg)
		}
	}
}
