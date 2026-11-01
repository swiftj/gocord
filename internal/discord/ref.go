package discord

import (
	"strings"

	"github.com/local/discord-mcp/internal/dto"
)

// Ref is a parsed user-supplied reference to a guild, channel, user, or role.
type Ref struct {
	// ID is set when the reference was a raw Snowflake or a mention.
	ID string
	// Name is set when the reference was a name (prefix and surrounding whitespace stripped).
	Name string
}

// ParseRef parses "100000000000000010", "<#id>", "<@id>", "<@!id>", "<@&id>",
// "#general", "@alice", or "general". Both the live resolver and the mock use it.
func ParseRef(ref string) Ref {
	s := strings.TrimSpace(ref)
	if dto.IsSnowflake(s) {
		return Ref{ID: s}
	}
	if strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") {
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "<"), ">")
		for _, p := range []string{"@!", "@&", "#", "@"} {
			if id, ok := strings.CutPrefix(inner, p); ok && dto.IsSnowflake(id) {
				return Ref{ID: id}
			}
		}
	}
	s = strings.TrimLeft(s, "#@")
	return Ref{Name: s}
}

// NameMatches compares a reference name with a Discord name case-insensitively.
func NameMatches(refName, name string) bool {
	return strings.EqualFold(strings.TrimSpace(refName), name)
}
