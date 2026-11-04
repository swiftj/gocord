package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/dto"
)

const (
	guildA = "100000000000000001"
	guildB = "100000000000000002"
	chanA  = "100000000000000010"
	chanB  = "100000000000000011"
	thread = "100000000000000060"
)

func TestCheck(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	fresh := dto.SnowflakeFromTime(now.Add(-time.Hour))
	stale := dto.SnowflakeFromTime(now.Add(-15 * 24 * time.Hour))

	cases := []struct {
		name string
		cfg  config.Config
		op   Op
		deny bool
	}{
		{"read allowed in read-only", config.Config{ReadOnly: true}, Op{Tool: "discord_read_messages", ChannelID: chanA}, false},
		{"write denied in read-only", config.Config{ReadOnly: true}, Op{Tool: "discord_send", Write: true, ChannelID: chanA}, true},
		{"dry run allowed in read-only", config.Config{ReadOnly: true}, Op{Tool: "discord_delete_channel", Destructive: true, ChannelID: chanA, DryRun: true}, false},
		{"any guild allowed", config.Config{}, Op{Tool: "discord_get_server_info", GuildID: guildA}, false},
		{"any channel or thread allowed", config.Config{}, Op{Tool: "discord_send", Write: true, ChannelID: thread, ParentChannelID: chanA}, false},
		{"bulk too few", config.Config{}, Op{Tool: "discord_bulk_delete_messages", Destructive: true, ChannelID: chanA, MessageIDs: []string{fresh}}, true},
		{"bulk stale", config.Config{}, Op{Tool: "discord_bulk_delete_messages", Destructive: true, ChannelID: chanA, MessageIDs: []string{fresh, stale}}, true},
		{"bulk ok", config.Config{}, Op{Tool: "discord_bulk_delete_messages", Destructive: true, ChannelID: chanA, MessageIDs: []string{fresh, fresh}}, false},
		{"destructive without scope", config.Config{}, Op{Tool: "discord_delete_webhook", Destructive: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := c.cfg
			g := New(&cfg)
			g.SetClock(func() time.Time { return now })
			err := g.Check(c.op)
			if (err != nil) != c.deny {
				t.Fatalf("deny=%v, err=%v", c.deny, err)
			}
			if err != nil && !errors.Is(err, ErrDenied) {
				t.Fatalf("error does not wrap ErrDenied: %v", err)
			}
		})
	}
}

func TestPreview(t *testing.T) {
	g := New(&config.Config{ReadOnly: true})
	p := g.Preview(Op{Tool: "discord_delete_channel", Destructive: true, ChannelID: chanA}, "#general", []string{"delete #general"})
	if !p.DryRun || p.Allowed || p.DeniedReason == "" {
		t.Fatalf("preview should report denial under read-only: %+v", p)
	}
	g = New(nil)
	p = g.Preview(Op{Tool: "discord_delete_channel", Destructive: true, ChannelID: chanA}, "#general", nil)
	if !p.Allowed {
		t.Fatalf("permissive guard should allow: %+v", p)
	}
}

func TestIsDestructive(t *testing.T) {
	g := New(nil)
	if !g.IsDestructive("discord_manage_roles:delete") || g.IsDestructive("discord_manage_roles:list") || g.IsDestructive("discord_send") {
		t.Fatal("destructive set mismatch")
	}
}
