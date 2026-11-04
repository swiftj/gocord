package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
)

func eventsFuture(h int) string {
	return time.Now().Add(time.Duration(h) * time.Hour).UTC().Format(time.RFC3339)
}

func eventsAddVoice(mock *discord.MockClient) {
	mock.Channels["100000000000000070"] = dto.ChannelInfo{ID: "100000000000000070", GuildID: discord.MockGuildID, Name: "Lounge", Type: dto.ChannelVoice, Position: 4}
	mock.Channels["100000000000000071"] = dto.ChannelInfo{ID: "100000000000000071", GuildID: discord.MockGuildID, Name: "Town Hall", Type: dto.ChannelStage, Position: 5}
}

func TestManageEventsSchema(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	s := newTestServer(t, d, RegisterEventTools)
	tool := nextgenSchema(t, s, eventsTool, "action", "guild_id")
	nextgenEnum(t, tool, "action", "list", "create")
	nextgenEnum(t, tool, "entity_type", "stage", "voice", "external")
}

func TestManageEventsCreateAndList(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	eventsAddVoice(mock)
	s := newTestServer(t, d, RegisterEventTools)

	var ev dto.ScheduledEvent
	decodeResult(t, callTool(t, s, eventsTool, map[string]any{
		"action": "create", "guild_id": "Test Guild", "name": "Launch party", "entity_type": "external",
		"location_or_channel_id": "https://example.com/stream", "start_time_iso": eventsFuture(48), "end_time_iso": eventsFuture(50),
	}), &ev)
	if ev.Location != "https://example.com/stream" || ev.GuildID != discord.MockGuildID {
		t.Fatalf("event = %+v", ev)
	}
	decodeResult(t, callTool(t, s, eventsTool, map[string]any{
		"action": "create", "guild_id": discord.MockGuildID, "name": "Office hours", "entity_type": "voice",
		"location_or_channel_id": "#Lounge", "start_time_iso": eventsFuture(24),
	}), &ev)
	if ev.ChannelID != "100000000000000070" {
		t.Fatalf("voice event = %+v", ev)
	}

	var list struct {
		Count int                  `json:"count"`
		Items []dto.ScheduledEvent `json:"items"`
	}
	decodeResult(t, callTool(t, s, eventsTool, map[string]any{"action": "list", "guild_id": discord.MockGuildID}), &list)
	if list.Count != 2 {
		t.Fatalf("list = %+v", list)
	}
}

func TestManageEventsValidation(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	eventsAddVoice(mock)
	s := newTestServer(t, d, RegisterEventTools)
	base := func(over map[string]any) map[string]any {
		m := map[string]any{"action": "create", "guild_id": discord.MockGuildID, "name": "E", "entity_type": "external",
			"location_or_channel_id": "Park", "start_time_iso": eventsFuture(24), "end_time_iso": eventsFuture(25)}
		for k, v := range over {
			if v == nil {
				delete(m, k)
			} else {
				m[k] = v
			}
		}
		return m
	}
	for name, args := range map[string]map[string]any{
		"past start":         base(map[string]any{"start_time_iso": "2020-01-01T00:00:00Z"}),
		"bad time":           base(map[string]any{"start_time_iso": "tomorrow"}),
		"end before start":   base(map[string]any{"end_time_iso": eventsFuture(1)}),
		"external no loc":    base(map[string]any{"location_or_channel_id": nil}),
		"external no end":    base(map[string]any{"end_time_iso": nil}),
		"no name":            base(map[string]any{"name": nil}),
		"no entity":          base(map[string]any{"entity_type": nil}),
		"voice no channel":   base(map[string]any{"entity_type": "voice", "location_or_channel_id": nil}),
		"voice wrong type":   base(map[string]any{"entity_type": "voice", "location_or_channel_id": "#general"}),
		"stage on voice":     base(map[string]any{"entity_type": "stage", "location_or_channel_id": "#Lounge"}),
		"bad action":         {"action": "delete", "guild_id": discord.MockGuildID},
		"unknown guild list": {"action": "list", "guild_id": "Nope"},
	} {
		if res := callTool(t, s, eventsTool, args); !res.IsError {
			t.Errorf("%s: expected error, got %s", name, resultText(t, res))
		}
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatalf("invalid input made mutating calls: %+v", mock.MutatingCalls())
	}
	// Stage on a stage channel is fine.
	if res := callTool(t, s, eventsTool, base(map[string]any{"entity_type": "stage", "location_or_channel_id": "Town Hall", "end_time_iso": nil})); res.IsError {
		t.Fatal(resultText(t, res))
	}
}

func TestManageEventsPolicy(t *testing.T) {
	d, mock := newTestDeps(t, &config.Config{ReadOnly: true})
	s := newTestServer(t, d, RegisterEventTools)
	if res := callTool(t, s, eventsTool, map[string]any{"action": "list", "guild_id": discord.MockGuildID}); res.IsError {
		t.Fatalf("list under read-only: %s", resultText(t, res))
	}
	res := callTool(t, s, eventsTool, map[string]any{"action": "create", "guild_id": discord.MockGuildID, "name": "E", "entity_type": "external",
		"location_or_channel_id": "Park", "start_time_iso": eventsFuture(24), "end_time_iso": eventsFuture(25)})
	if !res.IsError || !strings.Contains(resultText(t, res), "read-only") {
		t.Fatalf("create under read-only: %s", resultText(t, res))
	}
	if len(mock.MutatingCalls()) != 0 {
		t.Fatal("denied calls mutated")
	}
}
