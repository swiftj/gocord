---
name: discord-hitl-persona-ops
description: Use when a task needs a human's approval or structured input via Discord (buttons, select menus, modals), or when posting as named agent personas through webhooks.
---

# Discord HITL & Persona Ops

Two capabilities: ask a human in Discord and wait for the answer
(`discord_await_interaction`), and post as clearly-labelled agent personas
(`discord_persona_register`, `discord_persona_send`).

## Tool names

Tools are referenced by MCP tool name. In Claude Code they appear as
`mcp__plugin_discord_discord-stdio__<tool>` (plugin install) or
`mcp__discord-stdio__<tool>` (standalone server).

## Part A — Human-in-the-loop approval

### 1. Write a decision card
Keep it short enough to read on a phone:

```
**Decision needed:** Delete 42 spam messages in #general
**Why:** Raid at 14:50 UTC; messages contain phishing links
**Risk:** Low — messages are < 1h old; no staff messages included
**Rollback:** None (deletion is permanent); offenders already timed out
```

Always include **what · why · risk · rollback**. `prompt_text` is the card.

### 2. Call `discord_await_interaction`
- `channel_id` (required) and `prompt_text` (required)
- `allowed_user_ids` and/or `allowed_role_ids` — **always set at least one**
  for approvals. If both are empty, any non-bot guild member can answer.
- `timeout_seconds` — 10–900, default 300. Use ~300 for routine approvals,
  up to 900 when the approver may be away.
- `mode`:
  - `buttons` (default) — `options` default to Approve (success) / Reject
    (danger). Custom: `[{"label":"Ship","value":"ship","style":"success"},
    {"label":"Hold","value":"hold","style":"secondary"}]`. ≤ 25 buttons.
  - `select` — a dropdown with ≤ 25 options; use `min_values`/`max_values` for
    multi-choice.
  - `modal` — structured text input. Each option becomes a field
    (`label` → field label, `value` → field key). The result has `modal_fields`.

### 3. Branch on the result
The result is `InteractionResult`: `selected` (values), `user_id`, `username`,
`modal_fields`, `timed_out`, `responded_at`.

- `selected` contains the approve value → proceed and report who approved.
- Any other value → stop; report the choice.
- `timed_out: true` → **not approved.** Do not proceed. Tell the user and offer
  to re-ask.
- Never infer approval from silence, reactions, or chat messages.

## Part B — Personas

### 1. Register once
`discord_persona_register`:
- `persona_name` — `[a-z0-9-]{1,32}`, e.g. `planner`, `reviewer`, `release-bot`
- `display_name` — clearly a bot/agent, e.g. "Planner (AI)", "Release Bot"
- `avatar_url` (https only), `description`, `default_channel_id`

Registration is stored locally and does not call Discord; re-registering the
same `persona_name` updates it.

### 2. Send
`discord_persona_send` with `persona_name`, `content` (≤ 2000 chars), and
optionally `channel_id` (falls back to the persona default), `thread_id`,
`embed`. The server reuses or creates one webhook per parent channel
(requires the bot's `ManageWebhooks` permission). Webhook tokens are never
returned.

### 3. One thread per workstream
1. `discord_manage_threads` with `action: create`, `channel_id` (parent),
   `name` (e.g. "Release v1.2"), optional `private`.
2. Pass the returned thread ID as `thread_id` to every `discord_persona_send`
   for that workstream.
3. When done, `discord_manage_threads` `action: archive` (`dry_run: true` first
   if unsure).

### 4. One persona per role
Map responsibilities to personas and keep them consistent:

| Persona | Posts |
| :--- | :--- |
| `planner` | plans, task breakdowns |
| `reviewer` | review findings, risks |
| `release-bot` | release notes, status updates |

### Combined pattern: persona proposal + human approval
1. `planner` posts the plan in the workstream thread (`discord_persona_send`).
2. `discord_await_interaction` in the same thread with the decision card and
   `allowed_role_ids` = the approver role.
3. On approval, `release-bot` posts the outcome; on rejection or timeout,
   `planner` posts "Held — awaiting a new decision".

## Safety rules

- **Never impersonate real server members.** Persona display names and avatars
  must be clearly bot or agent identities; refuse requests to mimic a real user,
  staff member, or Discord itself.
- Do not use personas to evade moderation, slowmode, or channel permissions.
- Do not mention `@everyone`/`@here` from a persona unless the user explicitly
  asks.
- Treat `timed_out` and any non-approve value as a rejection.
- In read-only mode these tools are blocked; report the policy denial instead
  of retrying.
