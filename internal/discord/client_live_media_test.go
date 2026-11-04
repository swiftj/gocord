package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/dto"
)

func TestToMessageItemIdentity(t *testing.T) {
	const (
		hook    = "100000000000000700"
		carol   = "100000000000000053"
		dave    = "100000000000000054"
		cdnBase = "https://cdn.discordapp.com"
	)
	r := seededResolver(nil)
	r.applyMember(tGuild, &discordgo.Member{
		User: &discordgo.User{ID: carol, Username: "carol", GlobalName: "Carol C", Avatar: "u1"},
		Nick: "Captain Carol", Avatar: "a_g1",
	})

	tests := []struct {
		name                         string
		msg                          *discordgo.Message
		webhook                      bool
		display, avatar              string
		attachW, attachH, attachSize int
	}{
		{
			name: "tupperbox webhook uses per-message name and art",
			msg: &discordgo.Message{ID: "100000000000000120", ChannelID: tGeneral, WebhookID: hook,
				Author: &discordgo.User{ID: hook, Username: "Ser Thaddeus Mournvale", Avatar: "abc123", Discriminator: "0000"},
				Attachments: []*discordgo.MessageAttachment{{ID: "100000000000000500", Filename: "map.png",
					URL: cdnBase + "/attachments/1/2/map.png?ex=1&is=2&hm=3", ProxyURL: "https://media.discordapp.net/attachments/1/2/map.png",
					Width: 800, Height: 600, Size: 1234, ContentType: "image/png"}}},
			webhook: true, display: "Ser Thaddeus Mournvale",
			avatar:  cdnBase + "/avatars/" + hook + "/abc123.png?size=1024",
			attachW: 800, attachH: 600, attachSize: 1234,
		},
		{
			name: "webhook without avatar falls back to a default",
			msg: &discordgo.Message{ID: "100000000000000121", ChannelID: tGeneral, WebhookID: hook,
				Author: &discordgo.User{ID: hook, Username: "Nameless", Discriminator: "0000"}},
			webhook: true, display: "Nameless", avatar: dto.DefaultAvatarURL(hook, "0000", 0),
		},
		{
			name: "cached member: nick and animated guild avatar win",
			msg: &discordgo.Message{ID: "100000000000000122", ChannelID: tGeneral,
				Author: &discordgo.User{ID: carol, Username: "carol", GlobalName: "Carol C", Avatar: "u1"}},
			display: "Captain Carol",
			avatar:  cdnBase + "/guilds/" + tGuild + "/users/" + carol + "/avatars/a_g1.gif?size=1024",
		},
		{
			name: "gateway member on the message",
			msg: &discordgo.Message{ID: "100000000000000123", ChannelID: tGeneral, GuildID: tGuild,
				Author: &discordgo.User{ID: dave, Username: "dave", Avatar: "a_u2"},
				Member: &discordgo.Member{Nick: "Dee"}},
			display: "Dee", avatar: cdnBase + "/avatars/" + dave + "/a_u2.gif?size=1024",
		},
		{
			name: "uncached user: global name, then default avatar",
			msg: &discordgo.Message{ID: "100000000000000124", ChannelID: tGeneral,
				Author: &discordgo.User{ID: tBob, Username: "bob", GlobalName: "Bobby"}},
			display: "Bobby", avatar: dto.DefaultAvatarURL(tBob, "", 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toMessageItem(tt.msg, r)
			if got.IsWebhook != tt.webhook || (tt.webhook && got.WebhookID != hook) {
				t.Errorf("webhook = %v %q", got.IsWebhook, got.WebhookID)
			}
			if got.AuthorDisplayName != tt.display || got.AuthorAvatarURL != tt.avatar {
				t.Errorf("identity = %q %q, want %q %q", got.AuthorDisplayName, got.AuthorAvatarURL, tt.display, tt.avatar)
			}
			if tt.attachW > 0 {
				a := got.Attachments[0]
				if a.ID == "" || a.ProxyURL == "" || a.Width != tt.attachW || a.Height != tt.attachH || a.Size != tt.attachSize {
					t.Errorf("attachment = %+v", a)
				}
			}
		})
	}
}

func TestToChannelInfoOverwrites(t *testing.T) {
	info := toChannelInfo(&discordgo.Channel{ID: tGeneral, PermissionOverwrites: []*discordgo.PermissionOverwrite{
		{ID: tGuild, Type: discordgo.PermissionOverwriteTypeRole, Deny: 1 << 10},
		{ID: tAlice, Type: discordgo.PermissionOverwriteTypeMember, Allow: 1 << 10},
	}})
	if len(info.Overwrites) != 2 || info.Overwrites[0].Type != "role" || info.Overwrites[1].Type != "member" || info.Overwrites[1].Allow != 1<<10 {
		t.Fatalf("overwrites = %+v", info.Overwrites)
	}
}
