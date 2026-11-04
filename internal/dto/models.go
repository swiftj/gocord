// Package dto defines token-compacted data transfer objects returned by MCP
// tools and resources. Rules (see specs/01-architecture-and-contracts.md §5):
// snake_case JSON tags, omitempty on optional fields, string Snowflakes,
// RFC 3339 UTC time strings, and no raw discordgo types.
package dto

// ---------- Session & Guilds ----------

type SessionInfo struct {
	Profile     string `json:"profile"`
	BotUserID   string `json:"bot_user_id"`
	BotUsername string `json:"bot_username"`
	GuildCount  int    `json:"guild_count"`
}

type GuildSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count,omitempty"`
	OwnerID     string `json:"owner_id,omitempty"`
}

type GuildDetail struct {
	GuildSummary
	Description string        `json:"description,omitempty"`
	BoostTier   int           `json:"boost_tier,omitempty"`
	BoostCount  int           `json:"boost_count,omitempty"`
	Features    []string      `json:"features,omitempty"`
	Channels    []ChannelInfo `json:"channels,omitempty"`
	Roles       []RoleInfo    `json:"roles,omitempty"`
	CreatedAt   string        `json:"created_at,omitempty"`
}

type AuditLogEntry struct {
	ID         string   `json:"id"`
	ActionType int      `json:"action_type"`
	Action     string   `json:"action,omitempty"`
	UserID     string   `json:"user_id,omitempty"`
	User       string   `json:"user,omitempty"`
	TargetID   string   `json:"target_id,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Changes    []string `json:"changes,omitempty"`
	CreatedAt  string   `json:"created_at,omitempty"`
}

type AutoModRule struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	TriggerType    string   `json:"trigger_type,omitempty"`
	Enabled        bool     `json:"enabled"`
	Keywords       []string `json:"keywords,omitempty"`
	Actions        []string `json:"actions,omitempty"`
	ExemptRoles    []string `json:"exempt_roles,omitempty"`
	ExemptChannels []string `json:"exempt_channels,omitempty"`
}

// ---------- Channels ----------

// Channel type strings used in ChannelInfo.Type and CreateChannelReq.Type.
const (
	ChannelText         = "text"
	ChannelVoice        = "voice"
	ChannelCategory     = "category"
	ChannelAnnouncement = "announcement"
	ChannelStage        = "stage"
	ChannelForum        = "forum"
	ChannelThread       = "thread"
)

type ChannelInfo struct {
	ID       string `json:"id"`
	GuildID  string `json:"guild_id,omitempty"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	ParentID string `json:"parent_id,omitempty"`
	Position int    `json:"position,omitempty"`
	Topic    string `json:"topic,omitempty"`
	NSFW     bool   `json:"nsfw,omitempty"`
	// Overwrites are the channel's permission overwrites. They feed
	// discord_check_access and are never serialised into tool output.
	Overwrites []PermissionOverwrite `json:"-"`
}

// PermissionOverwrite is one channel permission overwrite. Type is "role" or "member".
type PermissionOverwrite struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Allow int64  `json:"allow"`
	Deny  int64  `json:"deny"`
}

type CreateChannelReq struct {
	GuildID   string `json:"guild_id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	ParentID  string `json:"parent_id,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Position  int    `json:"position,omitempty"`
	Bitrate   int    `json:"bitrate,omitempty"`
	UserLimit int    `json:"user_limit,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type EditChannelReq struct {
	ChannelID string  `json:"channel_id"`
	Name      *string `json:"name,omitempty"`
	Topic     *string `json:"topic,omitempty"`
	ParentID  *string `json:"parent_id,omitempty"`
	Archived  *bool   `json:"archived,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

// Permission overwrite target types.
const (
	TargetRole   = "role"
	TargetMember = "member"
)

type ChannelPermissionReq struct {
	ChannelID  string `json:"channel_id"`
	TargetID   string `json:"target_id"`
	TargetType string `json:"target_type"`
	Allow      int64  `json:"allow,omitempty"`
	Deny       int64  `json:"deny,omitempty"`
	Remove     bool   `json:"remove,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// ---------- Messages ----------

// MaxContentChars is the threshold above which message content is truncated in outputs.
const MaxContentChars = 4000

type MessageItem struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id,omitempty"`
	AuthorID  string `json:"author_id,omitempty"`
	Author    string `json:"author,omitempty"`
	// AuthorDisplayName is the name as displayed: the webhook username for
	// webhook messages (e.g. a Tupperbox character), else nick > global name > username.
	AuthorDisplayName string `json:"author_display_name,omitempty"`
	// AuthorAvatarURL is the avatar as displayed (see AvatarURL). Like
	// attachment URLs it is a CDN URL; re-resolve it rather than cache it.
	AuthorAvatarURL string `json:"author_avatar_url,omitempty"`
	// IsWebhook is true when the message was posted through a webhook
	// (Tupperbox, PluralKit, persona webhooks).
	IsWebhook   bool            `json:"is_webhook"`
	WebhookID   string          `json:"webhook_id,omitempty"`
	Content     string          `json:"content,omitempty"`
	Timestamp   string          `json:"timestamp,omitempty"`
	EditedAt    string          `json:"edited_at,omitempty"`
	ReplyToID   string          `json:"reply_to_id,omitempty"`
	Attachments []AttachmentRef `json:"attachments,omitempty"`
	Embeds      []EmbedSummary  `json:"embeds,omitempty"`
	Reactions   []ReactionCount `json:"reactions,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
}

// AttachmentRef is one message attachment. URL and ProxyURL are signed Discord
// CDN URLs (?ex=&is=&hm=) that expire, typically after about 24 hours:
// re-resolve them from channel_id + message_id instead of caching them.
type AttachmentRef struct {
	ID          string `json:"id,omitempty"`
	Filename    string `json:"filename"`
	URL         string `json:"url,omitempty"`
	ProxyURL    string `json:"proxy_url,omitempty"`
	Size        int    `json:"size,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Width       int    `json:"width,omitempty"`
	Height      int    `json:"height,omitempty"`
}

type ReactionCount struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Me    bool   `json:"me,omitempty"`
}

type EmbedField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

// EmbedSummary is the compact output form of an embed.
type EmbedSummary struct {
	Title       string       `json:"title,omitempty"`
	Description string       `json:"description,omitempty"`
	URL         string       `json:"url,omitempty"`
	Color       int          `json:"color,omitempty"`
	Fields      []EmbedField `json:"fields,omitempty"`
	Footer      string       `json:"footer,omitempty"`
	ImageURL    string       `json:"image_url,omitempty"`
}

// EmbedInput is the input form of an embed accepted by send tools.
type EmbedInput EmbedSummary

type SendMessageReq struct {
	ChannelID        string      `json:"channel_id"`
	Content          string      `json:"content,omitempty"`
	Embed            *EmbedInput `json:"embed,omitempty"`
	FilePath         string      `json:"file_path,omitempty"`
	ReplyToMessageID string      `json:"reply_to_message_id,omitempty"`
}

// ReadMessagesReq cursors are Snowflakes; the tool layer converts ISO-8601 input.
type ReadMessagesReq struct {
	ChannelID string `json:"channel_id"`
	Limit     int    `json:"limit,omitempty"`
	Before    string `json:"before,omitempty"`
	After     string `json:"after,omitempty"`
	Around    string `json:"around,omitempty"`
}

// ---------- Polls ----------

type CreatePollReq struct {
	ChannelID        string   `json:"channel_id"`
	Question         string   `json:"question"`
	Answers          []string `json:"answers"`
	DurationHours    int      `json:"duration_hours,omitempty"`
	AllowMultiselect bool     `json:"allow_multiselect,omitempty"`
}

type PollAnswerTally struct {
	ID    int    `json:"id"`
	Text  string `json:"text"`
	Votes int    `json:"votes"`
}

type PollResult struct {
	MessageID  string            `json:"message_id"`
	Question   string            `json:"question"`
	Answers    []PollAnswerTally `json:"answers"`
	TotalVotes int               `json:"total_votes"`
	Finalized  bool              `json:"finalized,omitempty"`
	Winners    []string          `json:"winners,omitempty"`
	ExpiresAt  string            `json:"expires_at,omitempty"`
}

// ---------- Forums & Threads ----------

type CreateForumReq struct {
	GuildID         string   `json:"guild_id"`
	Name            string   `json:"name"`
	Topic           string   `json:"topic,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	ParentID        string   `json:"parent_id,omitempty"`
	DefaultReaction string   `json:"default_reaction,omitempty"`
}

type CreateForumPostReq struct {
	ForumChannelID string   `json:"forum_channel_id"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	TagNames       []string `json:"tag_names,omitempty"`
}

type ThreadInfo struct {
	ID           string   `json:"id"`
	ParentID     string   `json:"parent_id,omitempty"`
	Name         string   `json:"name"`
	OwnerID      string   `json:"owner_id,omitempty"`
	Archived     bool     `json:"archived,omitempty"`
	Locked       bool     `json:"locked,omitempty"`
	Private      bool     `json:"private,omitempty"`
	MessageCount int      `json:"message_count,omitempty"`
	AppliedTags  []string `json:"applied_tags,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
}

type ForumPostDetail struct {
	Thread  ThreadInfo    `json:"thread"`
	Starter *MessageItem  `json:"starter,omitempty"`
	Replies []MessageItem `json:"replies,omitempty"`
}

type CreateThreadReq struct {
	ChannelID          string `json:"channel_id"`
	MessageID          string `json:"message_id,omitempty"`
	Name               string `json:"name"`
	Private            bool   `json:"private,omitempty"`
	AutoArchiveMinutes int    `json:"auto_archive_minutes,omitempty"`
}

// ---------- Members & Roles ----------

type MemberInfo struct {
	UserID       string   `json:"user_id"`
	Username     string   `json:"username"`
	Nick         string   `json:"nick,omitempty"`
	Roles        []string `json:"roles,omitempty"`
	JoinedAt     string   `json:"joined_at,omitempty"`
	Bot          bool     `json:"bot,omitempty"`
	TimeoutUntil string   `json:"timeout_until,omitempty"`
}

type RoleInfo struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Color           string   `json:"color,omitempty"` // "#RRGGBB"
	Position        int      `json:"position,omitempty"`
	Permissions     string   `json:"permissions,omitempty"` // decimal bitfield
	PermissionNames []string `json:"permission_names,omitempty"`
	Hoist           bool     `json:"hoist,omitempty"`
	Mentionable     bool     `json:"mentionable,omitempty"`
	Managed         bool     `json:"managed,omitempty"`
}

type RoleMutationReq struct {
	GuildID     string  `json:"guild_id"`
	Name        *string `json:"name,omitempty"`
	Color       *int    `json:"color,omitempty"`
	Permissions *int64  `json:"permissions,omitempty"`
	Hoist       *bool   `json:"hoist,omitempty"`
	Mentionable *bool   `json:"mentionable,omitempty"`
	Reason      string  `json:"reason,omitempty"`
}

// ---------- Webhooks & Personas ----------

// WebhookInfo never carries the webhook token.
type WebhookInfo struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id,omitempty"`
	GuildID   string `json:"guild_id,omitempty"`
	Name      string `json:"name,omitempty"`
	AvatarURL string `json:"avatar_url,omitempty"`
}

type WebhookMessageReq struct {
	WebhookID  string      `json:"webhook_id,omitempty"`
	WebhookURL string      `json:"-"` // contains a token; never serialised
	Content    string      `json:"content,omitempty"`
	Username   string      `json:"username,omitempty"`
	AvatarURL  string      `json:"avatar_url,omitempty"`
	ThreadID   string      `json:"thread_id,omitempty"`
	Embed      *EmbedInput `json:"embed,omitempty"`
}

type Persona struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	AvatarURL        string `json:"avatar_url,omitempty"`
	Description      string `json:"description,omitempty"`
	DefaultChannelID string `json:"default_channel_id,omitempty"`
	CreatedAt        string `json:"created_at,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

// ---------- Scheduled Events ----------

// Scheduled event entity types.
const (
	EventStage    = "stage"
	EventVoice    = "voice"
	EventExternal = "external"
)

type ScheduledEvent struct {
	ID              string `json:"id"`
	GuildID         string `json:"guild_id,omitempty"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	StartTime       string `json:"start_time,omitempty"`
	EndTime         string `json:"end_time,omitempty"`
	EntityType      string `json:"entity_type,omitempty"`
	ChannelID       string `json:"channel_id,omitempty"`
	Location        string `json:"location,omitempty"`
	Status          string `json:"status,omitempty"`
	InterestedCount int    `json:"interested_count,omitempty"`
}

type CreateEventReq struct {
	GuildID             string `json:"guild_id"`
	Name                string `json:"name"`
	Description         string `json:"description,omitempty"`
	StartTimeISO        string `json:"start_time_iso"`
	EndTimeISO          string `json:"end_time_iso,omitempty"`
	EntityType          string `json:"entity_type"`
	LocationOrChannelID string `json:"location_or_channel_id,omitempty"`
}

// ---------- HITL Interactions ----------

// Interaction modes.
const (
	ModeButtons = "buttons"
	ModeSelect  = "select"
	ModeModal   = "modal"
)

type InteractionOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Style string `json:"style,omitempty"` // primary|secondary|success|danger
	Emoji string `json:"emoji,omitempty"`
}

type AwaitInteractionReq struct {
	ChannelID      string              `json:"channel_id"`
	PromptText     string              `json:"prompt_text"`
	Mode           string              `json:"mode,omitempty"`
	Options        []InteractionOption `json:"options,omitempty"`
	AllowedUserIDs []string            `json:"allowed_user_ids,omitempty"`
	AllowedRoleIDs []string            `json:"allowed_role_ids,omitempty"`
	TimeoutSeconds int                 `json:"timeout_seconds,omitempty"`
	MinValues      int                 `json:"min_values,omitempty"`
	MaxValues      int                 `json:"max_values,omitempty"`
}

// Defaults and bounds for AwaitInteractionReq.TimeoutSeconds.
const (
	DefaultInteractionTimeout = 300
	MaxInteractionTimeout     = 900
)

type InteractionResult struct {
	MessageID   string            `json:"message_id,omitempty"`
	Selected    []string          `json:"selected,omitempty"`
	UserID      string            `json:"user_id,omitempty"`
	Username    string            `json:"username,omitempty"`
	ModalFields map[string]string `json:"modal_fields,omitempty"`
	TimedOut    bool              `json:"timed_out,omitempty"`
	RespondedAt string            `json:"responded_at,omitempty"`
}

// ---------- Search ----------

type SearchQuery struct {
	GuildID        string `json:"guild_id"`
	Query          string `json:"query"`
	ChannelID      string `json:"channel_id,omitempty"`
	AuthorID       string `json:"author_id,omitempty"`
	Before         string `json:"before,omitempty"` // Snowflake
	After          string `json:"after,omitempty"`  // Snowflake
	HasAttachments *bool  `json:"has_attachments,omitempty"`
	Limit          int    `json:"limit,omitempty"`
}

// Search limits.
const (
	DefaultSearchLimit = 25
	MaxSearchLimit     = 100
)

type SearchHit struct {
	MessageItem
	Snippet string  `json:"snippet,omitempty"`
	Rank    float64 `json:"rank,omitempty"`
}

// ---------- Governance ----------

type DryRunPreview struct {
	DryRun       bool     `json:"dry_run"`
	Action       string   `json:"action"`
	Target       string   `json:"target,omitempty"`
	Changes      []string `json:"changes,omitempty"`
	Warnings     []string `json:"warnings,omitempty"`
	Allowed      bool     `json:"allowed"`
	DeniedReason string   `json:"denied_reason,omitempty"`
}
