package discord

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

const (
	tGuild     = "100000000000000001"
	tOther     = "100000000000000002"
	tGeneral   = "100000000000000010"
	tAnnounce  = "100000000000000011"
	tGeneral2  = "100000000000000012"
	tCategory  = "100000000000000020"
	tThread    = "100000000000000060"
	tModRole   = "100000000000000040"
	tAlice     = "100000000000000050"
	tBob       = "100000000000000051"
	tUnknownID = "100000000000000098"
)

// seededResolver builds a cache purely from synthetic Gateway events.
func seededResolver(rest resolverREST) *Resolver {
	r := NewResolver(rest)
	r.applyGuild(&discordgo.Guild{
		ID: tGuild, Name: "Test Guild", OwnerID: tAlice, MemberCount: 3,
		Channels: []*discordgo.Channel{
			{ID: tGeneral, Name: "general", Type: discordgo.ChannelTypeGuildText, ParentID: tCategory},
			{ID: tAnnounce, Name: "announcements", Type: discordgo.ChannelTypeGuildNews},
			{ID: tCategory, Name: "Community", Type: discordgo.ChannelTypeGuildCategory},
		},
		Threads: []*discordgo.Channel{
			{ID: tThread, Name: "Release planning", Type: discordgo.ChannelTypeGuildPublicThread, ParentID: tGeneral},
		},
		Roles: []*discordgo.Role{{ID: tModRole, Name: "Moderator", Position: 5}},
		Members: []*discordgo.Member{
			{User: &discordgo.User{ID: tAlice, Username: "alice", GlobalName: "Alice A"}},
			{User: &discordgo.User{ID: tBob, Username: "bob"}, Nick: "Bobby"},
		},
	})
	r.applyGuild(&discordgo.Guild{
		ID: tOther, Name: "Other",
		Channels: []*discordgo.Channel{{ID: tGeneral2, Name: "general", Type: discordgo.ChannelTypeGuildText}},
	})
	return r
}

func TestResolveChannelID(t *testing.T) {
	r := seededResolver(nil)
	ctx := context.Background()
	tests := []struct {
		name, guild, ref, want string
		wantErr                error
	}{
		{"raw id", tGuild, tGeneral, tGeneral, nil},
		{"hash name", tGuild, "#general", tGeneral, nil},
		{"bare name", tGuild, "general", tGeneral, nil},
		{"case insensitive", tGuild, "#GENERAL", tGeneral, nil},
		{"mention", tGuild, "<#" + tGeneral + ">", tGeneral, nil},
		{"thread by name", tGuild, "release planning", tThread, nil},
		{"other guild scoped", tOther, "#general", tGeneral2, nil},
		{"ambiguous across guilds", "", "#general", "", ErrAmbiguous},
		{"id in wrong guild", tOther, tGeneral, "", ErrNotFound},
		{"unknown name", tGuild, "#nope", "", ErrNotFound},
		{"unknown id", tGuild, tUnknownID, "", ErrNotFound},
		{"empty", tGuild, "  ", "", ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.ResolveChannelID(ctx, tc.guild, tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestResolveAmbiguousListsCandidates(t *testing.T) {
	r := seededResolver(nil)
	_, err := r.ResolveChannelID(context.Background(), "", "general")
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{tGeneral, tGeneral2, "#general in Test Guild", "#general in Other"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err, want)
		}
	}
}

func TestResolveUserID(t *testing.T) {
	r := seededResolver(nil)
	ctx := context.Background()
	tests := []struct {
		name, guild, ref, want string
		wantErr                error
	}{
		{"raw id", tGuild, tAlice, tAlice, nil},
		{"at name", tGuild, "@alice", tAlice, nil},
		{"bare name", tGuild, "alice", tAlice, nil},
		{"mention", tGuild, "<@" + tAlice + ">", tAlice, nil},
		{"nick mention", tGuild, "<@!" + tBob + ">", tBob, nil},
		{"global name", tGuild, "Alice A", tAlice, nil},
		{"nickname", tGuild, "@bobby", tBob, nil},
		{"any guild", "", "@bob", tBob, nil},
		{"not member of guild", tOther, tAlice, "", ErrNotFound},
		{"unknown", tGuild, "@carol", "", ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.ResolveUserID(ctx, tc.guild, tc.ref)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestResolveUserAmbiguous(t *testing.T) {
	r := seededResolver(nil)
	r.applyMember(tGuild, &discordgo.Member{User: &discordgo.User{ID: "100000000000000052", Username: "alice2"}, Nick: "alice"})
	_, err := r.ResolveUserID(context.Background(), tGuild, "@alice")
	if !errors.Is(err, ErrAmbiguous) || !strings.Contains(err.Error(), tAlice) || !strings.Contains(err.Error(), "100000000000000052") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveGuildID(t *testing.T) {
	r := seededResolver(nil)
	ctx := context.Background()
	for _, tc := range []struct {
		ref, want string
		wantErr   error
	}{
		{tGuild, tGuild, nil},
		{"Test Guild", tGuild, nil},
		{"test guild", tGuild, nil},
		{"Nope", "", ErrNotFound},
		{tUnknownID, "", ErrNotFound},
	} {
		got, err := r.ResolveGuildID(ctx, tc.ref)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%q: err = %v, want %v", tc.ref, err, tc.wantErr)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%q: got %q, %v", tc.ref, got, err)
		}
	}
	r.applyGuild(&discordgo.Guild{ID: "100000000000000003", Name: "test guild"})
	if _, err := r.ResolveGuildID(ctx, "Test Guild"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("want ambiguous, got %v", err)
	}
}

func TestCacheUpdates(t *testing.T) {
	r := seededResolver(nil)
	ctx := context.Background()

	// Channel delete removes it from resolution.
	r.removeChannel(tAnnounce)
	if _, err := r.ResolveChannelID(ctx, tGuild, "#announcements"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted channel still resolves: %v", err)
	}
	// Channel rename.
	r.applyChannel(&discordgo.Channel{ID: tGeneral, GuildID: tGuild, Name: "lobby", Type: discordgo.ChannelTypeGuildText})
	if got, err := r.ResolveChannelID(ctx, tGuild, "#lobby"); err != nil || got != tGeneral {
		t.Fatalf("renamed channel: %q %v", got, err)
	}
	if _, err := r.ResolveChannelID(ctx, tGuild, "#general"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name still resolves: %v", err)
	}
	// Role rename is reflected in name lookups.
	r.applyRole(tGuild, &discordgo.Role{ID: tModRole, Name: "Mods"})
	if n, ok := r.roleName(tModRole); !ok || n != "Mods" {
		t.Fatalf("role name = %q %v", n, ok)
	}
	r.removeRole(tModRole)
	if _, ok := r.roleName(tModRole); ok {
		t.Fatal("deleted role still cached")
	}
	// Member removal.
	r.removeMember(tGuild, tBob)
	if _, err := r.ResolveUserID(ctx, tGuild, "@bob"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removed member still resolves: %v", err)
	}
	// Thread parent lookup.
	if p := r.threadParent(tThread); p != tGeneral {
		t.Fatalf("thread parent = %q", p)
	}
	if p := r.threadParent(tGeneral); p != "" {
		t.Fatalf("non-thread parent = %q", p)
	}
	// Guild delete drops everything in that guild.
	r.removeGuild(tGuild)
	if _, err := r.ResolveChannelID(ctx, "", "#lobby"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("channel of removed guild resolves: %v", err)
	}
	r.Reset()
	if _, err := r.ResolveGuildID(ctx, "Other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reset did not clear: %v", err)
	}
}

// fakeREST serves cache misses without a network.
type fakeREST struct {
	calls    []string
	channels map[string]*discordgo.Channel
	members  []*discordgo.Member
}

func (f *fakeREST) restGuild(_ context.Context, id string) (*discordgo.Guild, error) {
	f.calls = append(f.calls, "guild")
	return nil, refNotFound("guild", id)
}

func (f *fakeREST) restUserGuilds(context.Context) ([]*discordgo.UserGuild, error) {
	f.calls = append(f.calls, "userGuilds")
	return []*discordgo.UserGuild{{ID: "100000000000000005", Name: "Fresh"}}, nil
}

func (f *fakeREST) restChannel(_ context.Context, id string) (*discordgo.Channel, error) {
	f.calls = append(f.calls, "channel")
	if c, ok := f.channels[id]; ok {
		return c, nil
	}
	return nil, refNotFound("channel", id)
}

func (f *fakeREST) restGuildChannels(_ context.Context, guildID string) ([]*discordgo.Channel, error) {
	f.calls = append(f.calls, "guildChannels")
	var out []*discordgo.Channel
	for _, c := range f.channels {
		if c.GuildID == guildID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeREST) restGuildMember(_ context.Context, _, id string) (*discordgo.Member, error) {
	f.calls = append(f.calls, "member")
	return nil, refNotFound("user", id)
}

func (f *fakeREST) restSearchMembers(context.Context, string, string) ([]*discordgo.Member, error) {
	f.calls = append(f.calls, "searchMembers")
	return f.members, nil
}

func (f *fakeREST) restUser(_ context.Context, id string) (*discordgo.User, error) {
	f.calls = append(f.calls, "user")
	return nil, refNotFound("user", id)
}

func TestResolverRESTFallback(t *testing.T) {
	f := &fakeREST{
		channels: map[string]*discordgo.Channel{
			"100000000000000013": {ID: "100000000000000013", GuildID: tGuild, Name: "dev", Type: discordgo.ChannelTypeGuildText},
		},
		members: []*discordgo.Member{{User: &discordgo.User{ID: "100000000000000053", Username: "carol"}}},
	}
	r := seededResolver(f)
	ctx := context.Background()

	if got, err := r.ResolveChannelID(ctx, tGuild, "#dev"); err != nil || got != "100000000000000013" {
		t.Fatalf("channel name fallback: %q %v", got, err)
	}
	// Second lookup is served from cache.
	n := len(f.calls)
	if _, err := r.ResolveChannelID(ctx, tGuild, "100000000000000013"); err != nil || len(f.calls) != n {
		t.Fatalf("expected cache hit; calls=%v err=%v", f.calls, err)
	}
	if got, err := r.ResolveUserID(ctx, tGuild, "@carol"); err != nil || got != "100000000000000053" {
		t.Fatalf("member search fallback: %q %v", got, err)
	}
	if got, err := r.ResolveGuildID(ctx, "Fresh"); err != nil || got != "100000000000000005" {
		t.Fatalf("guild list fallback: %q %v", got, err)
	}
	if _, err := r.ResolveGuildID(ctx, tUnknownID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown guild id: %v", err)
	}
	if _, err := r.ResolveUserID(ctx, tGuild, tUnknownID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user id: %v", err)
	}
}
