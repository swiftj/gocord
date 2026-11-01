package index

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/dto"
)

const (
	guild   = "100000000000000001"
	general = "100000000000000010"
	thread  = "100000000000000060"
	alice   = "100000000000000050"
	bob     = "100000000000000051"
)

func msgAt(t time.Time, channel, author, name, content string) dto.MessageItem {
	return dto.MessageItem{ID: dto.SnowflakeFromTime(t), ChannelID: channel, AuthorID: author, Author: name, Content: content}
}

func seed(t *testing.T, s *Store) (base time.Time) {
	t.Helper()
	base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	err := s.UpsertMessages(ctx, guild, "", []dto.MessageItem{
		msgAt(base, general, alice, "alice", "Shipping the release tomorrow"),
		msgAt(base.Add(time.Minute), general, bob, "bob", "Release notes are drafted"),
		msgAt(base.Add(2*time.Minute), general, alice, "alice", "Café meetup on Friday"),
	})
	if err != nil {
		t.Fatal(err)
	}
	m := msgAt(base.Add(3*time.Minute), thread, bob, "bob", "Thread reply about the release")
	m.Attachments = []dto.AttachmentRef{{Filename: "log.txt"}}
	if err := s.UpsertMessage(ctx, guild, general, m); err != nil {
		t.Fatal(err)
	}
	return base
}

func openMem(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSearch(t *testing.T) {
	s := openMem(t)
	base := seed(t, s)
	ctx := context.Background()
	yes := true

	cases := []struct {
		name string
		q    dto.SearchQuery
		want int
	}{
		{"term", dto.SearchQuery{GuildID: guild, Query: "release"}, 3},
		{"prefix", dto.SearchQuery{GuildID: guild, Query: "ship*"}, 1},
		{"phrase", dto.SearchQuery{GuildID: guild, Query: `"release notes"`}, 1},
		{"boolean NOT", dto.SearchQuery{GuildID: guild, Query: "release NOT notes"}, 2},
		{"OR", dto.SearchQuery{GuildID: guild, Query: "tomorrow OR friday"}, 2},
		{"diacritics", dto.SearchQuery{GuildID: guild, Query: "cafe"}, 1},
		{"author filter", dto.SearchQuery{GuildID: guild, Query: "release", AuthorID: bob}, 2},
		{"channel includes threads", dto.SearchQuery{GuildID: guild, Query: "release", ChannelID: general}, 3},
		{"thread only", dto.SearchQuery{GuildID: guild, Query: "release", ChannelID: thread}, 1},
		{"after", dto.SearchQuery{GuildID: guild, Query: "release", After: dto.SnowflakeFromTime(base.Add(30 * time.Second))}, 2},
		{"before", dto.SearchQuery{GuildID: guild, Query: "release", Before: dto.SnowflakeFromTime(base.Add(30 * time.Second))}, 1},
		{"attachments", dto.SearchQuery{GuildID: guild, Query: "release", HasAttachments: &yes}, 1},
		{"other guild", dto.SearchQuery{GuildID: "100000000000000002", Query: "release"}, 0},
		{"limit", dto.SearchQuery{GuildID: guild, Query: "release", Limit: 1}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			hits, err := s.Search(ctx, c.q)
			if err != nil {
				t.Fatal(err)
			}
			if len(hits) != c.want {
				t.Fatalf("got %d hits, want %d: %+v", len(hits), c.want, hits)
			}
		})
	}

	hits, _ := s.Search(ctx, dto.SearchQuery{GuildID: guild, Query: "shipping"})
	if len(hits) != 1 || hits[0].Snippet == "" || hits[0].Timestamp == "" || hits[0].Author != "alice" {
		t.Fatalf("hit fields not populated: %+v", hits)
	}
}

func TestSearchSyntaxFallback(t *testing.T) {
	s := openMem(t)
	seed(t, s)
	hits, warn, err := s.SearchEx(context.Background(), dto.SearchQuery{GuildID: guild, Query: `release "notes`})
	if err != nil {
		t.Fatal(err)
	}
	if warn == "" {
		t.Fatal("expected rewrite warning")
	}
	_ = hits
}

func TestUpdateAndDelete(t *testing.T) {
	s := openMem(t)
	base := seed(t, s)
	ctx := context.Background()
	m := msgAt(base, general, alice, "alice", "Postponed to next week")
	m.EditedAt = dto.FormatTime(base.Add(time.Hour))
	if err := s.UpsertMessage(ctx, guild, "", m); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, dto.SearchQuery{GuildID: guild, Query: "shipping"}); len(hits) != 0 {
		t.Fatal("edited content still indexed")
	}
	hits, _ := s.Search(ctx, dto.SearchQuery{GuildID: guild, Query: "postponed"})
	if len(hits) != 1 || hits[0].EditedAt == "" {
		t.Fatalf("edit not indexed: %+v", hits)
	}
	if err := s.MarkDeleted(ctx, general, m.ID); err != nil {
		t.Fatal(err)
	}
	if hits, _ := s.Search(ctx, dto.SearchQuery{GuildID: guild, Query: "postponed"}); len(hits) != 0 {
		t.Fatal("deleted message still returned")
	}
}

func TestCoverage(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	o, n, full, err := s.Coverage(ctx, general)
	if err != nil || o != "" || n != "" || full {
		t.Fatalf("unknown channel: %q %q %v %v", o, n, full, err)
	}
	if err := s.UpdateCoverage(ctx, general, guild, "100000000000000100", "100000000000000200", false); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCoverage(ctx, general, guild, "100000000000000050", "", true); err != nil {
		t.Fatal(err)
	}
	o, n, full, _ = s.Coverage(ctx, general)
	if o != "100000000000000050" || n != "100000000000000200" || !full {
		t.Fatalf("coverage = %q %q %v", o, n, full)
	}
}

func TestPersonas(t *testing.T) {
	s := openMem(t)
	ctx := context.Background()
	if _, err := s.GetPersona(ctx, "planner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	p := dto.Persona{Name: "planner", DisplayName: "Planner Bot", AvatarURL: "https://example.com/a.png", DefaultChannelID: general}
	if err := s.UpsertPersona(ctx, p); err != nil {
		t.Fatal(err)
	}
	p.DisplayName = "Planner"
	if err := s.UpsertPersona(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPersona(ctx, "PLANNER")
	if err != nil || got.DisplayName != "Planner" || got.CreatedAt == "" {
		t.Fatalf("got %+v %v", got, err)
	}
	all, _ := s.ListPersonas(ctx)
	if len(all) != 1 {
		t.Fatalf("list = %+v", all)
	}

	if _, _, err := s.GetPersonaWebhook(ctx, general); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	if err := s.PutPersonaWebhook(ctx, general, "100000000000000900", "tok"); err != nil {
		t.Fatal(err)
	}
	id, tok, err := s.GetPersonaWebhook(ctx, general)
	if err != nil || id != "100000000000000900" || tok != "tok" {
		t.Fatalf("webhook = %q %q %v", id, tok, err)
	}
	if err := s.DeletePersonaWebhook(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetPersonaWebhook(ctx, general); !errors.Is(err, ErrNotFound) {
		t.Fatal("webhook not deleted")
	}
}

func TestMemoryStoresIsolated(t *testing.T) {
	a, b := openMem(t), openMem(t)
	seed(t, a)
	if hits, _ := b.Search(context.Background(), dto.SearchQuery{GuildID: guild, Query: "release"}); len(hits) != 0 {
		t.Fatal("in-memory stores share data")
	}
}

func TestDiskStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "index.db")
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	seed(t, s)
	s.Close()
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if hits, _ := s.Search(ctx, dto.SearchQuery{GuildID: guild, Query: "release"}); len(hits) != 3 {
		t.Fatalf("persisted hits = %d", len(hits))
	}
}
