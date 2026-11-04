package mcp

import (
	"context"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/policy"
)

// expectedTools is the public contract: specs/02-mcp-surface.md (28 baseline + 9 next-gen)
// plus specs/05-media-and-local-files.md (7 media, local-file, and diagnostics tools).
var expectedTools = []string{
	// Baseline parity (1–28)
	"discord_login", "discord_list_servers", "discord_get_server_info", "discord_send",
	"discord_read_messages", "discord_edit_message", "discord_delete_message", "discord_bulk_delete_messages",
	"discord_add_reaction", "discord_add_multiple_reactions", "discord_remove_reaction",
	"discord_create_text_channel", "discord_create_voice_channel", "discord_create_category",
	"discord_edit_channel", "discord_delete_channel", "discord_set_channel_permissions",
	"discord_create_forum_channel", "discord_create_forum_post", "discord_get_forum_post",
	"discord_reply_to_forum", "discord_list_forum_threads", "discord_list_members", "discord_manage_roles",
	"discord_create_webhook", "discord_send_webhook_message", "discord_edit_webhook", "discord_delete_webhook",
	// Differentiating (29–37)
	"discord_search_messages", "discord_await_interaction", "discord_persona_register", "discord_persona_send",
	"discord_create_poll", "discord_get_poll_results", "discord_manage_threads", "discord_manage_events",
	"discord_governance_audit",
	// Media, local files, and diagnostics (38–44)
	"discord_get_member_avatar", "discord_fetch_media", "fetch_url_to_file",
	"discord_batch_fetch_media", "media_list_files", "discord_list_tuppers",
	"discord_check_access",
}

func testDeps(t *testing.T) Deps {
	t.Helper()
	store, err := index.Open(context.Background(), index.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{}
	return Deps{Discord: discord.NewMockClient(), Store: store, Guard: policy.New(cfg), Config: cfg}
}

func TestServerRegistersFullSurface(t *testing.T) {
	s := NewServer(testDeps(t), "test")
	var got []string
	for name, tool := range s.ListTools() {
		got = append(got, name)
		if tool.Tool.Description == "" {
			t.Errorf("%s has no description", name)
		}
		if tool.Tool.InputSchema.Type != "object" {
			t.Errorf("%s input schema type = %q", name, tool.Tool.InputSchema.Type)
		}
		if tool.Handler == nil {
			t.Errorf("%s has no handler", name)
		}
	}
	sort.Strings(got)
	want := slices.Clone(expectedTools)
	sort.Strings(want)
	if len(want) != 44 {
		t.Fatalf("contract list has %d names", len(want))
	}
	if !slices.Equal(got, want) {
		for _, n := range want {
			if !slices.Contains(got, n) {
				t.Errorf("missing tool %s", n)
			}
		}
		for _, n := range got {
			if !slices.Contains(want, n) {
				t.Errorf("unexpected tool %s", n)
			}
		}
	}
}

func TestServerRegistersPrompts(t *testing.T) {
	prompts := NewServer(testDeps(t), "test").ListPrompts()
	if len(prompts) != 3 {
		t.Errorf("registered %d prompts, want 3", len(prompts))
	}
	for _, name := range []string{"discord-channel-digest", "discord-server-blueprint", "discord-hitl-approval"} {
		if prompts[name] == nil {
			t.Errorf("prompt %s not registered", name)
		}
	}
}

type sent struct{ session, uri string }

func newTestNotifier() (*Notifier, *[]sent, *[]func(), *time.Time) {
	var (
		mu     sync.Mutex
		out    []sent
		timers []func()
		clock  = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	)
	n := newNotifier(func(sid, uri string) {
		mu.Lock()
		defer mu.Unlock()
		out = append(out, sent{sid, uri})
	})
	n.now = func() time.Time { return clock }
	n.after = func(_ time.Duration, f func()) { timers = append(timers, f) }
	return n, &out, &timers, &clock
}

func TestNotifierRoutesAndDebounces(t *testing.T) {
	n, out, timers, clock := newTestNotifier()
	transcript := "discord://channels/100000000000000010/transcript"
	transcriptQ := transcript + "?limit=200"
	guildMap := "discord://guilds/100000000000000001/map"
	n.Subscribe("s1", transcript)
	n.Subscribe("s2", transcriptQ)
	n.Subscribe("s2", guildMap)

	// Message in the channel: both transcript subscriptions fire, map does not.
	n.HandleEvent(discord.EventMessageCreate, "100000000000000001", "100000000000000010")
	if len(*out) != 2 {
		t.Fatalf("sent = %+v", *out)
	}
	// Message in another channel: nothing.
	n.HandleEvent(discord.EventMessageCreate, "100000000000000001", "100000000000000011")
	if len(*out) != 2 {
		t.Fatalf("unrelated channel notified: %+v", *out)
	}
	// Burst within the debounce window: deferred to a single timer.
	n.HandleEvent(discord.EventMessageCreate, "100000000000000001", "100000000000000010")
	n.HandleEvent(discord.EventMessageCreate, "100000000000000001", "100000000000000010")
	if len(*out) != 2 || len(*timers) != 2 { // one timer per URI
		t.Fatalf("debounce failed: sent=%d timers=%d", len(*out), len(*timers))
	}
	*clock = clock.Add(DefaultDebounce)
	for _, f := range *timers {
		f()
	}
	if len(*out) != 4 {
		t.Fatalf("deferred flush: %+v", *out)
	}

	// Role change hits the guild map only.
	*clock = clock.Add(time.Minute)
	n.HandleEvent(discord.EventRoleUpdate, "100000000000000001", "")
	if last := (*out)[len(*out)-1]; last.uri != guildMap || last.session != "s2" {
		t.Fatalf("map notification = %+v", last)
	}

	// Dropped sessions receive nothing.
	n.DropSession("s1")
	n.DropSession("s2")
	before := len(*out)
	*clock = clock.Add(time.Minute)
	n.HandleEvent(discord.EventMessageCreate, "100000000000000001", "100000000000000010")
	if len(*out) != before {
		t.Fatal("notified after session dropped")
	}
}
