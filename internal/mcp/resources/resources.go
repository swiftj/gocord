// Package resources implements discord:// MCP resources (spec 02 §4).
package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

// URI templates and MIME types.
const (
	GuildsURI          = "discord://guilds"
	GuildMapTemplate   = "discord://guilds/{guild_id}/map"
	TranscriptTemplate = "discord://channels/{channel_id}/transcript{?limit,before}"

	DefaultTranscriptLimit = 100
	MaxTranscriptLimit     = 500
	mapTopicMax            = 80
)

// Register registers discord:// resources and resource templates.
func Register(s *server.MCPServer, d deps.Deps) {
	s.AddResource(mcp.NewResource(GuildsURI, "Discord guilds",
		mcp.WithResourceDescription("JSON list of guilds the bot belongs to (allow/block lists applied)."),
		mcp.WithMIMEType("application/json"),
	), func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return ReadGuilds(ctx, d, req.Params.URI)
	})

	s.AddResourceTemplate(mcp.NewResourceTemplate(GuildMapTemplate, "Discord guild map",
		mcp.WithTemplateDescription("Markdown hierarchy of a guild's roles, categories, channels, and active threads with Snowflakes."),
		mcp.WithTemplateMIMEType("text/markdown"),
	), func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return ReadGuildMap(ctx, d, req.Params.URI, argString(req.Params.Arguments, "guild_id"))
	})

	s.AddResourceTemplate(mcp.NewResourceTemplate(TranscriptTemplate, "Discord channel transcript",
		mcp.WithTemplateDescription("Chronological plain-text transcript of a channel or thread (default 100 messages, ?limit= up to 500, ?before= Snowflake or ISO-8601) with mentions resolved."),
		mcp.WithTemplateMIMEType("text/plain"),
	), func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		return ReadTranscript(ctx, d, req.Params.URI, req.Params.Arguments)
	})
}

// argString extracts a template variable (the uritemplate library may yield a string or []string).
func argString(args map[string]any, key string) string {
	switch v := args[key].(type) {
	case string:
		return v
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	case []any:
		if len(v) > 0 {
			if s, ok := v[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

// ---------- discord://guilds ----------

// ReadGuilds returns the ListGuilds output as JSON.
func ReadGuilds(ctx context.Context, d deps.Deps, uri string) ([]mcp.ResourceContents, error) {
	gs, err := d.Discord.ListGuilds(ctx)
	if err != nil {
		return nil, fmt.Errorf("list guilds: %w", err)
	}
	out := make([]dto.GuildSummary, 0, len(gs))
	for _, g := range gs {
		if d.Guard.GuildAllowed(g.ID) {
			out = append(out, g)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal guilds: %w", err)
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{URI: uri, MIMEType: "application/json", Text: string(b)}}, nil
}

// ---------- discord://guilds/{guild_id}/map ----------

// ReadGuildMap renders the guild map resource.
func ReadGuildMap(ctx context.Context, d deps.Deps, uri, guildRef string) ([]mcp.ResourceContents, error) {
	if guildRef == "" {
		return nil, fmt.Errorf("guild_id is required in %s", uri)
	}
	gid, err := d.Discord.ResolveGuildID(ctx, guildRef)
	if err != nil {
		return nil, err
	}
	if err := d.Guard.Check(policyRead("resource:guild_map", gid, "")); err != nil {
		return nil, err
	}
	text, err := RenderGuildMap(ctx, d, gid)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{URI: uri, MIMEType: "text/markdown", Text: text}}, nil
}

// RenderGuildMap builds the compact Markdown hierarchy for a guild.
func RenderGuildMap(ctx context.Context, d deps.Deps, guildID string) (string, error) {
	g, err := d.Discord.GetGuildInfo(ctx, guildID)
	if err != nil {
		return "", fmt.Errorf("guild info: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s) · %s members · tier %d\n", g.Name, g.ID, thousands(g.MemberCount), g.BoostTier)

	// Roles, highest first, @everyone omitted.
	roles := slices.Clone(g.Roles)
	slices.SortStableFunc(roles, func(a, b dto.RoleInfo) int { return b.Position - a.Position })
	b.WriteString("## Roles\n")
	for _, r := range roles {
		if r.ID == g.ID || r.Name == "@everyone" {
			continue
		}
		fmt.Fprintf(&b, "- @%s (%s) pos %d", r.Name, r.ID, r.Position)
		var tags []string
		if len(r.PermissionNames) > 0 {
			tags = append(tags, strings.Join(r.PermissionNames, ", "))
		}
		if r.Managed {
			tags = append(tags, "managed")
		}
		if len(tags) > 0 {
			b.WriteString(" · " + strings.Join(tags, " · "))
		}
		b.WriteString("\n")
	}

	// Channels grouped by category.
	var cats, loose []dto.ChannelInfo
	children := map[string][]dto.ChannelInfo{}
	catIDs := map[string]bool{}
	for _, c := range g.Channels {
		if c.Type == dto.ChannelCategory {
			catIDs[c.ID] = true
		}
	}
	for _, c := range g.Channels {
		if c.Type == dto.ChannelThread || !d.Guard.ChannelAllowed(c.ID) {
			continue
		}
		switch {
		case c.Type == dto.ChannelCategory:
			cats = append(cats, c)
		case c.ParentID != "" && catIDs[c.ParentID]:
			children[c.ParentID] = append(children[c.ParentID], c)
		default:
			loose = append(loose, c)
		}
	}
	byPos := func(a, b dto.ChannelInfo) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return cmpSnowflake(a.ID, b.ID)
	}
	slices.SortFunc(cats, byPos)
	slices.SortFunc(loose, byPos)

	b.WriteString("## Channels\n")
	for _, cat := range cats {
		fmt.Fprintf(&b, "- 📁 %s (%s)\n", cat.Name, cat.ID)
		kids := children[cat.ID]
		slices.SortFunc(kids, byPos)
		for _, c := range kids {
			writeMapChannel(ctx, d, &b, c, "  ")
		}
	}
	for _, c := range loose {
		writeMapChannel(ctx, d, &b, c, "")
	}
	return b.String(), nil
}

func writeMapChannel(ctx context.Context, d deps.Deps, b *strings.Builder, c dto.ChannelInfo, indent string) {
	var threads []dto.ThreadInfo
	if c.Type == dto.ChannelText || c.Type == dto.ChannelAnnouncement || c.Type == dto.ChannelForum {
		if ts, err := d.Discord.ListForumThreads(ctx, c.ID, false); err == nil {
			for _, t := range ts {
				if !t.Archived {
					threads = append(threads, t)
				}
			}
		} else {
			d.Log().Debug("guild map: list threads failed", "channel_id", c.ID, "error", err)
		}
	}
	switch c.Type {
	case dto.ChannelVoice:
		fmt.Fprintf(b, "%s- 🔊 %s (%s) voice", indent, c.Name, c.ID)
	case dto.ChannelStage:
		fmt.Fprintf(b, "%s- 🎙 %s (%s) stage", indent, c.Name, c.ID)
	case dto.ChannelForum:
		fmt.Fprintf(b, "%s- 🗂 #%s (%s) forum", indent, c.Name, c.ID)
		var tags []string
		for _, t := range threads {
			for _, tag := range t.AppliedTags {
				if !slices.Contains(tags, tag) {
					tags = append(tags, tag)
				}
			}
		}
		if len(tags) > 0 {
			slices.Sort(tags)
			fmt.Fprintf(b, " · tags: %s", strings.Join(tags, ", "))
		}
	default:
		fmt.Fprintf(b, "%s- #%s (%s) %s", indent, c.Name, c.ID, c.Type)
	}
	if c.Topic != "" {
		fmt.Fprintf(b, " · %q", truncateRunes(oneLine(c.Topic), mapTopicMax))
	}
	if c.NSFW {
		b.WriteString(" · nsfw")
	}
	b.WriteString("\n")
	for _, t := range threads {
		state := "active"
		if t.Locked {
			state += ", locked"
		}
		if t.Private {
			state += ", private"
		}
		fmt.Fprintf(b, "%s  - 🧵 %s (%s) %s\n", indent, t.Name, t.ID, state)
	}
}

// ---------- discord://channels/{channel_id}/transcript ----------

// ReadTranscript renders the transcript resource. args holds template variables;
// query parameters on uri are honoured as a fallback.
func ReadTranscript(ctx context.Context, d deps.Deps, uri string, args map[string]any) ([]mcp.ResourceContents, error) {
	chRef := argString(args, "channel_id")
	limitStr := argString(args, "limit")
	beforeStr := argString(args, "before")
	if u, err := url.Parse(uri); err == nil {
		q := u.Query()
		if limitStr == "" {
			limitStr = q.Get("limit")
		}
		if beforeStr == "" {
			beforeStr = q.Get("before")
		}
		if chRef == "" {
			parts := strings.Split(strings.Trim(u.Host+u.Path, "/"), "/")
			if len(parts) == 3 && parts[0] == "channels" {
				chRef = parts[1]
			}
		}
	}
	if chRef == "" {
		return nil, fmt.Errorf("channel_id is required in %s", uri)
	}
	limit := DefaultTranscriptLimit
	if limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("limit must be a positive integer, got %q", limitStr)
		}
		limit = min(n, MaxTranscriptLimit)
	}
	before, err := dto.NormalizeCursor(beforeStr)
	if err != nil {
		return nil, fmt.Errorf("before: %w", err)
	}

	chID, err := d.Discord.ResolveChannelID(ctx, "", chRef)
	if err != nil {
		return nil, err
	}
	ch, err := d.Discord.GetChannel(ctx, chID)
	if err != nil {
		return nil, err
	}
	parent := ""
	if ch.Type == dto.ChannelThread {
		parent = ch.ParentID
	}
	if err := d.Guard.Check(policyRead("resource:transcript", ch.GuildID, ch.ID, parent)); err != nil {
		return nil, err
	}
	text, err := RenderTranscript(ctx, d, ch, limit, before)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{mcp.TextResourceContents{URI: uri, MIMEType: "text/plain", Text: text}}, nil
}

// RenderTranscript fetches up to limit messages before the cursor and renders them oldest first.
func RenderTranscript(ctx context.Context, d deps.Deps, ch *dto.ChannelInfo, limit int, before string) (string, error) {
	var msgs []dto.MessageItem // newest first
	cursor := before
	for len(msgs) < limit {
		want := min(100, limit-len(msgs))
		page, err := d.Discord.ReadMessages(ctx, dto.ReadMessagesReq{ChannelID: ch.ID, Limit: want, Before: cursor})
		if err != nil {
			return "", fmt.Errorf("read messages: %w", err)
		}
		if len(page) == 0 {
			break
		}
		msgs = append(msgs, page...)
		oldest := page[0].ID
		for _, m := range page {
			if cmpSnowflake(m.ID, oldest) < 0 {
				oldest = m.ID
			}
		}
		if oldest == cursor || len(page) < want {
			break
		}
		cursor = oldest
	}
	slices.SortFunc(msgs, func(a, b dto.MessageItem) int { return cmpSnowflake(a.ID, b.ID) })
	msgs = slices.CompactFunc(msgs, func(a, b dto.MessageItem) bool { return a.ID == b.ID })

	if d.Store != nil && len(msgs) > 0 {
		parent := ""
		if ch.Type == dto.ChannelThread {
			parent = ch.ParentID
		}
		if err := d.Store.UpsertMessages(ctx, ch.GuildID, parent, msgs); err != nil {
			d.Log().Warn("transcript: index upsert failed", "channel_id", ch.ID, "error", err)
		}
	}

	authors := make(map[string]string, len(msgs))
	for _, m := range msgs {
		authors[m.ID] = m.Author
	}
	res := NewMentionResolver(d.Discord, ch.GuildID)
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(FormatTranscriptLine(ctx, m, authors, res))
		b.WriteString("\n")
	}
	return b.String(), nil
}

// FormatTranscriptLine renders one message as "[YYYY-MM-DD HH:MM] @author: content".
func FormatTranscriptLine(ctx context.Context, m dto.MessageItem, authors map[string]string, res *MentionResolver) string {
	ts := m.Timestamp
	if t, err := time.Parse(time.RFC3339, m.Timestamp); err == nil {
		ts = t.UTC().Format("2006-01-02 15:04")
	} else if t, err := dto.SnowflakeTime(m.ID); err == nil {
		ts = t.UTC().Format("2006-01-02 15:04")
	}
	author := m.Author
	if author == "" {
		author = m.AuthorID
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] @%s", ts, author)
	if m.ReplyToID != "" {
		if who, ok := authors[m.ReplyToID]; ok && who != "" {
			fmt.Fprintf(&b, " ↪ reply to %s", who)
		} else {
			fmt.Fprintf(&b, " ↪ reply to %s", m.ReplyToID)
		}
	}
	content := m.Content
	if res != nil {
		content = res.Resolve(ctx, content)
	}
	content = strings.ReplaceAll(strings.TrimRight(content, "\n"), "\n", "\n    ")
	b.WriteString(": " + content)
	var extras []string
	if n := len(m.Attachments); n == 1 {
		extras = append(extras, "[1 attachment]")
	} else if n > 1 {
		extras = append(extras, fmt.Sprintf("[%d attachments]", n))
	}
	for _, e := range m.Embeds {
		if e.Title != "" {
			extras = append(extras, fmt.Sprintf("[embed: %s]", truncateRunes(oneLine(e.Title), mapTopicMax)))
		} else {
			extras = append(extras, "[embed]")
		}
	}
	if len(m.Reactions) > 0 {
		var rs []string
		for _, r := range m.Reactions {
			rs = append(rs, fmt.Sprintf("%s×%d", r.Emoji, r.Count))
		}
		extras = append(extras, "["+strings.Join(rs, " ")+"]")
	}
	if len(extras) > 0 {
		b.WriteString("  " + strings.Join(extras, " "))
	}
	return b.String()
}

// ---------- Mention resolution ----------

var mentionRe = regexp.MustCompile(`<(@!?|@&|#)(\d{15,20})>`)

// MentionResolver rewrites <@id>, <@!id>, <@&id>, and <#id> to @name, @role, and #channel.
// Lookups are lazy (no Discord calls unless a mention is present) and best-effort:
// unknown IDs are left unchanged. Safe for concurrent use.
type MentionResolver struct {
	svc     discord.Service
	guildID string

	mu       sync.Mutex
	loaded   bool
	users    map[string]string
	roles    map[string]string
	channels map[string]string
}

// NewMentionResolver returns a resolver scoped to guildID ("" resolves channels only via GetChannel).
func NewMentionResolver(svc discord.Service, guildID string) *MentionResolver {
	return &MentionResolver{svc: svc, guildID: guildID}
}

func (r *MentionResolver) load(ctx context.Context) {
	if r.loaded {
		return
	}
	r.loaded = true
	r.users, r.roles, r.channels = map[string]string{}, map[string]string{}, map[string]string{}
	if r.guildID == "" {
		return
	}
	if ms, err := r.svc.ListMembers(ctx, r.guildID, 1000, ""); err == nil {
		for _, m := range ms {
			name := m.Username
			if m.Nick != "" {
				name = m.Nick
			}
			r.users[m.UserID] = name
		}
	}
	if rs, err := r.svc.ListRoles(ctx, r.guildID); err == nil {
		for _, role := range rs {
			r.roles[role.ID] = strings.TrimPrefix(role.Name, "@")
		}
	}
	if cs, err := r.svc.ListChannels(ctx, r.guildID); err == nil {
		for _, c := range cs {
			r.channels[c.ID] = c.Name
		}
	}
}

// Resolve rewrites mentions in s.
func (r *MentionResolver) Resolve(ctx context.Context, s string) string {
	if r == nil || !strings.Contains(s, "<") {
		return s
	}
	return mentionRe.ReplaceAllStringFunc(s, func(tok string) string {
		sm := mentionRe.FindStringSubmatch(tok)
		kind, id := sm[1], sm[2]
		r.mu.Lock()
		defer r.mu.Unlock()
		r.load(ctx)
		switch kind {
		case "@", "@!":
			if n, ok := r.users[id]; ok {
				return "@" + n
			}
		case "@&":
			if n, ok := r.roles[id]; ok {
				return "@" + n
			}
		case "#":
			n, ok := r.channels[id]
			if !ok {
				if c, err := r.svc.GetChannel(ctx, id); err == nil {
					n = c.Name
				}
				r.channels[id] = n
			}
			if n != "" {
				return "#" + n
			}
		}
		return tok
	})
}

// ---------- utils ----------

// policyRead builds a read-only policy op (allow/block lists still apply).
func policyRead(tool, guildID, channelID string, parent ...string) policy.Op {
	op := policy.Op{Tool: tool, GuildID: guildID, ChannelID: channelID}
	if len(parent) > 0 {
		op.ParentChannelID = parent[0]
	}
	return op
}

func cmpSnowflake(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

func thousands(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return s
	}
	var out []byte
	for i := range len(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
