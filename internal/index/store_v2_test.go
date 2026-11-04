package index

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/local/discord-mcp/internal/dto"
)

// v1Schema is the messages/coverage schema before webhook columns existed.
var v1Schema = []string{
	`CREATE TABLE messages (id TEXT PRIMARY KEY, guild_id TEXT NOT NULL, channel_id TEXT NOT NULL, thread_parent_id TEXT,
		author_id TEXT NOT NULL, author_name TEXT NOT NULL, content TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL,
		edited_at INTEGER, has_attachments INTEGER NOT NULL DEFAULT 0, deleted INTEGER NOT NULL DEFAULT 0)`,
	`CREATE TABLE channel_coverage (channel_id TEXT PRIMARY KEY, guild_id TEXT NOT NULL, oldest_id TEXT, newest_id TEXT,
		fully_backfilled INTEGER NOT NULL DEFAULT 0, updated_at INTEGER NOT NULL)`,
	`INSERT INTO channel_coverage VALUES ('100000000000000010', '100000000000000001', '100000000000000100', '100000000000000101', 1, 0)`,
	`PRAGMA user_version = 1`,
}

func TestMigrateV1ToV2(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "index.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range v1Schema {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	db.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	if _, _, full, _ := s.Coverage(ctx, "100000000000000010"); full {
		t.Fatal("coverage must be reset so channels are re-read for webhook data")
	}
	msg := dto.MessageItem{ID: "100000000000000150", ChannelID: "100000000000000010", AuthorID: "100000000000000700",
		Author: "Ser Thaddeus", AuthorDisplayName: "Ser Thaddeus", WebhookID: "100000000000000700", IsWebhook: true,
		AuthorAvatarURL: "https://cdn.discordapp.com/avatars/100000000000000700/a.png?size=1024", Content: "for the crown",
		Attachments: []dto.AttachmentRef{{ID: "100000000000000500", Filename: "sigil.png", Width: 64, Height: 64}}}
	if err := s.UpsertMessage(ctx, "100000000000000001", "", msg); err != nil {
		t.Fatal(err)
	}
	hits, err := s.Search(ctx, dto.SearchQuery{GuildID: "100000000000000001", Query: "crown"})
	if err != nil || len(hits) != 1 {
		t.Fatalf("search = %v, %v", hits, err)
	}
	h := hits[0]
	if !h.IsWebhook || h.WebhookID != msg.WebhookID || h.AuthorDisplayName != "Ser Thaddeus" || h.AuthorAvatarURL != msg.AuthorAvatarURL ||
		len(h.Attachments) != 1 || h.Attachments[0].Width != 64 || h.Attachments[0].ID != "100000000000000500" {
		t.Fatalf("enriched hit = %+v", h)
	}

	// Re-opening a v2 database is a no-op and keeps coverage.
	if err := s.UpdateCoverage(ctx, "100000000000000010", "100000000000000001", "1", "2", true); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, _, full, _ := s2.Coverage(ctx, "100000000000000010"); !full {
		t.Fatal("v2 reopen must not reset coverage")
	}
	authors, err := s2.ListWebhookAuthors(ctx, dto.TupperQuery{GuildID: "100000000000000001"})
	if err != nil || len(authors) != 1 || authors[0].MessageCount != 1 || authors[0].DisplayName != "Ser Thaddeus" {
		t.Fatalf("authors = %+v, %v", authors, err)
	}
	if _, err := s2.ListWebhookAuthors(ctx, dto.TupperQuery{GuildID: "100000000000000001", After: "yesterday"}); err == nil {
		t.Fatal("non-Snowflake after must fail")
	}
}
