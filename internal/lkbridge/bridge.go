// Package lkbridge uploads images into a LegendKeeper project's asset library
// through the golk SDK, creating folders by path and deduplicating repeat
// uploads of identical bytes. See specs/05-media-and-legendkeeper.md.
package lkbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/swiftj/golk/lk"

	"github.com/local/discord-mcp/internal/media"
)

// MaxNameLen is the longest asset name accepted, in characters.
const MaxNameLen = 120

// ErrNotConfigured is returned by every bridge tool when LEGENDKEEPER_API_KEY is unset.
var ErrNotConfigured = errors.New("LegendKeeper not configured: set LEGENDKEEPER_API_KEY (and LEGENDKEEPER_PROJECT_ID for a default project)")

// Assets is the part of the golk asset service the bridge uses. *lk.AssetsService implements it.
type Assets interface {
	List(ctx context.Context, p lk.AssetListParams, opts ...lk.CallOption) (*lk.Page[lk.Asset], error)
	CreateFolder(ctx context.Context, f lk.AssetFolderCreate, opts ...lk.CallOption) (*lk.Asset, error)
	UploadImage(ctx context.Context, p lk.UploadImageParams, opts ...lk.CallOption) (*lk.Asset, error)
}

// Config configures New.
type Config struct {
	APIKey         string
	DefaultProject string
	BaseURL        string
	// CacheDir holds lk-assets.json (the dedupe index). Empty disables dedupe persistence.
	CacheDir string
	Logger   *slog.Logger
	// Assets overrides the golk client (tests).
	Assets Assets
}

// Bridge uploads images to LegendKeeper.
type Bridge struct {
	assets         Assets
	defaultProject string
	dedupe         *Dedupe
	log            *slog.Logger

	mu      sync.Mutex
	folders map[string]string // project + "\x00" + normalized path -> folder ID
}

// New returns a Bridge, or (nil, nil) when cfg.APIKey is empty and no Assets
// override is given: the bridge tools then report ErrNotConfigured.
func New(cfg Config) (*Bridge, error) {
	assets := cfg.Assets
	if assets == nil {
		if cfg.APIKey == "" {
			return nil, nil
		}
		opts := []lk.Option{lk.WithUserAgent("gocord"), lk.WithTimeout(60 * time.Second)}
		if cfg.BaseURL != "" {
			opts = append(opts, lk.WithBaseURL(cfg.BaseURL))
		}
		if cfg.DefaultProject != "" {
			opts = append(opts, lk.WithProjectID(cfg.DefaultProject))
		}
		c, err := lk.New(cfg.APIKey, opts...)
		if err != nil {
			return nil, fmt.Errorf("legendkeeper client: %w", err)
		}
		assets = c.Assets
	}
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Bridge{
		assets: assets, defaultProject: cfg.DefaultProject, dedupe: NewDedupe(cfg.CacheDir),
		log: log, folders: map[string]string{},
	}, nil
}

// Request is one upload.
type Request struct {
	Project     string // "" = the default project
	FolderID    string // exclusive with FolderPath
	FolderPath  string // "A/B/C", created if missing
	Name        string // "" = the source filename's stem
	Description string
	Media       *media.Fetched
}

// Result describes the created (or deduplicated) asset.
type Result struct {
	AssetID      string `json:"asset_id"`
	URL          string `json:"url"`
	Name         string `json:"name"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	ContentType  string `json:"content_type"`
	Bytes        int    `json:"bytes"`
	FolderID     string `json:"folder_id,omitempty"`
	SourceURL    string `json:"source_url,omitempty"`
	Project      string `json:"project"`
	Deduplicated bool   `json:"deduplicated,omitempty"`
}

// Upload sends one image to the asset library.
func (b *Bridge) Upload(ctx context.Context, r Request) (*Result, error) {
	if b == nil {
		return nil, ErrNotConfigured
	}
	if r.Media == nil || len(r.Media.Bytes) == 0 {
		return nil, errors.New("no image bytes to upload")
	}
	project := strings.TrimSpace(r.Project)
	if project == "" {
		project = b.defaultProject
	}
	if project == "" {
		return nil, errors.New("lk_project is required (or set LEGENDKEEPER_PROJECT_ID)")
	}
	if r.FolderID != "" && r.FolderPath != "" {
		return nil, errors.New("pass lk_folder_id or lk_folder_path, not both")
	}
	name, err := SanitizeName(r.Name, r.Media.Filename)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(r.Media.Bytes)
	sha := hex.EncodeToString(sum[:])
	res := &Result{
		Name: name, ContentType: r.Media.ContentType, Bytes: len(r.Media.Bytes), SourceURL: r.Media.SourceURL,
		Project: project, Width: r.Media.Width, Height: r.Media.Height,
	}
	if e, ok := b.dedupe.Lookup(project, sha); ok {
		res.AssetID, res.URL, res.FolderID, res.Deduplicated = e.AssetID, e.URL, e.FolderID, true
		if e.Name != "" {
			res.Name = e.Name
		}
		b.log.Info("legendkeeper upload deduplicated", "asset_id", e.AssetID, "bytes", res.Bytes)
		return res, nil
	}

	folderID := r.FolderID
	if r.FolderPath != "" {
		if folderID, err = b.EnsureFolder(ctx, project, r.FolderPath); err != nil {
			return nil, err
		}
	}
	p := lk.UploadImageParams{Data: r.Media.Bytes, ContentType: r.Media.ContentType, Name: name}
	if r.Description != "" {
		p.Description = lk.Some(r.Description)
	}
	if folderID != "" {
		p.FolderID = lk.Some(folderID)
	}
	a, err := b.assets.UploadImage(ctx, p, lk.ForProject(project))
	if err != nil {
		return nil, fmt.Errorf("legendkeeper upload: %w", err)
	}
	res.AssetID, res.URL, res.FolderID = a.ID, a.URL, a.FolderID
	if a.Width > 0 {
		res.Width, res.Height = a.Width, a.Height
	}
	if a.Name != "" {
		res.Name = a.Name
	}
	b.log.Info("legendkeeper upload", "asset_id", a.ID, "bytes", res.Bytes)
	if err := b.dedupe.Store(project, sha, Entry{AssetID: a.ID, URL: a.URL, Project: project, FolderID: res.FolderID, Name: res.Name}); err != nil {
		b.log.Warn("legendkeeper dedupe index not saved", "err", err)
	}
	return res, nil
}

// EnsureFolder resolves a "/"-separated folder path to a folder ID, creating
// missing folders. Matching is case-insensitive; results are cached.
func (b *Bridge) EnsureFolder(ctx context.Context, project, folderPath string) (string, error) {
	parts := splitPath(folderPath)
	if len(parts) == 0 {
		return "", nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	parent := ""
	for i, name := range parts {
		key := project + "\x00" + strings.ToLower(strings.Join(parts[:i+1], "/"))
		if id, ok := b.folders[key]; ok {
			parent = id
			continue
		}
		id, err := b.findFolder(ctx, project, parent, name)
		if err != nil {
			return "", err
		}
		if id == "" {
			f := lk.AssetFolderCreate{Name: name}
			if parent != "" {
				f.FolderID = lk.Some(parent)
			}
			a, err := b.assets.CreateFolder(ctx, f, lk.ForProject(project))
			if err != nil {
				return "", fmt.Errorf("legendkeeper create folder %q: %w", strings.Join(parts[:i+1], "/"), err)
			}
			id = a.ID
			b.log.Info("legendkeeper folder created", "folder_id", id)
		}
		b.folders[key] = id
		parent = id
	}
	return parent, nil
}

func (b *Bridge) findFolder(ctx context.Context, project, parent, name string) (string, error) {
	cursor := ""
	for {
		page, err := b.assets.List(ctx, lk.AssetListParams{FolderID: parent, Limit: 100, Cursor: cursor}, lk.ForProject(project))
		if err != nil {
			return "", fmt.Errorf("legendkeeper list folder: %w", err)
		}
		for _, a := range page.Items {
			if a.Type == lk.AssetFolder && strings.EqualFold(a.Name, name) {
				return a.ID, nil
			}
		}
		if !page.HasMore || page.NextCursor == "" {
			return "", nil
		}
		cursor = page.NextCursor
	}
}

func splitPath(p string) []string {
	var out []string
	for part := range strings.SplitSeq(p, "/") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// SanitizeName returns a usable asset name: control characters and path
// separators removed, whitespace collapsed, at most MaxNameLen characters.
// An empty name falls back to the filename's stem.
func SanitizeName(name, filename string) (string, error) {
	if utf8.RuneCountInString(name) > MaxNameLen {
		return "", fmt.Errorf("name must be at most %d characters (got %d)", MaxNameLen, utf8.RuneCountInString(name))
	}
	clean := cleanName(name)
	if clean == "" {
		stem := filename
		if i := strings.LastIndexByte(stem, '.'); i > 0 {
			stem = stem[:i]
		}
		clean = cleanName(strings.NewReplacer("_", " ", "-", " ").Replace(stem))
	}
	if clean == "" {
		return "", errors.New("name is required (the source has no usable filename)")
	}
	if r := []rune(clean); len(r) > MaxNameLen {
		clean = strings.TrimSpace(string(r[:MaxNameLen]))
	}
	return clean, nil
}

func cleanName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == '\\' || unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.IsControl(r) || r == utf8.RuneError:
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
