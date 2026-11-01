package discord

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/dto"
)

// Pure conversion helpers from discordgo types to DTOs (spec 01 §5). None of
// these touch the network, so they are unit-tested directly.

// embedDescriptionMax is the summary length for embed descriptions.
const embedDescriptionMax = 280

// ---------- channels ----------

func channelTypeString(t discordgo.ChannelType) string {
	switch t {
	case discordgo.ChannelTypeGuildText:
		return dto.ChannelText
	case discordgo.ChannelTypeGuildVoice:
		return dto.ChannelVoice
	case discordgo.ChannelTypeGuildCategory:
		return dto.ChannelCategory
	case discordgo.ChannelTypeGuildNews:
		return dto.ChannelAnnouncement
	case discordgo.ChannelTypeGuildStageVoice:
		return dto.ChannelStage
	case discordgo.ChannelTypeGuildForum, discordgo.ChannelTypeGuildMedia:
		return dto.ChannelForum
	case discordgo.ChannelTypeGuildNewsThread, discordgo.ChannelTypeGuildPublicThread, discordgo.ChannelTypeGuildPrivateThread:
		return dto.ChannelThread
	case discordgo.ChannelTypeDM, discordgo.ChannelTypeGroupDM:
		return "dm"
	}
	return "other"
}

func channelTypeFromString(s string) (discordgo.ChannelType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", dto.ChannelText:
		return discordgo.ChannelTypeGuildText, nil
	case dto.ChannelVoice:
		return discordgo.ChannelTypeGuildVoice, nil
	case dto.ChannelCategory:
		return discordgo.ChannelTypeGuildCategory, nil
	case dto.ChannelAnnouncement:
		return discordgo.ChannelTypeGuildNews, nil
	case dto.ChannelStage:
		return discordgo.ChannelTypeGuildStageVoice, nil
	case dto.ChannelForum:
		return discordgo.ChannelTypeGuildForum, nil
	}
	return 0, fmt.Errorf("unsupported channel type %q (want text, voice, category, announcement, stage, or forum)", s)
}

func toChannelInfo(c *discordgo.Channel) dto.ChannelInfo {
	if c == nil {
		return dto.ChannelInfo{}
	}
	return dto.ChannelInfo{
		ID: c.ID, GuildID: c.GuildID, Name: c.Name, Type: channelTypeString(c.Type),
		ParentID: c.ParentID, Position: c.Position, Topic: c.Topic, NSFW: c.NSFW,
	}
}

// toThreadInfo converts a thread channel. tags maps forum tag IDs to names
// (may be nil, in which case raw tag IDs are reported).
func toThreadInfo(c *discordgo.Channel, tags map[string]string) dto.ThreadInfo {
	if c == nil {
		return dto.ThreadInfo{}
	}
	t := dto.ThreadInfo{
		ID: c.ID, ParentID: c.ParentID, Name: c.Name, OwnerID: c.OwnerID,
		Private: c.Type == discordgo.ChannelTypeGuildPrivateThread, MessageCount: c.MessageCount,
	}
	if c.ThreadMetadata != nil {
		t.Archived = c.ThreadMetadata.Archived
		t.Locked = c.ThreadMetadata.Locked
	}
	for _, id := range c.AppliedTags {
		if n, ok := tags[id]; ok {
			t.AppliedTags = append(t.AppliedTags, n)
		} else {
			t.AppliedTags = append(t.AppliedTags, id)
		}
	}
	if ts, err := dto.SnowflakeTime(c.ID); err == nil {
		t.CreatedAt = dto.FormatTime(ts)
	}
	return t
}

func forumTagNames(c *discordgo.Channel) map[string]string {
	if c == nil || len(c.AvailableTags) == 0 {
		return nil
	}
	m := make(map[string]string, len(c.AvailableTags))
	for _, t := range c.AvailableTags {
		m[t.ID] = t.Name
	}
	return m
}

// ---------- messages ----------

// nameLookup resolves IDs to display names for mention rewriting. *Resolver
// implements it; a nil lookup leaves unknown mentions untouched.
type nameLookup interface {
	userName(id string) (string, bool)
	roleName(id string) (string, bool)
	channelName(id string) (string, bool)
}

var mentionRe = regexp.MustCompile(`<(@!?|@&|#)(\d{15,20})>`)

// resolveMentions rewrites <@id>, <@!id>, <@&id>, and <#id> to @name, @role, and
// #channel. users supplies message-local user mentions; names is the cache.
func resolveMentions(content string, users map[string]string, names nameLookup) string {
	if !strings.Contains(content, "<") {
		return content
	}
	return mentionRe.ReplaceAllStringFunc(content, func(s string) string {
		sub := mentionRe.FindStringSubmatch(s)
		kind, id := sub[1], sub[2]
		switch kind {
		case "@", "@!":
			if n, ok := users[id]; ok {
				return "@" + n
			}
			if names != nil {
				if n, ok := names.userName(id); ok {
					return "@" + n
				}
			}
		case "@&":
			if names != nil {
				if n, ok := names.roleName(id); ok {
					return "@" + n
				}
			}
		case "#":
			if names != nil {
				if n, ok := names.channelName(id); ok {
					return "#" + n
				}
			}
		}
		return s
	})
}

func toMessageItem(m *discordgo.Message, names nameLookup) dto.MessageItem {
	if m == nil {
		return dto.MessageItem{}
	}
	users := map[string]string{}
	for _, u := range m.Mentions {
		if u != nil {
			users[u.ID] = u.Username
		}
	}
	item := dto.MessageItem{
		ID:        m.ID,
		ChannelID: m.ChannelID,
		Content:   dto.Truncate(resolveMentions(m.Content, users, names), dto.MaxContentChars),
		Timestamp: dto.FormatTime(m.Timestamp),
	}
	if item.Timestamp == "" {
		if ts, err := dto.SnowflakeTime(m.ID); err == nil {
			item.Timestamp = dto.FormatTime(ts)
		}
	}
	if m.Author != nil {
		item.AuthorID = m.Author.ID
		item.Author = m.Author.Username
	}
	if m.EditedTimestamp != nil {
		item.EditedAt = dto.FormatTime(*m.EditedTimestamp)
	}
	if m.MessageReference != nil && m.MessageReference.Type == discordgo.MessageReferenceTypeDefault &&
		m.MessageReference.MessageID != "" {
		item.ReplyToID = m.MessageReference.MessageID
	}
	for _, a := range m.Attachments {
		if a == nil {
			continue
		}
		item.Attachments = append(item.Attachments, dto.AttachmentRef{
			Filename: a.Filename, URL: a.URL, Size: a.Size, ContentType: a.ContentType,
		})
	}
	for _, e := range m.Embeds {
		if e != nil {
			item.Embeds = append(item.Embeds, toEmbedSummary(e))
		}
	}
	for _, r := range m.Reactions {
		if r == nil || r.Emoji == nil {
			continue
		}
		item.Reactions = append(item.Reactions, dto.ReactionCount{Emoji: emojiString(r.Emoji), Count: r.Count, Me: r.Me})
	}
	if m.Thread != nil {
		item.ThreadID = m.Thread.ID
	}
	return item
}

func toEmbedSummary(e *discordgo.MessageEmbed) dto.EmbedSummary {
	s := dto.EmbedSummary{
		Title: e.Title, Description: dto.Truncate(e.Description, embedDescriptionMax), URL: e.URL, Color: e.Color,
	}
	for _, f := range e.Fields {
		if f != nil {
			s.Fields = append(s.Fields, dto.EmbedField{Name: f.Name, Value: dto.Truncate(f.Value, embedDescriptionMax), Inline: f.Inline})
		}
	}
	if e.Footer != nil {
		s.Footer = e.Footer.Text
	}
	if e.Image != nil {
		s.ImageURL = e.Image.URL
	}
	return s
}

func toDiscordEmbed(e *dto.EmbedInput) *discordgo.MessageEmbed {
	if e == nil {
		return nil
	}
	out := &discordgo.MessageEmbed{
		Type: discordgo.EmbedTypeRich, Title: e.Title, Description: e.Description, URL: e.URL, Color: e.Color,
	}
	for _, f := range e.Fields {
		out.Fields = append(out.Fields, &discordgo.MessageEmbedField{Name: f.Name, Value: f.Value, Inline: f.Inline})
	}
	if e.Footer != "" {
		out.Footer = &discordgo.MessageEmbedFooter{Text: e.Footer}
	}
	if e.ImageURL != "" {
		out.Image = &discordgo.MessageEmbedImage{URL: e.ImageURL}
	}
	return out
}

func embedsOf(e *dto.EmbedInput) []*discordgo.MessageEmbed {
	if e == nil {
		return nil
	}
	return []*discordgo.MessageEmbed{toDiscordEmbed(e)}
}

// ---------- emoji ----------

// emojiString renders a reaction emoji as unicode or "name:id".
func emojiString(e *discordgo.Emoji) string {
	if e.ID == "" {
		return e.Name
	}
	return e.Name + ":" + e.ID
}

var customEmojiRe = regexp.MustCompile(`^<?a?:?([A-Za-z0-9_~]+):(\d{15,20})>?$`)

// parseCustomEmoji accepts "name:id", "<:name:id>", or "<a:name:id>".
func parseCustomEmoji(s string) (name, id string, ok bool) {
	m := customEmojiRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// shortcodeName returns "name" for ":name:" input.
func shortcodeName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) > 2 && strings.HasPrefix(s, ":") && strings.HasSuffix(s, ":") && !strings.Contains(s[1:len(s)-1], ":") {
		return s[1 : len(s)-1], true
	}
	return "", false
}

func toComponentEmoji(s string) *discordgo.ComponentEmoji {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if name, id, ok := parseCustomEmoji(s); ok {
		return &discordgo.ComponentEmoji{Name: name, ID: id, Animated: strings.HasPrefix(s, "<a:")}
	}
	return &discordgo.ComponentEmoji{Name: s}
}

// ---------- roles, members, permissions ----------

func colorHex(c int) string {
	if c == 0 {
		return ""
	}
	return fmt.Sprintf("#%06X", c&0xFFFFFF)
}

// permissionBitNames mirrors Discord's permission flags. Kept local so the
// discord package does not depend on the tool layer.
var permissionBitNames = []struct {
	bit  int64
	name string
}{
	{1 << 0, "CreateInstantInvite"}, {1 << 1, "KickMembers"}, {1 << 2, "BanMembers"}, {1 << 3, "Administrator"},
	{1 << 4, "ManageChannels"}, {1 << 5, "ManageGuild"}, {1 << 6, "AddReactions"}, {1 << 7, "ViewAuditLog"},
	{1 << 8, "PrioritySpeaker"}, {1 << 9, "Stream"}, {1 << 10, "ViewChannel"}, {1 << 11, "SendMessages"},
	{1 << 12, "SendTTSMessages"}, {1 << 13, "ManageMessages"}, {1 << 14, "EmbedLinks"}, {1 << 15, "AttachFiles"},
	{1 << 16, "ReadMessageHistory"}, {1 << 17, "MentionEveryone"}, {1 << 18, "UseExternalEmojis"}, {1 << 19, "ViewGuildInsights"},
	{1 << 20, "Connect"}, {1 << 21, "Speak"}, {1 << 22, "MuteMembers"}, {1 << 23, "DeafenMembers"},
	{1 << 24, "MoveMembers"}, {1 << 25, "UseVAD"}, {1 << 26, "ChangeNickname"}, {1 << 27, "ManageNicknames"},
	{1 << 28, "ManageRoles"}, {1 << 29, "ManageWebhooks"}, {1 << 30, "ManageGuildExpressions"}, {1 << 31, "UseApplicationCommands"},
	{1 << 32, "RequestToSpeak"}, {1 << 33, "ManageEvents"}, {1 << 34, "ManageThreads"}, {1 << 35, "CreatePublicThreads"},
	{1 << 36, "CreatePrivateThreads"}, {1 << 37, "UseExternalStickers"}, {1 << 38, "SendMessagesInThreads"}, {1 << 39, "UseEmbeddedActivities"},
	{1 << 40, "ModerateMembers"}, {1 << 41, "ViewCreatorMonetizationAnalytics"}, {1 << 42, "UseSoundboard"}, {1 << 43, "CreateGuildExpressions"},
	{1 << 44, "CreateEvents"}, {1 << 45, "UseExternalSounds"}, {1 << 46, "SendVoiceMessages"}, {1 << 49, "SendPolls"},
	{1 << 50, "UseExternalApps"}, {1 << 51, "PinMessages"},
}

func permNames(bits int64) []string {
	var out []string
	for _, p := range permissionBitNames {
		if bits&p.bit != 0 {
			out = append(out, p.name)
		}
	}
	sort.Strings(out)
	return out
}

func toRoleInfo(r *discordgo.Role) dto.RoleInfo {
	if r == nil {
		return dto.RoleInfo{}
	}
	info := dto.RoleInfo{
		ID: r.ID, Name: r.Name, Color: colorHex(r.Color), Position: r.Position,
		PermissionNames: permNames(r.Permissions), Hoist: r.Hoist, Mentionable: r.Mentionable, Managed: r.Managed,
	}
	if r.Permissions != 0 {
		info.Permissions = strconv.FormatInt(r.Permissions, 10)
	}
	return info
}

func sortRoles(roles []dto.RoleInfo) {
	slices.SortStableFunc(roles, func(a, b dto.RoleInfo) int {
		if a.Position != b.Position {
			return b.Position - a.Position
		}
		return cmpStr(a.ID, b.ID)
	})
}

// toMemberInfo converts a member; roleNames maps role IDs to names.
func toMemberInfo(m *discordgo.Member, roleNames func(string) (string, bool)) dto.MemberInfo {
	if m == nil || m.User == nil {
		return dto.MemberInfo{}
	}
	info := dto.MemberInfo{
		UserID: m.User.ID, Username: m.User.Username, Nick: m.Nick, Bot: m.User.Bot,
		JoinedAt: dto.FormatTime(m.JoinedAt),
	}
	for _, id := range m.Roles {
		if roleNames != nil {
			if n, ok := roleNames(id); ok {
				info.Roles = append(info.Roles, n)
				continue
			}
		}
		info.Roles = append(info.Roles, id)
	}
	if m.CommunicationDisabledUntil != nil && m.CommunicationDisabledUntil.After(time.Now()) {
		info.TimeoutUntil = dto.FormatTime(*m.CommunicationDisabledUntil)
	}
	return info
}

// ---------- audit log & automod ----------

var auditActionNames = map[int]string{
	1: "guild_update", 10: "channel_create", 11: "channel_update", 12: "channel_delete",
	13: "channel_overwrite_create", 14: "channel_overwrite_update", 15: "channel_overwrite_delete",
	20: "member_kick", 21: "member_prune", 22: "member_ban_add", 23: "member_ban_remove", 24: "member_update",
	25: "member_role_update", 26: "member_move", 27: "member_disconnect", 28: "bot_add",
	30: "role_create", 31: "role_update", 32: "role_delete", 40: "invite_create", 41: "invite_update", 42: "invite_delete",
	50: "webhook_create", 51: "webhook_update", 52: "webhook_delete", 60: "emoji_create", 61: "emoji_update", 62: "emoji_delete",
	72: "message_delete", 73: "message_bulk_delete", 74: "message_pin", 75: "message_unpin",
	80: "integration_create", 81: "integration_update", 82: "integration_delete",
	83: "stage_instance_create", 84: "stage_instance_update", 85: "stage_instance_delete",
	90: "sticker_create", 91: "sticker_update", 92: "sticker_delete",
	100: "guild_scheduled_event_create", 101: "guild_scheduled_event_update", 102: "guild_scheduled_event_delete",
	110: "thread_create", 111: "thread_update", 112: "thread_delete", 121: "application_command_permission_update",
	140: "auto_moderation_rule_create", 141: "auto_moderation_rule_update", 142: "auto_moderation_rule_delete",
	143: "auto_moderation_block_message", 144: "auto_moderation_flag_to_channel", 145: "auto_moderation_user_communication_disabled",
}

const auditValueMax = 80

func auditValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "∅"
	case string:
		return dto.Truncate(t, auditValueMax)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	case []any:
		parts := make([]string, 0, len(t))
		for _, x := range t {
			if m, ok := x.(map[string]any); ok {
				if n, ok := m["name"].(string); ok {
					parts = append(parts, n)
					continue
				}
				if id, ok := m["id"].(string); ok {
					parts = append(parts, id)
					continue
				}
			}
			parts = append(parts, auditValue(x))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	return dto.Truncate(fmt.Sprint(v), auditValueMax)
}

func toAuditLogEntry(e *discordgo.AuditLogEntry, users map[string]string) dto.AuditLogEntry {
	out := dto.AuditLogEntry{ID: e.ID, UserID: e.UserID, User: users[e.UserID], TargetID: e.TargetID, Reason: e.Reason}
	if e.ActionType != nil {
		out.ActionType = int(*e.ActionType)
		out.Action = auditActionNames[out.ActionType]
	}
	for _, c := range e.Changes {
		if c == nil || c.Key == nil {
			continue
		}
		key := string(*c.Key)
		switch {
		case c.OldValue == nil && c.NewValue != nil:
			out.Changes = append(out.Changes, key+": "+auditValue(c.NewValue))
		default:
			out.Changes = append(out.Changes, key+": "+auditValue(c.OldValue)+" → "+auditValue(c.NewValue))
		}
	}
	if ts, err := dto.SnowflakeTime(e.ID); err == nil {
		out.CreatedAt = dto.FormatTime(ts)
	}
	return out
}

var automodTriggerNames = map[int]string{1: "keyword", 2: "harmful_link", 3: "spam", 4: "keyword_preset", 5: "mention_spam", 6: "member_profile"}

var automodPresetNames = map[int]string{1: "preset:profanity", 2: "preset:sexual_content", 3: "preset:slurs"}

func toAutoModRule(r *discordgo.AutoModerationRule) dto.AutoModRule {
	out := dto.AutoModRule{ID: r.ID, Name: r.Name, TriggerType: automodTriggerNames[int(r.TriggerType)]}
	if out.TriggerType == "" {
		out.TriggerType = strconv.Itoa(int(r.TriggerType))
	}
	if r.Enabled != nil {
		out.Enabled = *r.Enabled
	}
	if md := r.TriggerMetadata; md != nil {
		out.Keywords = append(out.Keywords, md.KeywordFilter...)
		for _, p := range md.RegexPatterns {
			out.Keywords = append(out.Keywords, "regex:"+p)
		}
		for _, p := range md.Presets {
			out.Keywords = append(out.Keywords, automodPresetNames[int(p)])
		}
		if md.MentionTotalLimit > 0 {
			out.Keywords = append(out.Keywords, fmt.Sprintf("mention_limit:%d", md.MentionTotalLimit))
		}
	}
	for _, a := range r.Actions {
		switch a.Type {
		case discordgo.AutoModerationRuleActionBlockMessage:
			out.Actions = append(out.Actions, "block_message")
		case discordgo.AutoModerationRuleActionSendAlertMessage:
			s := "send_alert"
			if a.Metadata != nil && a.Metadata.ChannelID != "" {
				s += ":" + a.Metadata.ChannelID
			}
			out.Actions = append(out.Actions, s)
		case discordgo.AutoModerationRuleActionTimeout:
			s := "timeout"
			if a.Metadata != nil && a.Metadata.Duration > 0 {
				s += fmt.Sprintf(":%ds", a.Metadata.Duration)
			}
			out.Actions = append(out.Actions, s)
		default:
			out.Actions = append(out.Actions, fmt.Sprintf("action:%d", a.Type))
		}
	}
	if r.ExemptRoles != nil {
		out.ExemptRoles = slices.Clone(*r.ExemptRoles)
	}
	if r.ExemptChannels != nil {
		out.ExemptChannels = slices.Clone(*r.ExemptChannels)
	}
	return out
}

// ---------- events, webhooks, polls ----------

var eventStatusNames = map[discordgo.GuildScheduledEventStatus]string{
	discordgo.GuildScheduledEventStatusScheduled: "scheduled", discordgo.GuildScheduledEventStatusActive: "active",
	discordgo.GuildScheduledEventStatusCompleted: "completed", discordgo.GuildScheduledEventStatusCanceled: "canceled",
}

func eventEntityTypeString(t discordgo.GuildScheduledEventEntityType) string {
	switch t {
	case discordgo.GuildScheduledEventEntityTypeStageInstance:
		return dto.EventStage
	case discordgo.GuildScheduledEventEntityTypeVoice:
		return dto.EventVoice
	case discordgo.GuildScheduledEventEntityTypeExternal:
		return dto.EventExternal
	}
	return ""
}

func eventEntityTypeFromString(s string) (discordgo.GuildScheduledEventEntityType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case dto.EventStage:
		return discordgo.GuildScheduledEventEntityTypeStageInstance, nil
	case dto.EventVoice:
		return discordgo.GuildScheduledEventEntityTypeVoice, nil
	case dto.EventExternal:
		return discordgo.GuildScheduledEventEntityTypeExternal, nil
	}
	return 0, fmt.Errorf("unsupported entity_type %q (want stage, voice, or external)", s)
}

func toScheduledEvent(e *discordgo.GuildScheduledEvent) dto.ScheduledEvent {
	out := dto.ScheduledEvent{
		ID: e.ID, GuildID: e.GuildID, Name: e.Name, Description: e.Description,
		StartTime: dto.FormatTime(e.ScheduledStartTime), EntityType: eventEntityTypeString(e.EntityType),
		ChannelID: e.ChannelID, Location: e.EntityMetadata.Location, Status: eventStatusNames[e.Status],
		InterestedCount: e.UserCount,
	}
	if e.ScheduledEndTime != nil {
		out.EndTime = dto.FormatTime(*e.ScheduledEndTime)
	}
	return out
}

// toWebhookInfo never copies the token.
func toWebhookInfo(w *discordgo.Webhook) dto.WebhookInfo {
	if w == nil {
		return dto.WebhookInfo{}
	}
	info := dto.WebhookInfo{ID: w.ID, ChannelID: w.ChannelID, GuildID: w.GuildID, Name: w.Name}
	if w.Avatar != "" {
		info.AvatarURL = discordgo.EndpointCDN + "avatars/" + w.ID + "/" + w.Avatar + ".png"
	}
	return info
}

func toPollResult(m *discordgo.Message) dto.PollResult {
	p := m.Poll
	out := dto.PollResult{MessageID: m.ID, Question: p.Question.Text}
	counts := map[int]int{}
	if p.Results != nil {
		out.Finalized = p.Results.Finalized
		for _, c := range p.Results.AnswerCounts {
			if c != nil {
				counts[c.ID] = c.Count
			}
		}
	}
	best := 0
	for _, a := range p.Answers {
		text := ""
		if a.Media != nil {
			text = a.Media.Text
		}
		v := counts[a.AnswerID]
		out.Answers = append(out.Answers, dto.PollAnswerTally{ID: a.AnswerID, Text: text, Votes: v})
		out.TotalVotes += v
		best = max(best, v)
	}
	if best > 0 {
		for _, a := range out.Answers {
			if a.Votes == best {
				out.Winners = append(out.Winners, a.Text)
			}
		}
	}
	if p.Expiry != nil {
		out.ExpiresAt = dto.FormatTime(*p.Expiry)
	}
	return out
}

// ---------- webhook URLs ----------

var webhookURLRe = regexp.MustCompile(`^https://(?:(?:ptb|canary)\.)?(?:discord|discordapp)\.com/api(?:/v\d+)?/webhooks/(\d{15,20})/([A-Za-z0-9_\-]+)/?$`)

// parseWebhookURL extracts the ID and token from a Discord webhook URL. The
// error never echoes the URL (it contains a secret).
func parseWebhookURL(u string) (id, token string, err error) {
	m := webhookURLRe.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return "", "", fmt.Errorf("invalid webhook_url: must match https://discord.com/api/webhooks/{id}/{token}")
	}
	return m[1], m[2], nil
}
