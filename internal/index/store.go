// Package index is the CGO-free SQLite FTS5 message and persona store.
// See specs/01-architecture-and-contracts.md §8.
package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver registered as "sqlite"

	"github.com/local/discord-mcp/internal/dto"
)

// ErrNotFound is returned when a persona or persona webhook does not exist.
var ErrNotFound = errors.New("index: not found")

// MemoryPath selects a RAM-only database.
const MemoryPath = ":memory:"

const schemaVersion = 1

// Store wraps the SQLite database.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// Open opens (and migrates) the store at path (":memory:" or a file path).
// In-memory stores use a single connection so every query sees the same database
// and separate Store values are isolated from each other.
func Open(ctx context.Context, path string) (*Store, error) {
	var dsn string
	if path == "" || path == MemoryPath {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	} else {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("index: create dir: %w", err)
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) //nolint:gosec // G304: operator-configured --db-path
		if err != nil {
			return nil, fmt.Errorf("index: create db file: %w", err)
		}
		f.Close()
		dsn = "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("index: open: %w", err)
	}
	if path == "" || path == MemoryPath {
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(0)
		db.SetConnMaxIdleTime(0)
	}
	s := &Store{db: db, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS messages (
		id               TEXT PRIMARY KEY,
		guild_id         TEXT NOT NULL,
		channel_id       TEXT NOT NULL,
		thread_parent_id TEXT,
		author_id        TEXT NOT NULL,
		author_name      TEXT NOT NULL,
		content          TEXT NOT NULL DEFAULT '',
		created_at       INTEGER NOT NULL,
		edited_at        INTEGER,
		has_attachments  INTEGER NOT NULL DEFAULT 0,
		deleted          INTEGER NOT NULL DEFAULT 0
	)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_chan_time ON messages(channel_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_guild_time ON messages(guild_id, created_at)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_author ON messages(guild_id, author_id)`,
	`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		content, author_name,
		content='messages', content_rowid='rowid',
		tokenize='unicode61 remove_diacritics 2'
	)`,
	`CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
		INSERT INTO messages_fts(rowid, content, author_name) VALUES (new.rowid, new.content, new.author_name);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content, author_name) VALUES ('delete', old.rowid, old.content, old.author_name);
	END`,
	`CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, content, author_name) VALUES ('delete', old.rowid, old.content, old.author_name);
		INSERT INTO messages_fts(rowid, content, author_name) VALUES (new.rowid, new.content, new.author_name);
	END`,
	`CREATE TABLE IF NOT EXISTS channel_coverage (
		channel_id       TEXT PRIMARY KEY,
		guild_id         TEXT NOT NULL,
		oldest_id        TEXT,
		newest_id        TEXT,
		fully_backfilled INTEGER NOT NULL DEFAULT 0,
		updated_at       INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS personas (
		name               TEXT PRIMARY KEY COLLATE NOCASE,
		display_name       TEXT NOT NULL,
		avatar_url         TEXT,
		description        TEXT,
		default_channel_id TEXT,
		created_at         INTEGER NOT NULL,
		updated_at         INTEGER NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS persona_webhooks (
		channel_id    TEXT PRIMARY KEY,
		webhook_id    TEXT NOT NULL,
		webhook_token TEXT NOT NULL,
		created_at    INTEGER NOT NULL
	)`,
	fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion),
}

func (s *Store) migrate(ctx context.Context) error {
	for _, stmt := range migrations {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("index: migrate: %w", err)
		}
	}
	return nil
}

// ---------- Messages ----------

const upsertSQL = `INSERT INTO messages
	(id, guild_id, channel_id, thread_parent_id, author_id, author_name, content, created_at, edited_at, has_attachments, deleted)
	VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, 0)
	ON CONFLICT(id) DO UPDATE SET
		content = excluded.content,
		author_name = excluded.author_name,
		edited_at = excluded.edited_at,
		has_attachments = excluded.has_attachments,
		thread_parent_id = COALESCE(excluded.thread_parent_id, messages.thread_parent_id),
		deleted = 0`

// UpsertMessage indexes one message. threadParentID is the parent channel when
// m.ChannelID is a thread ("" otherwise).
func (s *Store) UpsertMessage(ctx context.Context, guildID, threadParentID string, m dto.MessageItem) error {
	return s.UpsertMessages(ctx, guildID, threadParentID, []dto.MessageItem{m})
}

// UpsertMessages indexes messages in a single transaction.
func (s *Store) UpsertMessages(ctx context.Context, guildID, threadParentID string, ms []dto.MessageItem) error {
	if len(ms) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("index: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after Commit
	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return fmt.Errorf("index: prepare: %w", err)
	}
	defer stmt.Close()
	for _, m := range ms {
		created, err := dto.SnowflakeTime(m.ID)
		if err != nil {
			return fmt.Errorf("index: %w", err)
		}
		var edited any
		if m.EditedAt != "" {
			if t, err := time.Parse(time.RFC3339, m.EditedAt); err == nil {
				edited = t.UnixMilli()
			}
		}
		if _, err := stmt.ExecContext(ctx, m.ID, guildID, m.ChannelID, threadParentID, m.AuthorID, m.Author,
			m.Content, created.UnixMilli(), edited, boolInt(len(m.Attachments) > 0)); err != nil {
			return fmt.Errorf("index: upsert %s: %w", m.ID, err)
		}
	}
	return tx.Commit()
}

// MarkDeleted soft-deletes a message so it no longer appears in search.
func (s *Store) MarkDeleted(ctx context.Context, channelID, messageID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE messages SET deleted = 1 WHERE id = ? AND channel_id = ?`, messageID, channelID)
	if err != nil {
		return fmt.Errorf("index: mark deleted: %w", err)
	}
	return nil
}

// Search runs an FTS5 query. See SearchEx for the rewrite warning.
func (s *Store) Search(ctx context.Context, q dto.SearchQuery) ([]dto.SearchHit, error) {
	hits, _, err := s.SearchEx(ctx, q)
	return hits, err
}

// SearchEx runs an FTS5 query with filters. If the query has invalid FTS5 syntax
// it is retried once as a quoted phrase and a non-empty warning is returned.
func (s *Store) SearchEx(ctx context.Context, q dto.SearchQuery) ([]dto.SearchHit, string, error) {
	if strings.TrimSpace(q.Query) == "" {
		return nil, "", errors.New("index: empty query")
	}
	hits, err := s.search(ctx, q, q.Query)
	if err == nil {
		return hits, "", nil
	}
	if !isFTSSyntaxError(err) {
		return nil, "", err
	}
	phrase := `"` + strings.ReplaceAll(q.Query, `"`, `""`) + `"`
	hits, err = s.search(ctx, q, phrase)
	if err != nil {
		return nil, "", err
	}
	return hits, "query was not valid FTS5 syntax; searched it as an exact phrase", nil
}

func (s *Store) search(ctx context.Context, q dto.SearchQuery, match string) ([]dto.SearchHit, error) {
	var (
		where = []string{"messages_fts MATCH ?", "m.guild_id = ?", "m.deleted = 0"}
		args  = []any{match, q.GuildID}
	)
	if q.ChannelID != "" {
		where = append(where, "(m.channel_id = ? OR m.thread_parent_id = ?)")
		args = append(args, q.ChannelID, q.ChannelID)
	}
	if q.AuthorID != "" {
		where = append(where, "m.author_id = ?")
		args = append(args, q.AuthorID)
	}
	if q.Before != "" {
		t, err := dto.SnowflakeTime(q.Before)
		if err != nil {
			return nil, err
		}
		where = append(where, "m.created_at < ?")
		args = append(args, t.UnixMilli())
	}
	if q.After != "" {
		t, err := dto.SnowflakeTime(q.After)
		if err != nil {
			return nil, err
		}
		where = append(where, "m.created_at > ?")
		args = append(args, t.UnixMilli())
	}
	if q.HasAttachments != nil {
		where = append(where, "m.has_attachments = ?")
		args = append(args, boolInt(*q.HasAttachments))
	}
	limit := q.Limit
	if limit <= 0 {
		limit = dto.DefaultSearchLimit
	}
	limit = min(limit, dto.MaxSearchLimit)
	args = append(args, limit)

	//nolint:gosec // G202: WHERE fragments are constant strings; every value is a bound parameter
	query := `SELECT m.id, m.channel_id, m.author_id, m.author_name, m.content, m.created_at, m.edited_at, m.has_attachments,
		snippet(messages_fts, 0, '**', '**', '…', 12), bm25(messages_fts)
		FROM messages_fts JOIN messages m ON m.rowid = messages_fts.rowid
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY bm25(messages_fts), m.created_at DESC
		LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("index: search: %w", err)
	}
	defer rows.Close()
	var out []dto.SearchHit
	for rows.Next() {
		var (
			h       dto.SearchHit
			created int64
			edited  sql.NullInt64
			hasAtt  int
		)
		if err := rows.Scan(&h.ID, &h.ChannelID, &h.AuthorID, &h.Author, &h.Content, &created, &edited, &hasAtt, &h.Snippet, &h.Rank); err != nil {
			return nil, fmt.Errorf("index: scan: %w", err)
		}
		h.Content = dto.Truncate(h.Content, dto.MaxContentChars)
		h.Timestamp = dto.FormatTime(time.UnixMilli(created))
		if edited.Valid {
			h.EditedAt = dto.FormatTime(time.UnixMilli(edited.Int64))
		}
		if hasAtt == 1 {
			h.Attachments = []dto.AttachmentRef{{Filename: "(attachment)"}}
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("index: search: %w", err)
	}
	return out, nil
}

func isFTSSyntaxError(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "fts5: syntax error") || strings.Contains(msg, "unterminated string") ||
		strings.Contains(msg, "no such column") || strings.Contains(msg, "unknown special query")
}

// ---------- Coverage ----------

// Coverage reports the indexed Snowflake range for a channel. Unknown channels
// return empty IDs and full=false with a nil error.
func (s *Store) Coverage(ctx context.Context, channelID string) (oldestID, newestID string, full bool, err error) {
	var o, n sql.NullString
	var f int
	err = s.db.QueryRowContext(ctx,
		`SELECT oldest_id, newest_id, fully_backfilled FROM channel_coverage WHERE channel_id = ?`, channelID).Scan(&o, &n, &f)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("index: coverage: %w", err)
	}
	return o.String, n.String, f == 1, nil
}

// UpdateCoverage records the indexed range. Empty IDs leave the existing value unchanged.
func (s *Store) UpdateCoverage(ctx context.Context, channelID, guildID, oldestID, newestID string, full bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO channel_coverage (channel_id, guild_id, oldest_id, newest_id, fully_backfilled, updated_at)
		VALUES (?, ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			oldest_id = COALESCE(excluded.oldest_id, channel_coverage.oldest_id),
			newest_id = COALESCE(excluded.newest_id, channel_coverage.newest_id),
			fully_backfilled = excluded.fully_backfilled,
			updated_at = excluded.updated_at`,
		channelID, guildID, oldestID, newestID, boolInt(full), s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("index: update coverage: %w", err)
	}
	return nil
}

// ---------- Personas ----------

// UpsertPersona creates or updates a persona (name is case-insensitive).
func (s *Store) UpsertPersona(ctx context.Context, p dto.Persona) error {
	now := s.now().UnixMilli()
	_, err := s.db.ExecContext(ctx, `INSERT INTO personas (name, display_name, avatar_url, description, default_channel_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			display_name = excluded.display_name,
			avatar_url = excluded.avatar_url,
			description = excluded.description,
			default_channel_id = excluded.default_channel_id,
			updated_at = excluded.updated_at`,
		p.Name, p.DisplayName, p.AvatarURL, p.Description, p.DefaultChannelID, now, now)
	if err != nil {
		return fmt.Errorf("index: upsert persona: %w", err)
	}
	return nil
}

const personaCols = `name, display_name, COALESCE(avatar_url,''), COALESCE(description,''), COALESCE(default_channel_id,''), created_at, updated_at`

func scanPersona(sc interface{ Scan(...any) error }) (*dto.Persona, error) {
	var p dto.Persona
	var c, u int64
	if err := sc.Scan(&p.Name, &p.DisplayName, &p.AvatarURL, &p.Description, &p.DefaultChannelID, &c, &u); err != nil {
		return nil, err
	}
	p.CreatedAt = dto.FormatTime(time.UnixMilli(c))
	p.UpdatedAt = dto.FormatTime(time.UnixMilli(u))
	return &p, nil
}

// GetPersona returns a persona by name or ErrNotFound.
func (s *Store) GetPersona(ctx context.Context, name string) (*dto.Persona, error) {
	p, err := scanPersona(s.db.QueryRowContext(ctx, `SELECT `+personaCols+` FROM personas WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("persona %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("index: get persona: %w", err)
	}
	return p, nil
}

// ListPersonas returns all personas ordered by name.
func (s *Store) ListPersonas(ctx context.Context) ([]dto.Persona, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+personaCols+` FROM personas ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("index: list personas: %w", err)
	}
	defer rows.Close()
	var out []dto.Persona
	for rows.Next() {
		p, err := scanPersona(rows)
		if err != nil {
			return nil, fmt.Errorf("index: scan persona: %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// GetPersonaWebhook returns the webhook provisioned for a channel, or ErrNotFound.
func (s *Store) GetPersonaWebhook(ctx context.Context, channelID string) (id, token string, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT webhook_id, webhook_token FROM persona_webhooks WHERE channel_id = ?`, channelID).Scan(&id, &token)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("persona webhook for channel %s: %w", channelID, ErrNotFound)
	}
	if err != nil {
		return "", "", fmt.Errorf("index: get persona webhook: %w", err)
	}
	return id, token, nil
}

// PutPersonaWebhook stores (or replaces) the webhook for a channel.
func (s *Store) PutPersonaWebhook(ctx context.Context, channelID, id, token string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO persona_webhooks (channel_id, webhook_id, webhook_token, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET webhook_id = excluded.webhook_id, webhook_token = excluded.webhook_token, created_at = excluded.created_at`,
		channelID, id, token, s.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("index: put persona webhook: %w", err)
	}
	return nil
}

// DeletePersonaWebhook removes any row for webhookID (no error if absent).
func (s *Store) DeletePersonaWebhook(ctx context.Context, webhookID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM persona_webhooks WHERE webhook_id = ?`, webhookID); err != nil {
		return fmt.Errorf("index: delete persona webhook: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
