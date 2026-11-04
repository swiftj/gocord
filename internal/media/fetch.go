package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/local/discord-mcp/internal/dto"
)

// Limits.
const (
	DefaultMaxBytes  = 25 << 20
	DefaultTimeout   = 30 * time.Second
	MaxRedirects     = 5
	DefaultQuality   = 85
	EnvMaxBytes      = "GOCORD_MEDIA_MAX_BYTES"
	defaultUserAgent = "gocord-media/1"
)

// ErrTooLarge reports a download over the byte cap.
var ErrTooLarge = errors.New("media too large")

// ErrUnsupportedType reports bytes that are not PNG, JPEG, GIF, or WebP.
var ErrUnsupportedType = errors.New("unsupported media type")

// MessageGetter re-fetches a message. discord.Service implements it.
type MessageGetter interface {
	GetMessage(ctx context.Context, channelID, messageID string) (*dto.MessageItem, error)
}

// Config tunes a Fetcher. Zero values take the defaults.
type Config struct {
	MaxBytes int64
	Timeout  time.Duration
	// HTTPClient overrides the transport (tests). Its CheckRedirect is replaced.
	// Without it, connections go through publicDialer.
	HTTPClient *http.Client
	Logger     *slog.Logger
}

// Fetcher resolves Sources and downloads them.
type Fetcher struct {
	msgs     MessageGetter
	hc       *http.Client
	maxBytes int64
	timeout  time.Duration
	log      *slog.Logger
	cache    *Cache
}

// New returns a Fetcher. msgs may be nil when only URL sources are used.
func New(msgs MessageGetter, cfg Config) *Fetcher {
	f := &Fetcher{
		msgs:     msgs,
		maxBytes: cfg.MaxBytes,
		timeout:  cfg.Timeout,
		log:      cfg.Logger,
		cache:    NewCache(DefaultCacheEntries, DefaultCacheBytes, DefaultCacheTTL),
	}
	if f.maxBytes <= 0 {
		f.maxBytes = DefaultMaxBytes
	}
	if f.timeout <= 0 {
		f.timeout = DefaultTimeout
	}
	if f.log == nil {
		f.log = slog.Default()
	}
	hc := &http.Client{Transport: publicTransport()}
	if cfg.HTTPClient != nil {
		*hc = *cfg.HTTPClient
	}
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= MaxRedirects {
			return fmt.Errorf("stopped after %d redirects", MaxRedirects)
		}
		if _, err := CheckURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect: %w", err)
		}
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		return nil
	}
	f.hc = hc
	return f
}

// MaxBytes returns the download cap.
func (f *Fetcher) MaxBytes() int64 { return f.maxBytes }

// Resolve returns the current download URL and a filename for s. Message
// sources are re-fetched so signed CDN URLs are fresh.
func (f *Fetcher) Resolve(ctx context.Context, s Source) (string, string, error) {
	if err := s.Validate(); err != nil {
		return "", "", err
	}
	if s.URL != "" {
		u, err := CheckURL(s.URL)
		if err != nil {
			return "", "", err
		}
		return u.String(), filenameFromURL(u), nil
	}
	if f.msgs == nil {
		return "", "", errors.New("media: no Discord client configured")
	}
	m, err := f.msgs.GetMessage(ctx, s.ChannelID, s.MessageID)
	if err != nil {
		return "", "", err
	}
	if s.Avatar {
		if m.AuthorAvatarURL == "" {
			return "", "", fmt.Errorf("message %s has no author avatar", s.MessageID)
		}
		u, err := url.Parse(m.AuthorAvatarURL)
		if err != nil {
			return "", "", fmt.Errorf("avatar url: %w", err)
		}
		name := m.AuthorDisplayName
		if name == "" {
			name = m.Author
		}
		return m.AuthorAvatarURL, avatarFilename(name, u), nil
	}
	var att *dto.AttachmentRef
	switch {
	case s.AttachmentIndex != nil:
		if *s.AttachmentIndex >= len(m.Attachments) {
			return "", "", fmt.Errorf("message %s has %d attachments; attachment_index %d is out of range",
				s.MessageID, len(m.Attachments), *s.AttachmentIndex)
		}
		att = &m.Attachments[*s.AttachmentIndex]
	default:
		for i := range m.Attachments {
			if m.Attachments[i].ID == s.AttachmentID {
				att = &m.Attachments[i]
				break
			}
		}
		if att == nil {
			return "", "", fmt.Errorf("message %s has no attachment %s", s.MessageID, s.AttachmentID)
		}
	}
	if att.URL == "" {
		return "", "", fmt.Errorf("attachment %q has no URL", att.Filename)
	}
	return att.URL, att.Filename, nil
}

// Fetch resolves, downloads, sniffs, and (per opts) transforms one image.
func (f *Fetcher) Fetch(ctx context.Context, s Source, opts FetchOptions) (*Fetched, error) {
	raw, name, err := f.Resolve(ctx, s)
	if err != nil {
		return nil, err
	}
	u, err := CheckURL(raw)
	if err != nil {
		return nil, err
	}
	b, err := f.download(ctx, u)
	if err != nil {
		return nil, err
	}
	out, err := Transform(b, name, opts)
	if err != nil {
		return nil, err
	}
	out.SourceURL = raw
	f.log.Debug("media fetched", "host", u.Hostname(), "bytes_in", len(b), "bytes_out", len(out.Bytes), "content_type", out.ContentType)
	return out, nil
}

// FetchCached is Fetch with the encoded result cached by (source, options),
// so repeated chunked reads of one payload are byte-identical.
func (f *Fetcher) FetchCached(ctx context.Context, s Source, opts FetchOptions) (*Fetched, error) {
	key := CacheKey(s, opts)
	if v, ok := f.cache.Get(key); ok {
		return v, nil
	}
	v, err := f.Fetch(ctx, s, opts)
	if err != nil {
		return nil, err
	}
	f.cache.Put(key, v)
	return v, nil
}

// download GETs u with no credentials, a timeout, and the byte cap.
func (f *Fetcher) download(ctx context.Context, u *url.URL) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("media: build request: %w", err)
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	resp, err := f.hc.Do(req)
	if err != nil {
		// url.Error repeats the (signed) URL; report the host only.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("media: download from %s: %w", u.Hostname(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		hint := ""
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
			hint = " (signed Discord URLs expire; pass channel_id+message_id to re-resolve)"
		}
		return nil, fmt.Errorf("media: download from %s: HTTP %d%s", u.Hostname(), resp.StatusCode, hint)
	}
	if resp.ContentLength > f.maxBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds the %d-byte limit (%s)", ErrTooLarge, resp.ContentLength, f.maxBytes, EnvMaxBytes)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("media: read from %s: %w", u.Hostname(), err)
	}
	if int64(len(b)) > f.maxBytes {
		return nil, fmt.Errorf("%w: exceeds the %d-byte limit (%s)", ErrTooLarge, f.maxBytes, EnvMaxBytes)
	}
	return b, nil
}

// publicTransport dials only public addresses: the check runs on the resolved
// IP, so a host name that resolves to a local or private address (including
// via DNS rebinding) is refused. Proxies are not used, since the check would
// then apply to the proxy instead of the destination.
func publicTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: publicDialControl}
	t.DialContext = d.DialContext
	return t
}

func publicDialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	if ip := net.ParseIP(host); ip == nil || !IsPublicIP(ip) {
		return fmt.Errorf("%w: refusing to connect to local or private address %s", ErrInvalidSource, host)
	}
	return nil
}

func filenameFromURL(u *url.URL) string {
	if base := path.Base(u.Path); base != "" && base != "/" && base != "." {
		return base
	}
	return "image"
}

func avatarFilename(name string, u *url.URL) string {
	ext := path.Ext(u.Path)
	if ext == "" {
		ext = ".png"
	}
	stem := sanitizeStem(name)
	if stem == "" {
		stem = "avatar"
	}
	return stem + ext
}

// sanitizeStem keeps letters, digits, '-', '_' and turns spaces into '-'.
func sanitizeStem(s string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(s) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		}
	}
	return b.String()
}
