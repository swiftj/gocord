---
name: discord-knowledge-synthesizer
description: Use when summarising, searching, or extracting knowledge (digests, FAQs, decision logs) from Discord channels, threads, or forums. Combines FTS5 search with backfill, transcripts, and forum posts.
---

# Discord Knowledge Synthesizer

Turn Discord conversations into digests, FAQs, and decision logs with accurate
citations. This skill is read-only: it never posts unless the user asks.

## Tool names

Tools are referenced by MCP tool name (e.g. `discord_search_messages`). In
Claude Code they appear as `mcp__plugin_discord_discord-stdio__<tool>` (plugin
install) or `mcp__discord-stdio__<tool>` (standalone server).

## Procedure

### 1. Choose the channels
Read `discord://guilds/{guild_id}/map` to find the relevant channels, forums,
and active threads by name and ID. Resolve user requests like "#help" or
"the release thread" against the map. If the guild is unknown, call
`discord_list_servers`.

### 2. Search with FTS5 (and backfill)
Call `discord_search_messages` with:

- `guild_id` (required), `query` (required)
- `channel_id` to narrow to one channel or thread
- `author_id`, `before`, `after` (Snowflake or ISO-8601), `has_attachments`
- `limit` (1–100, default 25)
- `auto_backfill_limit` — budget of messages to fetch via REST for channels not
  yet fully indexed. Size it to the window: **≈ 500 per active channel per
  week** (e.g. 3 channels × 2 weeks ≈ 3000; max 5000). Use `0` for an
  instant search over what is already indexed.

FTS5 query syntax:

| Syntax | Meaning |
| :--- | :--- |
| `deploy rollback` | both terms (implicit AND) |
| `"exact phrase"` | phrase match |
| `deploy OR release` | either term |
| `migration NOT postgres` | exclude a term |
| `deprec*` | prefix match |
| `(bug OR crash) AND login` | grouping |

Search tips: run 2–4 focused queries rather than one broad one; search
synonyms (`decided OR agreed OR "going with"`) for decision logs; use
`has_attachments: true` to find screenshots, logs, and specs.

### 3. Read for chronology
For ordered context, read the resource
`discord://channels/{channel_id}/transcript` (last 100 messages; append
`?limit=` up to 500 and `?before=` to page back). Lines look like
`[2026-11-01 14:55] @alice: …` with mentions already resolved.

For a specific window, `discord_read_messages` with `limit` (1–100) and one of
`before` / `after` / `around`.

### 4. Forums
List posts with `discord_list_forum_threads` (`forum_channel_id`,
`include_archived: true` for history), then read each relevant post with
`discord_get_forum_post` (`thread_id`, `limit`). Applied tags indicate status
(e.g. `resolved`, `bug`).

### 5. Synthesise
Pick the template that matches the request. Cite every claim with
`[YYYY-MM-DD HH:MM] #channel`.

## Output templates

### Daily digest
```markdown
# #channel digest — 2026-11-01 (last 24h)
## Summary
2–4 sentences on what happened.
## Decisions
- Ship v1.2 on Tuesday — @alice [2026-11-01 14:55] #release
## Open Questions
- Who owns the migration rollback? [2026-11-01 16:02] #dev
## Action Items
- @bob → update the release notes [2026-11-01 15:10] #release
## Notable Links
- Design doc (shared by @carol) [2026-11-01 11:20] #dev
```

### FAQ
```markdown
## Q: How do I reset my API key?
A: Settings → API → Regenerate. Old keys stop working immediately.
Sources: [2026-10-28 09:14] #help · [2026-10-30 17:41] #help-forum
```
Merge duplicate questions; prefer answers from staff or marked as resolved.

### Decision log
| Decision | Who | When | Link |
| :--- | :--- | :--- | :--- |
| Adopt FTS5 for search | @alice | 2026-10-12 14:03 | #architecture (message 100000000000000123) |

## Caveats

- **Partial index:** if the search result has a `warning` like
  `partial index: N channels not fully backfilled`, tell the user the results
  may be incomplete and offer to re-run with a larger `auto_backfill_limit` or
  a narrower `channel_id`.
- **Respect limits:** never request more than the user's window needs; prefer
  narrowing by channel and date over huge backfills.
- **Quote sparingly:** paraphrase; quote at most one short sentence per source.
  Never reproduce whole conversations.
- **Privacy:** do not surface content from channels the user did not ask about,
  and do not include personal data beyond usernames.
- **Posting back:** only post a digest to Discord (`discord_send` or
  `discord_persona_send`) after the user explicitly asks; content must be
  ≤ 2000 characters per message, so split long digests.
