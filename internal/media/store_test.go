package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pngFetched(t *testing.T, w, h int) *Fetched {
	t.Helper()
	return &Fetched{Bytes: pngBytes(t, w, h), ContentType: TypePNG, Filename: "art.png", Width: w, Height: h}
}

func TestResolvePath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "media")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Linked"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlinked directory inside the root that points outside it.
	if err := os.Symlink(outside, filepath.Join(root, "Escape")); err != nil {
		t.Fatal(err)
	}
	// A symlinked directory that stays inside the root is fine.
	if err := os.Symlink(filepath.Join(root, "Linked"), filepath.Join(root, "Alias")); err != nil {
		t.Fatal(err)
	}
	// A file symlink must never be written through.
	if err := os.WriteFile(filepath.Join(outside, "victim.png"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "victim.png"), filepath.Join(root, "trap.png")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		allowAbs bool
		saveTo   string
		ct       string
		want     string // relative to root ("" = expect error); "ABS:" prefix = absolute
		errIs    error
	}{
		{"relative nested", false, "Characters/DanteKeane-portrait.png", TypePNG, "Characters/DanteKeane-portrait.png", nil},
		{"extension appended", false, "Characters/Dante", TypePNG, "Characters/Dante.png", nil},
		{"jpeg gets .jpg", false, "maps/world", TypeJPEG, "maps/world.jpg", nil},
		{".jpeg accepted", false, "maps/world.JPEG", TypeJPEG, "maps/world.JPEG", nil},
		{"dotted name is not an extension", false, "Ser T. Mournvale", TypeGIF, "Ser T. Mournvale.gif", nil},
		{"extension mismatch", false, "Characters/Dante.png", TypeJPEG, "", ErrPath},
		{"non-image extension mismatch", false, "notes.txt", TypePNG, "", ErrPath},
		{"dotdot escape", false, "../outside.png", TypePNG, "", ErrPath},
		{"nested dotdot escape", false, "a/../../outside.png", TypePNG, "", ErrPath},
		{"dotdot that stays inside", false, "a/../b/ok.png", TypePNG, "b/ok.png", nil},
		{"symlinked parent escapes", false, "Escape/x.png", TypePNG, "", ErrPath},
		{"symlinked parent, new subdir", false, "Escape/new/deep/x.png", TypePNG, "", ErrPath},
		{"symlinked parent inside root", false, "Alias/x.png", TypePNG, "Alias/x.png", nil},
		{"symlink target", false, "trap.png", TypePNG, "", ErrPath},
		{"directory name", false, "Characters/", TypePNG, "", ErrPath},
		{"empty", false, "  ", TypePNG, "", ErrPath},
		{"root itself", false, ".", TypePNG, "", ErrPath},
		{"absolute inside root", false, filepath.Join(root, "abs/in.png"), TypePNG, "abs/in.png", nil},
		{"absolute outside denied", false, filepath.Join(outside, "o.png"), TypePNG, "", ErrPath},
		{"absolute outside allowed", true, filepath.Join(outside, "o.png"), TypePNG, "ABS:" + filepath.Join(outside, "o.png"), nil},
		{"unsupported type", false, "x", "text/html", "", ErrUnsupportedType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewStore(root, tt.allowAbs)
			got, err := s.ResolvePath(tt.saveTo, tt.ct)
			if tt.want == "" {
				if err == nil || (tt.errIs != nil && !errors.Is(err, tt.errIs)) {
					t.Fatalf("ResolvePath(%q) = %q, %v; want error %v", tt.saveTo, got, err, tt.errIs)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePath(%q): %v", tt.saveTo, err)
			}
			want := filepath.Join(root, tt.want)
			if strings.HasPrefix(tt.want, "ABS:") {
				want = strings.TrimPrefix(tt.want, "ABS:")
			}
			if got != want {
				t.Fatalf("ResolvePath(%q) = %q, want %q", tt.saveTo, got, want)
			}
			if fi, err := os.Stat(filepath.Dir(got)); err != nil || !fi.IsDir() {
				t.Fatalf("parent not created: %v", err)
			}
		})
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 1 { // only victim.png and nothing created through Escape/
		t.Fatalf("something was created outside the root: %v", entries)
	}
}

func TestSaveDedupeAndOverwrite(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, false)
	first := pngFetched(t, 40, 20)

	saved, err := s.Save(first, "Characters/Dante", false)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(first.Bytes)
	if saved.Path != filepath.Join(root, "Characters/Dante.png") || saved.Filename != "Dante.png" || saved.SHA256 != hex.EncodeToString(sum[:]) ||
		saved.Bytes != len(first.Bytes) || saved.Width != 40 || saved.Overwritten || saved.Deduplicated {
		t.Fatalf("saved = %+v", saved)
	}
	b, _ := os.ReadFile(saved.Path)
	fi, _ := os.Stat(saved.Path)
	if !bytes.Equal(b, first.Bytes) || fi.Mode().Perm() != 0o644 {
		t.Fatalf("content or mode wrong: %v", fi.Mode())
	}
	if dfi, _ := os.Stat(filepath.Dir(saved.Path)); dfi.Mode().Perm() != 0o755 {
		t.Fatalf("dir mode = %v", dfi.Mode())
	}

	// Identical bytes: deduplicated, file untouched.
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(saved.Path, old, old)
	again, err := s.Save(first, "Characters/Dante.png", false)
	if err != nil || !again.Deduplicated || again.Overwritten {
		t.Fatalf("dedupe: %+v %v", again, err)
	}
	if fi, _ := os.Stat(saved.Path); !fi.ModTime().Equal(old) {
		t.Fatal("deduplicated save rewrote the file")
	}

	// Different bytes without overwrite: refused, original kept.
	second := pngFetched(t, 30, 30)
	if _, err := s.Save(second, "Characters/Dante.png", false); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if b, _ := os.ReadFile(saved.Path); !bytes.Equal(b, first.Bytes) {
		t.Fatal("refused save changed the file")
	}

	// With overwrite: replaced atomically, no temp files left behind.
	over, err := s.Save(second, "Characters/Dante.png", true)
	if err != nil || !over.Overwritten || over.Deduplicated {
		t.Fatalf("overwrite: %+v %v", over, err)
	}
	if b, _ := os.ReadFile(saved.Path); !bytes.Equal(b, second.Bytes) {
		t.Fatal("overwrite did not replace the content")
	}
	entries, _ := os.ReadDir(filepath.Dir(saved.Path))
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
	if _, err := s.Save(&Fetched{}, "x.png", false); err == nil {
		t.Fatal("empty payload must fail")
	}
}

func TestList(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root, false)
	for _, p := range []string{"Characters/Dante", "Characters/Ysolde", "Maps/world"} {
		if _, err := s.Save(pngFetched(t, 12, 8), p, true); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(root, "notes.txt"), []byte("hello"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "Characters", ".gocord-123.tmp"), []byte("partial"), 0o644)
	_ = os.MkdirAll(filepath.Join(root, ".hidden"), 0o755)
	_ = os.WriteFile(filepath.Join(root, ".hidden", "secret.png"), pngBytes(t, 2, 2), 0o644)
	outside := t.TempDir()
	_ = os.WriteFile(filepath.Join(outside, "o.png"), pngBytes(t, 2, 2), 0o644)
	_ = os.Symlink(outside, filepath.Join(root, "Escape"))
	_ = os.Symlink(filepath.Join(outside, "o.png"), filepath.Join(root, "link.png"))

	files, truncated, err := s.List("", "")
	if err != nil || truncated {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		rel, _ := filepath.Rel(root, f.Path)
		names = append(names, rel)
	}
	if strings.Join(names, ",") != "Characters/Dante.png,Characters/Ysolde.png,Maps/world.png,notes.txt" {
		t.Fatalf("files = %v", names)
	}
	if files[0].Mime != TypePNG || files[0].Width != 12 || files[0].Height != 8 || files[0].Bytes == 0 || files[0].ModifiedAt == "" {
		t.Fatalf("entry = %+v", files[0])
	}
	if files[3].Mime != "text/plain" || files[3].Width != 0 {
		t.Fatalf("text entry = %+v", files[3])
	}

	sub, _, err := s.List("Characters", "")
	if err != nil || len(sub) != 2 {
		t.Fatalf("subdir: %v %v", sub, err)
	}
	g, _, err := s.List("", "*.png")
	if err != nil || len(g) != 3 {
		t.Fatalf("glob by name: %v %v", g, err)
	}
	g, _, err = s.List("", "Maps/*")
	if err != nil || len(g) != 1 {
		t.Fatalf("glob by path: %v %v", g, err)
	}
	for _, bad := range []struct{ sub, glob string }{{"../", ""}, {"Escape", ""}, {"/etc", ""}, {"Missing", ""}, {"", "["}} {
		if _, _, err := s.List(bad.sub, bad.glob); !errors.Is(err, ErrPath) {
			t.Errorf("List(%q,%q) = %v, want ErrPath", bad.sub, bad.glob, err)
		}
	}
	empty := NewStore(filepath.Join(root, "does-not-exist"), false)
	if files, _, err := empty.List("", ""); err != nil || len(files) != 0 {
		t.Fatalf("missing root: %v %v", files, err)
	}
}
