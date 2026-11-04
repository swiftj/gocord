package lkbridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DedupeFile is the index file name inside the cache directory.
const DedupeFile = "lk-assets.json"

// Entry records one uploaded asset.
type Entry struct {
	AssetID    string `json:"asset_id"`
	URL        string `json:"url"`
	Project    string `json:"project"`
	FolderID   string `json:"folder_id,omitempty"`
	Name       string `json:"name,omitempty"`
	UploadedAt string `json:"uploaded_at,omitempty"`
}

// Dedupe maps project + sha256(bytes) to the asset already uploaded. It is
// kept on disk so repeat uploads across restarts are skipped.
type Dedupe struct {
	path string // "" = memory only

	mu      sync.Mutex
	loaded  bool
	entries map[string]Entry // project + ":" + sha -> entry
}

// NewDedupe returns an index stored in dir/lk-assets.json ("" = memory only).
func NewDedupe(dir string) *Dedupe {
	d := &Dedupe{entries: map[string]Entry{}}
	if dir != "" {
		d.path = filepath.Join(dir, DedupeFile)
	}
	return d
}

func dedupeKey(project, sha string) string { return project + ":" + sha }

// Lookup returns the asset uploaded for these bytes in this project.
func (d *Dedupe) Lookup(project, sha string) (Entry, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.loadLocked()
	e, ok := d.entries[dedupeKey(project, sha)]
	return e, ok
}

// Store records an upload and rewrites the index file atomically.
func (d *Dedupe) Store(project, sha string, e Entry) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.loadLocked()
	if e.UploadedAt == "" {
		e.UploadedAt = time.Now().UTC().Format(time.RFC3339)
	}
	d.entries[dedupeKey(project, sha)] = e
	if d.path == "" {
		return nil
	}
	b, err := json.MarshalIndent(d.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("dedupe: encode: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(d.path), 0o700); err != nil {
		return fmt.Errorf("dedupe: create dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(d.path), DedupeFile+".*")
	if err != nil {
		return fmt.Errorf("dedupe: write: %w", err)
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return fmt.Errorf("dedupe: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("dedupe: write: %w", err)
	}
	if err := os.Rename(tmp.Name(), d.path); err != nil {
		return fmt.Errorf("dedupe: write: %w", err)
	}
	return nil
}

// loadLocked reads the file once. A missing or corrupt file starts empty.
func (d *Dedupe) loadLocked() {
	if d.loaded || d.path == "" {
		d.loaded = true
		return
	}
	d.loaded = true
	b, err := os.ReadFile(d.path)
	if err != nil { // missing (first run) or unreadable: start empty
		return
	}
	var m map[string]Entry
	if json.Unmarshal(b, &m) == nil && m != nil {
		d.entries = m
	}
}
