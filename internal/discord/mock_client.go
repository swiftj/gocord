package discord

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/local/discord-mcp/internal/dto"
)

// Deterministic fixture IDs (synthetic Snowflakes).
const (
	MockGuildID        = "100000000000000001"
	MockGeneralID      = "100000000000000010"
	MockAnnouncementID = "100000000000000011"
	MockCategoryID     = "100000000000000020"
	MockForumID        = "100000000000000030"
	MockModeratorRole  = "100000000000000040"
	MockBotRoleID      = "100000000000000041" // managed bot role above @Moderator
	MockAliceID        = "100000000000000050"
	MockBotID          = "100000000000000099"
	MockForumThreadID  = "100000000000000060"
	MockFirstMessageID = "100000000000000100"
)

// MockCall records one Service invocation.
type MockCall struct {
	Method string
	Args   []any
}

// mutatingMethods are Service methods that change Discord state.
var mutatingMethods = map[string]bool{
	"CreateChannel": true, "EditChannel": true, "DeleteChannel": true, "SetChannelPermissions": true,
	"SendMessage": true, "EditMessage": true, "DeleteMessage": true, "BulkDeleteMessages": true,
	"AddReaction": true, "AddMultipleReactions": true, "RemoveReaction": true, "CreatePoll": true,
	"CreateForumChannel": true, "CreateForumPost": true, "ReplyToForum": true, "CreateThread": true, "ModifyThread": true,
	"TimeoutMember": true, "CreateRole": true, "EditRole": true, "DeleteRole": true, "AssignRole": true, "RemoveRole": true,
	"CreateWebhook": true, "EditWebhook": true, "DeleteWebhook": true, "SendWebhookMessage": true,
	"CreateScheduledEvent": true, "AwaitInteraction": true,
}

// IsMutating reports whether a Service method name changes Discord state.
func IsMutating(method string) bool { return mutatingMethods[method] }

// MockClient is a deterministic, in-memory Service for unit tests.
// All exported maps may be modified by tests before use; access is mutex-protected.
type MockClient struct {
	mu sync.Mutex

	Session   dto.SessionInfo
	Guilds    map[string]dto.GuildDetail      // guildID -> detail (Channels/Roles fields are ignored; see below)
	Channels  map[string]dto.ChannelInfo      // channelID -> channel (includes categories, forums, threads)
	Threads   map[string]dto.ThreadInfo       // threadID -> thread
	Messages  map[string][]dto.MessageItem    // channelID -> messages, oldest first
	Roles     map[string][]dto.RoleInfo       // guildID -> roles
	Members   map[string][]dto.MemberInfo     // guildID -> members
	Webhooks  map[string]dto.WebhookInfo      // webhookID -> webhook
	Events    map[string][]dto.ScheduledEvent // guildID -> events
	Polls     map[string]dto.PollResult       // messageID -> poll
	AuditLogs map[string][]dto.AuditLogEntry  // guildID -> entries
	AutoMod   map[string][]dto.AutoModRule    // guildID -> rules
	// Profiles overrides GetMember (guildID -> userID -> profile). A member
	// without an entry is derived from Members, with role names mapped to IDs.
	Profiles map[string]map[string]dto.MemberProfile

	// NextInteraction is returned by AwaitInteraction. Nil means the wait times out.
	NextInteraction *dto.InteractionResult

	// Err injects an error for a method name (e.g. Err["DeleteChannel"] = ErrForbidden).
	Err map[string]error

	// Calls records every invocation in order.
	Calls []MockCall

	// Now is the clock used for generated IDs and timestamps.
	Now func() time.Time

	seq uint64
}

var _ Service = (*MockClient)(nil)

// NewMockClient returns a MockClient seeded with the deterministic fixture.
func NewMockClient() *MockClient {
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	m := &MockClient{
		Session: dto.SessionInfo{Profile: "default", BotUserID: MockBotID, BotUsername: "discord-mcp", GuildCount: 1},
		Guilds: map[string]dto.GuildDetail{
			MockGuildID: {
				GuildSummary: dto.GuildSummary{ID: MockGuildID, Name: "Test Guild", MemberCount: 3, OwnerID: MockAliceID},
				BoostTier:    1, BoostCount: 2, Features: []string{"COMMUNITY"}, CreatedAt: "2024-01-01T00:00:00Z",
			},
		},
		Channels: map[string]dto.ChannelInfo{
			MockCategoryID:     {ID: MockCategoryID, GuildID: MockGuildID, Name: "Community", Type: dto.ChannelCategory, Position: 0},
			MockGeneralID:      {ID: MockGeneralID, GuildID: MockGuildID, Name: "general", Type: dto.ChannelText, ParentID: MockCategoryID, Position: 1, Topic: "General chat"},
			MockAnnouncementID: {ID: MockAnnouncementID, GuildID: MockGuildID, Name: "announcements", Type: dto.ChannelAnnouncement, ParentID: MockCategoryID, Position: 2},
			MockForumID:        {ID: MockForumID, GuildID: MockGuildID, Name: "help-forum", Type: dto.ChannelForum, ParentID: MockCategoryID, Position: 3},
			MockForumThreadID:  {ID: MockForumThreadID, GuildID: MockGuildID, Name: "How do I search?", Type: dto.ChannelThread, ParentID: MockForumID},
		},
		Threads: map[string]dto.ThreadInfo{
			MockForumThreadID: {ID: MockForumThreadID, ParentID: MockForumID, Name: "How do I search?", OwnerID: MockAliceID, MessageCount: 1, AppliedTags: []string{"question"}, CreatedAt: dto.FormatTime(base)},
		},
		Messages: map[string][]dto.MessageItem{
			MockGeneralID: {
				{ID: MockFirstMessageID, ChannelID: MockGeneralID, AuthorID: MockAliceID, Author: "alice", Content: "Hello world, shipping the release tomorrow", Timestamp: dto.FormatTime(base)},
				{ID: "100000000000000101", ChannelID: MockGeneralID, AuthorID: MockBotID, Author: "discord-mcp", Content: "Release checklist posted", Timestamp: dto.FormatTime(base.Add(time.Minute))},
			},
			MockForumThreadID: {
				{ID: "100000000000000110", ChannelID: MockForumThreadID, AuthorID: MockAliceID, Author: "alice", Content: "How do I search old messages?", Timestamp: dto.FormatTime(base)},
			},
		},
		Roles: map[string][]dto.RoleInfo{
			MockGuildID: {
				{ID: MockGuildID, Name: "@everyone", Position: 0, Permissions: "1024"},
				{ID: MockModeratorRole, Name: "Moderator", Color: "#3498DB", Position: 5, Permissions: "8192", PermissionNames: []string{"ManageMessages"}, Hoist: true},
				{ID: MockBotRoleID, Name: "MCP Bot", Position: 10, Permissions: "268435456", PermissionNames: []string{"ManageRoles"}, Managed: true},
			},
		},
		Members: map[string][]dto.MemberInfo{
			MockGuildID: {
				{UserID: MockAliceID, Username: "alice", Roles: []string{"Moderator"}, JoinedAt: "2024-01-02T00:00:00Z"},
				{UserID: "100000000000000051", Username: "bob", JoinedAt: "2024-02-01T00:00:00Z"},
				{UserID: MockBotID, Username: "discord-mcp", Roles: []string{"MCP Bot"}, Bot: true, JoinedAt: "2024-01-01T00:00:00Z"},
			},
		},
		Webhooks:  map[string]dto.WebhookInfo{},
		Events:    map[string][]dto.ScheduledEvent{},
		Polls:     map[string]dto.PollResult{},
		AuditLogs: map[string][]dto.AuditLogEntry{MockGuildID: {{ID: "100000000000000200", ActionType: 22, Action: "MEMBER_BAN_ADD", UserID: MockAliceID, User: "alice", TargetID: "100000000000000052", Reason: "spam", CreatedAt: dto.FormatTime(base)}}},
		AutoMod:   map[string][]dto.AutoModRule{MockGuildID: {{ID: "100000000000000300", Name: "Block invites", TriggerType: "keyword", Enabled: true, Keywords: []string{"discord.gg/*"}, Actions: []string{"block_message"}}}},
		Profiles:  map[string]map[string]dto.MemberProfile{},
		Err:       map[string]error{},
		Now:       func() time.Time { return base.Add(time.Hour) },
		seq:       1000,
	}
	return m
}

// ---------- helpers ----------

// record logs a call and returns any injected error. Caller must hold m.mu.
func (m *MockClient) record(method string, args ...any) error {
	m.Calls = append(m.Calls, MockCall{Method: method, Args: args})
	return m.Err[method]
}

// MutatingCalls returns recorded calls that would change Discord state.
func (m *MockClient) MutatingCalls() []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MockCall
	for _, c := range m.Calls {
		if mutatingMethods[c.Method] {
			out = append(out, c)
		}
	}
	return out
}

// CallsTo returns recorded calls to one method.
func (m *MockClient) CallsTo(method string) []MockCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []MockCall
	for _, c := range m.Calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// Reset clears recorded calls.
func (m *MockClient) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = nil
}

func (m *MockClient) newID() string {
	m.seq++
	return strconv.FormatUint(100000000000000000+m.seq, 10)
}

func (m *MockClient) now() string { return dto.FormatTime(m.Now()) }

func notFound(kind, ref string) error { return fmt.Errorf("%s %q: %w", kind, ref, ErrNotFound) }

func (m *MockClient) channel(id string) (dto.ChannelInfo, error) {
	c, ok := m.Channels[id]
	if !ok {
		return c, notFound("channel", id)
	}
	return c, nil
}

func resolveByName[T any](kind string, ref Ref, items []T, id func(T) string, name func(T) string) (string, error) {
	if ref.ID != "" {
		for _, it := range items {
			if id(it) == ref.ID {
				return ref.ID, nil
			}
		}
		return "", notFound(kind, ref.ID)
	}
	var hits []string
	for _, it := range items {
		if NameMatches(ref.Name, name(it)) {
			hits = append(hits, id(it))
		}
	}
	switch len(hits) {
	case 0:
		return "", notFound(kind, ref.Name)
	case 1:
		return hits[0], nil
	default:
		slices.Sort(hits)
		return "", fmt.Errorf("%s %q matches %v: %w", kind, ref.Name, hits, ErrAmbiguous)
	}
}

// ---------- Session & Resolution ----------

func (m *MockClient) Login(_ context.Context, token, profile string) (*dto.SessionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("Login", "***", profile); err != nil {
		return nil, err
	}
	if token == "" {
		return nil, fmt.Errorf("empty token: %w", ErrNotLoggedIn)
	}
	s := m.Session
	if profile != "" {
		s.Profile = profile
	}
	m.Session = s
	return &s, nil
}

func (m *MockClient) ResolveGuildID(_ context.Context, guildRef string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ResolveGuildID", guildRef); err != nil {
		return "", err
	}
	var gs []dto.GuildDetail
	for _, g := range m.Guilds {
		gs = append(gs, g)
	}
	return resolveByName("guild", ParseRef(guildRef), gs,
		func(g dto.GuildDetail) string { return g.ID }, func(g dto.GuildDetail) string { return g.Name })
}

func (m *MockClient) ResolveChannelID(_ context.Context, guildID, channelRef string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ResolveChannelID", guildID, channelRef); err != nil {
		return "", err
	}
	var cs []dto.ChannelInfo
	for _, c := range m.Channels {
		if guildID == "" || c.GuildID == guildID {
			cs = append(cs, c)
		}
	}
	return resolveByName("channel", ParseRef(channelRef), cs,
		func(c dto.ChannelInfo) string { return c.ID }, func(c dto.ChannelInfo) string { return c.Name })
}

func (m *MockClient) ResolveUserID(_ context.Context, guildID, userRef string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ResolveUserID", guildID, userRef); err != nil {
		return "", err
	}
	var ms []dto.MemberInfo
	if guildID != "" {
		ms = m.Members[guildID]
	} else {
		for _, l := range m.Members {
			ms = append(ms, l...)
		}
	}
	return resolveByName("user", ParseRef(userRef), ms,
		func(u dto.MemberInfo) string { return u.UserID }, func(u dto.MemberInfo) string { return u.Username })
}

// ---------- Guilds, Audit & AutoMod ----------

func (m *MockClient) ListGuilds(_ context.Context) ([]dto.GuildSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListGuilds"); err != nil {
		return nil, err
	}
	out := make([]dto.GuildSummary, 0, len(m.Guilds))
	for _, g := range m.Guilds {
		out = append(out, g.GuildSummary)
	}
	slices.SortFunc(out, func(a, b dto.GuildSummary) int { return cmpStr(a.ID, b.ID) })
	return out, nil
}

func (m *MockClient) GetGuildInfo(_ context.Context, guildID string) (*dto.GuildDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetGuildInfo", guildID); err != nil {
		return nil, err
	}
	g, ok := m.Guilds[guildID]
	if !ok {
		return nil, notFound("guild", guildID)
	}
	g.Channels = m.listChannels(guildID)
	g.Roles = slices.Clone(m.Roles[guildID])
	return &g, nil
}

func (m *MockClient) GetAuditLogs(_ context.Context, guildID string, actionType, limit int) ([]dto.AuditLogEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetAuditLogs", guildID, actionType, limit); err != nil {
		return nil, err
	}
	var out []dto.AuditLogEntry
	for _, e := range m.AuditLogs[guildID] {
		if actionType == 0 || e.ActionType == actionType {
			out = append(out, e)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MockClient) ListAutoModRules(_ context.Context, guildID string) ([]dto.AutoModRule, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListAutoModRules", guildID); err != nil {
		return nil, err
	}
	return slices.Clone(m.AutoMod[guildID]), nil
}

// ---------- Channels ----------

func (m *MockClient) listChannels(guildID string) []dto.ChannelInfo {
	var out []dto.ChannelInfo
	for _, c := range m.Channels {
		if c.GuildID == guildID && c.Type != dto.ChannelThread {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b dto.ChannelInfo) int {
		if a.Position != b.Position {
			return a.Position - b.Position
		}
		return cmpStr(a.ID, b.ID)
	})
	return out
}

func (m *MockClient) ListChannels(_ context.Context, guildID string) ([]dto.ChannelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListChannels", guildID); err != nil {
		return nil, err
	}
	if _, ok := m.Guilds[guildID]; !ok {
		return nil, notFound("guild", guildID)
	}
	return m.listChannels(guildID), nil
}

func (m *MockClient) GetChannel(_ context.Context, channelID string) (*dto.ChannelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetChannel", channelID); err != nil {
		return nil, err
	}
	c, err := m.channel(channelID)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *MockClient) CreateChannel(_ context.Context, req dto.CreateChannelReq) (*dto.ChannelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateChannel", req); err != nil {
		return nil, err
	}
	if _, ok := m.Guilds[req.GuildID]; !ok {
		return nil, notFound("guild", req.GuildID)
	}
	c := dto.ChannelInfo{ID: m.newID(), GuildID: req.GuildID, Name: req.Name, Type: req.Type, ParentID: req.ParentID, Position: req.Position, Topic: req.Topic}
	if c.Type == "" {
		c.Type = dto.ChannelText
	}
	m.Channels[c.ID] = c
	return &c, nil
}

func (m *MockClient) EditChannel(_ context.Context, req dto.EditChannelReq) (*dto.ChannelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("EditChannel", req); err != nil {
		return nil, err
	}
	c, err := m.channel(req.ChannelID)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.Topic != nil {
		c.Topic = *req.Topic
	}
	if req.ParentID != nil {
		c.ParentID = *req.ParentID
	}
	m.Channels[c.ID] = c
	if t, ok := m.Threads[c.ID]; ok && req.Archived != nil {
		t.Archived = *req.Archived
		m.Threads[c.ID] = t
	}
	return &c, nil
}

func (m *MockClient) DeleteChannel(_ context.Context, channelID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("DeleteChannel", channelID, reason); err != nil {
		return err
	}
	if _, err := m.channel(channelID); err != nil {
		return err
	}
	delete(m.Channels, channelID)
	delete(m.Threads, channelID)
	delete(m.Messages, channelID)
	return nil
}

func (m *MockClient) SetChannelPermissions(_ context.Context, req dto.ChannelPermissionReq) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("SetChannelPermissions", req); err != nil {
		return err
	}
	_, err := m.channel(req.ChannelID)
	return err
}

// ---------- Messages ----------

func (m *MockClient) appendMessage(channelID, authorID, author, content string) dto.MessageItem {
	msg := dto.MessageItem{ID: m.newID(), ChannelID: channelID, AuthorID: authorID, Author: author, Content: content, Timestamp: m.now()}
	m.Messages[channelID] = append(m.Messages[channelID], msg)
	return msg
}

func (m *MockClient) SendMessage(_ context.Context, req dto.SendMessageReq) (*dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("SendMessage", req); err != nil {
		return nil, err
	}
	if _, err := m.channel(req.ChannelID); err != nil {
		return nil, err
	}
	msg := m.appendMessage(req.ChannelID, MockBotID, m.Session.BotUsername, req.Content)
	msg.ReplyToID = req.ReplyToMessageID
	if req.Embed != nil {
		msg.Embeds = []dto.EmbedSummary{dto.EmbedSummary(*req.Embed)}
	}
	if req.FilePath != "" {
		msg.Attachments = []dto.AttachmentRef{{Filename: req.FilePath}}
	}
	l := m.Messages[req.ChannelID]
	l[len(l)-1] = msg
	return &msg, nil
}

func (m *MockClient) GetMessage(_ context.Context, channelID, messageID string) (*dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetMessage", channelID, messageID); err != nil {
		return nil, err
	}
	if _, err := m.channel(channelID); err != nil {
		return nil, err
	}
	for _, msg := range m.Messages[channelID] {
		if msg.ID == messageID {
			return &msg, nil
		}
	}
	return nil, notFound("message", messageID)
}

func (m *MockClient) ReadMessages(_ context.Context, req dto.ReadMessagesReq) ([]dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ReadMessages", req); err != nil {
		return nil, err
	}
	if _, err := m.channel(req.ChannelID); err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var out []dto.MessageItem
	for _, msg := range m.Messages[req.ChannelID] {
		if req.Before != "" && !snowLess(msg.ID, req.Before) {
			continue
		}
		if req.After != "" && !snowLess(req.After, msg.ID) {
			continue
		}
		out = append(out, msg)
	}
	// Discord returns newest first.
	slices.Reverse(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MockClient) findMessage(channelID, messageID string) (int, error) {
	for i, msg := range m.Messages[channelID] {
		if msg.ID == messageID {
			return i, nil
		}
	}
	return -1, notFound("message", messageID)
}

func (m *MockClient) EditMessage(_ context.Context, channelID, messageID, content string) (*dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("EditMessage", channelID, messageID, content); err != nil {
		return nil, err
	}
	i, err := m.findMessage(channelID, messageID)
	if err != nil {
		return nil, err
	}
	msg := &m.Messages[channelID][i]
	if msg.AuthorID != MockBotID {
		return nil, fmt.Errorf("cannot edit a message authored by another user: %w", ErrForbidden)
	}
	msg.Content = content
	msg.EditedAt = m.now()
	out := *msg
	return &out, nil
}

func (m *MockClient) DeleteMessage(_ context.Context, channelID, messageID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("DeleteMessage", channelID, messageID, reason); err != nil {
		return err
	}
	i, err := m.findMessage(channelID, messageID)
	if err != nil {
		return err
	}
	m.Messages[channelID] = slices.Delete(m.Messages[channelID], i, i+1)
	return nil
}

func (m *MockClient) BulkDeleteMessages(_ context.Context, channelID string, messageIDs []string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("BulkDeleteMessages", channelID, slices.Clone(messageIDs), reason); err != nil {
		return err
	}
	m.Messages[channelID] = slices.DeleteFunc(m.Messages[channelID], func(msg dto.MessageItem) bool {
		return slices.Contains(messageIDs, msg.ID)
	})
	return nil
}

// ---------- Reactions & Polls ----------

func (m *MockClient) AddReaction(_ context.Context, channelID, messageID, emoji string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("AddReaction", channelID, messageID, emoji); err != nil {
		return err
	}
	return m.react(channelID, messageID, emoji, 1)
}

func (m *MockClient) AddMultipleReactions(_ context.Context, channelID, messageID string, emojis []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("AddMultipleReactions", channelID, messageID, slices.Clone(emojis)); err != nil {
		return err
	}
	for _, e := range emojis {
		if err := m.react(channelID, messageID, e, 1); err != nil {
			return err
		}
	}
	return nil
}

func (m *MockClient) RemoveReaction(_ context.Context, channelID, messageID, emoji, userID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("RemoveReaction", channelID, messageID, emoji, userID); err != nil {
		return err
	}
	return m.react(channelID, messageID, emoji, -1)
}

func (m *MockClient) react(channelID, messageID, emoji string, delta int) error {
	i, err := m.findMessage(channelID, messageID)
	if err != nil {
		return err
	}
	msg := &m.Messages[channelID][i]
	for j := range msg.Reactions {
		if msg.Reactions[j].Emoji == emoji {
			msg.Reactions[j].Count += delta
			if msg.Reactions[j].Count <= 0 {
				msg.Reactions = slices.Delete(msg.Reactions, j, j+1)
			}
			return nil
		}
	}
	if delta > 0 {
		msg.Reactions = append(msg.Reactions, dto.ReactionCount{Emoji: emoji, Count: delta, Me: true})
	}
	return nil
}

func (m *MockClient) CreatePoll(_ context.Context, req dto.CreatePollReq) (*dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreatePoll", req); err != nil {
		return nil, err
	}
	if _, err := m.channel(req.ChannelID); err != nil {
		return nil, err
	}
	msg := m.appendMessage(req.ChannelID, MockBotID, m.Session.BotUsername, "")
	p := dto.PollResult{MessageID: msg.ID, Question: req.Question}
	for i, a := range req.Answers {
		p.Answers = append(p.Answers, dto.PollAnswerTally{ID: i + 1, Text: a})
	}
	hours := req.DurationHours
	if hours <= 0 {
		hours = 24
	}
	p.ExpiresAt = dto.FormatTime(m.Now().Add(time.Duration(hours) * time.Hour))
	m.Polls[msg.ID] = p
	return &msg, nil
}

func (m *MockClient) GetPollResults(_ context.Context, channelID, messageID string) (*dto.PollResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetPollResults", channelID, messageID); err != nil {
		return nil, err
	}
	p, ok := m.Polls[messageID]
	if !ok {
		return nil, notFound("poll", messageID)
	}
	return &p, nil
}

// ---------- Forums & Threads ----------

func (m *MockClient) CreateForumChannel(_ context.Context, req dto.CreateForumReq) (*dto.ChannelInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateForumChannel", req); err != nil {
		return nil, err
	}
	if _, ok := m.Guilds[req.GuildID]; !ok {
		return nil, notFound("guild", req.GuildID)
	}
	c := dto.ChannelInfo{ID: m.newID(), GuildID: req.GuildID, Name: req.Name, Type: dto.ChannelForum, ParentID: req.ParentID, Topic: req.Topic}
	m.Channels[c.ID] = c
	return &c, nil
}

func (m *MockClient) newThread(parentID, name string, private bool, tags []string) dto.ThreadInfo {
	parent := m.Channels[parentID]
	t := dto.ThreadInfo{ID: m.newID(), ParentID: parentID, Name: name, OwnerID: MockBotID, Private: private, AppliedTags: tags, CreatedAt: m.now()}
	m.Threads[t.ID] = t
	m.Channels[t.ID] = dto.ChannelInfo{ID: t.ID, GuildID: parent.GuildID, Name: name, Type: dto.ChannelThread, ParentID: parentID}
	return t
}

func (m *MockClient) CreateForumPost(_ context.Context, req dto.CreateForumPostReq) (*dto.ThreadInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateForumPost", req); err != nil {
		return nil, err
	}
	c, err := m.channel(req.ForumChannelID)
	if err != nil {
		return nil, err
	}
	if c.Type != dto.ChannelForum {
		return nil, fmt.Errorf("channel %s is not a forum", c.ID)
	}
	t := m.newThread(c.ID, req.Title, false, req.TagNames)
	m.appendMessage(t.ID, MockBotID, m.Session.BotUsername, req.Content)
	t.MessageCount = 1
	m.Threads[t.ID] = t
	return &t, nil
}

func (m *MockClient) GetForumPost(_ context.Context, threadID string, limit int) (*dto.ForumPostDetail, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetForumPost", threadID, limit); err != nil {
		return nil, err
	}
	t, ok := m.Threads[threadID]
	if !ok {
		return nil, notFound("thread", threadID)
	}
	d := &dto.ForumPostDetail{Thread: t}
	msgs := m.Messages[threadID]
	if len(msgs) > 0 {
		s := msgs[0]
		d.Starter = &s
		d.Replies = slices.Clone(msgs[1:])
		if limit > 0 && len(d.Replies) > limit {
			d.Replies = d.Replies[:limit]
		}
	}
	return d, nil
}

func (m *MockClient) ReplyToForum(_ context.Context, threadID, content string) (*dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ReplyToForum", threadID, content); err != nil {
		return nil, err
	}
	t, ok := m.Threads[threadID]
	if !ok {
		return nil, notFound("thread", threadID)
	}
	if t.Locked {
		return nil, fmt.Errorf("thread %s is locked: %w", threadID, ErrForbidden)
	}
	msg := m.appendMessage(threadID, MockBotID, m.Session.BotUsername, content)
	t.MessageCount++
	m.Threads[threadID] = t
	return &msg, nil
}

func (m *MockClient) ListForumThreads(_ context.Context, channelID string, includeArchived bool) ([]dto.ThreadInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListForumThreads", channelID, includeArchived); err != nil {
		return nil, err
	}
	if _, err := m.channel(channelID); err != nil {
		return nil, err
	}
	var out []dto.ThreadInfo
	for _, t := range m.Threads {
		if t.ParentID == channelID && (includeArchived || !t.Archived) {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b dto.ThreadInfo) int { return cmpStr(a.ID, b.ID) })
	return out, nil
}

func (m *MockClient) CreateThread(_ context.Context, req dto.CreateThreadReq) (*dto.ThreadInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateThread", req); err != nil {
		return nil, err
	}
	if _, err := m.channel(req.ChannelID); err != nil {
		return nil, err
	}
	if req.MessageID != "" {
		if _, err := m.findMessage(req.ChannelID, req.MessageID); err != nil {
			return nil, err
		}
	}
	t := m.newThread(req.ChannelID, req.Name, req.Private, nil)
	return &t, nil
}

func (m *MockClient) ModifyThread(_ context.Context, threadID string, archived, locked *bool) (*dto.ThreadInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ModifyThread", threadID, archived, locked); err != nil {
		return nil, err
	}
	t, ok := m.Threads[threadID]
	if !ok {
		return nil, notFound("thread", threadID)
	}
	if archived != nil {
		t.Archived = *archived
	}
	if locked != nil {
		t.Locked = *locked
	}
	m.Threads[threadID] = t
	return &t, nil
}

// ---------- Members & Roles ----------

func (m *MockClient) ListMembers(_ context.Context, guildID string, limit int, after string) ([]dto.MemberInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListMembers", guildID, limit, after); err != nil {
		return nil, err
	}
	ms := slices.Clone(m.Members[guildID])
	slices.SortFunc(ms, func(a, b dto.MemberInfo) int { return cmpStr(a.UserID, b.UserID) })
	var out []dto.MemberInfo
	for _, mem := range ms {
		if after == "" || snowLess(after, mem.UserID) {
			out = append(out, mem)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MockClient) GetMember(_ context.Context, guildID, userID string) (*dto.MemberProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetMember", guildID, userID); err != nil {
		return nil, err
	}
	if userID == "" {
		userID = m.Session.BotUserID
	}
	if p, ok := m.Profiles[guildID][userID]; ok {
		return &p, nil
	}
	for _, mem := range m.Members[guildID] {
		if mem.UserID != userID {
			continue
		}
		p := dto.MemberProfile{GuildID: guildID, UserID: mem.UserID, Username: mem.Username, Nick: mem.Nick, Bot: mem.Bot}
		for _, name := range mem.Roles {
			for _, r := range m.Roles[guildID] {
				if r.Name == name {
					p.Roles = append(p.Roles, r.ID)
				}
			}
		}
		return &p, nil
	}
	return nil, notFound("member", userID)
}

func (m *MockClient) TimeoutMember(_ context.Context, guildID, userID string, durationSeconds int, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("TimeoutMember", guildID, userID, durationSeconds, reason); err != nil {
		return err
	}
	for i, mem := range m.Members[guildID] {
		if mem.UserID == userID {
			if durationSeconds <= 0 {
				m.Members[guildID][i].TimeoutUntil = ""
			} else {
				m.Members[guildID][i].TimeoutUntil = dto.FormatTime(m.Now().Add(time.Duration(durationSeconds) * time.Second))
			}
			return nil
		}
	}
	return notFound("member", userID)
}

func (m *MockClient) ListRoles(_ context.Context, guildID string) ([]dto.RoleInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListRoles", guildID); err != nil {
		return nil, err
	}
	if _, ok := m.Guilds[guildID]; !ok {
		return nil, notFound("guild", guildID)
	}
	return slices.Clone(m.Roles[guildID]), nil
}

func applyRole(r *dto.RoleInfo, req dto.RoleMutationReq) {
	if req.Name != nil {
		r.Name = *req.Name
	}
	if req.Color != nil {
		r.Color = fmt.Sprintf("#%06X", *req.Color)
	}
	if req.Permissions != nil {
		r.Permissions = strconv.FormatInt(*req.Permissions, 10)
	}
	if req.Hoist != nil {
		r.Hoist = *req.Hoist
	}
	if req.Mentionable != nil {
		r.Mentionable = *req.Mentionable
	}
}

func (m *MockClient) CreateRole(_ context.Context, req dto.RoleMutationReq) (*dto.RoleInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateRole", req); err != nil {
		return nil, err
	}
	if _, ok := m.Guilds[req.GuildID]; !ok {
		return nil, notFound("guild", req.GuildID)
	}
	r := dto.RoleInfo{ID: m.newID(), Name: "new role", Position: 1}
	applyRole(&r, req)
	m.Roles[req.GuildID] = append(m.Roles[req.GuildID], r)
	return &r, nil
}

func (m *MockClient) roleIndex(guildID, roleID string) (int, error) {
	for i, r := range m.Roles[guildID] {
		if r.ID == roleID {
			return i, nil
		}
	}
	return -1, notFound("role", roleID)
}

func (m *MockClient) EditRole(_ context.Context, roleID string, req dto.RoleMutationReq) (*dto.RoleInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("EditRole", roleID, req); err != nil {
		return nil, err
	}
	i, err := m.roleIndex(req.GuildID, roleID)
	if err != nil {
		return nil, err
	}
	r := &m.Roles[req.GuildID][i]
	applyRole(r, req)
	out := *r
	return &out, nil
}

func (m *MockClient) DeleteRole(_ context.Context, guildID, roleID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("DeleteRole", guildID, roleID, reason); err != nil {
		return err
	}
	i, err := m.roleIndex(guildID, roleID)
	if err != nil {
		return err
	}
	m.Roles[guildID] = slices.Delete(m.Roles[guildID], i, i+1)
	return nil
}

func (m *MockClient) memberRoles(guildID, userID, roleID string, add bool) error {
	i, err := m.roleIndex(guildID, roleID)
	if err != nil {
		return err
	}
	name := m.Roles[guildID][i].Name
	for j, mem := range m.Members[guildID] {
		if mem.UserID != userID {
			continue
		}
		roles := slices.DeleteFunc(slices.Clone(mem.Roles), func(r string) bool { return r == name })
		if add {
			roles = append(roles, name)
		}
		m.Members[guildID][j].Roles = roles
		return nil
	}
	return notFound("member", userID)
}

func (m *MockClient) AssignRole(_ context.Context, guildID, userID, roleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("AssignRole", guildID, userID, roleID); err != nil {
		return err
	}
	return m.memberRoles(guildID, userID, roleID, true)
}

func (m *MockClient) RemoveRole(_ context.Context, guildID, userID, roleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("RemoveRole", guildID, userID, roleID); err != nil {
		return err
	}
	return m.memberRoles(guildID, userID, roleID, false)
}

// ---------- Webhooks, Events & Interactions ----------

// BotUserID mirrors Client.BotUserID for role-hierarchy checks.
func (m *MockClient) BotUserID() string { return MockBotID }

func (m *MockClient) GetWebhook(_ context.Context, webhookID string) (*dto.WebhookInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("GetWebhook", webhookID); err != nil {
		return nil, err
	}
	w, ok := m.Webhooks[webhookID]
	if !ok {
		return nil, notFound("webhook", webhookID)
	}
	return &w, nil
}

func (m *MockClient) CreateWebhook(_ context.Context, channelID, name string) (*dto.WebhookInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateWebhook", channelID, name); err != nil {
		return nil, err
	}
	c, err := m.channel(channelID)
	if err != nil {
		return nil, err
	}
	w := dto.WebhookInfo{ID: m.newID(), ChannelID: channelID, GuildID: c.GuildID, Name: name}
	m.Webhooks[w.ID] = w
	return &w, nil
}

func (m *MockClient) EditWebhook(_ context.Context, webhookID, name, channelID string) (*dto.WebhookInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("EditWebhook", webhookID, name, channelID); err != nil {
		return nil, err
	}
	w, ok := m.Webhooks[webhookID]
	if !ok {
		return nil, notFound("webhook", webhookID)
	}
	if name != "" {
		w.Name = name
	}
	if channelID != "" {
		w.ChannelID = channelID
	}
	m.Webhooks[webhookID] = w
	return &w, nil
}

func (m *MockClient) DeleteWebhook(_ context.Context, webhookID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("DeleteWebhook", webhookID, reason); err != nil {
		return err
	}
	if _, ok := m.Webhooks[webhookID]; !ok {
		return notFound("webhook", webhookID)
	}
	delete(m.Webhooks, webhookID)
	return nil
}

func (m *MockClient) SendWebhookMessage(_ context.Context, req dto.WebhookMessageReq) (*dto.MessageItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("SendWebhookMessage", req); err != nil {
		return nil, err
	}
	channelID := ""
	if req.WebhookID != "" {
		w, ok := m.Webhooks[req.WebhookID]
		if !ok {
			return nil, notFound("webhook", req.WebhookID)
		}
		channelID = w.ChannelID
	} else if req.WebhookURL == "" {
		return nil, fmt.Errorf("webhook_id or webhook_url required")
	}
	if req.ThreadID != "" {
		channelID = req.ThreadID
	}
	username := req.Username
	if username == "" {
		username = "webhook"
	}
	msg := m.appendMessage(channelID, req.WebhookID, username, req.Content)
	return &msg, nil
}

func (m *MockClient) ListScheduledEvents(_ context.Context, guildID string) ([]dto.ScheduledEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("ListScheduledEvents", guildID); err != nil {
		return nil, err
	}
	return slices.Clone(m.Events[guildID]), nil
}

func (m *MockClient) CreateScheduledEvent(_ context.Context, req dto.CreateEventReq) (*dto.ScheduledEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.record("CreateScheduledEvent", req); err != nil {
		return nil, err
	}
	if _, ok := m.Guilds[req.GuildID]; !ok {
		return nil, notFound("guild", req.GuildID)
	}
	e := dto.ScheduledEvent{ID: m.newID(), GuildID: req.GuildID, Name: req.Name, Description: req.Description,
		StartTime: req.StartTimeISO, EndTime: req.EndTimeISO, EntityType: req.EntityType, Status: "scheduled"}
	if req.EntityType == dto.EventExternal {
		e.Location = req.LocationOrChannelID
	} else {
		e.ChannelID = req.LocationOrChannelID
	}
	m.Events[req.GuildID] = append(m.Events[req.GuildID], e)
	return &e, nil
}

// AwaitInteraction posts a prompt and returns NextInteraction immediately, or a
// timed-out result when NextInteraction is nil. It honours ctx cancellation.
func (m *MockClient) AwaitInteraction(ctx context.Context, req dto.AwaitInteractionReq) (*dto.InteractionResult, error) {
	m.mu.Lock()
	if err := m.record("AwaitInteraction", req); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	if _, err := m.channel(req.ChannelID); err != nil {
		m.mu.Unlock()
		return nil, err
	}
	msg := m.appendMessage(req.ChannelID, MockBotID, m.Session.BotUsername, req.PromptText)
	next := m.NextInteraction
	m.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if next == nil {
		return &dto.InteractionResult{MessageID: msg.ID, TimedOut: true}, nil
	}
	r := *next
	r.MessageID = msg.ID
	if r.RespondedAt == "" {
		r.RespondedAt = dto.FormatTime(m.Now())
	}
	return &r, nil
}

// ---------- utils ----------

func cmpStr(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// snowLess reports whether Snowflake a < b numerically.
func snowLess(a, b string) bool { return cmpStr(a, b) < 0 }
