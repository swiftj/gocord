package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/index"
)

func TestClientNotLoggedIn(t *testing.T) {
	c := NewClient(&config.Config{}, nil, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer c.Close()
	ctx := context.Background()
	checks := map[string]error{}
	_, checks["ResolveGuildID"] = c.ResolveGuildID(ctx, "x")
	_, checks["ListGuilds"] = c.ListGuilds(ctx)
	_, checks["GetChannel"] = c.GetChannel(ctx, tGeneral)
	_, checks["SendMessage"] = c.SendMessage(ctx, dto.SendMessageReq{ChannelID: tGeneral, Content: "hi"})
	checks["DeleteChannel"] = c.DeleteChannel(ctx, tGeneral, "")
	checks["AddMultipleReactions"] = c.AddMultipleReactions(ctx, tGeneral, tGeneral, []string{"👍"})
	_, checks["SendWebhookMessage"] = c.SendWebhookMessage(ctx, dto.WebhookMessageReq{WebhookID: tGeneral})
	_, checks["AwaitInteraction"] = c.AwaitInteraction(ctx, dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p"})
	_, checks["Login"] = c.Login(ctx, "", "")
	for name, err := range checks {
		if !errors.Is(err, ErrNotLoggedIn) {
			t.Errorf("%s: err = %v, want ErrNotLoggedIn", name, err)
		}
	}
	if err := c.Close(); err != nil { // idempotent
		t.Fatal(err)
	}
}

func restErr(status int, code int, msg string) error {
	body, _ := json.Marshal(map[string]any{"code": code, "message": msg})
	return &discordgo.RESTError{
		Response:     &http.Response{StatusCode: status, Status: http.StatusText(status)},
		ResponseBody: body,
		Message:      &discordgo.APIErrorMessage{Code: code, Message: msg},
	}
}

func TestMapErr(t *testing.T) {
	tests := []struct {
		err  error
		want error
		msg  string
	}{
		{restErr(404, 10003, "Unknown Channel"), ErrNotFound, "Unknown Channel"},
		{restErr(403, 50013, "Missing Permissions"), ErrForbidden, "Missing Permissions"},
		{restErr(400, 50013, "Missing Permissions"), ErrForbidden, "code 50013"},
		{restErr(429, 0, "You are being rate limited."), ErrRateLimited, "rate limited"},
		{restErr(401, 0, "401: Unauthorized"), ErrNotLoggedIn, "Unauthorized"},
		{&discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{RetryAfter: time.Second}}}, ErrRateLimited, "1s"},
		{context.Canceled, context.Canceled, "op"},
	}
	for _, tc := range tests {
		got := mapErr("op", tc.err)
		if !errors.Is(got, tc.want) || !strings.Contains(got.Error(), tc.msg) {
			t.Errorf("mapErr(%v) = %v; want %v containing %q", tc.err, got, tc.want, tc.msg)
		}
	}
	if mapErr("op", nil) != nil {
		t.Error("nil should map to nil")
	}
	if got := mapErr("op", restErr(400, 50035, "Invalid Form Body")); errors.Is(got, ErrNotFound) || !strings.Contains(got.Error(), "Invalid Form Body") {
		t.Errorf("400 mapped to %v", got)
	}
}

func TestParseWebhookURL(t *testing.T) {
	tok := "abcDEF_123-xyz"
	for _, u := range []string{
		"https://discord.com/api/webhooks/100000000000000070/" + tok,
		"https://discordapp.com/api/webhooks/100000000000000070/" + tok,
		"https://canary.discord.com/api/v10/webhooks/100000000000000070/" + tok + "/",
	} {
		id, token, err := parseWebhookURL(u)
		if err != nil || id != "100000000000000070" || token != tok {
			t.Errorf("%s: %q %q %v", u, id, token, err)
		}
	}
	for _, u := range []string{"http://discord.com/api/webhooks/100000000000000070/x", "https://evil.com/api/webhooks/100000000000000070/x", "nope"} {
		_, _, err := parseWebhookURL(u)
		if err == nil {
			t.Errorf("%s: expected error", u)
		} else if strings.Contains(err.Error(), "100000000000000070") {
			t.Errorf("error leaks URL: %v", err)
		}
	}
}

func TestOpenAttachment(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, closeFn, err := openAttachment(p)
	if err != nil {
		t.Fatal(err)
	}
	closeFn()
	if f.Name != "notes.txt" || !strings.HasPrefix(f.ContentType, "text/plain") {
		t.Fatalf("file = %+v", f)
	}
	if _, _, err := openAttachment("relative.txt"); err == nil {
		t.Error("relative path accepted")
	}
	if _, _, err := openAttachment(dir); err == nil {
		t.Error("directory accepted")
	}
	big := filepath.Join(dir, "big.bin")
	fh, _ := os.Create(big)
	_ = fh.Truncate(MaxAttachmentBytes + 1)
	_ = fh.Close()
	if _, _, err := openAttachment(big); err == nil || !strings.Contains(err.Error(), "25 MiB") {
		t.Errorf("oversize accepted: %v", err)
	}
}

func TestSanitizeRoute(t *testing.T) {
	got := sanitizeRoute("/api/v9/webhooks/100000000000000070/SECRETTOKEN")
	if strings.Contains(got, "SECRETTOKEN") || !strings.Contains(got, "***") {
		t.Fatalf("token leaked: %s", got)
	}
	got = sanitizeRoute("/api/v9/interactions/100000000000000300/INTERACTIONTOKEN/callback")
	if strings.Contains(got, "INTERACTIONTOKEN") {
		t.Fatalf("token leaked: %s", got)
	}
	if got := sanitizeRoute("/api/v9/channels/100000000000000010/messages"); got != "/api/v9/channels/:id/messages" {
		t.Fatalf("got %s", got)
	}
}

type fakeRT struct{ resp *http.Response }

func (f fakeRT) RoundTrip(*http.Request) (*http.Response, error) { return f.resp, nil }

func TestTelemetryTransport(t *testing.T) {
	var buf bytes.Buffer
	tel := &rateLimitTelemetry{log: slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "3")
	h.Set("X-RateLimit-Reset-After", "1.5")
	h.Set("X-RateLimit-Bucket", "abc")
	rt := &telemetryTransport{base: fakeRT{&http.Response{StatusCode: 429, Header: h, Body: io.NopCloser(strings.NewReader(""))}}, t: tel}
	req, _ := http.NewRequest("POST", "https://discord.com/api/v9/webhooks/100000000000000070/TOPSECRET", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	info := tel.last()
	if info.Remaining != 3 || info.ResetAfter != 1500*time.Millisecond || info.Bucket != "abc" {
		t.Fatalf("info = %+v", info)
	}
	logs := buf.String()
	if !strings.Contains(logs, `"level":"WARN"`) || !strings.Contains(logs, `"level":"DEBUG"`) {
		t.Fatalf("expected warn+debug logs: %s", logs)
	}
	if strings.Contains(logs, "TOPSECRET") {
		t.Fatalf("token logged: %s", logs)
	}
}

func TestToMessageItem(t *testing.T) {
	r := seededResolver(nil)
	edited := time.Date(2026, 11, 1, 15, 0, 0, 0, time.UTC)
	m := &discordgo.Message{
		ID: "100000000000000100", ChannelID: tGeneral,
		Content:          "hey <@" + tBob + "> see <#" + tAnnounce + "> ping <@&" + tModRole + "> <@100000000000000097>",
		Timestamp:        time.Date(2026, 11, 1, 14, 55, 0, 0, time.FixedZone("x", 3600)),
		EditedTimestamp:  &edited,
		Author:           &discordgo.User{ID: tAlice, Username: "alice"},
		Mentions:         []*discordgo.User{{ID: tBob, Username: "bob"}},
		MessageReference: &discordgo.MessageReference{MessageID: "100000000000000099"},
		Attachments:      []*discordgo.MessageAttachment{{Filename: "a.png", URL: "https://cdn/a.png", Size: 10, ContentType: "image/png"}},
		Embeds:           []*discordgo.MessageEmbed{{Title: "T", Description: strings.Repeat("d", 300), Footer: &discordgo.MessageEmbedFooter{Text: "f"}}},
		Reactions: []*discordgo.MessageReactions{
			{Count: 2, Me: true, Emoji: &discordgo.Emoji{Name: "👍"}},
			{Count: 1, Emoji: &discordgo.Emoji{Name: "party", ID: "100000000000000080"}},
		},
		Thread: &discordgo.Channel{ID: tThread},
	}
	got := toMessageItem(m, r)
	if got.Content != "hey @bob see #announcements ping @Moderator <@100000000000000097>" {
		t.Errorf("content = %q", got.Content)
	}
	if got.Timestamp != "2026-11-01T13:55:00Z" || got.EditedAt != "2026-11-01T15:00:00Z" {
		t.Errorf("times = %q %q", got.Timestamp, got.EditedAt)
	}
	if got.Author != "alice" || got.AuthorID != tAlice || got.ReplyToID != "100000000000000099" || got.ThreadID != tThread {
		t.Errorf("item = %+v", got)
	}
	if len(got.Embeds) != 1 || !strings.Contains(got.Embeds[0].Description, "…[truncated 20 chars]") || got.Embeds[0].Footer != "f" {
		t.Errorf("embeds = %+v", got.Embeds)
	}
	if got.Reactions[1].Emoji != "party:100000000000000080" || !got.Reactions[0].Me {
		t.Errorf("reactions = %+v", got.Reactions)
	}
	long := toMessageItem(&discordgo.Message{ID: "100000000000000101", Content: strings.Repeat("x", 4100)}, nil)
	if !strings.HasSuffix(long.Content, "…[truncated 100 chars]") || long.Timestamp == "" {
		t.Errorf("long content = %q… ts=%q", long.Content[:10], long.Timestamp)
	}
}

func TestConversions(t *testing.T) {
	role := toRoleInfo(&discordgo.Role{ID: tModRole, Name: "Moderator", Color: 0x3498DB, Position: 5,
		Permissions: discordgo.PermissionManageMessages | discordgo.PermissionKickMembers, Hoist: true})
	if role.Color != "#3498DB" || role.Permissions != "8194" || !reflect.DeepEqual(role.PermissionNames, []string{"KickMembers", "ManageMessages"}) {
		t.Errorf("role = %+v", role)
	}
	for in, want := range map[discordgo.ChannelType]string{
		discordgo.ChannelTypeGuildText: "text", discordgo.ChannelTypeGuildNews: "announcement",
		discordgo.ChannelTypeGuildForum: "forum", discordgo.ChannelTypeGuildPrivateThread: "thread",
		discordgo.ChannelTypeGuildStageVoice: "stage", discordgo.ChannelTypeGuildCategory: "category",
	} {
		if got := channelTypeString(in); got != want {
			t.Errorf("channelTypeString(%d) = %q", in, got)
		}
		if want != "thread" {
			back, err := channelTypeFromString(want)
			if err != nil || back != in {
				t.Errorf("round trip %q: %d %v", want, back, err)
			}
		}
	}
	if _, err := channelTypeFromString("dm"); err == nil {
		t.Error("dm accepted")
	}

	th := toThreadInfo(&discordgo.Channel{ID: tThread, ParentID: "100000000000000030", Name: "Bug", Type: discordgo.ChannelTypeGuildPrivateThread,
		AppliedTags: []string{"100000000000000081"}, ThreadMetadata: &discordgo.ThreadMetadata{Archived: true, Locked: true}},
		map[string]string{"100000000000000081": "bug"})
	if !th.Archived || !th.Locked || !th.Private || th.AppliedTags[0] != "bug" || th.CreatedAt == "" {
		t.Errorf("thread = %+v", th)
	}

	until := time.Now().Add(time.Hour)
	mem := toMemberInfo(&discordgo.Member{User: &discordgo.User{ID: tAlice, Username: "alice"}, Roles: []string{tModRole, "100000000000000049"},
		CommunicationDisabledUntil: &until}, seededResolver(nil).roleName)
	if !reflect.DeepEqual(mem.Roles, []string{"Moderator", "100000000000000049"}) || mem.TimeoutUntil == "" {
		t.Errorf("member = %+v", mem)
	}

	w := toWebhookInfo(&discordgo.Webhook{ID: "100000000000000070", ChannelID: tGeneral, Name: "hook", Token: "SECRET"})
	b, _ := json.Marshal(w)
	if strings.Contains(string(b), "SECRET") {
		t.Errorf("webhook token leaked: %s", b)
	}

	exp := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	poll := toPollResult(&discordgo.Message{ID: "100000000000000110", Poll: &discordgo.Poll{
		Question: discordgo.PollMedia{Text: "Lunch?"},
		Answers: []discordgo.PollAnswer{
			{AnswerID: 1, Media: &discordgo.PollMedia{Text: "Pizza"}},
			{AnswerID: 2, Media: &discordgo.PollMedia{Text: "Sushi"}},
			{AnswerID: 3, Media: &discordgo.PollMedia{Text: "Salad"}},
		},
		Results: &discordgo.PollResults{Finalized: true, AnswerCounts: []*discordgo.PollAnswerCount{{ID: 1, Count: 3}, {ID: 2, Count: 3}, {ID: 3, Count: 1}}},
		Expiry:  &exp,
	}})
	if poll.TotalVotes != 7 || !poll.Finalized || !reflect.DeepEqual(poll.Winners, []string{"Pizza", "Sushi"}) || poll.ExpiresAt != "2026-11-02T00:00:00Z" {
		t.Errorf("poll = %+v", poll)
	}

	at := discordgo.AuditLogActionChannelUpdate
	key := discordgo.AuditLogChangeKeyName
	entry := toAuditLogEntry(&discordgo.AuditLogEntry{ID: "100000000000000120", UserID: tAlice, TargetID: tGeneral, ActionType: &at,
		Changes: []*discordgo.AuditLogChange{{Key: &key, OldValue: "general", NewValue: "lobby"}}}, map[string]string{tAlice: "alice"})
	if entry.Action != "channel_update" || entry.User != "alice" || entry.Changes[0] != "name: general → lobby" {
		t.Errorf("audit = %+v", entry)
	}

	enabled := true
	rule := toAutoModRule(&discordgo.AutoModerationRule{ID: "100000000000000130", Name: "no spam", Enabled: &enabled,
		TriggerType: discordgo.AutoModerationEventTriggerKeyword, TriggerMetadata: &discordgo.AutoModerationTriggerMetadata{KeywordFilter: []string{"spam"}},
		Actions: []discordgo.AutoModerationAction{{Type: discordgo.AutoModerationRuleActionBlockMessage}}})
	if rule.TriggerType != "keyword" || !rule.Enabled || rule.Keywords[0] != "spam" || rule.Actions[0] != "block_message" {
		t.Errorf("rule = %+v", rule)
	}

	end := time.Date(2026, 12, 1, 18, 0, 0, 0, time.UTC)
	ev := toScheduledEvent(&discordgo.GuildScheduledEvent{ID: "100000000000000140", Name: "Launch", ScheduledStartTime: end.Add(-time.Hour),
		ScheduledEndTime: &end, EntityType: discordgo.GuildScheduledEventEntityTypeExternal,
		EntityMetadata: discordgo.GuildScheduledEventEntityMetadata{Location: "Zoom"}, Status: discordgo.GuildScheduledEventStatusScheduled, UserCount: 4})
	if ev.EntityType != "external" || ev.Location != "Zoom" || ev.Status != "scheduled" || ev.EndTime != "2026-12-01T18:00:00Z" || ev.InterestedCount != 4 {
		t.Errorf("event = %+v", ev)
	}
}

func TestEmojiParsing(t *testing.T) {
	for in, want := range map[string]string{"party:100000000000000080": "party", "<:party:100000000000000080>": "party", "<a:dance:100000000000000081>": "dance"} {
		name, _, ok := parseCustomEmoji(in)
		if !ok || name != want {
			t.Errorf("%q → %q %v", in, name, ok)
		}
	}
	if _, _, ok := parseCustomEmoji("👍"); ok {
		t.Error("unicode parsed as custom")
	}
	if n, ok := shortcodeName(":thumbsup:"); !ok || n != "thumbsup" {
		t.Errorf("shortcode = %q %v", n, ok)
	}
	if e := toComponentEmoji("<a:dance:100000000000000081>"); e == nil || !e.Animated || e.ID != "100000000000000081" {
		t.Errorf("component emoji = %+v", e)
	}
}

func TestGatewayIngestionAndEvents(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, index.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c := NewClient(&config.Config{}, store, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	defer c.Close()

	events := make(chan string, 16)
	c.OnEvent(func(kind, guildID, channelID string) { events <- kind + ":" + channelID })

	c.onGuildCreate(nil, &discordgo.GuildCreate{Guild: &discordgo.Guild{ID: tGuild, Name: "Test Guild",
		Channels: []*discordgo.Channel{{ID: tGeneral, Name: "general", Type: discordgo.ChannelTypeGuildText}}}})
	c.onThreadCreate(nil, &discordgo.ThreadCreate{Channel: &discordgo.Channel{ID: tThread, GuildID: tGuild, ParentID: tGeneral,
		Name: "Release planning", Type: discordgo.ChannelTypeGuildPublicThread}})

	msg := func(id, ch, content string) *discordgo.Message {
		return &discordgo.Message{ID: id, ChannelID: ch, GuildID: tGuild, Content: content, Timestamp: time.Now(),
			Author: &discordgo.User{ID: tAlice, Username: "alice"}}
	}
	c.onMessageCreate(nil, &discordgo.MessageCreate{Message: msg("100000000000000150", tGeneral, "shipping the zebra release")})
	c.onMessageCreate(nil, &discordgo.MessageCreate{Message: msg("100000000000000151", tThread, "zebra thread reply")})
	c.onMessageCreate(nil, &discordgo.MessageCreate{Message: &discordgo.Message{ID: "100000000000000152", ChannelID: "100000000000000160",
		Content: "zebra in a DM", Author: &discordgo.User{ID: tBob, Username: "bob"}}}) // DM: not indexed

	waitFor := func(cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatal("condition not met in time")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	search := func() []dto.SearchHit {
		hits, err := store.Search(ctx, dto.SearchQuery{GuildID: tGuild, Query: "zebra", ChannelID: tGeneral})
		if err != nil {
			t.Fatal(err)
		}
		return hits
	}
	// Channel-scoped search includes the thread reply via thread_parent_id.
	waitFor(func() bool { return len(search()) == 2 })

	c.onMessageDelete(nil, &discordgo.MessageDelete{Message: &discordgo.Message{ID: "100000000000000151", ChannelID: tThread}})
	waitFor(func() bool { return len(search()) == 1 })
	c.onMessageDeleteBulk(nil, &discordgo.MessageDeleteBulk{ChannelID: tGeneral, Messages: []string{"100000000000000150"}})
	waitFor(func() bool { return len(search()) == 0 })

	c.onChannelDelete(nil, &discordgo.ChannelDelete{Channel: &discordgo.Channel{ID: tGeneral, GuildID: tGuild}})
	c.onRoleCreate(nil, &discordgo.GuildRoleCreate{GuildRole: &discordgo.GuildRole{GuildID: tGuild, Role: &discordgo.Role{ID: tModRole, Name: "Mod"}}})

	want := []string{EventThreadCreate + ":" + tThread, EventMessageCreate + ":" + tGeneral, EventMessageCreate + ":" + tThread,
		EventMessageCreate + ":100000000000000160", EventChannelDelete + ":" + tGeneral, EventRoleCreate + ":"}
	for _, w := range want {
		select {
		case got := <-events:
			if got != w {
				t.Fatalf("event = %q, want %q", got, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("missing event %q", w)
		}
	}
	if _, ok := c.resolver.channel(tGeneral); ok {
		t.Fatal("channel delete not applied to cache")
	}
}

func TestEnqueueNeverBlocks(t *testing.T) {
	ctx := context.Background()
	store, err := index.Open(ctx, index.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// A client whose workers are not running: the queue fills and then drops.
	c := &Client{store: store, log: slog.New(slog.NewJSONHandler(io.Discard, nil)), ingest: make(chan ingestJob, 2)}
	done := make(chan struct{})
	go func() {
		for range 10 {
			c.enqueue(ingestJob{del: true, channelID: tGeneral, ids: []string{"100000000000000150"}})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("enqueue blocked")
	}
	if got := c.droppedIngest.Load(); got != 8 {
		t.Fatalf("dropped = %d, want 8", got)
	}
}
