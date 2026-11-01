package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

const (
	eventsTool           = "discord_manage_events"
	eventsMaxName        = 100
	eventsMaxDescription = 1000
	eventsMaxLocation    = 100
)

// RegisterEventTools registers discord_manage_events.
func RegisterEventTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(mcp.NewTool(eventsTool,
		mcp.WithDescription("List or create Guild Scheduled Events. Start time must be in the future; external events need a location and end time."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("action", mcp.Required(), mcp.Enum("list", "create"), mcp.Description("list or create.")),
		mcp.WithString("guild_id", mcp.Required(), mcp.Description("Guild Snowflake or name.")),
		mcp.WithString("name", mcp.Description("create: event name (1-100 chars)."), mcp.MaxLength(eventsMaxName)),
		mcp.WithString("description", mcp.Description("create: description (<= 1000 chars)."), mcp.MaxLength(eventsMaxDescription)),
		mcp.WithString("start_time_iso", mcp.Description("create: ISO-8601 start time (future), e.g. 2026-12-01T18:00:00Z.")),
		mcp.WithString("end_time_iso", mcp.Description("create: ISO-8601 end time (required for external).")),
		mcp.WithString("location_or_channel_id", mcp.Description("create: location text for external events, or the voice/stage channel (Snowflake, #name, mention).")),
		mcp.WithString("entity_type", mcp.Enum(dto.EventStage, dto.EventVoice, dto.EventExternal), mcp.Description("create: stage, voice, or external.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return eventsHandle(ctx, d, req), nil
	})
}

func eventsHandle(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	action := strings.ToLower(strings.TrimSpace(req.GetString("action", "")))
	if action != "list" && action != "create" {
		return errorf("action must be list or create, got %q", action)
	}
	guildID, err := resolveGuild(ctx, d, req)
	if err != nil {
		return errorResult(err)
	}
	if action == "list" {
		if err := d.Guard.Check(guildOp(eventsTool, guildID, opRead, false)); err != nil {
			return errorResult(err)
		}
		events, err := d.Discord.ListScheduledEvents(ctx, guildID)
		if err != nil {
			return errorResult(err)
		}
		return listResult(events, "")
	}
	return eventsCreate(ctx, d, req, guildID)
}

func eventsCreate(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, guildID string) *mcp.CallToolResult {
	name := strings.TrimSpace(req.GetString("name", ""))
	if name == "" || utf8.RuneCountInString(name) > eventsMaxName {
		return errorf("create requires name (1-%d characters)", eventsMaxName)
	}
	desc := strings.TrimSpace(req.GetString("description", ""))
	if utf8.RuneCountInString(desc) > eventsMaxDescription {
		return errorf("description must be <= %d characters", eventsMaxDescription)
	}
	entity := strings.ToLower(strings.TrimSpace(req.GetString("entity_type", "")))
	switch entity {
	case dto.EventStage, dto.EventVoice, dto.EventExternal:
	case "":
		return errorf("create requires entity_type: stage, voice, or external")
	default:
		return errorf("entity_type must be stage, voice, or external; got %q", entity)
	}

	startRaw := strings.TrimSpace(req.GetString("start_time_iso", ""))
	if startRaw == "" {
		return errorf("create requires start_time_iso (ISO-8601, in the future)")
	}
	start, err := eventsParseTime(startRaw)
	if err != nil {
		return errorf("start_time_iso: %v", err)
	}
	if now := time.Now(); !start.After(now) {
		return errorf("start_time_iso %s is not in the future (now %s)", dto.FormatTime(start), dto.FormatTime(now))
	}
	var end time.Time
	if endRaw := strings.TrimSpace(req.GetString("end_time_iso", "")); endRaw != "" {
		if end, err = eventsParseTime(endRaw); err != nil {
			return errorf("end_time_iso: %v", err)
		}
		if !end.After(start) {
			return errorf("end_time_iso must be after start_time_iso")
		}
	}

	loc := strings.TrimSpace(req.GetString("location_or_channel_id", ""))
	op := guildOp(eventsTool, guildID, opWrite, false)
	creq := dto.CreateEventReq{GuildID: guildID, Name: name, Description: desc, StartTimeISO: dto.FormatTime(start), EntityType: entity}
	if !end.IsZero() {
		creq.EndTimeISO = dto.FormatTime(end)
	}
	if entity == dto.EventExternal {
		if loc == "" {
			return errorf("external events require location_or_channel_id (the location text)")
		}
		if utf8.RuneCountInString(loc) > eventsMaxLocation {
			return errorf("location must be <= %d characters", eventsMaxLocation)
		}
		if end.IsZero() {
			return errorf("external events require end_time_iso")
		}
		creq.LocationOrChannelID = loc
	} else {
		if loc == "" {
			return errorf("%s events require location_or_channel_id (a %s channel)", entity, entity)
		}
		ch, err := resolveChannel(ctx, d, guildID, loc)
		if err != nil {
			return errorResult(err)
		}
		want := dto.ChannelVoice
		if entity == dto.EventStage {
			want = dto.ChannelStage
		}
		if ch.Type != want {
			return errorf("%s events need a %s channel; %s (#%s) is a %s channel", entity, want, ch.ID, ch.Name, ch.Type)
		}
		creq.LocationOrChannelID = ch.ID
		op = channelOp(eventsTool, ch, opWrite, false)
		op.GuildID = guildID
	}

	if err := d.Guard.Check(op); err != nil {
		return errorResult(err)
	}
	ev, err := d.Discord.CreateScheduledEvent(ctx, creq)
	if err != nil {
		return errorResult(err)
	}
	return jsonResult(ev)
}

// eventsParseTime parses ISO-8601 timestamps (a missing zone is treated as UTC).
func eventsParseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid ISO-8601 time %q (want e.g. 2026-12-01T18:00:00Z)", s)
}
