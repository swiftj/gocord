// Package prompts implements built-in MCP prompts (spec 02 §5). Prompts only
// read data (to embed context); they never mutate Discord or the index.
package prompts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

// Prompt names.
const (
	ChannelDigest   = "discord-channel-digest"
	ServerBlueprint = "discord-server-blueprint"
	HITLApproval    = "discord-hitl-approval"
)

const (
	digestMaxMessages = 500
	digestPageSize    = 100
	digestMaxLineLen  = 500
)

// now is the clock used to compute relative windows (tests override it).
var now = time.Now

// Register registers the discord-* prompts.
func Register(s *server.MCPServer, d deps.Deps) {
	s.AddPrompt(mcp.NewPrompt(ChannelDigest,
		mcp.WithPromptDescription("Summarise a channel or forum thread: summary, decisions, open questions, action items, and links."),
		mcp.WithArgument("channel_id", mcp.RequiredArgument(), mcp.ArgumentDescription("Channel or thread (Snowflake, #name, or mention).")),
		mcp.WithArgument("since", mcp.ArgumentDescription("Window start: ISO-8601 time or a duration like 24h / 7d (default 24h).")),
		mcp.WithArgument("audience", mcp.ArgumentDescription("Who the digest is for (e.g. executives, new contributors).")),
	), func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return channelDigest(ctx, d, req.Params.Arguments)
	})

	s.AddPrompt(mcp.NewPrompt(ServerBlueprint,
		mcp.WithPromptDescription("Draft a dry-run provisioning plan (categories, channels, roles, permission overwrites) for a guild goal."),
		mcp.WithArgument("guild_id", mcp.RequiredArgument(), mcp.ArgumentDescription("Guild Snowflake or name.")),
		mcp.WithArgument("goal", mcp.RequiredArgument(), mcp.ArgumentDescription("What the server is for, e.g. \"open-source project with support forum\".")),
	), func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return serverBlueprint(ctx, d, req.Params.Arguments)
	})

	s.AddPrompt(mcp.NewPrompt(HITLApproval,
		mcp.WithPromptDescription("Post a decision card and wait for explicit human approval via discord_await_interaction before proceeding."),
		mcp.WithArgument("channel_id", mcp.RequiredArgument(), mcp.ArgumentDescription("Channel where the approval card is posted.")),
		mcp.WithArgument("decision", mcp.RequiredArgument(), mcp.ArgumentDescription("The action awaiting approval.")),
		mcp.WithArgument("context", mcp.ArgumentDescription("Background: why, risk, rollback.")),
		mcp.WithArgument("options", mcp.ArgumentDescription("Comma-separated choices (default Approve,Reject).")),
		mcp.WithArgument("approver", mcp.ArgumentDescription("@user or @role allowed to decide.")),
		mcp.WithArgument("timeout_seconds", mcp.ArgumentDescription("How long to wait (10-900, default 300).")),
	), func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return hitlApproval(ctx, d, req.Params.Arguments)
	})
}

func required(args map[string]string, key string) (string, error) {
	v := strings.TrimSpace(args[key])
	if v == "" {
		return "", fmt.Errorf("argument %q is required", key)
	}
	return v, nil
}

func scope(d deps.Deps) string {
	if d.Config != nil {
		return d.Config.DefaultGuildID
	}
	return ""
}

func userText(text string) mcp.PromptMessage {
	return mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(text))
}

func embedded(uri, mime, text string) mcp.PromptMessage {
	return mcp.NewPromptMessage(mcp.RoleUser, mcp.NewEmbeddedResource(mcp.TextResourceContents{URI: uri, MIMEType: mime, Text: text}))
}

// resolveChannel resolves a channel reference and applies read policy.
func resolveChannel(ctx context.Context, d deps.Deps, tool, ref string) (*dto.ChannelInfo, error) {
	id, err := d.Discord.ResolveChannelID(ctx, scope(d), ref)
	if err != nil {
		return nil, err
	}
	ch, err := d.Discord.GetChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	op := policy.Op{Tool: tool, GuildID: ch.GuildID, ChannelID: ch.ID}
	if ch.Type == dto.ChannelThread {
		op.ParentChannelID = ch.ParentID
	}
	if err := d.Guard.Check(op); err != nil {
		return nil, err
	}
	return ch, nil
}

// ---------- discord-channel-digest ----------

// parseSince accepts ISO-8601 or a relative duration ("24h", "7d", "90m", "2w").
func parseSince(s string, ref time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = "24h"
	}
	if n := len(s); n >= 2 {
		unit := s[n-1]
		if v, err := strconv.Atoi(s[:n-1]); err == nil && v > 0 {
			switch unit {
			case 'm':
				return ref.Add(-time.Duration(v) * time.Minute), nil
			case 'h':
				return ref.Add(-time.Duration(v) * time.Hour), nil
			case 'd':
				return ref.AddDate(0, 0, -v), nil
			case 'w':
				return ref.AddDate(0, 0, -7*v), nil
			}
		}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("since %q: want ISO-8601 or a duration like 24h / 7d", s)
}

// fetchWindow reads messages newer than since (newest pages first, capped) and
// returns them oldest first, plus whether the cap truncated the window.
func fetchWindow(ctx context.Context, d deps.Deps, channelID string, since time.Time) ([]dto.MessageItem, bool, error) {
	var all []dto.MessageItem
	before := ""
	for len(all) < digestMaxMessages {
		page, err := d.Discord.ReadMessages(ctx, dto.ReadMessagesReq{ChannelID: channelID, Limit: digestPageSize, Before: before})
		if err != nil {
			return nil, false, err
		}
		if len(page) == 0 {
			break
		}
		reachedStart := false
		for _, m := range page {
			if messageTime(m).Before(since) {
				reachedStart = true
				continue
			}
			all = append(all, m)
		}
		before = page[len(page)-1].ID
		for _, m := range page {
			if snowLess(m.ID, before) {
				before = m.ID
			}
		}
		if reachedStart || len(page) < digestPageSize {
			break
		}
	}
	truncated := len(all) >= digestMaxMessages
	if truncated {
		all = all[:digestMaxMessages]
	}
	slices.SortFunc(all, func(a, b dto.MessageItem) int {
		switch {
		case snowLess(a.ID, b.ID):
			return -1
		case snowLess(b.ID, a.ID):
			return 1
		}
		return 0
	})
	return all, truncated, nil
}

func snowLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// messageTime prefers the message timestamp, falling back to the Snowflake.
func messageTime(m dto.MessageItem) time.Time {
	if t, err := time.Parse(time.RFC3339, m.Timestamp); err == nil {
		return t.UTC()
	}
	t, _ := dto.SnowflakeTime(m.ID)
	return t
}

// formatTranscript renders "[YYYY-MM-DD HH:MM] @user: content" lines (UTC).
func formatTranscript(msgs []dto.MessageItem) string {
	byID := make(map[string]string, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m.Author
	}
	var b strings.Builder
	for _, m := range msgs {
		author := m.Author
		if author == "" {
			author = m.AuthorID
		}
		fmt.Fprintf(&b, "[%s] @%s", messageTime(m).Format("2006-01-02 15:04"), author)
		if m.ReplyToID != "" {
			if to := byID[m.ReplyToID]; to != "" {
				fmt.Fprintf(&b, " ↪ reply to %s", to)
			} else {
				b.WriteString(" ↪ reply")
			}
		}
		content := strings.ReplaceAll(strings.TrimSpace(m.Content), "\n", " ⏎ ")
		fmt.Fprintf(&b, ": %s", dto.Truncate(content, digestMaxLineLen))
		if n := len(m.Attachments); n > 0 {
			fmt.Fprintf(&b, "  [%d attachment%s]", n, plural(n))
		}
		for _, e := range m.Embeds {
			if e.Title != "" || e.URL != "" {
				fmt.Fprintf(&b, "  [embed: %s %s]", e.Title, e.URL)
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func channelDigest(ctx context.Context, d deps.Deps, args map[string]string) (*mcp.GetPromptResult, error) {
	ref, err := required(args, "channel_id")
	if err != nil {
		return nil, err
	}
	since, err := parseSince(args["since"], now())
	if err != nil {
		return nil, err
	}
	ch, err := resolveChannel(ctx, d, ChannelDigest, ref)
	if err != nil {
		return nil, err
	}
	uri := fmt.Sprintf("discord://channels/%s/transcript", ch.ID)
	audience := strings.TrimSpace(args["audience"])
	if audience == "" {
		audience = "the channel's members"
	}

	var msgs []dto.MessageItem
	var transcript string
	var truncated bool
	if ch.Type == dto.ChannelForum {
		transcript = "(forum channel: list posts with discord_list_forum_threads and read each with discord_get_forum_post)\n"
	} else if msgs, truncated, err = fetchWindow(ctx, d, ch.ID, since); err != nil {
		if errors.Is(err, discord.ErrNotLoggedIn) {
			return nil, err
		}
		transcript = fmt.Sprintf("(could not read messages: %v — read %s yourself)\n", err, uri)
	} else if len(msgs) == 0 {
		transcript = "(no messages in this window)\n"
	} else {
		transcript = formatTranscript(msgs)
	}
	header := fmt.Sprintf("Transcript of #%s (%s) since %s UTC, %d messages", ch.Name, ch.ID, since.Format("2006-01-02 15:04"), len(msgs))
	if truncated {
		header += fmt.Sprintf(" (capped at the latest %d)", digestMaxMessages)
	}

	instructions := fmt.Sprintf(`You are writing a digest of Discord channel #%[1]s (%[2]s) for %[3]s, covering messages since %[4]s UTC.
The transcript is embedded above (source resource: %[5]s; use discord_read_messages or discord_search_messages if you need more context).

Produce these sections, citing message timestamps like [2026-11-01 14:55] for every claim:
## Summary
3-6 bullet points of what was discussed.
## Decisions
What was agreed, by whom.
## Open Questions
Unresolved questions and who raised them.
## Action Items (owner → task)
One line per item as "@owner → task (due date if stated)". Write "none" if there are none.
## Notable Links
URLs, documents, and referenced channels, with one-line context.

Be concise, do not invent facts that are not in the transcript, and do not post anything to Discord unless asked.`,
		ch.Name, ch.ID, audience, since.Format("2006-01-02 15:04"), uri)

	return mcp.NewGetPromptResult("Digest of #"+ch.Name, []mcp.PromptMessage{
		embedded(uri, "text/plain", header+"\n"+transcript),
		userText(instructions),
	}), nil
}

// ---------- discord-server-blueprint ----------

// guildMap renders a compact Markdown map of the guild (same shape as the
// discord://guilds/{id}/map resource, without thread enumeration).
func guildMap(ctx context.Context, d deps.Deps, guildID string) (string, string, error) {
	info, err := d.Discord.GetGuildInfo(ctx, guildID)
	if err != nil {
		return "", "", err
	}
	channels, err := d.Discord.ListChannels(ctx, guildID)
	if err != nil {
		return "", "", err
	}
	roles, err := d.Discord.ListRoles(ctx, guildID)
	if err != nil {
		return "", "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s) · %d members · tier %d\n## Roles\n", info.Name, info.ID, info.MemberCount, info.BoostTier)
	slices.SortFunc(roles, func(a, b dto.RoleInfo) int { return b.Position - a.Position })
	for _, r := range roles {
		fmt.Fprintf(&b, "- @%s (%s) pos %d", r.Name, r.ID, r.Position)
		if len(r.PermissionNames) > 0 {
			fmt.Fprintf(&b, " · %s", strings.Join(r.PermissionNames, ", "))
		}
		b.WriteByte('\n')
	}
	b.WriteString("## Channels\n")
	slices.SortFunc(channels, func(a, b dto.ChannelInfo) int { return a.Position - b.Position })
	children := map[string][]dto.ChannelInfo{}
	var top []dto.ChannelInfo
	for _, c := range channels {
		if c.ParentID != "" && c.Type != dto.ChannelCategory {
			children[c.ParentID] = append(children[c.ParentID], c)
		} else {
			top = append(top, c)
		}
	}
	line := func(indent string, c dto.ChannelInfo) {
		switch c.Type {
		case dto.ChannelCategory:
			fmt.Fprintf(&b, "%s- 📁 %s (%s)\n", indent, c.Name, c.ID)
		default:
			fmt.Fprintf(&b, "%s- #%s (%s) %s", indent, c.Name, c.ID, c.Type)
			if c.Topic != "" {
				fmt.Fprintf(&b, " · %q", dto.Truncate(c.Topic, 80))
			}
			b.WriteByte('\n')
		}
	}
	for _, c := range top {
		line("", c)
		for _, child := range children[c.ID] {
			line("  ", child)
		}
	}
	return info.Name, b.String(), nil
}

func serverBlueprint(ctx context.Context, d deps.Deps, args map[string]string) (*mcp.GetPromptResult, error) {
	ref, err := required(args, "guild_id")
	if err != nil {
		return nil, err
	}
	goal, err := required(args, "goal")
	if err != nil {
		return nil, err
	}
	guildID, err := d.Discord.ResolveGuildID(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := d.Guard.Check(policy.Op{Tool: ServerBlueprint, GuildID: guildID}); err != nil {
		return nil, err
	}
	uri := fmt.Sprintf("discord://guilds/%s/map", guildID)
	name, mapText, err := guildMap(ctx, d, guildID)
	if err != nil {
		if errors.Is(err, discord.ErrNotLoggedIn) {
			return nil, err
		}
		name, mapText = guildID, fmt.Sprintf("(could not build the map: %v — read %s yourself)\n", err, uri)
	}
	readOnly := ""
	if d.Guard.ReadOnly() {
		readOnly = "\nNOTE: the server runs in read-only mode, so only dry runs are possible. Produce the plan and previews only.\n"
	}

	instructions := fmt.Sprintf(`You are designing the structure of Discord server %[1]q (%[2]s).
Goal: %[3]s
The current layout is embedded above (resource %[4]s). Reuse what exists; do not duplicate channels or roles.
%[5]s
1. Draft a blueprint as a table: categories → channels (type, topic), roles (name, colour, permissions, hoist), and permission overwrites (channel, role/member, allow, deny). Explain each choice in one line.
2. Preview EVERY change with dry_run: true first. Creation tools (discord_create_category, discord_create_text_channel, discord_create_voice_channel, discord_create_forum_channel) have no dry run, so list them as planned steps instead of calling them. Use dry_run: true with discord_manage_roles (create/edit/assign), discord_set_channel_permissions, and discord_edit_channel.
3. Present the combined preview (planned creations + all dry-run results, including any policy denials) and ask for explicit confirmation.
4. Only after the user confirms, apply the changes in order: roles, categories, channels, then permission overwrites. Re-read %[4]s afterwards and report what changed.
Never delete channels or roles as part of this plan unless the user explicitly asks.`,
		name, guildID, goal, uri, readOnly)

	return mcp.NewGetPromptResult("Server blueprint for "+name, []mcp.PromptMessage{
		embedded(uri, "text/markdown", mapText),
		userText(instructions),
	}), nil
}

// ---------- discord-hitl-approval ----------

func hitlApproval(ctx context.Context, d deps.Deps, args map[string]string) (*mcp.GetPromptResult, error) {
	ref, err := required(args, "channel_id")
	if err != nil {
		return nil, err
	}
	decision, err := required(args, "decision")
	if err != nil {
		return nil, err
	}
	timeout := dto.DefaultInteractionTimeout
	if s := strings.TrimSpace(args["timeout_seconds"]); s != "" {
		if timeout, err = strconv.Atoi(s); err != nil || timeout < 10 || timeout > dto.MaxInteractionTimeout {
			return nil, fmt.Errorf("timeout_seconds must be an integer between 10 and %d", dto.MaxInteractionTimeout)
		}
	}
	var labels []string
	for _, o := range strings.Split(args["options"], ",") {
		if o = strings.TrimSpace(o); o != "" {
			labels = append(labels, o)
		}
	}
	if len(labels) == 0 {
		labels = []string{"Approve", "Reject"}
	}
	if len(labels) > 25 {
		return nil, errors.New("at most 25 options are allowed")
	}

	// Resolve the channel best-effort so the tool call uses a Snowflake; fall back to the raw ref.
	channelID, guildID := ref, ""
	if ch, err := resolveChannel(ctx, d, HITLApproval, ref); err == nil {
		channelID, guildID = ch.ID, ch.GuildID
	} else {
		var de *policy.DeniedError
		if errors.As(err, &de) {
			return nil, err
		}
	}

	approverLine := "Leave allowed_user_ids and allowed_role_ids empty (any non-bot member may decide)."
	if approver := strings.TrimSpace(args["approver"]); approver != "" {
		approverLine = approverArgs(ctx, d, guildID, approver)
	}

	opts := make([]string, len(labels))
	for i, l := range labels {
		opts[i] = fmt.Sprintf("{\"label\": %q}", l)
	}
	approveValue := slug(labels[0])
	if approveValue == "" {
		approveValue = "option-1"
	}
	extra := strings.TrimSpace(args["context"])
	if extra == "" {
		extra = "(none provided: infer from the conversation, and say so if unknown)"
	}

	text := fmt.Sprintf(`Before doing the following, obtain explicit human approval in Discord.

Decision: %[1]s
Context: %[2]s

1. Write a short decision card (<= 1500 characters, Markdown) with these lines: **What** (the exact action), **Why**, **Risk** (blast radius, what could go wrong), and **Rollback** (how to undo it).
2. Call discord_await_interaction with:
   - channel_id: %[3]q
   - prompt_text: the decision card
   - mode: "buttons"
   - options: [%[4]s]
   - %[5]s
   - timeout_seconds: %[6]d
3. Proceed ONLY if the result's selected list contains %[7]q (the first option) and timed_out is not true. Report who approved (username) and when.
4. On any other choice, a timeout, or an error: do NOT proceed. Report the outcome and stop. Never re-ask more than once without the user's instruction.`,
		decision, extra, channelID, strings.Join(opts, ", "), approverLine, timeout, approveValue)

	return mcp.NewGetPromptResult("Human approval: "+dto.Truncate(decision, 80), []mcp.PromptMessage{userText(text)}), nil
}

// approverArgs decides whether the approver is a role or a user (best effort).
func approverArgs(ctx context.Context, d deps.Deps, guildID, approver string) string {
	if strings.HasPrefix(approver, "<@&") {
		return fmt.Sprintf("allowed_role_ids: [%q]", approver)
	}
	if guildID != "" {
		r := discord.ParseRef(approver)
		if roles, err := d.Discord.ListRoles(ctx, guildID); err == nil {
			for _, role := range roles {
				if (r.ID != "" && role.ID == r.ID) || (r.ID == "" && discord.NameMatches(r.Name, role.Name)) {
					return fmt.Sprintf("allowed_role_ids: [%q]  (role @%s)", role.ID, role.Name)
				}
			}
		}
		if id, err := d.Discord.ResolveUserID(ctx, guildID, approver); err == nil {
			return fmt.Sprintf("allowed_user_ids: [%q]  (%s)", id, approver)
		}
	}
	return fmt.Sprintf("allowed_user_ids or allowed_role_ids: [%q] (use allowed_role_ids if it names a role)", approver)
}

// slug mirrors the default option value derivation in discord_await_interaction.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ' || r == '\t':
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
