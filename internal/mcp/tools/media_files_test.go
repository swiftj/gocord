package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/media"
)

func TestFetchMediaSaveTo(t *testing.T) {
	avatar := mtSmallPNG(t)
	d, _ := mtSetup(t, nil, map[string][]byte{
		"/avatars/" + mtHook + "/abc.png": avatar,
		"/attachments/1/2/big-map.png":    mtNoisyPNG(t, 3000, 1500),
	})
	root := t.TempDir()
	d.MediaStore = media.NewStore(root, false)
	s := newTestServer(t, d, RegisterMediaTools)

	// The README workflow: a .png save_to keeps the avatar's original PNG bytes.
	var saved mediaSavedResult
	decodeResult(t, callTool(t, s, mediaToolFetch, map[string]any{
		"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true,
		"save_to": "Characters/DanteKeane-portrait.png"}), &saved)
	want := filepath.Join(root, "Characters", "DanteKeane-portrait.png")
	if saved.Path != want || saved.Mime != "image/png" || saved.Width != 40 || saved.Deduplicated {
		t.Fatalf("saved = %+v", saved.Saved)
	}
	if b, _ := os.ReadFile(want); !bytes.Equal(b, avatar) {
		t.Fatal("format inferred from .png should write the untouched bytes")
	}
	if strings.Contains(resultText(t, callTool(t, s, mediaToolFetch, map[string]any{
		"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true,
		"save_to": "Characters/DanteKeane-portrait.png"})), `"deduplicated":true`) == false {
		t.Fatal("second identical save should be deduplicated")
	}

	// Resize + JPEG on the way to disk; no extension → .jpg appended; no base64 in the result.
	res := callTool(t, s, mediaToolFetch, map[string]any{
		"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0,
		"save_to": "Maps/world", "max_dimension": 2048, "format": "jpeg"})
	decodeResult(t, res, &saved)
	if saved.Path != filepath.Join(root, "Maps", "world.jpg") || saved.Width != 2048 || saved.Height != 1024 || saved.Mime != "image/jpeg" {
		t.Fatalf("resized save = %+v", saved.Saved)
	}
	if strings.Contains(resultText(t, res), "base64") {
		t.Fatal("save_to must not return base64")
	}

	for name, a := range map[string]map[string]any{
		"escape":           {"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": "../x.png"},
		"absolute outside": {"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": filepath.Join(t.TempDir(), "x.png")},
		"ext mismatch":     {"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": "a.png", "format": "jpeg"},
		"offset with save": {"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": "a.png", "offset": 0},
		"differs, no overwrite": {"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0,
			"save_to": "Characters/DanteKeane-portrait.png", "format": "png", "max_dimension": 64},
	} {
		if res := callTool(t, s, mediaToolFetch, a); !res.IsError {
			t.Errorf("%s: expected a tool error, got %s", name, resultText(t, res))
		}
	}
	decodeResult(t, callTool(t, s, mediaToolFetch, map[string]any{
		"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0,
		"save_to": "Characters/DanteKeane-portrait.png", "format": "png", "max_dimension": 64, "overwrite": true}), &saved)
	if !saved.Overwritten || saved.Width != 64 {
		t.Fatalf("overwrite = %+v", saved.Saved)
	}

	noStore, _ := mtSetup(t, nil, nil)
	ns := newTestServer(t, noStore, RegisterMediaTools)
	if res := callTool(t, ns, mediaToolFetch, map[string]any{"url": mtCDN + "/a.png", "save_to": "a.png"}); !res.IsError {
		t.Fatal("save_to without a store must fail")
	}
}

func TestFetchURLToFile(t *testing.T) {
	art := mtNoisyPNG(t, 100, 50)
	d, _ := mtSetup(t, nil, map[string][]byte{"/render/4k.png": art})
	root := t.TempDir()
	d.MediaStore = media.NewStore(root, false)
	s := newTestServer(t, d, RegisterMediaTools)

	var out mediaSavedResult
	decodeResult(t, callTool(t, s, mediaToolURLFile, map[string]any{
		"url": mtCDN + "/render/4k.png", "save_to": "Renders/keep", "format": "original"}), &out)
	if out.Path != filepath.Join(root, "Renders", "keep.png") || out.SourceURL != mtCDN+"/render/4k.png" || out.Bytes != len(art) {
		t.Fatalf("url save = %+v (source %q)", out.Saved, out.SourceURL)
	}
	for name, a := range map[string]map[string]any{
		"private address": {"url": "http://10.0.0.1/a.png", "save_to": "a.png"},
		"missing save_to": {"url": mtCDN + "/render/4k.png"},
		"missing url":     {"save_to": "a.png"},
	} {
		if res := callTool(t, s, mediaToolURLFile, a); !res.IsError {
			t.Errorf("%s: expected a tool error", name)
		}
	}
}

func TestBatchFetchMedia(t *testing.T) {
	mediaBatchGap = 0
	t.Cleanup(func() { mediaBatchGap = 250 * time.Millisecond })
	d, _ := mtSetup(t, nil, map[string][]byte{
		"/avatars/" + mtHook + "/abc.png": mtSmallPNG(t),
		"/attachments/1/2/big-map.png":    mtNoisyPNG(t, 64, 64),
	})
	root := t.TempDir()
	d.MediaStore = media.NewStore(root, false)
	s := newTestServer(t, d, RegisterMediaTools)

	var out mediaBatchResponse
	decodeResult(t, callTool(t, s, mediaToolBatch, map[string]any{"items": []any{
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": "Characters/Thaddeus.png"},
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 9, "save_to": "Maps/missing"},
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 0},
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_id": mtAttachment, "save_to": "Maps/map", "format": "png", "max_dimension": 32},
	}}), &out)
	if out.Succeeded != 2 || out.Failed != 2 || len(out.Results) != 4 {
		t.Fatalf("batch = %+v", out)
	}
	if r := out.Results[0]; !r.OK || r.Path != filepath.Join(root, "Characters", "Thaddeus.png") || r.SHA256 == "" || r.Width != 40 {
		t.Fatalf("item 0 = %+v", r)
	}
	if !strings.Contains(out.Results[1].Error, "out of range") || !strings.Contains(out.Results[2].Error, "save_to is required") {
		t.Fatalf("errors = %q / %q", out.Results[1].Error, out.Results[2].Error)
	}
	if r := out.Results[3]; !r.OK || r.Width != 32 {
		t.Fatalf("item 3 = %+v", r)
	}

	decodeResult(t, callTool(t, s, mediaToolBatch, map[string]any{"continue_on_error": false, "items": []any{
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtArtMsg, "attachment_index": 9, "save_to": "x"},
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": "y"},
	}}), &out)
	if len(out.Results) != 1 || out.Failed != 1 {
		t.Fatalf("stop on error = %+v", out)
	}
	if res := callTool(t, s, mediaToolBatch, map[string]any{"items": []any{}}); !res.IsError {
		t.Fatal("empty batch must fail")
	}

	blocked, _ := mtSetup(t, &config.Config{BlockedChannels: []string{discord.MockGeneralID}}, nil)
	blocked.MediaStore = media.NewStore(t.TempDir(), false)
	bs := newTestServer(t, blocked, RegisterMediaTools)
	decodeResult(t, callTool(t, bs, mediaToolBatch, map[string]any{"items": []any{
		map[string]any{"channel_id": discord.MockGeneralID, "message_id": mtTupperMsg, "avatar": true, "save_to": "a.png"},
	}}), &out)
	if out.Failed != 1 || !strings.Contains(out.Results[0].Error, "policy") {
		t.Fatalf("blocked channel = %+v", out)
	}
}

func TestMediaListFiles(t *testing.T) {
	d, _ := newTestDeps(t, nil)
	root := t.TempDir()
	d.MediaStore = media.NewStore(root, false)
	for _, p := range []string{"Characters/a.png", "Characters/b.png", "Maps/m.png"} {
		if _, err := d.MediaStore.Save(&media.Fetched{Bytes: mtSmallPNG(t), ContentType: media.TypePNG}, p, false); err != nil {
			t.Fatal(err)
		}
	}
	s := newTestServer(t, d, RegisterMediaTools)
	var out mediaListResponse
	decodeResult(t, callTool(t, s, mediaToolList, map[string]any{"subdir": "Characters"}), &out)
	if out.Root != root || len(out.Files) != 2 || out.Files[0].Path != filepath.Join(root, "Characters", "a.png") || out.Files[0].Width != 40 {
		t.Fatalf("list = %+v", out)
	}
	decodeResult(t, callTool(t, s, mediaToolList, map[string]any{"glob": "Maps/*"}), &out)
	if len(out.Files) != 1 {
		t.Fatalf("glob = %+v", out.Files)
	}
	if res := callTool(t, s, mediaToolList, map[string]any{"subdir": "../.."}); !res.IsError {
		t.Fatal("escaping subdir must fail")
	}
}
