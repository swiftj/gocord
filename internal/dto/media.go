package dto

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------- Avatars ----------

// CDNBase is the Discord CDN origin used for avatar URLs.
const CDNBase = "https://cdn.discordapp.com"

// DefaultAvatarSize is the size requested for every avatar URL.
const DefaultAvatarSize = 1024

// AvatarSizes are the sizes discord_get_member_avatar accepts.
var AvatarSizes = []int{128, 256, 512, 1024, 2048, 4096}

// avatarExt is .gif for an animated hash ("a_" prefix), else .png.
func avatarExt(hash string) string {
	if IsAnimatedHash(hash) {
		return "gif"
	}
	return "png"
}

// IsAnimatedHash reports whether an avatar hash is animated.
func IsAnimatedHash(hash string) bool { return strings.HasPrefix(hash, "a_") }

// UserAvatarURL is a user's (or a webhook message author's) avatar URL. An
// empty hash yields the default embed avatar. size <= 0 means DefaultAvatarSize.
func UserAvatarURL(userID, hash, discriminator string, size int) string {
	if hash == "" {
		return DefaultAvatarURL(userID, discriminator, size)
	}
	if size <= 0 {
		size = DefaultAvatarSize
	}
	return fmt.Sprintf("%s/avatars/%s/%s.%s?size=%d", CDNBase, userID, hash, avatarExt(hash), size)
}

// GuildAvatarURL is a member's guild-specific avatar URL ("" when hash is empty).
func GuildAvatarURL(guildID, userID, hash string, size int) string {
	if hash == "" {
		return ""
	}
	if size <= 0 {
		size = DefaultAvatarSize
	}
	return fmt.Sprintf("%s/guilds/%s/users/%s/avatars/%s.%s?size=%d", CDNBase, guildID, userID, hash, avatarExt(hash), size)
}

// DefaultAvatarURL is Discord's default embed avatar: discriminator % 5 for
// legacy usernames, (id >> 22) % 6 for migrated ones (discriminator "0" or "").
func DefaultAvatarURL(userID, discriminator string, size int) string {
	if size <= 0 {
		size = DefaultAvatarSize
	}
	idx := uint64(0)
	if d, err := strconv.ParseUint(discriminator, 10, 64); err == nil && d != 0 {
		idx = d % 5
	} else if id, err := strconv.ParseUint(userID, 10, 64); err == nil {
		idx = (id >> 22) % 6
	}
	return fmt.Sprintf("%s/embed/avatars/%d.png?size=%d", CDNBase, idx, size)
}

// DisplayName returns nick > global name > username.
func DisplayName(nick, globalName, username string) string {
	switch {
	case nick != "":
		return nick
	case globalName != "":
		return globalName
	}
	return username
}

// MemberProfile is the identity data needed to render a member's name and avatar.
// GuildAvatar and Avatar are raw CDN hashes; Nick is empty for non-members.
type MemberProfile struct {
	GuildID       string   `json:"guild_id,omitempty"`
	UserID        string   `json:"user_id"`
	Username      string   `json:"username"`
	GlobalName    string   `json:"global_name,omitempty"`
	Nick          string   `json:"nick,omitempty"`
	Discriminator string   `json:"discriminator,omitempty"`
	Avatar        string   `json:"avatar,omitempty"`
	GuildAvatar   string   `json:"guild_avatar,omitempty"`
	Roles         []string `json:"roles,omitempty"`
	Bot           bool     `json:"bot,omitempty"`
}

// MemberAvatar is the output of discord_get_member_avatar.
type MemberAvatar struct {
	UserID         string `json:"user_id"`
	DisplayName    string `json:"display_name"`
	AvatarURL      string `json:"avatar_url"`
	GuildAvatarURL string `json:"guild_avatar_url,omitempty"`
	Animated       bool   `json:"animated"`
}

// ---------- Tupperbox / webhook authors ----------

// TupperQuery filters the index scan behind discord_list_tuppers.
type TupperQuery struct {
	GuildID   string
	ChannelID string // "" = whole guild; matches thread parents too
	After     string // Snowflake lower bound ("" = no bound)
	Limit     int
}

// TupperInfo is one webhook identity (webhook_id + display name) seen in the index.
type TupperInfo struct {
	DisplayName       string `json:"display_name"`
	AvatarURL         string `json:"avatar_url,omitempty"`
	WebhookID         string `json:"webhook_id"`
	LastSeenMessageID string `json:"last_seen_message_id"`
	LastSeenChannelID string `json:"last_seen_channel_id"`
	LastSeenAt        string `json:"last_seen_at"`
	MessageCount      int    `json:"message_count"`
}

// ---------- Access diagnostics ----------

// ChannelAccess is one row of discord_check_access.
type ChannelAccess struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Category       string   `json:"category,omitempty"`
	CanView        bool     `json:"can_view"`
	CanReadHistory bool     `json:"can_read_history"`
	Missing        []string `json:"missing,omitempty"`
	Fix            string   `json:"fix,omitempty"`
}

// AccessSummary counts readable and blocked channels.
type AccessSummary struct {
	Readable int `json:"readable"`
	Blocked  int `json:"blocked"`
}
