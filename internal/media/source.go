// Package media resolves and downloads images referenced by Discord messages
// (attachments, author avatars) or by allowlisted URLs, and optionally resizes
// and re-encodes them in pure Go, then optionally saves them under a local
// media root (Store). It backs discord_fetch_media and the local-file tools.
// See specs/05-media-and-local-files.md.
package media

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// Source names one image. Exactly one of AttachmentIndex, AttachmentID,
// Avatar, or URL is set; the first three also need ChannelID and MessageID.
type Source struct {
	ChannelID, MessageID string
	AttachmentIndex      *int   // nth (0-based) attachment of the message
	AttachmentID         string // or the attachment with this ID
	Avatar               bool   // the message author's avatar (a Tupper's art for webhook messages)
	URL                  string // an allowlisted http(s) URL
}

// ErrInvalidSource reports a Source that does not name exactly one image.
var ErrInvalidSource = errors.New("invalid media source")

// Validate checks that exactly one selector is set and its inputs are present.
func (s Source) Validate() error {
	n := 0
	if s.AttachmentIndex != nil {
		n++
	}
	if s.AttachmentID != "" {
		n++
	}
	if s.Avatar {
		n++
	}
	if s.URL != "" {
		n++
	}
	if n != 1 {
		return fmt.Errorf("%w: set exactly one of attachment_index, attachment_id, avatar, or url (got %d)", ErrInvalidSource, n)
	}
	if s.URL != "" {
		if s.ChannelID != "" || s.MessageID != "" {
			return fmt.Errorf("%w: url cannot be combined with channel_id/message_id", ErrInvalidSource)
		}
		return nil
	}
	if s.ChannelID == "" || s.MessageID == "" {
		return fmt.Errorf("%w: channel_id and message_id are required for attachment and avatar sources", ErrInvalidSource)
	}
	if s.AttachmentIndex != nil && *s.AttachmentIndex < 0 {
		return fmt.Errorf("%w: attachment_index must be >= 0", ErrInvalidSource)
	}
	return nil
}

// Key is a stable identity for caching. Message sources are keyed by IDs, not
// by their signed CDN URL, which changes on every re-fetch.
func (s Source) Key() string {
	switch {
	case s.URL != "":
		return "url:" + s.URL
	case s.Avatar:
		return "avatar:" + s.ChannelID + "/" + s.MessageID
	case s.AttachmentID != "":
		return "att:" + s.ChannelID + "/" + s.MessageID + "/id/" + s.AttachmentID
	case s.AttachmentIndex != nil:
		return "att:" + s.ChannelID + "/" + s.MessageID + "/n/" + strconv.Itoa(*s.AttachmentIndex)
	}
	return ""
}

// DefaultAllowedHosts are the hosts a URL source may point at. Entries are an
// exact host, "*.suffix" (any subdomain), or a glob within the host
// ("images-ext-*.discordapp.net").
var DefaultAllowedHosts = []string{
	"cdn.discordapp.com",
	"media.discordapp.net",
	"images-ext-*.discordapp.net",
	"*.magnific.com",
	"cdn.magnific.com",
	"*.freepik.com",
	"assets.legendkeeper.com",
}

// discordHosts are the CDN hosts that serve Discord media.
var discordHosts = []string{"cdn.discordapp.com", "media.discordapp.net", "images-ext-*.discordapp.net"}

// ParseHostList splits a comma-separated GOCORD_MEDIA_ALLOWED_HOSTS value.
func ParseHostList(v string) []string {
	var out []string
	for h := range strings.SplitSeq(v, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// HostAllowed reports whether host matches any pattern.
func HostAllowed(host string, patterns []string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "" {
		return false
	}
	for _, p := range patterns {
		p = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(p)), ".")
		switch {
		case p == "":
		case strings.HasPrefix(p, "*."):
			if strings.HasSuffix(host, p[1:]) && len(host) > len(p)-1 {
				return true
			}
		case strings.Contains(p, "*"):
			if ok, err := path.Match(p, host); err == nil && ok {
				return true
			}
		case p == host:
			return true
		}
	}
	return false
}

// IsDiscordHost reports whether host is a Discord CDN host.
func IsDiscordHost(host string) bool { return HostAllowed(host, discordHosts) }

// CheckURL validates an http(s) URL against the allowlist and returns it parsed.
func CheckURL(raw string, allowed []string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: url: %v", ErrInvalidSource, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return nil, fmt.Errorf("%w: url must be http or https", ErrInvalidSource)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: url must not contain credentials", ErrInvalidSource)
	}
	host := u.Hostname()
	if net.ParseIP(host) != nil || !HostAllowed(host, allowed) {
		return nil, fmt.Errorf("%w: host %q is not allowed; allowed hosts: %s (extend with GOCORD_MEDIA_ALLOWED_HOSTS)",
			ErrInvalidSource, host, strings.Join(allowed, ", "))
	}
	return u, nil
}
