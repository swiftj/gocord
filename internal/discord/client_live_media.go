package discord

import (
	"context"
	"errors"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/dto"
)

// maxMemberWarm bounds the member lookups one message read may trigger to fill
// guild nicks and guild avatars that REST message objects omit.
const maxMemberWarm = 10

// GetMessage re-fetches one message so its signed CDN URLs are fresh.
func (c *Client) GetMessage(ctx context.Context, channelID, messageID string) (*dto.MessageItem, error) {
	s, err := c.sess()
	if err != nil {
		return nil, err
	}
	m, err := channelMessage(ctx, s, channelID, messageID)
	if err != nil {
		return nil, mapErr("get message", err)
	}
	c.warmMembers(ctx, []*discordgo.Message{m})
	return c.toItem(m), nil
}

// GetMember returns a member's profile; userID "" means the bot itself.
func (c *Client) GetMember(ctx context.Context, guildID, userID string) (*dto.MemberProfile, error) {
	if userID == "" {
		userID = c.botUserID()
		if userID == "" {
			return nil, ErrNotLoggedIn
		}
	}
	m, err := c.restGuildMember(ctx, guildID, userID)
	if err != nil {
		return nil, err
	}
	if m == nil || m.User == nil {
		return nil, ErrNotFound
	}
	c.resolver.applyMember(guildID, m)
	return &dto.MemberProfile{
		GuildID: guildID, UserID: m.User.ID, Username: m.User.Username, GlobalName: m.User.GlobalName,
		Nick: m.Nick, Discriminator: m.User.Discriminator, Avatar: m.User.Avatar, GuildAvatar: m.Avatar,
		Roles: m.Roles, Bot: m.User.Bot,
	}, nil
}

// warmMembers caches the guild members behind a batch of REST messages so
// their nicks and guild avatars resolve. It skips webhooks and bots, makes at
// most maxMemberWarm lookups, and ignores failures (e.g. authors who left).
func (c *Client) warmMembers(ctx context.Context, ms []*discordgo.Message) {
	seen := map[string]bool{}
	lookups := 0
	for _, m := range ms {
		if m == nil || m.Author == nil || m.WebhookID != "" || m.Author.Bot || seen[m.Author.ID] {
			continue
		}
		seen[m.Author.ID] = true
		guildID := m.GuildID
		if guildID == "" {
			guildID = c.resolver.channelGuild(m.ChannelID)
		}
		if guildID == "" {
			continue
		}
		if _, ok := c.resolver.member(guildID, m.Author.ID); ok {
			continue
		}
		if lookups >= maxMemberWarm {
			return
		}
		lookups++
		mem, err := c.restGuildMember(ctx, guildID, m.Author.ID)
		if err != nil {
			if errors.Is(err, ErrRateLimited) || ctx.Err() != nil {
				return
			}
			continue
		}
		c.resolver.applyMember(guildID, mem)
	}
}

// noteMessageMember caches the partial member a Gateway message carries.
func (c *Client) noteMessageMember(m *discordgo.Message) {
	if m == nil || m.Member == nil || m.Author == nil || m.GuildID == "" || m.WebhookID != "" {
		return
	}
	mem := *m.Member
	mem.User = m.Author
	c.resolver.applyMember(m.GuildID, &mem)
}
