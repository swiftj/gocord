package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/media"
)

// attachMaxFileBytes is the largest local file gocord reads for upload
// (100 MiB, Discord's limit at boost level 3). Images over the server's real
// limit are shrunk before upload; other files over it are refused.
const attachMaxFileBytes = discord.MaxAttachmentBytes

// attachOverhead is kept free under the upload limit for the multipart
// envelope and message JSON.
const attachOverhead = 64 << 10

// attachFileParam is the shared file_path schema option.
func attachFileParam() mcp.ToolOption {
	return mcp.WithString("file_path", mcp.Description(
		"A local file to attach: an absolute path, or a path relative to the media root (where discord_fetch_media and fetch_url_to_file save)."))
}

// attachFilesParam is the shared file_paths schema option.
func attachFilesParam() mcp.ToolOption {
	return mcp.WithArray("file_paths", mcp.WithStringItems(), mcp.MaxItems(discord.MaxAttachments), mcp.Description(
		"More local files to attach, as for file_path (at most 10 in all). Images over the server's upload limit are shrunk automatically; the files on disk are not changed."))
}

// attachImageParams are the optional image adjustments for attachments.
func attachImageParams() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("image_max_dimension", mcp.Min(0), mcp.Max(mediaMaxDimension), mcp.Description(
			"Scale attached images so the longest edge is at most this many pixels (0 or omitted: only shrink when over the upload limit).")),
		mcp.WithString("image_format", mcp.Enum("jpeg", "png", mediaFormatOriginal), mcp.Description(
			"Re-encode attached images as jpeg or png (default original; images over the upload limit become jpeg, or png if transparent).")),
	}
}

// attachParams are all attachment schema options.
func attachParams() []mcp.ToolOption {
	return append([]mcp.ToolOption{attachFileParam(), attachFilesParam()}, attachImageParams()...)
}

// attachFiles reads file_path and file_paths, resolves each against the media
// root when relative, and checks that each is a regular file within the size
// limit. It returns absolute paths in order, or a tool error.
func attachFiles(d deps.Deps, req mcp.CallToolRequest) ([]string, *mcp.CallToolResult) {
	var refs []string
	if p := strings.TrimSpace(req.GetString("file_path", "")); p != "" {
		refs = append(refs, p)
	}
	for _, p := range req.GetStringSlice("file_paths", nil) {
		if p = strings.TrimSpace(p); p != "" {
			refs = append(refs, p)
		}
	}
	if len(refs) > discord.MaxAttachments {
		return nil, errorf("%d files given; Discord allows at most %d per message", len(refs), discord.MaxAttachments)
	}
	if _, res := attachImageOpts(req); res != nil {
		return nil, res
	}
	out := make([]string, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		p, err := attachResolve(d, ref)
		if err != nil {
			return nil, errorResult(err)
		}
		if seen[p] {
			return nil, errorf("%q is attached twice", ref)
		}
		seen[p] = true
		fi, err := os.Stat(p)
		if err != nil {
			return nil, errorf("file %q: %v", ref, err)
		}
		if !fi.Mode().IsRegular() {
			return nil, errorf("file %q is not a regular file", ref)
		}
		if fi.Size() > attachMaxFileBytes {
			return nil, errorf("file %q is %d bytes; the limit is 100 MiB (%d bytes)", ref, fi.Size(), attachMaxFileBytes)
		}
		out = append(out, p)
	}
	return out, nil
}

func attachResolve(d deps.Deps, ref string) (string, error) {
	if filepath.IsAbs(ref) {
		return filepath.Clean(ref), nil
	}
	if d.MediaStore == nil {
		return "", fmt.Errorf("file %q must be an absolute path (no media root is configured)", ref)
	}
	return d.MediaStore.ResolveRead(ref)
}

// attachImageOpts reads image_max_dimension and image_format.
func attachImageOpts(req mcp.CallToolRequest) (media.FetchOptions, *mcp.CallToolResult) {
	var o media.FetchOptions
	dim := req.GetInt("image_max_dimension", 0)
	if dim < 0 || dim > mediaMaxDimension {
		return o, errorf("image_max_dimension must be 0-%d", mediaMaxDimension)
	}
	o.MaxDimension = dim
	switch f := strings.ToLower(strings.TrimSpace(req.GetString("image_format", ""))); f {
	case "", mediaFormatOriginal:
	case "jpeg", "jpg", "png":
		o.Format = f
	default:
		return o, errorf("image_format must be jpeg, png, or original")
	}
	return o, nil
}

// attachChange describes one attachment gocord shrank or re-encoded.
type attachChange struct {
	File      string `json:"file"`
	SentAs    string `json:"sent_as"`
	FromBytes int64  `json:"from_bytes"`
	ToBytes   int    `json:"to_bytes"`
	FromSize  string `json:"from_size,omitempty"`
	ToSize    string `json:"to_size,omitempty"`
}

// attachPrepared is the outcome of attachPrepare.
type attachPrepared struct {
	Paths   []string
	Changes []attachChange
	Limit   int64 // the upload limit applied, when one was needed
	cleanup func()
}

// Close removes any temporary copies.
func (p *attachPrepared) Close() {
	if p != nil && p.cleanup != nil {
		p.cleanup()
	}
}

// attachPrepare applies the image options and makes the attachments fit the
// upload limit of guildID ("" when unknown: the 10 MiB base limit). Images
// that change are written to a temporary directory; originals are untouched.
// The caller must Close the result.
func attachPrepare(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, paths []string, guildID string) (*attachPrepared, *mcp.CallToolResult) {
	out := &attachPrepared{Paths: paths}
	if len(paths) == 0 {
		return out, nil
	}
	opts, res := attachImageOpts(req)
	if res != nil {
		return nil, res
	}
	explicit := opts.MaxDimension > 0 || opts.Format != ""

	sizes := make([]int64, len(paths))
	var total int64
	for i, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, errorf("file %q: %v", p, err)
		}
		sizes[i] = fi.Size()
		total += sizes[i]
	}
	var limit int64
	if total+attachOverhead > discord.BaseUploadLimit {
		limit = attachUploadLimit(ctx, d, guildID)
		out.Limit = limit
	}
	over := limit > 0 && total+attachOverhead > limit
	if !explicit && !over {
		return out, nil
	}

	// Read the images; everything else is sent as is.
	type item struct {
		data  []byte
		image bool
	}
	items := make([]item, len(paths))
	var imageTotal, otherTotal int64
	for i, p := range paths {
		b, err := os.ReadFile(p) //nolint:gosec // G304: attaching a caller-chosen local file is the documented purpose
		if err != nil {
			return nil, errorf("file %q: %v", p, err)
		}
		_, serr := media.Sniff(b)
		items[i] = item{data: b, image: serr == nil}
		if items[i].image {
			imageTotal += sizes[i]
		} else {
			otherTotal += sizes[i]
		}
	}
	if over && otherTotal+attachOverhead > limit {
		return nil, errorf("the non-image files total %d bytes, over this server's %s upload limit; send them separately or make them smaller", otherTotal, mib(limit))
	}

	dir, err := os.MkdirTemp("", "gocord-attach-")
	if err != nil {
		return nil, errorResult(fmt.Errorf("create temp dir: %w", err))
	}
	out.cleanup = func() { _ = os.RemoveAll(dir) }
	fail := func(r *mcp.CallToolResult) (*attachPrepared, *mcp.CallToolResult) {
		out.Close()
		return nil, r
	}

	out.Paths = make([]string, len(paths))
	var final int64
	for i, p := range paths {
		out.Paths[i] = p
		if !items[i].image {
			final += sizes[i]
			continue
		}
		var budget int64
		if over {
			// Share the room left by other files in proportion to each image's size.
			budget = (limit - attachOverhead - otherTotal) * sizes[i] / max(imageTotal, 1)
		}
		f, err := media.Fit(items[i].data, filepath.Base(p), budget, opts)
		if err != nil {
			if errors.Is(err, media.ErrCannotFit) && over {
				return fail(errorf("%v; this server's upload limit is %s", err, mib(limit)))
			}
			return fail(errorResult(err))
		}
		if len(f.Bytes) == len(items[i].data) && f.Filename == filepath.Base(p) {
			final += sizes[i]
			continue // unchanged
		}
		sub := filepath.Join(dir, fmt.Sprint(i))
		if err := os.Mkdir(sub, 0o700); err != nil {
			return fail(errorResult(err))
		}
		np := filepath.Join(sub, f.Filename)
		if err := os.WriteFile(np, f.Bytes, 0o600); err != nil {
			return fail(errorResult(err))
		}
		out.Paths[i] = np
		final += int64(len(f.Bytes))
		ch := attachChange{File: filepath.Base(p), SentAs: f.Filename, FromBytes: sizes[i], ToBytes: len(f.Bytes)}
		if w, h, ok := imageSize(items[i].data); ok {
			ch.FromSize = fmt.Sprintf("%dx%d", w, h)
		}
		ch.ToSize = fmt.Sprintf("%dx%d", f.Width, f.Height)
		out.Changes = append(out.Changes, ch)
	}
	if limit > 0 && final+attachOverhead > limit {
		return fail(errorf("the attachments total %d bytes after shrinking, over this server's %s upload limit; send fewer files per message", final, mib(limit)))
	}
	return out, nil
}

// imageSize reads an image's dimensions without decoding it.
func imageSize(b []byte) (int, int, bool) {
	f, err := media.Transform(b, "", media.FetchOptions{})
	if err != nil {
		return 0, 0, false
	}
	return f.Width, f.Height, true
}

func mib(n int64) string { return fmt.Sprintf("%d MiB", n>>20) }

// attachResult appends a note about changed attachments to a send result.
func attachResult(res *mcp.CallToolResult, p *attachPrepared) *mcp.CallToolResult {
	if res == nil || res.IsError || p == nil || len(p.Changes) == 0 {
		return res
	}
	var b strings.Builder
	b.WriteString("Attachments adjusted before upload (the files on disk are unchanged)")
	if p.Limit > 0 {
		fmt.Fprintf(&b, " to fit this server's %s upload limit", mib(p.Limit))
	}
	b.WriteString(":")
	for _, c := range p.Changes {
		fmt.Fprintf(&b, "\n- %s -> %s", c.File, c.SentAs)
		if c.FromSize != "" {
			fmt.Fprintf(&b, ", %s -> %s", c.FromSize, c.ToSize)
		}
		fmt.Fprintf(&b, ", %s -> %s", humanBytes(c.FromBytes), humanBytes(int64(c.ToBytes)))
	}
	res.Content = append(res.Content, mcp.NewTextContent(b.String()))
	return res
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// uploadLimits caches each guild's upload limit for a few minutes, since
// looking it up costs a guild fetch.
var uploadLimits sync.Map // guild ID -> uploadLimitEntry

type uploadLimitEntry struct {
	limit int64
	at    time.Time
}

const uploadLimitTTL = 10 * time.Minute

// attachUploadLimit returns guildID's upload limit from its boost level, or
// the 10 MiB base limit when the guild is unknown or cannot be read.
func attachUploadLimit(ctx context.Context, d deps.Deps, guildID string) int64 {
	if guildID == "" {
		return discord.BaseUploadLimit
	}
	if v, ok := uploadLimits.Load(guildID); ok {
		if e := v.(uploadLimitEntry); time.Since(e.at) < uploadLimitTTL {
			return e.limit
		}
	}
	g, err := d.Discord.GetGuildInfo(ctx, guildID)
	if err != nil {
		d.Log().Warn("upload limit: guild lookup failed; using the base limit", "guild_id", guildID, "error", err.Error())
		return discord.BaseUploadLimit
	}
	limit := discord.UploadLimit(g.BoostTier)
	uploadLimits.Store(guildID, uploadLimitEntry{limit: limit, at: time.Now()})
	return limit
}
