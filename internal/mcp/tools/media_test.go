package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/swiftj/golk/lk"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/lkbridge"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/media"
)

const (
	mtHook       = "100000000000000700"
	mtTupperMsg  = "100000000000000150"
	mtArtMsg     = "100000000000000151"
	mtAttachment = "100000000000000500"
	mtCDN        = "https://cdn.discordapp.com"
)

// mtRewrite sends media requests to the test CDN, keeping the path.
type mtRewrite struct{ target *url.URL }

func (r mtRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// mtNoisyPNG is an incompressible w×h PNG (random pixels), so it can exceed a chunk.
func mtNoisyPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Intn(256))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mtSmallPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// mtSetup seeds a Tupperbox message and an art message, and serves their
// files from a test CDN.
func mtSetup(t *testing.T, cfg *config.Config, files map[string][]byte) (deps.Deps, *discord.MockClient) {
	t.Helper()
	d, mock := newTestDeps(t, cfg)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	d.Media = media.New(mock, media.Config{HTTPClient: &http.Client{Transport: mtRewrite{u}}})
	mock.Messages[discord.MockGeneralID] = append(mock.Messages[discord.MockGeneralID],
		dto.MessageItem{ID: mtTupperMsg, ChannelID: discord.MockGeneralID, AuthorID: mtHook, Author: "Ser Thaddeus Mournvale",
			AuthorDisplayName: "Ser Thaddeus Mournvale", IsWebhook: true, WebhookID: mtHook,
			AuthorAvatarURL: mtCDN + "/avatars/" + mtHook + "/abc.png?size=1024", Content: "I ride at dawn."},
		dto.MessageItem{ID: mtArtMsg, ChannelID: discord.MockGeneralID, AuthorID: discord.MockAliceID, Author: "alice",
			Attachments: []dto.AttachmentRef{{ID: mtAttachment, Filename: "big-map.png",
				URL: mtCDN + "/attachments/1/2/big-map.png?ex=1&is=2&hm=3"}}},
	)
	return d, mock
}

func TestFetchMediaChunksReassemble(t *testing.T) {
	big := mtNoisyPNG(t, 800, 600) // ~1.4 MB, over one chunk
	d, _ := mtSetup(t, nil, map[string][]byte{"/attachments/1/2/big-map.png": big})
	s := newTestServer(t, d, RegisterMediaTools)
	args := func(extra map[string]any) map[string]any {
		a := map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0, "format": "original"}
		for k, v := range extra {
			a[k] = v
		}
		return a
	}

	var first mediaFetchResponse
	decodeResult(t, callTool(t, s, mediaToolFetch, args(nil)), &first)
	if first.EOF || first.Hint == "" || first.Length != mediaMaxChunk || first.TotalBytes != len(big) || first.Mime != "image/png" {
		t.Fatalf("first chunk: eof=%v len=%d total=%d hint=%q", first.EOF, first.Length, first.TotalBytes, first.Hint)
	}

	var all []byte
	for off := 0; ; {
		var c mediaFetchResponse
		decodeResult(t, callTool(t, s, mediaToolFetch, args(map[string]any{"offset": off, "length": 400_000})), &c)
		b, err := base64.StdEncoding.DecodeString(c.Base64)
		if err != nil {
			t.Fatal(err)
		}
		if c.SHA256 != first.SHA256 || c.Hint != "" {
			t.Fatalf("chunk at %d: sha changed or hint set", off)
		}
		all = append(all, b...)
		off += c.Length
		if c.EOF {
			break
		}
	}
	sum := sha256.Sum256(all)
	if hex.EncodeToString(sum[:]) != first.SHA256 || !bytes.Equal(all, big) {
		t.Fatal("concatenated chunks do not reproduce the payload")
	}

	res := callTool(t, s, mediaToolFetch, args(map[string]any{"offset": len(big) + 1}))
	if !res.IsError {
		t.Fatal("offset past end must fail")
	}
}

func TestFetchMediaDefaultsAndValidation(t *testing.T) {
	art := mtNoisyPNG(t, 2000, 1000)
	d, mock := mtSetup(t, nil, map[string][]byte{
		"/attachments/1/2/big-map.png":    art,
		"/avatars/" + mtHook + "/abc.png": mtSmallPNG(t),
	})
	s := newTestServer(t, d, RegisterMediaTools)

	var r mediaFetchResponse
	decodeResult(t, callTool(t, s, mediaToolFetch, map[string]any{
		"channel_id": "#general", "message_id": mtArtMsg, "attachment_id": mtAttachment}), &r)
	if r.Mime != "image/jpeg" || r.Width != 1600 || r.Height != 800 || !r.EOF || r.Filename != "big-map.jpg" {
		t.Fatalf("defaults (jpeg, 1600px): %+v", r)
	}

	decodeResult(t, callTool(t, s, mediaToolFetch, map[string]any{
		"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "format": "png"}), &r)
	if r.Mime != "image/png" || r.Width != 40 || r.Filename != "Ser-Thaddeus-Mournvale.png" {
		t.Fatalf("avatar: %+v", r)
	}
	if n := len(mock.Calls); mock.Calls[n-1].Method != "GetMessage" {
		t.Fatalf("message must be re-fetched for fresh URLs; last call %s", mock.Calls[n-1].Method)
	}

	for name, a := range map[string]map[string]any{
		"two selectors":  {"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "avatar": true, "attachment_index": 0},
		"no selector":    {"channel_id": discord.MockGeneralID, "message_id": mtArtMsg},
		"bad host":       {"url": "https://example.com/x.png"},
		"bad format":     {"url": mtCDN + "/x.png", "format": "tiff"},
		"bad length":     {"url": mtCDN + "/x.png", "length": mediaMaxChunk + 1},
		"index too high": {"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 4},
	} {
		if res := callTool(t, s, mediaToolFetch, a); !res.IsError {
			t.Errorf("%s: expected a tool error", name)
		}
	}

	blocked, _ := mtSetup(t, &config.Config{BlockedChannels: []string{discord.MockGeneralID}}, nil)
	bs := newTestServer(t, blocked, RegisterMediaTools)
	res := callTool(t, bs, mediaToolFetch, map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0})
	if !res.IsError || !strings.Contains(resultText(t, res), "policy") {
		t.Fatalf("blocked channel must be denied: %s", resultText(t, res))
	}
}

func TestGetMemberAvatar(t *testing.T) {
	d, mock := newTestDeps(t, nil)
	mock.Profiles[discord.MockGuildID] = map[string]dto.MemberProfile{discord.MockAliceID: {
		UserID: discord.MockAliceID, Username: "alice", GlobalName: "Alice A", Nick: "Ali", Avatar: "u1", GuildAvatar: "a_g1",
	}}
	s := newTestServer(t, d, RegisterMediaTools)
	var got dto.MemberAvatar
	decodeResult(t, callTool(t, s, mediaToolAvatar, map[string]any{"user_id": "@alice", "size": 256}), &got)
	if got.DisplayName != "Ali" || !got.Animated ||
		got.AvatarURL != mtCDN+"/avatars/"+discord.MockAliceID+"/u1.png?size=256" ||
		got.GuildAvatarURL != mtCDN+"/guilds/"+discord.MockGuildID+"/users/"+discord.MockAliceID+"/avatars/a_g1.gif?size=256" {
		t.Fatalf("avatar = %+v", got)
	}
	var bob dto.MemberAvatar
	decodeResult(t, callTool(t, s, mediaToolAvatar, map[string]any{"user_id": "100000000000000051"}), &bob)
	if bob.DisplayName != "bob" || bob.GuildAvatarURL != "" || !strings.Contains(bob.AvatarURL, "/embed/avatars/") || bob.Animated {
		t.Fatalf("default avatar = %+v", bob)
	}
	if res := callTool(t, s, mediaToolAvatar, map[string]any{"user_id": "@alice", "size": 300}); !res.IsError {
		t.Fatal("size 300 must be rejected")
	}
}

// ---------- LegendKeeper bridge ----------

// mtLK is an in-memory lkbridge.Assets.
type mtLK struct {
	uploads []lk.UploadImageParams
	folders []lk.Asset
	fail    map[string]bool // upload name -> fail
}

func (f *mtLK) List(_ context.Context, p lk.AssetListParams, _ ...lk.CallOption) (*lk.Page[lk.Asset], error) {
	var items []lk.Asset
	for _, a := range f.folders {
		if a.FolderID == p.FolderID {
			items = append(items, a)
		}
	}
	return &lk.Page[lk.Asset]{Items: items}, nil
}

func (f *mtLK) CreateFolder(_ context.Context, c lk.AssetFolderCreate, _ ...lk.CallOption) (*lk.Asset, error) {
	a := lk.Asset{ID: "folder-" + c.Name, Name: c.Name, Type: lk.AssetFolder, FolderID: c.FolderID.Or("")}
	f.folders = append(f.folders, a)
	return &a, nil
}

func (f *mtLK) UploadImage(_ context.Context, p lk.UploadImageParams, _ ...lk.CallOption) (*lk.Asset, error) {
	if f.fail[p.Name] {
		return nil, &lk.UploadError{Status: 413, Body: "too big"}
	}
	f.uploads = append(f.uploads, p)
	id := "img-" + p.Name
	return &lk.Asset{ID: id, Name: p.Name, Type: lk.AssetImage, URL: "https://assets.legendkeeper.com/" + id, FolderID: p.FolderID.Or("")}, nil
}

func TestLegendKeeperBridgeTools(t *testing.T) {
	lkBatchGap = 0
	t.Cleanup(func() { lkBatchGap = 250 * time.Millisecond })
	avatar := mtSmallPNG(t)
	files := map[string][]byte{"/avatars/" + mtHook + "/abc.png": avatar, "/attachments/1/2/big-map.png": mtNoisyPNG(t, 64, 64)}

	t.Run("not configured", func(t *testing.T) {
		d, _ := mtSetup(t, nil, files)
		s := newTestServer(t, d, RegisterLegendKeeperTools)
		for _, tool := range []string{lkToolMessage, lkToolURL, lkToolBatch} {
			res := callTool(t, s, tool, map[string]any{})
			if !res.IsError || !strings.Contains(resultText(t, res), "LegendKeeper not configured") {
				t.Errorf("%s: %s", tool, resultText(t, res))
			}
		}
	})

	fake := &mtLK{fail: map[string]bool{"broken": true}}
	d, _ := mtSetup(t, nil, files)
	b, err := lkbridge.New(lkbridge.Config{Assets: fake, DefaultProject: "proj-1", CacheDir: t.TempDir(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	d.LegendKeeper = b
	s := newTestServer(t, d, RegisterLegendKeeperTools)

	t.Run("avatar to folder path", func(t *testing.T) {
		var out lkbridge.Result
		decodeResult(t, callTool(t, s, lkToolMessage, map[string]any{
			"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true,
			"lk_folder_path": "Druskenvald SPG/Characters", "name": "Ser Thaddeus Mournvale"}), &out)
		if out.AssetID != "img-Ser Thaddeus Mournvale" || out.FolderID != "folder-Characters" || out.ContentType != "image/png" ||
			out.Bytes != len(avatar) || out.Width != 40 || out.Project != "proj-1" || !strings.Contains(out.SourceURL, "/avatars/") {
			t.Fatalf("result = %+v", out)
		}
		if !bytes.Equal(fake.uploads[0].Data, avatar) {
			t.Fatal("format original must upload the untouched bytes")
		}
		// Same bytes again: deduplicated.
		decodeResult(t, callTool(t, s, lkToolMessage, map[string]any{
			"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "name": "again"}), &out)
		if !out.Deduplicated || len(fake.uploads) != 1 {
			t.Fatalf("dedupe: %+v uploads=%d", out, len(fake.uploads))
		}
	})

	t.Run("url tool", func(t *testing.T) {
		var out lkbridge.Result
		decodeResult(t, callTool(t, s, lkToolURL, map[string]any{
			"url": mtCDN + "/attachments/1/2/big-map.png", "format": "jpeg", "max_dimension": 32}), &out)
		if out.ContentType != "image/jpeg" || out.Width != 32 || out.Name != "big map" {
			t.Fatalf("url result = %+v", out)
		}
		if res := callTool(t, s, lkToolURL, map[string]any{"url": "https://example.com/a.png"}); !res.IsError {
			t.Fatal("disallowed host must fail")
		}
	})

	t.Run("batch continues on error", func(t *testing.T) {
		var out lkBatchResponse
		decodeResult(t, callTool(t, s, lkToolBatch, map[string]any{
			"lk_folder_path": "Druskenvald SPG/Maps",
			"items": []any{
				map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0, "name": "broken"},
				map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0, "format": "png", "max_dimension": 16},
				map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 9},
			}}), &out)
		if out.Succeeded != 1 || out.Failed != 2 || len(out.Results) != 3 || out.Results[1].AssetID == "" ||
			!strings.Contains(out.Results[0].Error, "413") || !strings.Contains(out.Results[2].Error, "out of range") {
			t.Fatalf("batch = %+v", out)
		}
		decodeResult(t, callTool(t, s, lkToolBatch, map[string]any{"continue_on_error": false,
			"items": []any{
				map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 9},
				map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true},
			}}), &out)
		if len(out.Results) != 1 || out.Failed != 1 {
			t.Fatalf("stop on error = %+v", out)
		}
		if res := callTool(t, s, lkToolBatch, map[string]any{"items": []any{}}); !res.IsError {
			t.Fatal("empty batch must fail")
		}
	})

	t.Run("read-only blocks uploads", func(t *testing.T) {
		ro, _ := mtSetup(t, &config.Config{ReadOnly: true}, files)
		ro.LegendKeeper = b
		rs := newTestServer(t, ro, RegisterLegendKeeperTools)
		res := callTool(t, rs, lkToolURL, map[string]any{"url": mtCDN + "/attachments/1/2/big-map.png"})
		if !res.IsError || !strings.Contains(resultText(t, res), "read-only") {
			t.Fatalf("read-only: %s", resultText(t, res))
		}
	})
}
