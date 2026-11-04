package dto

import (
	"fmt"
	"testing"
)

func TestAvatarURLs(t *testing.T) {
	const uid = "100000000000000050"
	tests := []struct{ name, got, want string }{
		{"static", UserAvatarURL(uid, "abc", "0", 0), "https://cdn.discordapp.com/avatars/" + uid + "/abc.png?size=1024"},
		{"animated", UserAvatarURL(uid, "a_abc", "0", 256), "https://cdn.discordapp.com/avatars/" + uid + "/a_abc.gif?size=256"},
		{"legacy default", UserAvatarURL(uid, "", "0007", 0), "https://cdn.discordapp.com/embed/avatars/2.png?size=1024"},
		{"migrated default", DefaultAvatarURL(uid, "0", 0), fmt.Sprintf("https://cdn.discordapp.com/embed/avatars/%d.png?size=1024", (100000000000000050>>22)%6)},
		{"guild", GuildAvatarURL("100000000000000001", uid, "a_g", 0), "https://cdn.discordapp.com/guilds/100000000000000001/users/" + uid + "/avatars/a_g.gif?size=1024"},
		{"no guild avatar", GuildAvatarURL("100000000000000001", uid, "", 0), ""},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if DisplayName("", "", "u") != "u" || DisplayName("", "G", "u") != "G" || DisplayName("N", "G", "u") != "N" {
		t.Error("DisplayName precedence nick > global > username")
	}
}
