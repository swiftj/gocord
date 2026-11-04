package lkbridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/swiftj/golk/lk"

	"github.com/local/discord-mcp/internal/media"
)

const testProject = "proj-test-1"

// fakeLK is an in-memory LegendKeeper asset API plus signed-storage endpoint.
type fakeLK struct {
	t   *testing.T
	srv *httptest.Server

	mu      sync.Mutex
	seq     int
	assets  []map[string]any
	uploads map[string][]byte // upload key -> stored bytes
	calls   []string
	authOK  bool
}

func newFakeLK(t *testing.T) *fakeLK {
	f := &fakeLK{t: t, uploads: map[string][]byte{}, authOK: true}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLK) nextID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%d", prefix, f.seq)
}

func (f *fakeLK) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	base := "/api/v3/projects/" + testProject + "/assets"
	if strings.HasPrefix(r.URL.Path, "/storage/") {
		if r.Header.Get("Authorization") != "" {
			f.t.Error("API key sent to storage")
		}
		b, _ := io.ReadAll(r.Body)
		f.uploads[strings.TrimPrefix(r.URL.Path, "/storage/")] = b
		w.WriteHeader(http.StatusOK)
		return
	}
	if r.Header.Get("Authorization") == "" {
		f.authOK = false
	}
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == base:
		folder := r.URL.Query().Get("folderId")
		data := []map[string]any{}
		for _, a := range f.assets {
			if fid, _ := a["folderId"].(string); fid == folder {
				data = append(data, a)
			}
		}
		reply(map[string]any{"data": data, "hasMore": false})
	case r.Method == http.MethodPost && r.URL.Path == base:
		a := map[string]any{"id": f.nextID("folder"), "name": body["name"], "type": "FOLDER"}
		if fid, ok := body["folderId"]; ok {
			a["folderId"] = fid
		}
		f.assets = append(f.assets, a)
		reply(a)
	case r.Method == http.MethodPost && r.URL.Path == base+"/uploads":
		key := f.nextID("upload")
		reply(map[string]any{
			"uploadKey": key, "uploadUrl": f.srv.URL + "/storage/" + key, "method": "PUT",
			"headers": map[string]string{"Content-Type": body["contentType"].(string)}, "expiresAt": "2099-01-01T00:00:00Z",
		})
	case r.Method == http.MethodPost && r.URL.Path == base+"/images":
		key, _ := body["uploadKey"].(string)
		if _, ok := f.uploads[key]; !ok {
			http.Error(w, `{"error":{"code":"validation","message":"no upload"}}`, http.StatusUnprocessableEntity)
			return
		}
		id := f.nextID("img")
		a := map[string]any{"id": id, "name": body["name"], "type": "IMAGE",
			"url": "https://assets.legendkeeper.com/" + id + ".png", "width": 64, "height": 32}
		if fid, ok := body["folderId"]; ok {
			a["folderId"] = fid
		}
		f.assets = append(f.assets, a)
		reply(a)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeLK) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func newTestBridge(t *testing.T, f *fakeLK, cacheDir string) *Bridge {
	t.Helper()
	c, err := lk.New("test-key", lk.WithBaseURL(f.srv.URL), lk.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Config{DefaultProject: testProject, CacheDir: cacheDir, Assets: c.Assets, Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func pngMedia(seed byte) *media.Fetched {
	// The PNG signature is enough for type detection; LK validates server-side.
	return &media.Fetched{
		Bytes:       append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), seed),
		ContentType: media.TypePNG, Filename: "ser_thaddeus.png", Width: 64, Height: 32,
		SourceURL: "https://cdn.discordapp.com/avatars/1/abc.png?size=1024",
	}
}

func TestUploadCreatesFolderPathAndDedupes(t *testing.T) {
	f := newFakeLK(t)
	dir := t.TempDir()
	b := newTestBridge(t, f, dir)
	ctx := context.Background()

	res, err := b.Upload(ctx, Request{FolderPath: "Druskenvald SPG/Characters", Name: "Ser Thaddeus Mournvale", Media: pngMedia(1)})
	if err != nil {
		t.Fatal(err)
	}
	if res.AssetID == "" || !strings.HasPrefix(res.URL, "https://assets.legendkeeper.com/") || res.Deduplicated {
		t.Fatalf("unexpected result %+v", res)
	}
	if res.FolderID == "" || res.Project != testProject || res.Name != "Ser Thaddeus Mournvale" {
		t.Fatalf("folder/project/name not set: %+v", res)
	}
	if got := f.count("POST /api/v3/projects/" + testProject + "/assets"); got != 2+2 { // 2 folders + upload + image
		t.Fatalf("expected 2 folder creates and one upload, saw %d POSTs: %v", got, f.calls)
	}
	if !f.authOK {
		t.Fatal("API call without Authorization")
	}

	// Same bytes again: deduplicated, no new upload.
	again, err := b.Upload(ctx, Request{FolderPath: "Druskenvald SPG/Characters", Name: "dup", Media: pngMedia(1)})
	if err != nil {
		t.Fatal(err)
	}
	if !again.Deduplicated || again.AssetID != res.AssetID {
		t.Fatalf("expected dedupe hit, got %+v", again)
	}
	if got := f.count("POST /api/v3/projects/" + testProject + "/assets/images"); got != 1 {
		t.Fatalf("image created %d times", got)
	}

	// The dedupe index survives a restart (new Bridge, same cache dir).
	b2 := newTestBridge(t, f, dir)
	again, err = b2.Upload(ctx, Request{Media: pngMedia(1)})
	if err != nil || !again.Deduplicated {
		t.Fatalf("persisted dedupe: %+v %v", again, err)
	}

	// Different bytes into the same path reuse the existing folders.
	other, err := b.Upload(ctx, Request{FolderPath: "druskenvald spg/characters", Media: pngMedia(2)})
	if err != nil {
		t.Fatal(err)
	}
	if other.FolderID != res.FolderID || other.Name != "ser thaddeus" {
		t.Fatalf("folder reuse / default name: %+v", other)
	}
	if got := f.count("POST /api/v3/projects/" + testProject + "/assets/uploads"); got != 2 {
		t.Fatalf("uploads: %d", got)
	}
}

func TestUploadValidation(t *testing.T) {
	f := newFakeLK(t)
	b := newTestBridge(t, f, "")
	ctx := context.Background()
	if _, err := b.Upload(ctx, Request{FolderID: "x", FolderPath: "y", Media: pngMedia(1)}); err == nil {
		t.Error("folder id and path together must fail")
	}
	if _, err := b.Upload(ctx, Request{Name: strings.Repeat("n", MaxNameLen+1), Media: pngMedia(1)}); err == nil {
		t.Error("over-long name must fail")
	}
	if _, err := b.Upload(ctx, Request{Media: &media.Fetched{}}); err == nil {
		t.Error("empty bytes must fail")
	}
	noDefault, _ := New(Config{Assets: b.assets})
	if _, err := noDefault.Upload(ctx, Request{Media: pngMedia(1)}); err == nil || !strings.Contains(err.Error(), "lk_project") {
		t.Errorf("missing project: %v", err)
	}
	var unconfigured *Bridge
	if _, err := unconfigured.Upload(ctx, Request{Media: pngMedia(1)}); err != ErrNotConfigured {
		t.Errorf("nil bridge: %v", err)
	}
	if got, err := New(Config{}); got != nil || err != nil {
		t.Errorf("New without a key should be (nil, nil), got %v, %v", got, err)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct{ name, file, want string }{
		{"Ser Thaddeus Mournvale", "x.png", "Ser Thaddeus Mournvale"},
		{"  a\tb\nc  ", "", "a b c"},
		{"a/b\\c", "", "a b c"},
		{"bell\x07", "", "bell"},
		{"", "ser_thaddeus-mournvale.png", "ser thaddeus mournvale"},
		{"", "noext", "noext"},
	}
	for _, tt := range tests {
		got, err := SanitizeName(tt.name, tt.file)
		if err != nil || got != tt.want {
			t.Errorf("SanitizeName(%q,%q) = %q, %v; want %q", tt.name, tt.file, got, err, tt.want)
		}
	}
	if _, err := SanitizeName("", ""); err == nil {
		t.Error("no name and no filename must fail")
	}
}
