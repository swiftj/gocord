// Package media resolves and downloads images referenced by Discord messages
// (attachments, author avatars) or by http(s) URLs, and optionally resizes
// and re-encodes them in pure Go, then optionally saves them under a local
// media root (Store). It backs discord_fetch_media and the local-file tools.
// See specs/05-media-and-local-files.md.
package media

import (
	"errors"
	"fmt"
	"net"
	"net/url"
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
	URL                  string // any public http(s) URL
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

// CheckURL validates an http(s) URL source and returns it parsed. Any host is
// accepted except an IP literal on a loopback, private, link-local, or other
// non-public network; host names are checked against the same rule at connect
// time (see publicDialer), after DNS resolution.
func CheckURL(raw string) (*url.URL, error) {
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
	if host == "" {
		return nil, fmt.Errorf("%w: url has no host", ErrInvalidSource)
	}
	if ip := net.ParseIP(host); ip != nil && !IsPublicIP(ip) {
		return nil, fmt.Errorf("%w: %s is a local or private network address", ErrInvalidSource, host)
	}
	return u, nil
}

// cgnat is the carrier-grade NAT range (100.64.0.0/10), not covered by IsPrivate.
var cgnat = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// IsPublicIP reports whether ip is a globally routable unicast address.
func IsPublicIP(ip net.IP) bool {
	local := ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || cgnat.Contains(ip)
	return !local
}
