---
paths: ["internal/mcp/**"]
---

# MCP Tool Rules

Full schemas: `specs/02-mcp-surface.md`. Tool names and parameter names there are a public contract. Do not rename them.

- **Resolve references first:** every guild, channel, user, or role parameter accepts a Snowflake, `#name`, `@name`, a mention (`<#id>`, `<@id>`, `<@&id>`), or a bare name. Resolve through `Service.Resolve*ID` before any Discord call. Ambiguous or unknown names return `mcp.NewToolResultError` listing the candidate IDs.
- **Policy before mutation:** call `policy.Guard.Check` before any write. Every destructive tool (delete, bulk delete, edit/delete channel, permissions, role mutations, webhook edit/delete, timeouts, thread archive/lock) accepts `dry_run`. A dry run returns `dto.DryRunPreview` and makes **zero** mutating calls. Test this by asserting on `MockClient.Calls`.
- **Token-compacted outputs:** return DTOs from `internal/dto` as compact JSON (no indentation). Use `omitempty` everywhere, string Snowflakes, and RFC 3339 times. Never return raw `discordgo` structs. Truncate long content per spec 01 §5. Never return webhook tokens or bot tokens.
- **Errors:** return user-correctable failures (validation, not found, forbidden, policy denied, rate-limited) as `mcp.NewToolResultError` with an actionable message. Return Go errors only for internal faults.
- **Schemas:** mark required params with `mcp.Required()`, use `mcp.Enum` for `action`/`type`, add min/max bounds, and set `ReadOnlyHint`/`DestructiveHint` annotations. Keep descriptions to 1–2 sentences.
- **Ownership:** edit only the files your Wave 2 role owns (see `CLAUDE.md` §5). `server.go` belongs to the Lead Agent.
