package media

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/dto"
)

func intp(n int) *int { return &n }

func TestSourceValidate(t *testing.T) {
	const ch, msg = "100000000000000010", "100000000000000100"
	tests := []struct {
		name string
		src  Source
		ok   bool
	}{
		{"attachment index", Source{ChannelID: ch, MessageID: msg, AttachmentIndex: intp(0)}, true},
		{"attachment id", Source{ChannelID: ch, MessageID: msg, AttachmentID: "100000000000000500"}, true},
		{"avatar", Source{ChannelID: ch, MessageID: msg, Avatar: true}, true},
		{"url", Source{URL: "https://cdn.discordapp.com/a.png"}, true},
		{"nothing", Source{ChannelID: ch, MessageID: msg}, false},
		{"two selectors", Source{ChannelID: ch, MessageID: msg, Avatar: true, AttachmentID: "1"}, false},
		{"index and id", Source{ChannelID: ch, MessageID: msg, AttachmentIndex: intp(1), AttachmentID: "1"}, false},
		{"missing message", Source{ChannelID: ch, Avatar: true}, false},
		{"negative index", Source{ChannelID: ch, MessageID: msg, AttachmentIndex: intp(-1)}, false},
		{"url with message", Source{URL: "https://cdn.discordapp.com/a.png", ChannelID: ch, MessageID: msg}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.src.Validate()
			if (err == nil) != tt.ok {
				t.Fatalf("Validate() = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("error %v does not wrap ErrInvalidSource", err)
			}
		})
	}
}

func TestCheckURL(t *testing.T) {
	for _, raw := range []string{
		"https://cdn.discordapp.com/attachments/1/2/a.png?ex=1&is=2&hm=3",
		"https://pikaso.cdnpk.net/private/production/1/render.png",
		"https://img.magnific.com/a.png",
		"http://example.com/a.png",
		"https://8.8.8.8/a.png", // a public IP literal
	} {
		if _, err := CheckURL(raw); err != nil {
			t.Errorf("CheckURL(%q) rejected a public URL: %v", raw, err)
		}
	}
	for _, raw := range []string{
		"ftp://cdn.discordapp.com/a.png",
		"file:///etc/passwd",
		"https://user:pw@cdn.discordapp.com/a.png",
		"https:///a.png",
		"http://127.0.0.1/a.png",
		"http://[::1]/a.png",
		"http://10.0.0.5/a.png",
		"http://192.168.1.1/a.png",
		"http://169.254.169.254/latest/meta-data",
		"http://100.64.0.1/a.png",
		"http://0.0.0.0/a.png",
	} {
		if _, err := CheckURL(raw); !errors.Is(err, ErrInvalidSource) {
			t.Errorf("CheckURL(%q) = %v, want ErrInvalidSource", raw, err)
		}
	}
}

func TestPublicDialerRefusesLocalAddresses(t *testing.T) {
	// A host *name* that resolves to loopback passes CheckURL, so the dialer
	// itself must refuse it (this also covers DNS rebinding).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pngBytes(t, 2, 2)) }))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	f := New(nil, Config{Timeout: 5 * time.Second})
	_, err := f.Fetch(context.Background(), Source{URL: "http://localhost:" + u.Port() + "/a.png"}, FetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "local or private address") {
		t.Fatalf("fetch from localhost = %v, want a refusal", err)
	}
	for addr, ok := range map[string]bool{"8.8.8.8:443": true, "127.0.0.1:80": false, "[::1]:80": false, "10.1.2.3:443": false, "[fe80::1]:80": false} {
		if err := publicDialControl("tcp", addr, nil); (err == nil) != ok {
			t.Errorf("publicDialControl(%s) = %v, want allowed=%v", addr, err, ok)
		}
	}
}

func TestScaledSize(t *testing.T) {
	tests := []struct{ w, h, limit, ww, wh int }{
		{4000, 3000, 1600, 1600, 1200},
		{3000, 4000, 1600, 1200, 1600},
		{1000, 800, 1600, 1000, 800}, // never upscale
		{1600, 1600, 1600, 1600, 1600},
		{5000, 1, 100, 100, 1}, // floor at 1px
		{1920, 1080, 0, 1920, 1080},
		{333, 1000, 500, 167, 500}, // rounds to nearest
	}
	for _, tt := range tests {
		if w, h := ScaledSize(tt.w, tt.h, tt.limit); w != tt.ww || h != tt.wh {
			t.Errorf("ScaledSize(%d,%d,%d) = %dx%d, want %dx%d", tt.w, tt.h, tt.limit, w, h, tt.ww, tt.wh)
		}
	}
}

// ---------- fixtures ----------

func testImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	return img
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, testImage(w, h)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, testImage(w, h), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func animatedGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{color.Black, color.White}
	g := &gif.GIF{}
	for range 2 {
		g.Image = append(g.Image, image.NewPaletted(image.Rect(0, 0, 40, 20), pal))
		g.Delay = append(g.Delay, 10)
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A minimal lossless 1x1 WebP (VP8L).
var webp1x1 = []byte{
	0x52, 0x49, 0x46, 0x46, 0x1a, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50,
	0x56, 0x50, 0x38, 0x4c, 0x0d, 0x00, 0x00, 0x00, 0x2f, 0x00, 0x00, 0x00,
	0x10, 0x07, 0x10, 0x11, 0x11, 0x88, 0x88, 0xfe, 0x07, 0x00,
}

func TestSniff(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want string
		ok   bool
	}{
		{"png", pngBytes(t, 2, 2), TypePNG, true},
		{"jpeg", jpegBytes(t, 2, 2), TypeJPEG, true},
		{"gif", animatedGIF(t), TypeGIF, true},
		{"webp", webp1x1, TypeWebP, true},
		{"html", []byte("<!doctype html><p>hi"), "text/html", false},
		{"pdf", []byte("%PDF-1.7 ..."), "application/pdf", false},
	}
	for _, tt := range tests {
		got, err := Sniff(tt.b)
		if got != tt.want || (err == nil) != tt.ok {
			t.Errorf("%s: Sniff = %q, %v; want %q ok=%v", tt.name, got, err, tt.want, tt.ok)
		}
		if err != nil && !errors.Is(err, ErrUnsupportedType) {
			t.Errorf("%s: error does not wrap ErrUnsupportedType", tt.name)
		}
	}
}

func TestTransform(t *testing.T) {
	big := pngBytes(t, 400, 200)
	t.Run("untouched when no resize and same format", func(t *testing.T) {
		f, err := Transform(big, "art.png", FetchOptions{MaxDimension: 1600})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(f.Bytes, big) || f.Width != 400 || f.Height != 200 || f.ContentType != TypePNG {
			t.Fatalf("expected original bytes, got %s %dx%d", f.ContentType, f.Width, f.Height)
		}
	})
	t.Run("resize keeps format", func(t *testing.T) {
		f, err := Transform(big, "art.png", FetchOptions{MaxDimension: 100})
		if err != nil {
			t.Fatal(err)
		}
		if f.ContentType != TypePNG || f.Width != 100 || f.Height != 50 || f.Filename != "art.png" {
			t.Fatalf("got %s %dx%d %s", f.ContentType, f.Width, f.Height, f.Filename)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(f.Bytes))
		if err != nil || cfg.Width != 100 || cfg.Height != 50 {
			t.Fatalf("encoded png is %+v, %v", cfg, err)
		}
	})
	t.Run("convert to jpeg renames", func(t *testing.T) {
		f, err := Transform(big, "art.png", FetchOptions{Format: "jpeg", Quality: 70})
		if err != nil {
			t.Fatal(err)
		}
		if f.ContentType != TypeJPEG || f.Filename != "art.jpg" {
			t.Fatalf("got %s %s", f.ContentType, f.Filename)
		}
		if ct, _ := Sniff(f.Bytes); ct != TypeJPEG {
			t.Fatalf("bytes sniff as %s", ct)
		}
	})
	t.Run("webp request yields png", func(t *testing.T) {
		f, err := Transform(jpegBytes(t, 50, 50), "x.jpg", FetchOptions{Format: "webp"})
		if err != nil {
			t.Fatal(err)
		}
		if f.ContentType != TypePNG || f.Filename != "x.png" {
			t.Fatalf("got %s %s", f.ContentType, f.Filename)
		}
	})
	t.Run("webp source decodes", func(t *testing.T) {
		f, err := Transform(webp1x1, "x.webp", FetchOptions{Format: "png"})
		if err != nil {
			t.Fatal(err)
		}
		if f.ContentType != TypePNG || f.Width != 1 || f.Height != 1 {
			t.Fatalf("got %s %dx%d", f.ContentType, f.Width, f.Height)
		}
	})
	t.Run("animated gif needs strip", func(t *testing.T) {
		g := animatedGIF(t)
		if _, err := Transform(g, "a.gif", FetchOptions{Format: "png"}); err == nil {
			t.Fatal("expected an error re-encoding an animated GIF without StripGIFToFirstFrame")
		}
		f, err := Transform(g, "a.gif", FetchOptions{Format: "png", StripGIFToFirstFrame: true})
		if err != nil || f.ContentType != TypePNG || f.Width != 40 {
			t.Fatalf("strip: %v %+v", err, f)
		}
		f, err = Transform(g, "a.gif", FetchOptions{})
		if err != nil || !bytes.Equal(f.Bytes, g) {
			t.Fatalf("original animated gif should pass through untouched: %v", err)
		}
	})
	t.Run("unknown format", func(t *testing.T) {
		if _, err := Transform(big, "a.png", FetchOptions{Format: "bmp"}); err == nil {
			t.Fatal("expected unknown-format error")
		}
	})
}

// ---------- Fetcher ----------

type fakeMessages struct{ msg dto.MessageItem }

func (f fakeMessages) GetMessage(_ context.Context, _, id string) (*dto.MessageItem, error) {
	if id != f.msg.ID {
		return nil, errors.New("not found")
	}
	m := f.msg
	return &m, nil
}

// rewrite sends every request to the test server, keeping the original path.
type rewrite struct{ target *url.URL }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func newTestFetcher(t *testing.T, h http.Handler, msg dto.MessageItem, maxBytes int64) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return New(fakeMessages{msg}, Config{HTTPClient: &http.Client{Transport: rewrite{u}}, MaxBytes: maxBytes, Timeout: 5 * time.Second})
}

func TestFetcher(t *testing.T) {
	art := pngBytes(t, 300, 150)
	var gotAuth []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/attachments/1/2/art.png", "/avatars/100000000000000700/abc.png":
			w.Header().Set("Content-Type", "application/octet-stream") // header lies; bytes are sniffed
			_, _ = w.Write(art)
		case "/redirect":
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		case "/page.html":
			_, _ = w.Write([]byte("<html><body>not an image</body></html>"))
		default:
			http.NotFound(w, r)
		}
	})
	msg := dto.MessageItem{
		ID: "100000000000000100", ChannelID: "100000000000000010", IsWebhook: true, WebhookID: "100000000000000700",
		AuthorDisplayName: "Ser Thaddeus Mournvale",
		AuthorAvatarURL:   "https://cdn.discordapp.com/avatars/100000000000000700/abc.png?size=1024",
		Attachments: []dto.AttachmentRef{{ID: "100000000000000500", Filename: "art.png",
			URL: "https://cdn.discordapp.com/attachments/1/2/art.png?ex=1&is=2&hm=3"}},
	}
	f := newTestFetcher(t, h, msg, 1<<20)
	ctx := context.Background()

	got, err := f.Fetch(ctx, Source{ChannelID: msg.ChannelID, MessageID: msg.ID, AttachmentIndex: intp(0)}, FetchOptions{MaxDimension: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentType != TypePNG || got.Width != 100 || got.Height != 50 || got.Filename != "art.png" {
		t.Fatalf("attachment: %s %dx%d %s", got.ContentType, got.Width, got.Height, got.Filename)
	}

	got, err = f.Fetch(ctx, Source{ChannelID: msg.ChannelID, MessageID: msg.ID, AttachmentID: "100000000000000500"}, FetchOptions{})
	if err != nil || !bytes.Equal(got.Bytes, art) {
		t.Fatalf("attachment by id: %v", err)
	}

	got, err = f.Fetch(ctx, Source{ChannelID: msg.ChannelID, MessageID: msg.ID, Avatar: true}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Filename != "Ser-Thaddeus-Mournvale.png" || got.SourceURL != msg.AuthorAvatarURL {
		t.Fatalf("avatar: %s %s", got.Filename, got.SourceURL)
	}

	if _, err := f.Fetch(ctx, Source{ChannelID: msg.ChannelID, MessageID: msg.ID, AttachmentIndex: intp(3)}, FetchOptions{}); err == nil ||
		!strings.Contains(err.Error(), "out of range") {
		t.Fatalf("index out of range: %v", err)
	}
	if _, err := f.Fetch(ctx, Source{URL: "https://cdn.discordapp.com/redirect"}, FetchOptions{}); err == nil ||
		!strings.Contains(err.Error(), "private network address") {
		t.Fatalf("redirect to a private address must fail: %v", err)
	}
	if _, err := f.Fetch(ctx, Source{URL: "https://cdn.discordapp.com/page.html"}, FetchOptions{}); !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("html must be rejected by sniffing: %v", err)
	}
	_, err = f.Fetch(ctx, Source{URL: "https://cdn.discordapp.com/missing.png"}, FetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") || strings.Contains(err.Error(), "/missing.png") {
		t.Fatalf("404 should report status without the URL: %v", err)
	}
	for _, a := range gotAuth {
		if a != "" {
			t.Fatal("an Authorization header was sent to a media host")
		}
	}

	small := newTestFetcher(t, h, msg, 100)
	if _, err := small.Fetch(ctx, Source{URL: "https://cdn.discordapp.com/attachments/1/2/art.png"}, FetchOptions{}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size cap: %v", err)
	}
}

func TestFetchCachedIsStable(t *testing.T) {
	calls := 0
	art := pngBytes(t, 500, 500)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write(art)
	})
	f := newTestFetcher(t, h, dto.MessageItem{}, 1<<20)
	src := Source{URL: "https://cdn.discordapp.com/a.png"}
	opts := FetchOptions{MaxDimension: 64, Format: "jpeg"}
	a, err := f.FetchCached(context.Background(), src, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.FetchCached(context.Background(), src, opts)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !bytes.Equal(a.Bytes, b.Bytes) {
		t.Fatalf("cache: %d downloads, identical=%v", calls, bytes.Equal(a.Bytes, b.Bytes))
	}
	if CacheKey(src, opts) == CacheKey(src, FetchOptions{MaxDimension: 64, Format: "png"}) {
		t.Fatal("different options must have different cache keys")
	}
}

func TestCacheBounds(t *testing.T) {
	now := time.Unix(0, 0)
	c := NewCache(2, 10, time.Minute)
	c.now = func() time.Time { return now }
	put := func(k string, n int) { c.Put(k, &Fetched{Bytes: make([]byte, n)}) }

	put("a", 4)
	put("b", 4)
	if _, ok := c.Get("a"); !ok { // a is now most recent
		t.Fatal("a missing")
	}
	put("c", 4) // entry cap: evicts LRU (b)
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should be evicted (LRU)")
	}
	put("d", 6) // byte cap 10: a(4)+c(4)+d(6)=14 → drops LRU a, leaving c+d=10
	if _, ok := c.Get("a"); ok || c.Len() != 2 {
		t.Fatalf("byte bound: %d entries, a present=%v", c.Len(), ok)
	}
	put("huge", 11) // larger than the bound: not cached
	if _, ok := c.Get("huge"); ok {
		t.Fatal("oversized value cached")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := c.Get("d"); ok {
		t.Fatal("expired entry returned")
	}
}
