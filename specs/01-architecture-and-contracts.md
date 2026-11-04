# Spec 01 — Architecture & Contracts

**Read when:** Wave 1 bootstrap, or editing `cmd/`, `internal/config/`, `internal/dto/`, `internal/policy/`, `internal/discord/`, `internal/index/`.

## 1. Mission & Differentiators
`discord-mcp` is a static, CGO-free Go MCP server for Discord with 100% baseline parity with `barryyip0625/mcp-discord`, plus six differentiators:

1. **Embedded SQLite + FTS5 search:** Works around Discord's `403 Forbidden` bot search restriction via real-time Gateway indexing and lazy REST backfill. Runs on disk or RAM-only (`:memory:`).
2. **Human-in-the-Loop (HITL) components:** Awaitable Buttons, Select Menus, and Modals (`discord_await_interaction`), so Claude can request mobile approval or structured input.
3. **Multi-Persona webhook engine:** A persona registry (`discord_persona_register`, `discord_persona_send`) that provisions or reuses channel webhooks to post as named agents with custom avatars and thread support.
4. **Multi-profile bot routing:** Use a default `DISCORD_BOT_TOKEN` or multiple named profiles (`--config` file or env vars) to switch identities.
5. **Full MCP surface:** Tools, Resources (`discord://`), Resource Subscriptions (Gateway push), and Prompts. Outputs are token-compacted DTOs, and `#channel`/`@user` references resolve to Snowflakes automatically.
6. **Governance & dry-run policy engine:** `--read-only`, guild/channel allow and block lists, rate-limit telemetry, and `dry_run: true` previews on every destructive operation.

## 2. Locked Stack
| Concern | Choice |
| :--- | :--- |
| Language | Go 1.27+ (required by `golk`; `mcp-go` v1.5.0 needs 1.26), `CGO_ENABLED=0` for every shipped artifact |
| Module | `github.com/local/discord-mcp` (keep an existing `go.mod` path if present) |
| MCP SDK | `github.com/mark3labs/mcp-go`: `server.NewMCPServer`, `server.ServeStdio`, `server.NewStreamableHTTPServer` (endpoint `/mcp`) |
| Discord | `github.com/bwmarrin/discordgo` (REST + Gateway v10) |
| Index DB | `modernc.org/sqlite` (pure Go, FTS5 enabled) |
| CLI/config | stdlib `flag` + env vars (see §6) |
| Logging | `log/slog` JSON handler on `os.Stderr` only. Never write to `os.Stdout`. |

## 3. Monorepo Directory Layout
```text
.
├── CLAUDE.md
├── Makefile
├── .goreleaser.yaml
├── go.mod
├── specs/                              # Progressive-disclosure specifications (this folder)
├── .claude/rules/                      # Path-scoped rules loaded automatically
├── .github/workflows/{ci,release}.yml  # See spec 04
├── cmd/
│   └── discord-mcp/
│       └── main.go                     # Entrypoint: flags, env config, stdio & HTTP server startup
├── internal/
│   ├── config/
│   │   └── config.go                   # Env/flag parsing, multi-profile config, safety policy rules
│   ├── dto/
│   │   └── models.go                   # Token-compacted structs for MCP tool/resource responses
│   ├── policy/
│   │   ├── guard.go                    # Read-only, guild/channel allowlist, dry-run diff evaluator
│   │   └── guard_test.go
│   ├── discord/
│   │   ├── interface.go                # discord.Service interface (Wave 1 contract for all tools)
│   │   ├── client.go                   # discordgo implementation, Gateway handlers, rate-limit wrapper
│   │   ├── resolver.go                 # In-memory cache resolving #channel-name and @username -> Snowflake
│   │   └── mock_client.go              # Deterministic mock implementation for unit tests
│   ├── index/
│   │   ├── store.go                    # modernc.org/sqlite FTS5 message & persona store (:memory: or disk)
│   │   └── store_test.go
│   └── mcp/
│       ├── server.go                   # MCP server bootstrap, tool/resource/prompt registration
│       ├── tools/
│       │   ├── guilds.go               # Server info, list servers, audit logs, automod rules
│       │   ├── channels.go             # Text/voice/stage/category CRUD + permission overwrites
│       │   ├── messages.go             # Send, read, edit, delete, bulk delete, embeds, file attachments
│       │   ├── search.go               # FTS5 instant search + automatic REST pagination backfill
│       │   ├── forums_threads.go       # Forum channels, forum posts, tags, public/private threads
│       │   ├── reactions_polls.go      # Single/batch emoji reactions + native Discord Polls API
│       │   ├── members_roles.go        # Member list/inspect, timeouts, role CRUD & assignment
│       │   ├── webhooks_persona.go     # Webhook CRUD + Multi-Persona identity registry & sender
│       │   ├── events.go               # Guild Scheduled Events CRUD & RSVP listing
│       │   └── interactions.go         # HITL Buttons, Select Menus, Modals & await callback broker
│       ├── resources/
│       │   └── resources.go            # discord://guilds/{id}/map, discord://channels/{id}/transcript
│       └── prompts/
│           └── prompts.go              # Built-in MCP prompts
├── ecosystem/                          # See spec 03
│   ├── dxt/{manifest.json,build-dxt.sh}
│   └── claude-plugin/...
└── docs/
    ├── ARCHITECTURE.md
    └── TOOL_REFERENCE.md
```

### Package dependency rules
- `dto` imports nothing internal.
- `config` imports nothing internal.
- `policy` imports `config` and `dto`.
- `index` imports `dto`.
- `discord` imports `dto` (and `index` for Gateway → indexer wiring in `client.go` only).
- `mcp/*` imports `discord`, `dto`, `policy`, `index`, and `config`. Tool handlers depend on the `discord.Service` **interface**, never on `*discord.Client`.
- `cmd/discord-mcp` is the only package that constructs concrete implementations.

## 4. `discord.Service` Interface (Contract Lock)
`internal/discord/interface.go` MUST match this exactly in Wave 1. Changes after Wave 1 require Lead Agent approval because all Wave 2 teammates compile against it.

```go
// internal/discord/interface.go
package discord

import (
	"context"

	"github.com/local/discord-mcp/internal/dto"
)

type Service interface {
	// Session & Resolution
	Login(ctx context.Context, token string, profile string) (*dto.SessionInfo, error)
	ResolveGuildID(ctx context.Context, guildRef string) (string, error)
	ResolveChannelID(ctx context.Context, guildID, channelRef string) (string, error)
	ResolveUserID(ctx context.Context, guildID, userRef string) (string, error)

	// Guilds, Audit & AutoMod
	ListGuilds(ctx context.Context) ([]dto.GuildSummary, error)
	GetGuildInfo(ctx context.Context, guildID string) (*dto.GuildDetail, error)
	GetAuditLogs(ctx context.Context, guildID string, actionType int, limit int) ([]dto.AuditLogEntry, error)
	ListAutoModRules(ctx context.Context, guildID string) ([]dto.AutoModRule, error)

	// Channels & Categories
	ListChannels(ctx context.Context, guildID string) ([]dto.ChannelInfo, error)
	GetChannel(ctx context.Context, channelID string) (*dto.ChannelInfo, error) // added in Wave 1
	CreateChannel(ctx context.Context, req dto.CreateChannelReq) (*dto.ChannelInfo, error)
	EditChannel(ctx context.Context, req dto.EditChannelReq) (*dto.ChannelInfo, error)
	DeleteChannel(ctx context.Context, channelID, reason string) error
	SetChannelPermissions(ctx context.Context, req dto.ChannelPermissionReq) error

	// Messages, Embeds & Attachments
	SendMessage(ctx context.Context, req dto.SendMessageReq) (*dto.MessageItem, error)
	ReadMessages(ctx context.Context, req dto.ReadMessagesReq) ([]dto.MessageItem, error)
	EditMessage(ctx context.Context, channelID, messageID, content string) (*dto.MessageItem, error)
	DeleteMessage(ctx context.Context, channelID, messageID, reason string) error
	BulkDeleteMessages(ctx context.Context, channelID string, messageIDs []string, reason string) error

	// Reactions & Polls
	AddReaction(ctx context.Context, channelID, messageID, emoji string) error
	AddMultipleReactions(ctx context.Context, channelID, messageID string, emojis []string) error
	RemoveReaction(ctx context.Context, channelID, messageID, emoji, userID string) error
	CreatePoll(ctx context.Context, req dto.CreatePollReq) (*dto.MessageItem, error)
	GetPollResults(ctx context.Context, channelID, messageID string) (*dto.PollResult, error)

	// Forums & Threads
	CreateForumChannel(ctx context.Context, req dto.CreateForumReq) (*dto.ChannelInfo, error)
	CreateForumPost(ctx context.Context, req dto.CreateForumPostReq) (*dto.ThreadInfo, error)
	GetForumPost(ctx context.Context, threadID string, limit int) (*dto.ForumPostDetail, error)
	ReplyToForum(ctx context.Context, threadID, content string) (*dto.MessageItem, error)
	ListForumThreads(ctx context.Context, channelID string, includeArchived bool) ([]dto.ThreadInfo, error)
	CreateThread(ctx context.Context, req dto.CreateThreadReq) (*dto.ThreadInfo, error)
	ModifyThread(ctx context.Context, threadID string, archived, locked *bool) (*dto.ThreadInfo, error)

	// Members & Roles
	ListMembers(ctx context.Context, guildID string, limit int, after string) ([]dto.MemberInfo, error)
	TimeoutMember(ctx context.Context, guildID, userID string, durationSeconds int, reason string) error
	ListRoles(ctx context.Context, guildID string) ([]dto.RoleInfo, error)
	CreateRole(ctx context.Context, req dto.RoleMutationReq) (*dto.RoleInfo, error)
	EditRole(ctx context.Context, roleID string, req dto.RoleMutationReq) (*dto.RoleInfo, error)
	DeleteRole(ctx context.Context, guildID, roleID, reason string) error
	AssignRole(ctx context.Context, guildID, userID, roleID string) error
	RemoveRole(ctx context.Context, guildID, userID, roleID string) error

	// Webhooks, Personas, Events & HITL Interactions
	GetWebhook(ctx context.Context, webhookID string) (*dto.WebhookInfo, error) // added in Wave 3 (never returns the token)
	CreateWebhook(ctx context.Context, channelID, name string) (*dto.WebhookInfo, error)
	EditWebhook(ctx context.Context, webhookID, name, channelID string) (*dto.WebhookInfo, error)
	DeleteWebhook(ctx context.Context, webhookID, reason string) error
	SendWebhookMessage(ctx context.Context, req dto.WebhookMessageReq) (*dto.MessageItem, error)
	ListScheduledEvents(ctx context.Context, guildID string) ([]dto.ScheduledEvent, error)
	CreateScheduledEvent(ctx context.Context, req dto.CreateEventReq) (*dto.ScheduledEvent, error)
	AwaitInteraction(ctx context.Context, req dto.AwaitInteractionReq) (*dto.InteractionResult, error)
}
```

### Interface notes
- `GetChannel` was added during Wave 1. Channel-only tools need it to find the channel's guild (for allowlist checks) and a thread's parent, and to fetch the current state that dry-run previews compare against.
- `GetWebhook` was added in Wave 3 so webhook tools scope policy checks to the webhook's real channel and guild, not just the default guild.
- Optional interface (not on `Service`): `BotUserID() string`, implemented by `*Client` and `*MockClient`. `discord_manage_roles` uses it to refuse edits to roles at or above the bot's highest role. The mock fixture gives the bot a managed role `MCP Bot` (`100000000000000041`, position 10).
- `ParseRef` and `NameMatches` in `internal/discord/ref.go` are the single parser for `#name`, `@name`, `<#id>`, `<@id>`, `<@!id>`, `<@&id>`, and raw IDs. `resolver.go` MUST use them.
- `EditRole` takes `guildID` through `req.GuildID` (Discord's role endpoints are guild-scoped).
- Resolver methods accept a raw Snowflake (returned unchanged), `#name`, `name`, or a `<#id>`/`<@id>`/`<@!id>` mention. Ambiguous names return `ErrAmbiguous` listing the candidate IDs. Unknown names return `ErrNotFound`.
- Export sentinel errors from `internal/discord`: `ErrNotFound`, `ErrAmbiguous`, `ErrForbidden`, `ErrRateLimited`, `ErrNotLoggedIn`.
- `AwaitInteraction` MUST honour `ctx` cancellation and `req.TimeoutSeconds`. On timeout it returns `InteractionResult{TimedOut: true}` and a nil error.

### Mock client (`mock_client.go`)
- `type MockClient struct` with exported, mutex-protected fixture maps (`Guilds`, `Channels`, `Messages`, `Roles`, `Members`, `Webhooks`, `Events`) and a `Calls []MockCall` recorder (`Method string; Args []any`).
- Constructor `NewMockClient()` seeds a deterministic fixture: guild `100000000000000001` "Test Guild", channels `#general` (`100000000000000010`) and `#announcements` (`100000000000000011`), category `100000000000000020`, forum `100000000000000030`, role `@Moderator` (`100000000000000040`), and user `@alice` (`100000000000000050`).
- Optional `Err map[string]error` keyed by method name, for failure-injection tests.
- Add `var _ Service = (*MockClient)(nil)` as a compile-time assertion.
- Use synthetic Snowflakes only (`1000000000000000xx`).

## 5. DTO Requirements (`internal/dto/models.go`)
Every DTO is optimised for LLM token cost:

- **JSON tags:** `snake_case`, and `omitempty` on every optional field. Never emit `null`, empty strings, empty arrays, or zero-valued structs.
- **IDs:** Snowflakes are `string`, never `int64`.
- **Times:** RFC 3339 UTC strings (`"2026-11-01T14:55:00Z"`), never `time.Time`, so marshalling stays deterministic.
- **No raw discordgo types** leak into DTOs. Flatten nested objects. For example, `MessageItem.Author` is a username string plus `AuthorID`, not a full user object.
- **Truncation:** Message `Content` over 4,000 chars is truncated with a `"…[truncated N chars]"` suffix. Embeds are summarised as `title + description[:280]`.
- **Permissions:** Exposed as a decimal string bitfield plus a `[]string` of human-readable names (`"ManageChannels"`).

### Required types and minimum fields
| Type | Minimum fields |
| :--- | :--- |
| `SessionInfo` | `Profile`, `BotUserID`, `BotUsername`, `GuildCount` |
| `GuildSummary` | `ID`, `Name`, `MemberCount`, `OwnerID` |
| `GuildDetail` | `GuildSummary` (embedded) + `Description`, `BoostTier`, `BoostCount`, `Features []string`, `Channels []ChannelInfo`, `Roles []RoleInfo`, `CreatedAt` |
| `ChannelInfo` | `ID`, `GuildID`, `Name`, `Type` (string: `text`/`voice`/`category`/`announcement`/`stage`/`forum`/`thread`), `ParentID`, `Position`, `Topic`, `NSFW` |
| `CreateChannelReq` | `GuildID`, `Name`, `Type`, `ParentID`, `Topic`, `Position`, `Bitrate`, `UserLimit`, `Reason` |
| `EditChannelReq` | `ChannelID`, `Name *string`, `Topic *string`, `ParentID *string`, `Archived *bool`, `Reason` |
| `ChannelPermissionReq` | `ChannelID`, `TargetID`, `TargetType` (`role`/`member`), `Allow`, `Deny` (int64 bitfields), `Remove bool`, `Reason` |
| `MessageItem` | `ID`, `ChannelID`, `AuthorID`, `Author`, `Content`, `Timestamp`, `EditedAt`, `ReplyToID`, `Attachments []AttachmentRef`, `Embeds []EmbedSummary`, `Reactions []ReactionCount`, `ThreadID` |
| `AttachmentRef` | `Filename`, `URL`, `Size`, `ContentType` |
| `EmbedSummary` / `EmbedInput` | `Title`, `Description`, `URL`, `Color`, `Fields []EmbedField{Name, Value, Inline}`, `Footer`, `ImageURL` |
| `SendMessageReq` | `ChannelID`, `Content`, `Embed *EmbedInput`, `FilePath`, `ReplyToMessageID` |
| `ReadMessagesReq` | `ChannelID`, `Limit` (1–100), `Before`, `After`, `Around` (Snowflake or ISO-8601; the tool layer converts timestamps to Snowflakes) |
| `CreatePollReq` | `ChannelID`, `Question`, `Answers []string` (1–10), `DurationHours` (1–768), `AllowMultiselect` |
| `PollResult` | `MessageID`, `Question`, `Answers []PollAnswerTally{ID, Text, Votes}`, `TotalVotes`, `Finalized`, `Winners []string`, `ExpiresAt` |
| `CreateForumReq` | `GuildID`, `Name`, `Topic`, `Tags []string`, `ParentID`, `DefaultReaction` |
| `CreateForumPostReq` | `ForumChannelID`, `Title`, `Content`, `TagNames []string` |
| `ForumPostDetail` | `Thread ThreadInfo`, `Starter *MessageItem`, `Replies []MessageItem` |
| `ThreadInfo` | `ID`, `ParentID`, `Name`, `OwnerID`, `Archived`, `Locked`, `Private`, `MessageCount`, `AppliedTags []string`, `CreatedAt` |
| `CreateThreadReq` | `ChannelID`, `MessageID` (optional), `Name`, `Private`, `AutoArchiveMinutes` |
| `MemberInfo` | `UserID`, `Username`, `Nick`, `Roles []string` (names), `JoinedAt`, `Bot`, `TimeoutUntil` |
| `RoleInfo` | `ID`, `Name`, `Color` (hex `"#RRGGBB"`), `Position`, `Permissions`, `PermissionNames []string`, `Hoist`, `Mentionable`, `Managed` |
| `RoleMutationReq` | `GuildID`, `Name *string`, `Color *int`, `Permissions *int64`, `Hoist *bool`, `Mentionable *bool`, `Reason` |
| `AuditLogEntry` | `ID`, `ActionType` (int), `Action` (name), `UserID`, `User`, `TargetID`, `Reason`, `Changes []string` (compact `key: old → new`), `CreatedAt` |
| `AutoModRule` | `ID`, `Name`, `TriggerType`, `Enabled`, `Keywords []string`, `Actions []string`, `ExemptRoles []string`, `ExemptChannels []string` |
| `WebhookInfo` | `ID`, `ChannelID`, `GuildID`, `Name`, `AvatarURL`. The webhook **token/URL is never returned** in tool output (stored server-side only). |
| `WebhookMessageReq` | `WebhookID`, `WebhookURL`, `Content`, `Username`, `AvatarURL`, `ThreadID`, `Embed *EmbedInput` |
| `ScheduledEvent` | `ID`, `GuildID`, `Name`, `Description`, `StartTime`, `EndTime`, `EntityType` (`stage`/`voice`/`external`), `ChannelID`, `Location`, `Status`, `InterestedCount` |
| `CreateEventReq` | `GuildID`, `Name`, `Description`, `StartTimeISO`, `EndTimeISO`, `EntityType`, `LocationOrChannelID` |
| `AwaitInteractionReq` | `ChannelID`, `PromptText`, `Mode` (`buttons`/`select`/`modal`), `Options []InteractionOption{Label, Value, Style, Emoji}`, `AllowedUserIDs`, `AllowedRoleIDs`, `TimeoutSeconds` (default 300, max 900) |
| `InteractionResult` | `MessageID`, `Selected []string` (values), `UserID`, `Username`, `ModalFields map[string]string`, `TimedOut`, `RespondedAt` |
| `Persona` | `Name` (unique key), `DisplayName`, `AvatarURL`, `Description`, `DefaultChannelID`, `CreatedAt`, `UpdatedAt` |
| `SearchQuery` | `GuildID`, `Query`, `ChannelID`, `AuthorID`, `Before`, `After`, `HasAttachments *bool`, `Limit` (default 25, max 100) |
| `SearchHit` | `MessageItem` (embedded) + `Snippet` (FTS5 `snippet()` output), `Rank` |
| `DryRunPreview` | `DryRun: true`, `Action`, `Target`, `Changes []string`, `Warnings []string`, `Allowed bool`, `DeniedReason` |

## 6. Configuration (`internal/config/config.go`)
| Flag | Env var | Default | Notes |
| :--- | :--- | :--- | :--- |
| `--token` | `DISCORD_BOT_TOKEN` | — | Prefer the env var. Never logged. |
| `--config` | `DISCORD_MCP_CONFIG` | — | JSON file defining named `profiles` (see below) |
| `--profile` | `DISCORD_MCP_PROFILE` | `default` | Active profile at startup |
| `--transport` | `DISCORD_MCP_TRANSPORT` | `stdio` | `stdio` or `http` |
| `--http-addr` | `DISCORD_MCP_HTTP_ADDR` | `127.0.0.1:8080` | Loopback by default |
| `--db-path` | `DISCORD_MCP_DB_PATH` | `:memory:` | e.g. `~/.config/discord-mcp/index.db` (expand `~`) |
| `--read-only` | `DISCORD_MCP_READ_ONLY` | `false` | |
| `--allowed-guilds` | `DISCORD_MCP_ALLOWED_GUILDS` | empty (all) | Comma-separated Snowflakes |
| `--blocked-guilds` | `DISCORD_MCP_BLOCKED_GUILDS` | empty | |
| `--allowed-channels` | `DISCORD_MCP_ALLOWED_CHANNELS` | empty (all) | |
| `--blocked-channels` | `DISCORD_MCP_BLOCKED_CHANNELS` | empty | |
| `--default-guild` | `DISCORD_MCP_DEFAULT_GUILD_ID` | — | Used when a tool omits `guild_id` |
| `--log-level` | `DISCORD_MCP_LOG_LEVEL` | `info` | |
| `--version` | — | — | Prints the ldflags-injected `version`, `commit`, `date` |

Precedence: **flag > env > config file > default**.

Profiles file format:
```json
{ "profiles": { "ops": { "token_env": "DISCORD_OPS_TOKEN", "allowed_guilds": ["100000000000000001"], "read_only": false } } }
```
Profiles reference tokens through `token_env` (the name of an env var), never inline. `config.Load()` rejects a profile file containing a literal `"token"` key.

## 7. Policy Engine (`internal/policy/guard.go`)
```go
type Guard struct { /* built from config.Config */ }

type Op struct {
	Tool            string   // e.g. "discord_delete_channel"
	Write           bool     // changes Discord state (send/create/edit/delete/react)
	Destructive     bool     // in DestructiveTools; implies the tool accepts dry_run
	GuildID         string
	ChannelID       string
	ParentChannelID string   // parent when ChannelID is a thread
	MessageIDs      []string // bulk-delete validation
	DryRun          bool
}

func New(cfg *config.Config) *Guard
func (g *Guard) Check(op Op) error // nil or *DeniedError (wraps ErrDenied)
func (g *Guard) Preview(op Op, target string, changes []string, warnings ...string) dto.DryRunPreview
func (g *Guard) IsDestructive(toolOrToolAction string) bool // e.g. "discord_manage_roles:delete"
func (g *Guard) GuildAllowed(guildID string) bool          // for filtering list outputs
func (g *Guard) ChannelAllowed(channelID string) bool
```

### Rules, evaluated in order. The first denial wins.
1. **Read-only:** If `ReadOnly` and `(op.Write || op.Destructive) && !op.DryRun`, deny with `"read-only mode: <tool> is disabled"`. Dry runs are always allowed so Claude can still plan.
2. **Guild blocklist:** If `GuildID` is in `BlockedGuilds`, deny.
3. **Guild allowlist:** If `AllowedGuilds` is non-empty and `GuildID` is non-empty and not in it, deny.
4. **Channel blocklist / allowlist:** Same as rules 2–3 for `ChannelID`. A thread is evaluated against its parent channel as well (the caller passes the parent ID if known).
5. **Bulk limits:** `discord_bulk_delete_messages` requires 2–100 IDs, all younger than 14 days (Snowflake timestamp check). Otherwise deny with a precise reason.
6. **Unresolvable scope:** If a destructive op has neither a guild ID nor a channel ID, deny.

### Destructive tool set
`discord_delete_message`, `discord_bulk_delete_messages`, `discord_delete_channel`, `discord_edit_channel`, `discord_set_channel_permissions`, `discord_manage_roles` (all actions except `list`), `discord_delete_webhook`, `discord_edit_webhook`, `discord_governance_audit` (`timeout_member`), `discord_manage_threads` (`archive`/`lock`).
Sending, reacting, and creating are **write** operations (`Op.Write`), not destructive. Read-only mode blocks them as well. `policy.DestructiveTools` holds the set above, keyed as `tool` or `tool:action`.

### Dry run
When `dry_run: true`, the tool resolves all references, runs `Guard.Check`, and fetches current state where cheap (e.g. the existing overwrite for a permission change). It returns `DryRunPreview` with human-readable `Changes` such as `"#general: role @Moderator allow +ManageMessages"` and makes **zero** mutating API calls.

### Telemetry
`client.go` wraps discordgo's rate-limit callbacks and logs (stderr) `route`, `remaining`, `reset_after`, and `bucket` at `debug`, and 429s at `warn`. The last-seen values are exposed through `Guard`/`Client` for inclusion in error messages.

## 8. SQLite FTS5 Index (`internal/index/store.go`)
- Open with `sql.Open("sqlite", dsn)`. For `:memory:`, use a private (not `cache=shared`) in-memory DB with `SetMaxOpenConns(1)`. That one connection sees all the data, and separate `Store`s stay isolated (needed for parallel tests). For disk paths, create parent dirs with `0700` and the file with `0600`, then set pragmas through the DSN (`_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)`) so they apply to every pooled connection.
- Track schema version with `PRAGMA user_version`. Migrations are idempotent `CREATE ... IF NOT EXISTS`.

```sql
CREATE TABLE IF NOT EXISTS messages (
  id              TEXT PRIMARY KEY,          -- Snowflake
  guild_id        TEXT NOT NULL,
  channel_id      TEXT NOT NULL,
  thread_parent_id TEXT,                     -- non-null when channel_id is a thread
  author_id       TEXT NOT NULL,
  author_name     TEXT NOT NULL,
  content         TEXT NOT NULL DEFAULT '',
  created_at      INTEGER NOT NULL,          -- unix millis (derived from Snowflake)
  edited_at       INTEGER,
  has_attachments INTEGER NOT NULL DEFAULT 0,
  deleted         INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_messages_chan_time ON messages(channel_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_guild_time ON messages(guild_id, created_at);
CREATE INDEX IF NOT EXISTS idx_messages_author ON messages(guild_id, author_id);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
  content, author_name,
  content='messages', content_rowid='rowid',
  tokenize='unicode61 remove_diacritics 2'
);

-- External-content sync triggers
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
  INSERT INTO messages_fts(rowid, content, author_name) VALUES (new.rowid, new.content, new.author_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content, author_name) VALUES ('delete', old.rowid, old.content, old.author_name);
END;
CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
  INSERT INTO messages_fts(messages_fts, rowid, content, author_name) VALUES ('delete', old.rowid, old.content, old.author_name);
  INSERT INTO messages_fts(rowid, content, author_name) VALUES (new.rowid, new.content, new.author_name);
END;

-- Backfill coverage: which Snowflake range of each channel has been fetched via REST
CREATE TABLE IF NOT EXISTS channel_coverage (
  channel_id     TEXT PRIMARY KEY,
  guild_id       TEXT NOT NULL,
  oldest_id      TEXT,                      -- oldest message ID indexed
  newest_id      TEXT,                      -- newest message ID indexed
  fully_backfilled INTEGER NOT NULL DEFAULT 0,
  updated_at     INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS personas (
  name             TEXT PRIMARY KEY COLLATE NOCASE,
  display_name     TEXT NOT NULL,
  avatar_url       TEXT,
  description      TEXT,
  default_channel_id TEXT,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
);

-- Webhooks provisioned by discord-mcp for persona sends (one per channel)
CREATE TABLE IF NOT EXISTS persona_webhooks (
  channel_id   TEXT PRIMARY KEY,
  webhook_id   TEXT NOT NULL,
  webhook_token TEXT NOT NULL,             -- never returned through MCP
  created_at   INTEGER NOT NULL
);
```

### Store API (implemented and tested in Wave 1; `infra-and-index` consumes it)
```go
type Store struct{ /* *sql.DB */ }

func Open(ctx context.Context, path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) UpsertMessage(ctx context.Context, guildID, threadParentID string, m dto.MessageItem) error
func (s *Store) UpsertMessages(ctx context.Context, guildID, threadParentID string, ms []dto.MessageItem) error // single tx
func (s *Store) MarkDeleted(ctx context.Context, channelID, messageID string) error
func (s *Store) Search(ctx context.Context, q dto.SearchQuery) ([]dto.SearchHit, error)
func (s *Store) SearchEx(ctx context.Context, q dto.SearchQuery) ([]dto.SearchHit, string /*warning*/, error)
func (s *Store) Coverage(ctx context.Context, channelID string) (oldestID, newestID string, full bool, err error)
func (s *Store) UpdateCoverage(ctx context.Context, channelID, guildID, oldestID, newestID string, full bool) error
func (s *Store) UpsertPersona(ctx context.Context, p dto.Persona) error
func (s *Store) GetPersona(ctx context.Context, name string) (*dto.Persona, error)
func (s *Store) ListPersonas(ctx context.Context) ([]dto.Persona, error)
func (s *Store) GetPersonaWebhook(ctx context.Context, channelID string) (id, token string, err error)
func (s *Store) PutPersonaWebhook(ctx context.Context, channelID, id, token string) error
func (s *Store) DeletePersonaWebhook(ctx context.Context, webhookID string) error
```
`threadParentID` is the parent channel when the message's channel is a thread (`""` otherwise), so channel-scoped searches include thread replies. `GetPersona`/`GetPersonaWebhook` return errors wrapping `index.ErrNotFound`.

### Search semantics
- `Query` is passed to `MATCH` as FTS5 syntax (`AND`/`OR`/`NOT`, `"phrase"`, `prefix*`). On an FTS5 syntax error, retry once with the query quoted as a phrase and add a warning to the result.
- Filters (`channel_id`, matching `thread_parent_id` too, `author_id`, `before`/`after` as Snowflake or ISO-8601, `has_attachments`) are SQL `WHERE` clauses joined on `rowid`. Exclude `deleted = 1`.
- Order by `bm25(messages_fts)`, then `created_at DESC`. Use `snippet(messages_fts, 0, '**', '**', '…', 12)`.

### Gateway ingestion (in `client.go`)
- Intents: `Guilds | GuildMessages | GuildMembers | MessageContent | GuildMessageReactions | GuildScheduledEvents | GuildModeration`. Document that `MessageContent` and `GuildMembers` are privileged intents that must be enabled in the Developer Portal.
- `MessageCreate`/`MessageUpdate` call `UpsertMessage`. `MessageDelete`/`MessageDeleteBulk` call `MarkDeleted`.
- Indexing runs on a buffered channel worker (capacity 1024). If the buffer is full, drop and count at `warn`. Never block the Gateway goroutine.
- The guild/channel/role/member cache in `resolver.go` updates from the same events (`GuildCreate`, `ChannelCreate/Update/Delete`, `GuildRoleCreate/Update/Delete`, `GuildMemberAdd/Update/Remove`).

## 9. MCP Server Bootstrap (`internal/mcp/`)
- `internal/mcp/deps/deps.go`: `type Deps struct { Discord discord.Service; Store *index.Store; Guard *policy.Guard; Config *config.Config; Logger *slog.Logger }`. It is a leaf package, which avoids a `tools` → `mcp` import cycle.
- `internal/mcp/server.go`: `New(d deps.Deps, version string) (*server.MCPServer, *Notifier)` (with `NewServer` as a wrapper that returns only the server) calls every hook in order:
  `tools.Register{Guild,Channel,Message,Search,ForumThread,ReactionPoll,MemberRole,WebhookPersona,Event,Interaction}Tools`, `resources.Register`, `prompts.Register`. Each hook has the signature `func(*server.MCPServer, deps.Deps)` and lives in the file it registers.
- `internal/mcp/subscriptions.go`: `Notifier` tracks `resources/subscribe` per session through mcp-go hooks, because mcp-go itself does not filter which clients receive updates. `main.go` connects it with `client.OnEvent(notifier.HandleEvent)`. Sends are debounced to 1 per URI per 2 s and go only to subscribed sessions.
- `internal/mcp/tools/helpers.go` and `helpers_test.go` are **Lead-owned shared helpers** in package `tools`. They provide `jsonResult`, `listResult`, `errorResult`, `errorf`, `hasArg`/`optString`/`optBool`/`dryRun`/`objectArg`/`cursorArg`/`clamp`, `resolveGuild`/`resolveGuildRef`/`resolveChannel`/`resolveUser`/`resolveRole`, `channelOp`/`guildOp` with `opRead`/`opWrite`/`opDestructive`, `dryRunResult`, `parsePermissions`/`permissionNames`/`parseColor`/`containsFold`, and the test harness `newTestDeps`/`newTestServer`/`callTool`/`resultText`/`decodeResult`. Wave 2 teammates reuse these and never redefine them. File-private helpers take a file-specific prefix to avoid collisions in the shared package.

## 10. Testing Requirements
- Every package with logic has `_test.go` files. Tool tests drive handlers through `MockClient` and a `:memory:` `index.Store`. No test touches the network.
- Table-driven tests for: resolver inputs (`#general`, `general`, `<#id>`, raw ID, ambiguous), every policy rule, FTS5 boolean/phrase/prefix queries, coverage tracking, and dry-run previews (assert zero mutating calls in `MockClient.Calls`).
- `AwaitInteraction` tests use a fake clock or a short timeout (≤ 100 ms).
- Run `go test -race ./...` as described in spec 04 §2.
