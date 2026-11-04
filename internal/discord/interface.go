// Package discord defines the Service contract used by every MCP tool, plus a
// deterministic mock. The live discordgo implementation lives in client.go.
// The interface is LOCKED after Wave 1 (specs/01-architecture-and-contracts.md §4).
package discord

import (
	"context"
	"errors"

	"github.com/local/discord-mcp/internal/dto"
)

// Sentinel errors. Implementations wrap these with %w.
var (
	ErrNotFound    = errors.New("not found")
	ErrAmbiguous   = errors.New("ambiguous reference")
	ErrForbidden   = errors.New("forbidden")
	ErrRateLimited = errors.New("rate limited")
	ErrNotLoggedIn = errors.New("not logged in")
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
	// GetChannel returns one channel or thread (GuildID and ParentID populated) so tools
	// can derive the guild for policy checks, a thread's parent, and dry-run "before" state.
	GetChannel(ctx context.Context, channelID string) (*dto.ChannelInfo, error)
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
	// GetMessage re-fetches one message via REST so its signed CDN attachment
	// URLs are fresh. Added for the media resolver (spec 05).
	GetMessage(ctx context.Context, channelID, messageID string) (*dto.MessageItem, error)

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
	// GetMember returns one member's profile (avatar hashes, nick, roles).
	// userID "" means the bot itself. Added for avatars and access checks (spec 05).
	GetMember(ctx context.Context, guildID, userID string) (*dto.MemberProfile, error)
	TimeoutMember(ctx context.Context, guildID, userID string, durationSeconds int, reason string) error
	ListRoles(ctx context.Context, guildID string) ([]dto.RoleInfo, error)
	CreateRole(ctx context.Context, req dto.RoleMutationReq) (*dto.RoleInfo, error)
	EditRole(ctx context.Context, roleID string, req dto.RoleMutationReq) (*dto.RoleInfo, error)
	DeleteRole(ctx context.Context, guildID, roleID, reason string) error
	AssignRole(ctx context.Context, guildID, userID, roleID string) error
	RemoveRole(ctx context.Context, guildID, userID, roleID string) error

	// Webhooks, Personas, Events & HITL Interactions
	// GetWebhook was added in Wave 3 so webhook tools can scope policy checks to the
	// webhook's real channel and guild. It never returns the token.
	GetWebhook(ctx context.Context, webhookID string) (*dto.WebhookInfo, error)
	CreateWebhook(ctx context.Context, channelID, name string) (*dto.WebhookInfo, error)
	EditWebhook(ctx context.Context, webhookID, name, channelID string) (*dto.WebhookInfo, error)
	DeleteWebhook(ctx context.Context, webhookID, reason string) error
	SendWebhookMessage(ctx context.Context, req dto.WebhookMessageReq) (*dto.MessageItem, error)
	ListScheduledEvents(ctx context.Context, guildID string) ([]dto.ScheduledEvent, error)
	CreateScheduledEvent(ctx context.Context, req dto.CreateEventReq) (*dto.ScheduledEvent, error)
	AwaitInteraction(ctx context.Context, req dto.AwaitInteractionReq) (*dto.InteractionResult, error)
}
