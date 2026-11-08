package tools

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/media"
)

// attachSetup returns a server with message and persona tools, a media root
// holding Scenes/keep.png, a persona "dm", and a file outside the root.
func attachSetup(t *testing.T) (s *server.MCPServer, mock *discord.MockClient, root, outside string) {
	t.Helper()
	d, mock := newTestDeps(t, nil)
	root = t.TempDir()
	d.MediaStore = media.NewStore(root, false)
	if err := os.MkdirAll(filepath.Join(root, "Scenes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Scenes", "keep.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside = filepath.Join(t.TempDir(), "handout.pdf")
	if err := os.WriteFile(outside, []byte("pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = newTestServer(t, d, RegisterMessageTools, RegisterWebhookPersonaTools)
	res := callTool(t, s, personaRegisterTool, map[string]any{
		"persona_name": "dm", "display_name": "Dungeon Master", "default_channel_id": discord.MockGeneralID,
	})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	return s, mock, root, outside
}

func TestPersonaSendAttachesFiles(t *testing.T) {
	s, mock, root, outside := attachSetup(t)

	// No content: an image-only post is allowed.
	var out personaSendResult
	decodeResult(t, callTool(t, s, personaSendTool, map[string]any{
		"persona_name": "dm", "file_path": "Scenes/keep.png", "file_paths": []any{outside},
	}), &out)
	req := mock.CallsTo("SendWebhookMessage")[0].Args[0].(dto.WebhookMessageReq)
	want := []string{filepath.Join(root, "Scenes", "keep.png"), outside}
	if fmt.Sprint(req.FilePaths) != fmt.Sprint(want) || req.Username != "Dungeon Master" || req.Content != "" {
		t.Fatalf("send req = %+v, want files %v", req, want)
	}
	if out.Message == nil || len(out.Message.Attachments) != 2 || out.Message.Attachments[0].Filename != "keep.png" {
		t.Fatalf("message = %+v", out.Message)
	}
}

func TestWebhookAndSendAttachFiles(t *testing.T) {
	s, mock, root, _ := attachSetup(t)
	w, err := mock.CreateWebhook(t.Context(), discord.MockGeneralID, "hook")
	if err != nil {
		t.Fatal(err)
	}
	res := callTool(t, s, webhookSendTool, map[string]any{"webhook_id": w.ID, "file_path": "Scenes/keep.png"})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	wreq := mock.CallsTo("SendWebhookMessage")[0].Args[0].(dto.WebhookMessageReq)
	if len(wreq.FilePaths) != 1 || wreq.FilePaths[0] != filepath.Join(root, "Scenes", "keep.png") {
		t.Fatalf("webhook files = %v", wreq.FilePaths)
	}

	res = callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "file_paths": []any{"Scenes/keep.png"}})
	if res.IsError {
		t.Fatal(resultText(t, res))
	}
	sreq := mock.CallsTo("SendMessage")[0].Args[0].(dto.SendMessageReq)
	if len(sreq.FilePaths) != 1 || sreq.FilePaths[0] != filepath.Join(root, "Scenes", "keep.png") {
		t.Fatalf("send files = %v", sreq.FilePaths)
	}
}

func TestAttachFilesRejects(t *testing.T) {
	s, mock, root, outside := attachSetup(t)
	if err := os.Symlink(filepath.Dir(outside), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	eleven := make([]any, 11)
	for i := range eleven {
		eleven[i] = outside
	}
	big := filepath.Join(root, "big.bin")
	f, err := os.Create(big) //nolint:gosec // test file in a temp dir
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(discord.MaxAttachmentBytes + 1); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"persona_name": "dm"}, "content is required"},
		{map[string]any{"persona_name": "dm", "file_path": "../x.png"}, "escapes the media root"},
		{map[string]any{"persona_name": "dm", "file_path": "Scenes/missing.png"}, "not in the media root"},
		{map[string]any{"persona_name": "dm", "file_path": "escape/handout.pdf"}, "outside the media root"},
		{map[string]any{"persona_name": "dm", "file_path": "Scenes"}, "not a regular file"},
		{map[string]any{"persona_name": "dm", "file_path": "big.bin"}, "the limit is 100 MiB"},
		{map[string]any{"persona_name": "dm", "file_path": outside, "file_paths": []any{outside}}, "attached twice"},
		{map[string]any{"persona_name": "dm", "file_paths": eleven}, "at most 10"},
	} {
		res := callTool(t, s, personaSendTool, tc.args)
		if txt := resultText(t, res); !res.IsError || !strings.Contains(txt, tc.want) {
			t.Errorf("%v: got %q, want error containing %q", tc.args, txt, tc.want)
		}
	}
	if n := len(mock.MutatingCalls()); n != 0 {
		t.Fatalf("rejected sends made %d mutating calls", n)
	}
}

// writeFile writes b to dir/name and returns the path.
func writeFile(t *testing.T, dir, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPersonaSendShrinksOversizedImage(t *testing.T) {
	uploadLimits.Clear()
	t.Cleanup(uploadLimits.Clear)
	s, mock, root, _ := attachSetup(t)
	big := opaqueNoisePNG(t, 2200, 1700) // ~11 MB of opaque noise, over the 10 MiB limit at boost level 1
	if len(big) <= 10<<20 {
		t.Fatalf("fixture is only %d bytes", len(big))
	}
	orig := writeFile(t, filepath.Join(root, "Scenes"), "render.png", big)

	res := callTool(t, s, personaSendTool, map[string]any{"persona_name": "dm", "content": "Dusk", "file_path": "Scenes/render.png"})
	if res.IsError || len(res.Content) != 2 {
		t.Fatalf("result: %s (%d parts)", resultText(t, res), len(res.Content))
	}
	note := res.Content[1].(mcp.TextContent).Text
	if !strings.Contains(note, "render.png -> render.jpg") || !strings.Contains(note, "10 MiB upload limit") {
		t.Fatalf("note = %q", note)
	}
	req := mock.CallsTo("SendWebhookMessage")[0].Args[0].(dto.WebhookMessageReq)
	sent := req.FilePaths[0]
	if filepath.Base(sent) != "render.jpg" || strings.HasPrefix(sent, root) {
		t.Fatalf("sent %q, want a temporary render.jpg", sent)
	}
	if _, err := os.Stat(sent); !os.IsNotExist(err) {
		t.Fatalf("temporary copy %s was not removed (err = %v)", sent, err)
	}
	if b, err := os.ReadFile(orig); err != nil || len(b) != len(big) { //nolint:gosec // test path
		t.Fatalf("original changed: %d bytes, %v", len(b), err)
	}
	if n := len(mock.CallsTo("GetGuildInfo")); n != 1 {
		t.Fatalf("GetGuildInfo called %d times", n)
	}

	// A level 2 server allows 50 MiB, so the same file goes as is. The limit
	// is cached, so clear it to see the new tier.
	uploadLimits.Clear()
	g := mock.Guilds[discord.MockGuildID]
	g.BoostTier = 2
	mock.Guilds[discord.MockGuildID] = g
	res = callTool(t, s, personaSendTool, map[string]any{"persona_name": "dm", "file_path": "Scenes/render.png"})
	if res.IsError || len(res.Content) != 1 {
		t.Fatalf("level 2: %s (%d parts)", resultText(t, res), len(res.Content))
	}
	if got := mock.CallsTo("SendWebhookMessage")[1].Args[0].(dto.WebhookMessageReq).FilePaths[0]; got != orig {
		t.Fatalf("level 2 sent %q, want the original", got)
	}
}

func TestAttachImageOptions(t *testing.T) {
	uploadLimits.Clear()
	s, mock, root, _ := attachSetup(t)
	writeFile(t, root, "wide.png", mtNoisyPNG(t, 400, 200))

	res := callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "file_path": "wide.png",
		"image_max_dimension": 100, "image_format": "jpeg"})
	if res.IsError || len(res.Content) != 2 || !strings.Contains(res.Content[1].(mcp.TextContent).Text, "400x200 -> 100x50") {
		t.Fatalf("result: %+v", res.Content)
	}
	if got := filepath.Base(mock.CallsTo("SendMessage")[0].Args[0].(dto.SendMessageReq).FilePaths[0]); got != "wide.jpg" {
		t.Fatalf("sent %q", got)
	}
	if n := len(mock.CallsTo("GetGuildInfo")); n != 0 {
		t.Fatalf("small files must not look up the upload limit (%d calls)", n)
	}
	res = callTool(t, s, "discord_send", map[string]any{"channel_id": "#general", "file_path": "wide.png", "image_format": "gif"})
	if !res.IsError {
		t.Fatal("image_format gif must be rejected")
	}
}

func TestAttachOversizedNonImage(t *testing.T) {
	uploadLimits.Clear()
	t.Cleanup(uploadLimits.Clear)
	s, mock, root, _ := attachSetup(t)
	f, err := os.Create(filepath.Join(root, "notes.bin")) //nolint:gosec // test file in a temp dir
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(11 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	res := callTool(t, s, personaSendTool, map[string]any{"persona_name": "dm", "file_path": "notes.bin"})
	if txt := resultText(t, res); !res.IsError || !strings.Contains(txt, "10 MiB upload limit") {
		t.Fatalf("result = %q", txt)
	}
	if n := len(mock.CallsTo("SendWebhookMessage")); n != 0 {
		t.Fatalf("sent %d messages", n)
	}
}

// opaqueNoisePNG is an incompressible PNG with no transparency.
func opaqueNoisePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	rng := rand.New(rand.NewSource(3)) //nolint:gosec // deterministic test noise
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = byte(rng.Intn(256))
		if i%4 == 3 {
			img.Pix[i] = 255
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
