package tools

import (
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
)

const (
	taHookA    = "100000000000000700"
	taHookB    = "100000000000000701"
	taPrivCat  = "100000000000000021"
	taPrivChan = "100000000000000012"
	taBotAllow = "100000000000000013"
	taDenyBot  = "100000000000000014"
	taView     = int64(1 << 10)
	taHistory  = int64(1 << 16)
)

func taWebhookMsg(id, hook, name, avatar string) dto.MessageItem {
	return dto.MessageItem{ID: id, ChannelID: discord.MockGeneralID, AuthorID: hook, Author: name, AuthorDisplayName: name,
		IsWebhook: true, WebhookID: hook, AuthorAvatarURL: "https://cdn.discordapp.com/avatars/" + hook + "/" + avatar + ".png?size=1024"}
}

func TestListTuppers(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	mock.Messages[discord.MockGeneralID] = append(mock.Messages[discord.MockGeneralID],
		taWebhookMsg("100000000000000160", taHookA, "Ser Thaddeus Mournvale", "old"),
		taWebhookMsg("100000000000000161", taHookA, "Mother Ysolde", "ys1"),
		taWebhookMsg("100000000000000162", taHookA, "Ser Thaddeus Mournvale", "new"),
		taWebhookMsg("100000000000000163", taHookB, "Ser Thaddeus Mournvale", "pk"),
	)
	s := newTestServer(t, d, RegisterTupperAccessTools)

	var out tupListResponse
	decodeResult(t, callTool(t, s, tupToolList, map[string]any{}), &out)
	if out.Backfilled == 0 || len(out.Tuppers) != 3 {
		t.Fatalf("tuppers = %+v (backfilled %d)", out.Tuppers, out.Backfilled)
	}
	first := out.Tuppers[0] // newest first
	if first.WebhookID != taHookB || first.MessageCount != 1 {
		t.Fatalf("ordering: %+v", first)
	}
	var thaddeus *dto.TupperInfo
	for i := range out.Tuppers {
		if out.Tuppers[i].WebhookID == taHookA && out.Tuppers[i].DisplayName == "Ser Thaddeus Mournvale" {
			thaddeus = &out.Tuppers[i]
		}
	}
	if thaddeus == nil || thaddeus.MessageCount != 2 || !strings.Contains(thaddeus.AvatarURL, "/new.png") ||
		thaddeus.LastSeenMessageID != "100000000000000162" || thaddeus.LastSeenChannelID != discord.MockGeneralID || thaddeus.LastSeenAt == "" {
		t.Fatalf("Thaddeus = %+v", thaddeus)
	}

	decodeResult(t, callTool(t, s, tupToolList, map[string]any{"since": "100000000000000161", "limit": 5}), &out)
	if len(out.Tuppers) != 2 {
		t.Fatalf("since filter: %+v", out.Tuppers)
	}
	if res := callTool(t, s, tupToolList, map[string]any{"since": "not-a-time"}); !res.IsError {
		t.Fatal("bad since must fail")
	}

	blocked, _ := newTestDeps(t, &config.Config{BlockedChannels: []string{discord.MockGeneralID}})
	bs := newTestServer(t, blocked, RegisterTupperAccessTools)
	res := callTool(t, bs, tupToolList, map[string]any{"channel_id": discord.MockGeneralID})
	if !res.IsError {
		t.Fatal("blocked channel must be denied")
	}
}

func TestCheckAccess(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	everyoneDeny := dto.PermissionOverwrite{ID: discord.MockGuildID, Type: "role", Deny: taView}
	// A private category the bot's role is not in, with a synced child channel.
	mock.Channels[taPrivCat] = dto.ChannelInfo{ID: taPrivCat, GuildID: discord.MockGuildID, Name: "GM Only", Type: dto.ChannelCategory,
		Overwrites: []dto.PermissionOverwrite{everyoneDeny}}
	mock.Channels[taPrivChan] = dto.ChannelInfo{ID: taPrivChan, GuildID: discord.MockGuildID, Name: "gm-notes", Type: dto.ChannelText,
		ParentID: taPrivCat, Overwrites: []dto.PermissionOverwrite{everyoneDeny}}
	// Private, but the bot's role is allowed in.
	mock.Channels[taBotAllow] = dto.ChannelInfo{ID: taBotAllow, GuildID: discord.MockGuildID, Name: "bot-ok", Type: dto.ChannelText,
		ParentID: taPrivCat, Overwrites: []dto.PermissionOverwrite{everyoneDeny,
			{ID: discord.MockBotRoleID, Type: "role", Allow: taView | taHistory}}}
	// Visible, but the bot member is denied history directly.
	mock.Channels[taDenyBot] = dto.ChannelInfo{ID: taDenyBot, GuildID: discord.MockGuildID, Name: "no-history", Type: dto.ChannelText,
		Overwrites: []dto.PermissionOverwrite{{ID: discord.MockBotID, Type: "member", Deny: taHistory}}}
	// Base: @everyone has ViewChannel (1024); grant ReadMessageHistory too.
	roles := mock.Roles[discord.MockGuildID]
	roles[0].Permissions = "66560" // ViewChannel | ReadMessageHistory
	s := newTestServer(t, d, RegisterTupperAccessTools)

	var out accResponse
	decodeResult(t, callTool(t, s, accToolCheck, map[string]any{}), &out)
	rows := map[string]dto.ChannelAccess{}
	for _, r := range out.Channels {
		rows[r.ID] = r
	}
	if r := rows[discord.MockGeneralID]; !r.CanView || !r.CanReadHistory || r.Fix != "" || r.Category != "Community" {
		t.Errorf("general = %+v", r)
	}
	priv := rows[taPrivChan]
	// @everyone grants ReadMessageHistory at the guild level, so only ViewChannel is missing.
	if priv.CanView || len(priv.Missing) != 1 || priv.Missing[0] != "ViewChannel" || !strings.Contains(priv.Fix, `category "GM Only"`) || !strings.Contains(priv.Fix, `role "MCP Bot"`) {
		t.Errorf("private = %+v", priv)
	}
	if r := rows[taBotAllow]; !r.CanView || !r.CanReadHistory {
		t.Errorf("bot-allowed = %+v", r)
	}
	nh := rows[taDenyBot]
	if !nh.CanView || nh.CanReadHistory || len(nh.Missing) != 1 || nh.Missing[0] != "ReadMessageHistory" ||
		!strings.Contains(nh.Fix, "channel #no-history") || !strings.Contains(nh.Fix, "member overwrite") {
		t.Errorf("no-history = %+v", nh)
	}
	if out.Summary.Blocked != 2 || out.Summary.Readable != len(out.Channels)-2 {
		t.Errorf("summary = %+v of %d", out.Summary, len(out.Channels))
	}

	decodeResult(t, callTool(t, s, accToolCheck, map[string]any{"channel_ids": []any{"#gm-notes"}}), &out)
	if len(out.Channels) != 1 || out.Channels[0].ID != taPrivChan {
		t.Fatalf("explicit channel_ids = %+v", out.Channels)
	}

	// Administrator bypasses overwrites.
	roles[2].Permissions = "8"
	decodeResult(t, callTool(t, s, accToolCheck, map[string]any{"channel_ids": []any{taPrivChan}}), &out)
	if !out.Channels[0].CanReadHistory {
		t.Fatalf("admin = %+v", out.Channels[0])
	}
}
