package media

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// Store environment variables.
const (
	EnvMediaDir      = "GOCORD_MEDIA_DIR"
	EnvAllowAbsolute = "GOCORD_MEDIA_ALLOW_ABSOLUTE"
	// MaxListFiles bounds a List result.
	MaxListFiles = 1000
	tmpPrefix    = ".gocord-"
)

// ErrPath reports a save_to (or subdir) that is malformed or not permitted.
var ErrPath = errors.New("invalid path")

// ErrExists reports an existing file with different content and no overwrite.
var ErrExists = errors.New("file exists")

// extByType is the extension appended when save_to has none.
var extByType = map[string]string{TypePNG: ".png", TypeJPEG: ".jpg", TypeGIF: ".gif", TypeWebP: ".webp"}

// extOK lists the extensions that agree with each type.
var extOK = map[string][]string{TypePNG: {".png"}, TypeJPEG: {".jpg", ".jpeg"}, TypeGIF: {".gif"}, TypeWebP: {".webp"}}

// extRe recognises a real extension, so "Ser T. Mournvale" has none.
var extRe = regexp.MustCompile(`^\.[A-Za-z0-9]{1,5}$`)

// Store writes images under a root directory and lists what is there.
type Store struct {
	root          string
	allowAbsolute bool
}

// NewStore returns a Store rooted at root ("~" must already be expanded).
// allowAbsolute permits absolute paths outside the root.
func NewStore(root string, allowAbsolute bool) *Store {
	return &Store{root: filepath.Clean(root), allowAbsolute: allowAbsolute}
}

// Root returns the absolute media root.
func (s *Store) Root() string {
	if abs, err := filepath.Abs(s.root); err == nil {
		return abs
	}
	return s.root
}

// Saved describes a file written (or found identical) by Save.
type Saved struct {
	Path         string `json:"path"`
	Mime         string `json:"mime"`
	Filename     string `json:"filename"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	Bytes        int    `json:"bytes"`
	SHA256       string `json:"sha256"`
	Overwritten  bool   `json:"overwritten"`
	Deduplicated bool   `json:"deduplicated"`
}

// Save writes f to saveTo. An existing file with the same sha256 is left
// alone (Deduplicated); a different one is an ErrExists unless overwrite, in
// which case it is replaced atomically.
func (s *Store) Save(f *Fetched, saveTo string, overwrite bool) (*Saved, error) {
	if f == nil || len(f.Bytes) == 0 {
		return nil, errors.New("media: nothing to save")
	}
	p, err := s.ResolvePath(saveTo, f.ContentType)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(f.Bytes)
	out := &Saved{
		Path: p, Mime: f.ContentType, Filename: filepath.Base(p), Width: f.Width, Height: f.Height,
		Bytes: len(f.Bytes), SHA256: hex.EncodeToString(sum[:]),
	}
	existing, err := fileSHA256(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return nil, fmt.Errorf("media: read existing %s: %w", p, err)
	case existing == out.SHA256:
		out.Deduplicated = true
		return out, nil
	case !overwrite:
		return nil, fmt.Errorf("%w: %s has different content; pass overwrite:true to replace it", ErrExists, p)
	default:
		out.Overwritten = true
	}
	if err := writeAtomic(p, f.Bytes); err != nil {
		return nil, err
	}
	return out, nil
}

// ResolvePath turns save_to into an absolute file path for contentType,
// creating parent directories. Relative paths resolve under the root and may
// not escape it, including through symlinked directories. Absolute paths
// inside the root are always accepted; outside it they need allowAbsolute.
// A missing extension is appended; a conflicting one is an error.
func (s *Store) ResolvePath(saveTo, contentType string) (string, error) {
	saveTo = strings.TrimSpace(saveTo)
	if saveTo == "" || strings.ContainsRune(saveTo, 0) {
		return "", fmt.Errorf("%w: save_to is required", ErrPath)
	}
	if strings.HasSuffix(saveTo, "/") || strings.HasSuffix(saveTo, string(filepath.Separator)) {
		return "", fmt.Errorf("%w: save_to %q names a directory; include a file name", ErrPath, saveTo)
	}
	if base := filepath.Base(filepath.Clean(saveTo)); strings.HasPrefix(base, ".") {
		return "", fmt.Errorf("%w: save_to file name %q must not start with '.' (hidden files are not listed)", ErrPath, base)
	}
	want, ok := extByType[contentType]
	if !ok {
		return "", fmt.Errorf("%w: cannot save content type %s", ErrUnsupportedType, contentType)
	}
	if ext := filepath.Ext(saveTo); extRe.MatchString(ext) {
		if !slices.Contains(extOK[contentType], strings.ToLower(ext)) {
			return "", fmt.Errorf("%w: save_to extension %s does not match the output format %s (use %s, or set format to match)",
				ErrPath, ext, contentType, strings.Join(extOK[contentType], " or "))
		}
	} else {
		saveTo += want
	}

	root, rootReal, err := s.ensureRoot()
	if err != nil {
		return "", err
	}
	var p string
	restricted := true
	if filepath.IsAbs(saveTo) {
		p = filepath.Clean(saveTo)
		if !within(root, p) && !within(rootReal, p) {
			if !s.allowAbsolute {
				return "", fmt.Errorf("%w: absolute save_to %s is outside the media root %s; use a relative path or set %s=true",
					ErrPath, p, root, EnvAllowAbsolute)
			}
			restricted = false
		}
	} else {
		p = filepath.Join(root, saveTo)
		if !within(root, p) {
			return "", fmt.Errorf("%w: save_to %q escapes the media root %s", ErrPath, saveTo, root)
		}
	}

	parent := filepath.Dir(p)
	if restricted {
		// Check the deepest existing ancestor before creating anything, so a
		// symlinked directory cannot make MkdirAll write outside the root.
		anc := parent
		for {
			if _, err := os.Lstat(anc); err == nil {
				break
			}
			next := filepath.Dir(anc)
			if next == anc {
				break
			}
			anc = next
		}
		if err := checkWithin(anc, rootReal, saveTo); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(parent, 0o755); err != nil { //nolint:gosec // G301: the spec wants a world-readable media tree for other local tools
		return "", fmt.Errorf("media: create %s: %w", parent, err)
	}
	if restricted {
		if err := checkWithin(parent, rootReal, saveTo); err != nil {
			return "", err
		}
	}
	if fi, err := os.Lstat(p); err == nil {
		switch {
		case fi.Mode()&fs.ModeSymlink != 0:
			return "", fmt.Errorf("%w: %s is a symlink; refusing to write through it", ErrPath, p)
		case fi.IsDir():
			return "", fmt.Errorf("%w: %s is a directory", ErrPath, p)
		}
	}
	return p, nil
}

// ensureRoot creates the root and returns its absolute and symlink-resolved forms.
func (s *Store) ensureRoot() (string, string, error) {
	root := s.Root()
	if err := os.MkdirAll(root, 0o755); err != nil { //nolint:gosec // G301: see ResolvePath
		return "", "", fmt.Errorf("media: create media root %s: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("media: resolve media root %s: %w", root, err)
	}
	return root, resolved, nil
}

// checkWithin resolves dir's symlinks and requires it to be inside rootReal.
func checkWithin(dir, rootReal, ref string) error {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("media: resolve %s: %w", dir, err)
	}
	if resolved != rootReal && !within(rootReal, resolved) {
		return fmt.Errorf("%w: %q resolves outside the media root (through a symlink)", ErrPath, ref)
	}
	return nil
}

// within reports whether p is strictly inside root.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p) //nolint:gosec // G304: p was resolved by ResolvePath
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeAtomic writes b to a temp file in p's directory, fsyncs it, and
// renames it over p, so readers never see a partial file.
func writeAtomic(p string, b []byte) error {
	dir := filepath.Dir(p)
	tmp, err := os.CreateTemp(dir, tmpPrefix+"*.tmp")
	if err != nil {
		return fmt.Errorf("media: write %s: %w", p, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(b); err != nil {
		return fmt.Errorf("media: write %s: %w", p, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("media: sync %s: %w", p, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("media: write %s: %w", p, err)
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil { //nolint:gosec // G302: readable by the uploading tool
		return fmt.Errorf("media: chmod %s: %w", p, err)
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return fmt.Errorf("media: rename into %s: %w", p, err)
	}
	ok = true
	if d, err := os.Open(dir); err == nil { //nolint:gosec // G304: dir was resolved by ResolvePath
		_ = d.Sync() // persist the rename; best effort
		_ = d.Close()
	}
	return nil
}

// FileEntry is one file reported by List.
type FileEntry struct {
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
	Mime       string `json:"mime"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
	ModifiedAt string `json:"modified_at"`
}

// List walks subdir (relative to the root; "" = the root) and returns its
// files, optionally filtered by a glob matched against the base name or the
// path relative to the root. Hidden files, temp files, and symlinks are
// skipped. It reports truncated when more than MaxListFiles match.
func (s *Store) List(subdir, glob string) ([]FileEntry, bool, error) {
	if glob != "" {
		if _, err := filepath.Match(glob, ""); err != nil {
			return nil, false, fmt.Errorf("%w: glob %q: %v", ErrPath, glob, err)
		}
	}
	root := s.Root()
	if _, err := os.Stat(root); errors.Is(err, fs.ErrNotExist) {
		return []FileEntry{}, false, nil
	}
	rootReal, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, false, fmt.Errorf("media: resolve media root %s: %w", root, err)
	}
	dir := root
	if sub := strings.TrimSpace(subdir); sub != "" && sub != "." {
		if filepath.IsAbs(sub) {
			return nil, false, fmt.Errorf("%w: subdir must be relative to the media root %s", ErrPath, root)
		}
		dir = filepath.Join(root, sub)
		if !within(root, dir) {
			return nil, false, fmt.Errorf("%w: subdir %q escapes the media root", ErrPath, subdir)
		}
		if _, err := os.Stat(dir); err != nil {
			return nil, false, fmt.Errorf("%w: subdir %q: %v", ErrPath, subdir, err)
		}
		if err := checkWithin(dir, rootReal, subdir); err != nil {
			return nil, false, err
		}
	}

	files := []FileEntry{}
	truncated := false
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped
		}
		name := d.Name()
		if p != dir && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if glob != "" {
			mb, _ := filepath.Match(glob, name)
			mr, _ := filepath.Match(glob, rel)
			if !mb && !mr {
				return nil
			}
		}
		if len(files) >= MaxListFiles {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, describeFile(p, d))
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("media: list %s: %w", dir, err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, truncated, nil
}

func describeFile(p string, d fs.DirEntry) FileEntry {
	e := FileEntry{Path: p}
	if info, err := d.Info(); err == nil {
		e.Bytes = info.Size()
		e.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339)
	}
	f, err := os.Open(p) //nolint:gosec // G304: p is a regular file found under the media root
	if err != nil {
		return e
	}
	defer f.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	e.Mime = http.DetectContentType(head[:n])
	if i := strings.IndexByte(e.Mime, ';'); i >= 0 {
		e.Mime = e.Mime[:i]
	}
	if slices.Contains(AcceptedTypes, e.Mime) {
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				e.Width, e.Height = cfg.Width, cfg.Height
			}
		}
	}
	return e
}
