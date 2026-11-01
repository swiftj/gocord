package discord

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/dto"
)

// MaxAttachmentBytes is the largest local file SendMessage will upload.
const MaxAttachmentBytes = 25 << 20

// ---------- resolverREST (REST fallbacks for the resolver cache) ----------

func (c *Client) restGuild(ctx context.Context, guildID string) (*discordgo.Guild, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	g, err := s.Guild(guildID, discordgo.WithContext(ctx))
	return g, mapErr("get guild", err)
}

func (c *Client) restUserGuilds(ctx context.Context) ([]*discordgo.UserGuild, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	var out []*discordgo.UserGuild
	after := ""
	for {
		page, err := s.UserGuilds(200, "", after, true, discordgo.WithContext(ctx))
		if err != nil {
			return nil, mapErr("list guilds", err)
		}
		out = append(out, page...)
		if len(page) < 200 {
			return out, nil
		}
		after = page[len(page)-1].ID
	}
}

func (c *Client) restChannel(ctx context.Context, channelID string) (*discordgo.Channel, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	ch, err := s.Channel(channelID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("get channel", err)
	}
	c.resolver.applyChannel(ch)
	return ch, nil
}

func (c *Client) restGuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	chs, err := s.GuildChannels(guildID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("list channels", err)
	}
	if tl, terr := s.GuildThreadsActive(guildID, discordgo.WithContext(ctx)); terr == nil && tl != nil {
		for _, t := range tl.Threads {
			c.resolver.applyChannel(t)
		}
	}
	return chs, nil
}

func (c *Client) restGuildMember(ctx context.Context, guildID, userID string) (*discordgo.Member, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	m, err := s.GuildMember(guildID, userID, discordgo.WithContext(ctx))
	return m, mapErr("get member", err)
}

func (c *Client) restSearchMembers(ctx context.Context, guildID, query string) ([]*discordgo.Member, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	ms, err := s.GuildMembersSearch(guildID, query, 25, discordgo.WithContext(ctx))
	return ms, mapErr("search members", err)
}

func (c *Client) restUser(ctx context.Context, userID string) (*discordgo.User, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	u, err := s.User(userID, discordgo.WithContext(ctx))
	return u, mapErr("get user", err)
}

// ---------- Session & Resolution ----------

func (c *Client) ResolveGuildID(ctx context.Context, guildRef string) (string, error) {
	if _, err := c.sess(); err != nil {
		return "", err
	}
	return c.resolver.ResolveGuildID(ctx, guildRef)
}

func (c *Client) ResolveChannelID(ctx context.Context, guildID, channelRef string) (string, error) {
	if _, err := c.sess(); err != nil {
		return "", err
	}
	return c.resolver.ResolveChannelID(ctx, guildID, channelRef)
}

func (c *Client) ResolveUserID(ctx context.Context, guildID, userRef string) (string, error) {
	if _, err := c.sess(); err != nil {
		return "", err
	}
	return c.resolver.ResolveUserID(ctx, guildID, userRef)
}

// ---------- Guilds, Audit & AutoMod ----------

func (c *Client) ListGuilds(ctx context.Context) ([]dto.GuildSummary, error) {
	gs, err := c.restUserGuilds(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dto.GuildSummary, 0, len(gs))
	for _, g := range gs {
		c.resolver.applyUserGuild(g)
		cg, _ := c.resolver.guild(g.ID)
		out = append(out, dto.GuildSummary{ID: g.ID, Name: g.Name, MemberCount: g.ApproximateMemberCount, OwnerID: cg.OwnerID})
	}
	slices.SortFunc(out, func(a, b dto.GuildSummary) int { return cmpStr(a.ID, b.ID) })
	return out, nil
}

func (c *Client) GetGuildInfo(ctx context.Context, guildID string) (*dto.GuildDetail, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	g, err := s.GuildWithCounts(guildID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("get guild", err)
	}
	c.resolver.applyGuild(g)
	d := &dto.GuildDetail{
		GuildSummary: dto.GuildSummary{ID: g.ID, Name: g.Name, MemberCount: max(g.ApproximateMemberCount, g.MemberCount), OwnerID: g.OwnerID},
		Description:  g.Description, BoostTier: int(g.PremiumTier), BoostCount: g.PremiumSubscriptionCount,
	}
	for _, f := range g.Features {
		d.Features = append(d.Features, string(f))
	}
	slices.Sort(d.Features)
	if ts, err := dto.SnowflakeTime(g.ID); err == nil {
		d.CreatedAt = dto.FormatTime(ts)
	}
	chs, err := c.ListChannels(ctx, guildID)
	if err != nil {
		return nil, err
	}
	d.Channels = chs
	for _, r := range g.Roles {
		d.Roles = append(d.Roles, toRoleInfo(r))
	}
	sortRoles(d.Roles)
	return d, nil
}

func (c *Client) GetAuditLogs(ctx context.Context, guildID string, actionType int, limit int) ([]dto.AuditLogEntry, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	log, err := s.GuildAuditLog(guildID, "", "", actionType, limit, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("get audit logs", err)
	}
	users := map[string]string{}
	for _, u := range log.Users {
		if u != nil {
			users[u.ID] = u.Username
		}
	}
	out := make([]dto.AuditLogEntry, 0, len(log.AuditLogEntries))
	for _, e := range log.AuditLogEntries {
		if e != nil {
			out = append(out, toAuditLogEntry(e, users))
		}
	}
	return out, nil
}

func (c *Client) ListAutoModRules(ctx context.Context, guildID string) ([]dto.AutoModRule, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	rules, err := s.AutoModerationRules(guildID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("list automod rules", err)
	}
	out := make([]dto.AutoModRule, 0, len(rules))
	for _, r := range rules {
		if r != nil {
			out = append(out, toAutoModRule(r))
		}
	}
	return out, nil
}

// ---------- Channels ----------

// ListChannels returns the guild's channels (threads excluded), sorted by position.
func (c *Client) ListChannels(ctx context.Context, guildID string) ([]dto.ChannelInfo, error) {
	chs, err := c.restGuildChannels(ctx, guildID)
	if err != nil {
		return nil, err
	}
	out := make([]dto.ChannelInfo, 0, len(chs))
	for _, ch := range chs {
		c.resolver.applyChannel(ch)
		if ch.IsThread() {
			continue
		}
		out = append(out, toChannelInfo(ch))
	}
	slices.SortFunc(out, func(a, b dto.ChannelInfo) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return cmpStr(a.ID, b.ID)
	})
	return out, nil
}

func (c *Client) GetChannel(ctx context.Context, channelID string) (*dto.ChannelInfo, error) {
	ch, err := c.restChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	info := toChannelInfo(ch)
	return &info, nil
}

func (c *Client) CreateChannel(ctx context.Context, req dto.CreateChannelReq) (*dto.ChannelInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	t, err := channelTypeFromString(req.Type)
	if err != nil {
		return nil, err
	}
	data := discordgo.GuildChannelCreateData{
		Name: req.Name, Type: t, Topic: req.Topic, Bitrate: req.Bitrate, UserLimit: req.UserLimit,
		Position: req.Position, ParentID: req.ParentID,
	}
	ch, err := s.GuildChannelCreateComplex(req.GuildID, data, reqOpts(ctx, req.Reason)...)
	if err != nil {
		return nil, mapErr("create channel", err)
	}
	c.resolver.applyChannel(ch)
	info := toChannelInfo(ch)
	return &info, nil
}

// patchChannel issues a raw PATCH so fields can be cleared (e.g. parent_id → null),
// which discordgo's ChannelEdit (omitempty strings) cannot express.
func (c *Client) patchChannel(ctx context.Context, channelID string, body map[string]any, reason string) (*discordgo.Channel, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	ep := discordgo.EndpointChannel(channelID)
	raw, err := s.RequestWithBucketID("PATCH", ep, body, ep, reqOpts(ctx, reason)...)
	if err != nil {
		return nil, mapErr("edit channel", err)
	}
	var ch discordgo.Channel
	if err := json.Unmarshal(raw, &ch); err != nil {
		return nil, fmt.Errorf("edit channel: decode response: %w", err)
	}
	c.resolver.applyChannel(&ch)
	return &ch, nil
}

func (c *Client) EditChannel(ctx context.Context, req dto.EditChannelReq) (*dto.ChannelInfo, error) {
	body := map[string]any{}
	if req.Name != nil {
		body["name"] = *req.Name
	}
	if req.Topic != nil {
		body["topic"] = *req.Topic
	}
	if req.ParentID != nil {
		if *req.ParentID == "" {
			body["parent_id"] = nil
		} else {
			body["parent_id"] = *req.ParentID
		}
	}
	if req.Archived != nil {
		body["archived"] = *req.Archived
	}
	if len(body) == 0 {
		return c.GetChannel(ctx, req.ChannelID)
	}
	ch, err := c.patchChannel(ctx, req.ChannelID, body, req.Reason)
	if err != nil {
		return nil, err
	}
	info := toChannelInfo(ch)
	return &info, nil
}

func (c *Client) DeleteChannel(ctx context.Context, channelID, reason string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	if _, err := s.ChannelDelete(channelID, reqOpts(ctx, reason)...); err != nil {
		return mapErr("delete channel", err)
	}
	c.resolver.removeChannel(channelID)
	return nil
}

func (c *Client) SetChannelPermissions(ctx context.Context, req dto.ChannelPermissionReq) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	if req.Remove {
		return mapErr("delete permission overwrite", s.ChannelPermissionDelete(req.ChannelID, req.TargetID, reqOpts(ctx, req.Reason)...))
	}
	var t discordgo.PermissionOverwriteType
	switch req.TargetType {
	case dto.TargetRole:
		t = discordgo.PermissionOverwriteTypeRole
	case dto.TargetMember:
		t = discordgo.PermissionOverwriteTypeMember
	default:
		return fmt.Errorf("invalid target_type %q (want role or member)", req.TargetType)
	}
	return mapErr("set permission overwrite", s.ChannelPermissionSet(req.ChannelID, req.TargetID, t, req.Allow, req.Deny, reqOpts(ctx, req.Reason)...))
}

// ---------- Messages ----------

func (c *Client) toItem(m *discordgo.Message) *dto.MessageItem {
	c.resolver.noteUser(m.Author)
	it := toMessageItem(m, c.resolver)
	return &it
}

// openAttachment validates and opens a local file for upload.
func openAttachment(path string) (*discordgo.File, func(), error) {
	if !filepath.IsAbs(path) {
		return nil, nil, fmt.Errorf("file_path must be absolute: %q", path)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("file_path: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("file_path %q is not a regular file", path)
	}
	if fi.Size() > MaxAttachmentBytes {
		return nil, nil, fmt.Errorf("file_path %q is %d bytes; the limit is %d (25 MiB)", path, fi.Size(), MaxAttachmentBytes)
	}
	f, err := os.Open(path) //nolint:gosec // G304: attaching a caller-chosen local file is the documented purpose of file_path
	if err != nil {
		return nil, nil, fmt.Errorf("file_path: %w", err)
	}
	ct := mime.TypeByExtension(filepath.Ext(path))
	if ct == "" {
		ct = "application/octet-stream"
	}
	return &discordgo.File{Name: filepath.Base(path), ContentType: ct, Reader: f}, func() { _ = f.Close() }, nil
}

func (c *Client) SendMessage(ctx context.Context, req dto.SendMessageReq) (*dto.MessageItem, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	ms := &discordgo.MessageSend{Content: req.Content, Embeds: embedsOf(req.Embed)}
	if req.ReplyToMessageID != "" {
		fail := false
		ms.Reference = &discordgo.MessageReference{MessageID: req.ReplyToMessageID, ChannelID: req.ChannelID, FailIfNotExists: &fail}
	}
	if req.FilePath != "" {
		f, closeFn, err := openAttachment(req.FilePath)
		if err != nil {
			return nil, err
		}
		defer closeFn()
		ms.Files = []*discordgo.File{f}
	}
	m, err := s.ChannelMessageSendComplex(req.ChannelID, ms, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("send message", err)
	}
	return c.toItem(m), nil
}

func (c *Client) ReadMessages(ctx context.Context, req dto.ReadMessagesReq) ([]dto.MessageItem, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	ms, err := s.ChannelMessages(req.ChannelID, limit, req.Before, req.After, req.Around, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("read messages", err)
	}
	out := make([]dto.MessageItem, 0, len(ms))
	for _, m := range ms {
		if m != nil {
			out = append(out, *c.toItem(m))
		}
	}
	return out, nil
}

func (c *Client) EditMessage(ctx context.Context, channelID, messageID, content string) (*dto.MessageItem, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	m, err := s.ChannelMessageEdit(channelID, messageID, content, discordgo.WithContext(ctx))
	if err != nil {
		var rest *discordgo.RESTError
		if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == codeCannotEditOthers {
			return nil, fmt.Errorf("edit message: only messages authored by the bot can be edited: %w", ErrForbidden)
		}
		return nil, mapErr("edit message", err)
	}
	return c.toItem(m), nil
}

func (c *Client) DeleteMessage(ctx context.Context, channelID, messageID, reason string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	return mapErr("delete message", s.ChannelMessageDelete(channelID, messageID, reqOpts(ctx, reason)...))
}

func (c *Client) BulkDeleteMessages(ctx context.Context, channelID string, messageIDs []string, reason string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	if len(messageIDs) > 100 {
		return fmt.Errorf("bulk delete accepts at most 100 messages, got %d", len(messageIDs))
	}
	return mapErr("bulk delete messages", s.ChannelMessagesBulkDelete(channelID, messageIDs, reqOpts(ctx, reason)...))
}

// ---------- Reactions & Polls ----------

// normalizeEmoji converts user input to the form the reactions API expects:
// unicode as-is, custom as "name:id". ":name:" is looked up among the guild's
// custom emoji.
func (c *Client) normalizeEmoji(ctx context.Context, s *discordgo.Session, channelID, emoji string) (string, error) {
	emoji = strings.TrimSpace(emoji)
	if emoji == "" {
		return "", fmt.Errorf("emoji is required")
	}
	if name, id, ok := parseCustomEmoji(emoji); ok {
		return name + ":" + id, nil
	}
	name, ok := shortcodeName(emoji)
	if !ok {
		return emoji, nil
	}
	guildID := ""
	if ch, ok := c.resolver.channel(channelID); ok {
		guildID = ch.GuildID
	} else if ch, err := c.restChannel(ctx, channelID); err == nil {
		guildID = ch.GuildID
	}
	if guildID != "" {
		emojis, err := s.GuildEmojis(guildID, discordgo.WithContext(ctx))
		if err == nil {
			for _, e := range emojis {
				if strings.EqualFold(e.Name, name) {
					return e.Name + ":" + e.ID, nil
				}
			}
		}
	}
	return "", fmt.Errorf("unknown emoji %q: use a unicode emoji (e.g. 👍) or a custom emoji as name:id: %w", emoji, ErrNotFound)
}

func (c *Client) AddReaction(ctx context.Context, channelID, messageID, emoji string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	e, err := c.normalizeEmoji(ctx, s, channelID, emoji)
	if err != nil {
		return err
	}
	return mapErr("add reaction "+emoji, s.MessageReactionAdd(channelID, messageID, e, discordgo.WithContext(ctx)))
}

func (c *Client) AddMultipleReactions(ctx context.Context, channelID, messageID string, emojis []string) error {
	if _, err := c.sess(); err != nil {
		return err
	}
	var errs []error
	for _, e := range emojis {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.AddReaction(ctx, channelID, messageID, e); err != nil {
			errs = append(errs, fmt.Errorf("emoji %q: %w", e, err))
		}
	}
	return errors.Join(errs...)
}

func (c *Client) RemoveReaction(ctx context.Context, channelID, messageID, emoji, userID string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	e, err := c.normalizeEmoji(ctx, s, channelID, emoji)
	if err != nil {
		return err
	}
	if userID == "" {
		userID = "@me"
	}
	return mapErr("remove reaction "+emoji, s.MessageReactionRemove(channelID, messageID, e, userID, discordgo.WithContext(ctx)))
}

func (c *Client) CreatePoll(ctx context.Context, req dto.CreatePollReq) (*dto.MessageItem, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	hours := req.DurationHours
	if hours <= 0 {
		hours = 24
	}
	p := &discordgo.Poll{
		Question: discordgo.PollMedia{Text: req.Question}, AllowMultiselect: req.AllowMultiselect,
		LayoutType: discordgo.PollLayoutTypeDefault, Duration: hours,
	}
	for _, a := range req.Answers {
		p.Answers = append(p.Answers, discordgo.PollAnswer{Media: &discordgo.PollMedia{Text: a}})
	}
	m, err := s.ChannelMessageSendComplex(req.ChannelID, &discordgo.MessageSend{Poll: p}, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("create poll", err)
	}
	return c.toItem(m), nil
}

func (c *Client) GetPollResults(ctx context.Context, channelID, messageID string) (*dto.PollResult, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	m, err := s.ChannelMessage(channelID, messageID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("get poll", err)
	}
	if m.Poll == nil {
		return nil, fmt.Errorf("message %s has no poll: %w", messageID, ErrNotFound)
	}
	r := toPollResult(m)
	return &r, nil
}

// ---------- Forums & Threads ----------

func (c *Client) CreateForumChannel(ctx context.Context, req dto.CreateForumReq) (*dto.ChannelInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	// discordgo's GuildChannelCreateData lacks available_tags and
	// default_reaction_emoji, so POST the raw body.
	body := map[string]any{"name": req.Name, "type": discordgo.ChannelTypeGuildForum}
	if req.Topic != "" {
		body["topic"] = req.Topic
	}
	if req.ParentID != "" {
		body["parent_id"] = req.ParentID
	}
	if len(req.Tags) > 0 {
		tags := make([]discordgo.ForumTag, 0, len(req.Tags))
		for _, t := range req.Tags {
			tags = append(tags, discordgo.ForumTag{Name: t})
		}
		body["available_tags"] = tags
	}
	if dr := strings.TrimSpace(req.DefaultReaction); dr != "" {
		if _, id, ok := parseCustomEmoji(dr); ok {
			body["default_reaction_emoji"] = discordgo.ForumDefaultReaction{EmojiID: id}
		} else {
			body["default_reaction_emoji"] = discordgo.ForumDefaultReaction{EmojiName: dr}
		}
	}
	ep := discordgo.EndpointGuildChannels(req.GuildID)
	raw, err := s.RequestWithBucketID("POST", ep, body, ep, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("create forum channel", err)
	}
	var ch discordgo.Channel
	if err := json.Unmarshal(raw, &ch); err != nil {
		return nil, fmt.Errorf("create forum channel: decode response: %w", err)
	}
	c.resolver.applyChannel(&ch)
	info := toChannelInfo(&ch)
	return &info, nil
}

func (c *Client) CreateForumPost(ctx context.Context, req dto.CreateForumPostReq) (*dto.ThreadInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	forum, err := c.restChannel(ctx, req.ForumChannelID)
	if err != nil {
		return nil, err
	}
	if forum.Type != discordgo.ChannelTypeGuildForum && forum.Type != discordgo.ChannelTypeGuildMedia {
		return nil, fmt.Errorf("channel %s is not a forum channel", req.ForumChannelID)
	}
	var tagIDs []string
	for _, name := range req.TagNames {
		i := slices.IndexFunc(forum.AvailableTags, func(t discordgo.ForumTag) bool { return strings.EqualFold(t.Name, strings.TrimSpace(name)) })
		if i < 0 {
			avail := make([]string, 0, len(forum.AvailableTags))
			for _, t := range forum.AvailableTags {
				avail = append(avail, t.Name)
			}
			return nil, fmt.Errorf("unknown forum tag %q (available: %s): %w", name, strings.Join(avail, ", "), ErrNotFound)
		}
		tagIDs = append(tagIDs, forum.AvailableTags[i].ID)
	}
	th, err := s.ForumThreadStartComplex(req.ForumChannelID,
		&discordgo.ThreadStart{Name: req.Title, AppliedTags: tagIDs},
		&discordgo.MessageSend{Content: req.Content}, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("create forum post", err)
	}
	c.resolver.applyChannel(th)
	t := toThreadInfo(th, forumTagNames(forum))
	return &t, nil
}

func (c *Client) GetForumPost(ctx context.Context, threadID string, limit int) (*dto.ForumPostDetail, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	th, err := c.restChannel(ctx, threadID)
	if err != nil {
		return nil, err
	}
	var tags map[string]string
	if th.ParentID != "" {
		if parent, perr := c.restChannel(ctx, th.ParentID); perr == nil {
			tags = forumTagNames(parent)
		}
	}
	d := &dto.ForumPostDetail{Thread: toThreadInfo(th, tags)}
	// A forum post's starter message shares the thread's ID.
	if m, err := s.ChannelMessage(threadID, threadID, discordgo.WithContext(ctx)); err == nil {
		d.Starter = c.toItem(m)
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	ms, err := s.ChannelMessages(threadID, limit, "", "", "", discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("read forum post", err)
	}
	for i := len(ms) - 1; i >= 0; i-- { // chronological
		if ms[i] == nil || ms[i].ID == threadID {
			continue
		}
		d.Replies = append(d.Replies, *c.toItem(ms[i]))
	}
	return d, nil
}

func (c *Client) ReplyToForum(ctx context.Context, threadID, content string) (*dto.MessageItem, error) {
	return c.SendMessage(ctx, dto.SendMessageReq{ChannelID: threadID, Content: content})
}

func (c *Client) ListForumThreads(ctx context.Context, channelID string, includeArchived bool) ([]dto.ThreadInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	forum, err := c.restChannel(ctx, channelID)
	if err != nil {
		return nil, err
	}
	tags := forumTagNames(forum)
	active, err := s.GuildThreadsActive(forum.GuildID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("list active threads", err)
	}
	var out []dto.ThreadInfo
	seen := map[string]bool{}
	for _, t := range active.Threads {
		if t != nil && t.ParentID == channelID {
			c.resolver.applyChannel(t)
			seen[t.ID] = true
			out = append(out, toThreadInfo(t, tags))
		}
	}
	if includeArchived {
		arch, err := s.ThreadsArchived(channelID, nil, 100, discordgo.WithContext(ctx))
		if err != nil {
			return nil, mapErr("list archived threads", err)
		}
		for _, t := range arch.Threads {
			if t != nil && !seen[t.ID] {
				out = append(out, toThreadInfo(t, tags))
			}
		}
	}
	return out, nil
}

func (c *Client) CreateThread(ctx context.Context, req dto.CreateThreadReq) (*dto.ThreadInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	ts := &discordgo.ThreadStart{Name: req.Name, AutoArchiveDuration: req.AutoArchiveMinutes}
	var th *discordgo.Channel
	if req.MessageID != "" {
		th, err = s.MessageThreadStartComplex(req.ChannelID, req.MessageID, ts, discordgo.WithContext(ctx))
	} else {
		ts.Type = discordgo.ChannelTypeGuildPublicThread
		if req.Private {
			ts.Type = discordgo.ChannelTypeGuildPrivateThread
		}
		th, err = s.ThreadStartComplex(req.ChannelID, ts, discordgo.WithContext(ctx))
	}
	if err != nil {
		return nil, mapErr("create thread", err)
	}
	c.resolver.applyChannel(th)
	t := toThreadInfo(th, nil)
	return &t, nil
}

func (c *Client) ModifyThread(ctx context.Context, threadID string, archived, locked *bool) (*dto.ThreadInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	th, err := s.ChannelEdit(threadID, &discordgo.ChannelEdit{Archived: archived, Locked: locked}, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("modify thread", err)
	}
	c.resolver.applyChannel(th)
	t := toThreadInfo(th, nil)
	return &t, nil
}

// ---------- Members & Roles ----------

func (c *Client) ensureRoles(ctx context.Context, s *discordgo.Session, guildID string) {
	c.resolver.mu.RLock()
	have := false
	for _, r := range c.resolver.roles {
		if r.GuildID == guildID {
			have = true
			break
		}
	}
	c.resolver.mu.RUnlock()
	if have {
		return
	}
	if roles, err := s.GuildRoles(guildID, discordgo.WithContext(ctx)); err == nil {
		for _, r := range roles {
			c.resolver.applyRole(guildID, r)
		}
	}
}

func (c *Client) ListMembers(ctx context.Context, guildID string, limit int, after string) ([]dto.MemberInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	ms, err := s.GuildMembers(guildID, after, limit, discordgo.WithContext(ctx))
	if err != nil {
		var rest *discordgo.RESTError
		if errors.As(err, &rest) && rest.Message != nil && rest.Message.Code == codeMissingAccess {
			return nil, fmt.Errorf("list members: the privileged GuildMembers intent (Server Members Intent) must be enabled in the Discord Developer Portal: %w", ErrForbidden)
		}
		return nil, mapErr("list members", err)
	}
	c.ensureRoles(ctx, s, guildID)
	out := make([]dto.MemberInfo, 0, len(ms))
	for _, m := range ms {
		c.resolver.applyMember(guildID, m)
		out = append(out, toMemberInfo(m, c.resolver.roleName))
	}
	return out, nil
}

func (c *Client) TimeoutMember(ctx context.Context, guildID, userID string, durationSeconds int, reason string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	var until *time.Time
	if durationSeconds > 0 {
		t := time.Now().Add(time.Duration(durationSeconds) * time.Second)
		until = &t
	}
	return mapErr("timeout member", s.GuildMemberTimeout(guildID, userID, until, reqOpts(ctx, reason)...))
}

func (c *Client) ListRoles(ctx context.Context, guildID string) ([]dto.RoleInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	roles, err := s.GuildRoles(guildID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("list roles", err)
	}
	out := make([]dto.RoleInfo, 0, len(roles))
	for _, r := range roles {
		c.resolver.applyRole(guildID, r)
		out = append(out, toRoleInfo(r))
	}
	sortRoles(out)
	return out, nil
}

func roleParams(req dto.RoleMutationReq) *discordgo.RoleParams {
	p := &discordgo.RoleParams{Color: req.Color, Hoist: req.Hoist, Permissions: req.Permissions, Mentionable: req.Mentionable}
	if req.Name != nil {
		p.Name = *req.Name
	}
	return p
}

func (c *Client) CreateRole(ctx context.Context, req dto.RoleMutationReq) (*dto.RoleInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	r, err := s.GuildRoleCreate(req.GuildID, roleParams(req), reqOpts(ctx, req.Reason)...)
	if err != nil {
		return nil, mapErr("create role", err)
	}
	c.resolver.applyRole(req.GuildID, r)
	info := toRoleInfo(r)
	return &info, nil
}

func (c *Client) EditRole(ctx context.Context, roleID string, req dto.RoleMutationReq) (*dto.RoleInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	r, err := s.GuildRoleEdit(req.GuildID, roleID, roleParams(req), reqOpts(ctx, req.Reason)...)
	if err != nil {
		return nil, mapErr("edit role", err)
	}
	c.resolver.applyRole(req.GuildID, r)
	info := toRoleInfo(r)
	return &info, nil
}

func (c *Client) DeleteRole(ctx context.Context, guildID, roleID, reason string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	if err := s.GuildRoleDelete(guildID, roleID, reqOpts(ctx, reason)...); err != nil {
		return mapErr("delete role", err)
	}
	c.resolver.removeRole(roleID)
	return nil
}

func (c *Client) AssignRole(ctx context.Context, guildID, userID, roleID string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	return mapErr("assign role", s.GuildMemberRoleAdd(guildID, userID, roleID, discordgo.WithContext(ctx)))
}

func (c *Client) RemoveRole(ctx context.Context, guildID, userID, roleID string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	return mapErr("remove role", s.GuildMemberRoleRemove(guildID, userID, roleID, discordgo.WithContext(ctx)))
}

// ---------- Webhooks ----------

func (c *Client) rememberWebhookToken(id, token string) {
	if id == "" || token == "" {
		return
	}
	c.tokensMu.Lock()
	defer c.tokensMu.Unlock()
	c.webhookTokens[id] = token
}

func (c *Client) forgetWebhookToken(id string) {
	c.tokensMu.Lock()
	defer c.tokensMu.Unlock()
	delete(c.webhookTokens, id)
}

// webhookToken returns a cached token or fetches it (requires ManageWebhooks).
func (c *Client) webhookToken(ctx context.Context, s *discordgo.Session, id string) (string, error) {
	c.tokensMu.Lock()
	t := c.webhookTokens[id]
	c.tokensMu.Unlock()
	if t != "" {
		return t, nil
	}
	w, err := s.Webhook(id, discordgo.WithContext(ctx))
	if err != nil {
		return "", mapErr("get webhook", err)
	}
	if w.Token == "" {
		return "", fmt.Errorf("webhook %s has no token available to this bot (only incoming webhooks can be executed): %w", id, ErrForbidden)
	}
	c.rememberWebhookToken(id, w.Token)
	return w.Token, nil
}

// GetWebhook returns a webhook's metadata (never its token). Requires ManageWebhooks.
func (c *Client) GetWebhook(ctx context.Context, webhookID string) (*dto.WebhookInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	w, err := s.Webhook(webhookID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("get webhook", err)
	}
	c.rememberWebhookToken(w.ID, w.Token)
	info := toWebhookInfo(w)
	return &info, nil
}

func (c *Client) CreateWebhook(ctx context.Context, channelID, name string) (*dto.WebhookInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	w, err := s.WebhookCreate(channelID, name, "", discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("create webhook", err)
	}
	c.rememberWebhookToken(w.ID, w.Token)
	info := toWebhookInfo(w)
	return &info, nil
}

func (c *Client) EditWebhook(ctx context.Context, webhookID, name, channelID string) (*dto.WebhookInfo, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	w, err := s.WebhookEdit(webhookID, name, "", channelID, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("edit webhook", err)
	}
	info := toWebhookInfo(w)
	return &info, nil
}

func (c *Client) DeleteWebhook(ctx context.Context, webhookID, reason string) error {
	s, err := c.sess()
	if err != nil {
		return err
	}
	if err := s.WebhookDelete(webhookID, reqOpts(ctx, reason)...); err != nil {
		return mapErr("delete webhook", err)
	}
	c.forgetWebhookToken(webhookID)
	return nil
}

func (c *Client) SendWebhookMessage(ctx context.Context, req dto.WebhookMessageReq) (*dto.MessageItem, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	var id, token string
	switch {
	case req.WebhookURL != "":
		if id, token, err = parseWebhookURL(req.WebhookURL); err != nil {
			return nil, err
		}
	case req.WebhookID != "":
		id = req.WebhookID
		if token, err = c.webhookToken(ctx, s, id); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("webhook_id or webhook_url is required")
	}
	params := &discordgo.WebhookParams{Content: req.Content, Username: req.Username, AvatarURL: req.AvatarURL, Embeds: embedsOf(req.Embed)}
	var m *discordgo.Message
	if req.ThreadID != "" {
		m, err = s.WebhookThreadExecute(id, token, true, req.ThreadID, params, discordgo.WithContext(ctx))
	} else {
		m, err = s.WebhookExecute(id, token, true, params, discordgo.WithContext(ctx))
	}
	if err != nil {
		if errors.Is(mapErr("", err), ErrNotFound) {
			c.forgetWebhookToken(id)
		}
		return nil, mapErr("execute webhook "+id, err)
	}
	if m == nil {
		return &dto.MessageItem{}, nil
	}
	return c.toItem(m), nil
}

// ---------- Scheduled Events ----------

func (c *Client) ListScheduledEvents(ctx context.Context, guildID string) ([]dto.ScheduledEvent, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	evs, err := s.GuildScheduledEvents(guildID, true, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("list scheduled events", err)
	}
	out := make([]dto.ScheduledEvent, 0, len(evs))
	for _, e := range evs {
		if e != nil {
			out = append(out, toScheduledEvent(e))
		}
	}
	return out, nil
}

func parseISO(field, v string) (*time.Time, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		if t, err := time.Parse(layout, strings.TrimSpace(v)); err == nil {
			return &t, nil
		}
	}
	return nil, fmt.Errorf("%s: invalid ISO-8601 time %q", field, v)
}

func (c *Client) CreateScheduledEvent(ctx context.Context, req dto.CreateEventReq) (*dto.ScheduledEvent, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	et, err := eventEntityTypeFromString(req.EntityType)
	if err != nil {
		return nil, err
	}
	start, err := parseISO("start_time_iso", req.StartTimeISO)
	if err != nil {
		return nil, err
	}
	if start == nil {
		return nil, fmt.Errorf("start_time_iso is required")
	}
	end, err := parseISO("end_time_iso", req.EndTimeISO)
	if err != nil {
		return nil, err
	}
	p := &discordgo.GuildScheduledEventParams{
		Name: req.Name, Description: req.Description, ScheduledStartTime: start, ScheduledEndTime: end,
		PrivacyLevel: discordgo.GuildScheduledEventPrivacyLevelGuildOnly, EntityType: et,
	}
	if et == discordgo.GuildScheduledEventEntityTypeExternal {
		if req.LocationOrChannelID == "" || end == nil {
			return nil, fmt.Errorf("external events require location_or_channel_id (location) and end_time_iso")
		}
		p.EntityMetadata = &discordgo.GuildScheduledEventEntityMetadata{Location: req.LocationOrChannelID}
	} else {
		if req.LocationOrChannelID == "" {
			return nil, fmt.Errorf("%s events require location_or_channel_id (a channel)", req.EntityType)
		}
		p.ChannelID = req.LocationOrChannelID
	}
	e, err := s.GuildScheduledEventCreate(req.GuildID, p, discordgo.WithContext(ctx))
	if err != nil {
		return nil, mapErr("create scheduled event", err)
	}
	out := toScheduledEvent(e)
	return &out, nil
}

// ---------- HITL ----------

func (c *Client) AwaitInteraction(ctx context.Context, req dto.AwaitInteractionReq) (*dto.InteractionResult, error) {
	if _, err := c.sess(); err != nil {
		return nil, err
	}
	return c.broker.Await(ctx, req)
}
