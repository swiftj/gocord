package tools

import (
	"context"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/media"
)

// Local-file media tools (spec 05 §3).
const (
	mediaToolURLFile = "fetch_url_to_file"
	mediaToolBatch   = "discord_batch_fetch_media"
	mediaToolList    = "media_list_files"
	mediaBatchMax    = 25
	mediaNoStoreTx   = "saving media is not configured on this server"
)

// mediaBatchGap spaces batch items to respect Discord's rate limits.
var mediaBatchGap = 250 * time.Millisecond

func registerMediaFileTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool(mediaToolURLFile,
		"Download an image from an allowlisted URL (Magnific, Freepik, Discord CDN, LegendKeeper assets, GOCORD_MEDIA_ALLOWED_HOSTS) and save it under the local media root.",
		mediaSaveAnnotations(),
		append([]mcp.ToolOption{
			mcp.WithString("url", mcp.Required(), mcp.Description("Direct image URL on an allowlisted host.")),
			mediaSaveToParam(true), mediaOverwriteParam(),
		}, mediaTransformParams()...)...,
	), mediaURLFileHandler(d))

	s.AddTool(blTool(mediaToolBatch,
		"Save up to 25 Discord attachments or author avatars under the local media root in one call, sequentially.",
		mediaSaveAnnotations(),
		mcp.WithArray("items", mcp.Required(), mcp.MinItems(1), mcp.MaxItems(mediaBatchMax),
			mcp.Items(map[string]any{"type": "object", "required": []string{"channel_id", "message_id", "save_to"}}),
			mcp.Description("Items: {channel_id, message_id, attachment_index|attachment_id|avatar, save_to, overwrite?, max_dimension?, format?, quality?}.")),
		mcp.WithBoolean("continue_on_error", mcp.DefaultBool(true), mcp.Description("Keep going after a failed item (default true).")),
	), mediaBatchHandler(d))

	s.AddTool(blTool(mediaToolList,
		"List the files under the local media root (path, size, type, pixel size, modified time), to confirm names before uploading them elsewhere.",
		[]mcp.ToolOption{
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
			mcp.WithIdempotentHintAnnotation(true),
			mcp.WithOpenWorldHintAnnotation(false),
		},
		mcp.WithString("subdir", mcp.Description("Directory relative to the media root (default: the root).")),
		mcp.WithString("glob", mcp.Description("Pattern matched against the file name or the root-relative path, e.g. \"*.png\" or \"Characters/*\".")),
	), mediaListHandler(d))
}

// mediaSaveAnnotations: these tools may write local files.
func mediaSaveAnnotations() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false), // replacing a file needs overwrite:true
		mcp.WithIdempotentHintAnnotation(true),   // identical bytes are deduplicated
		mcp.WithOpenWorldHintAnnotation(true),
	}
}

func mediaSaveToParam(required bool) mcp.ToolOption {
	desc := "Write the image to this path instead of returning base64: relative to the media root (GOCORD_MEDIA_DIR, default ~/Downloads/gocord), " +
		"or absolute when inside it (or anywhere with GOCORD_MEDIA_ALLOW_ABSOLUTE=true). A missing extension is added from the output format."
	if required {
		return mcp.WithString("save_to", mcp.Required(), mcp.Description(desc))
	}
	return mcp.WithString("save_to", mcp.Description(desc))
}

func mediaOverwriteParam() mcp.ToolOption {
	return mcp.WithBoolean("overwrite", mcp.DefaultBool(false),
		mcp.Description("Replace an existing file with different content (identical content is never rewritten)."))
}

func mediaTransformParams() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("max_dimension", mcp.Min(0), mcp.Max(mediaMaxDimension), mcp.DefaultNumber(mediaDefaultMaxDim),
			mcp.Description("Scale the longest edge down to this many pixels (never up); 0 keeps the original size.")),
		mcp.WithString("format", mcp.Enum("jpeg", "png", "webp", mediaFormatOriginal),
			mcp.Description("Output encoding (default: from the save_to extension, else jpeg). \"original\" writes the untouched bytes; \"webp\" is written as PNG.")),
		mcp.WithNumber("quality", mcp.Min(1), mcp.Max(100), mcp.DefaultNumber(media.DefaultQuality), mcp.Description("JPEG quality (1-100).")),
	}
}

// mediaSavedResult is a saved file plus, for URL sources, where it came from.
type mediaSavedResult struct {
	*media.Saved
	SourceURL string `json:"source_url,omitempty"`
}

// mediaFetchAndSave downloads src with opts and writes it to saveTo.
func mediaFetchAndSave(ctx context.Context, d deps.Deps, src media.Source, opts media.FetchOptions, saveTo string, overwrite bool) (*mediaSavedResult, *mcp.CallToolResult) {
	f, err := d.Media.Fetch(ctx, src, opts)
	if err != nil {
		return nil, mediaErr(err)
	}
	saved, err := d.MediaStore.Save(f, saveTo, overwrite)
	if err != nil {
		return nil, mediaErr(err)
	}
	d.Log().Info("media saved", "path", saved.Path, "bytes", saved.Bytes, "deduplicated", saved.Deduplicated, "overwritten", saved.Overwritten)
	return &mediaSavedResult{Saved: saved, SourceURL: f.SourceURL}, nil
}

func mediaFilesReady(d deps.Deps) *mcp.CallToolResult {
	if d.Media == nil {
		return errorf(mediaNotConfiguredTx)
	}
	if d.MediaStore == nil {
		return errorf(mediaNoStoreTx)
	}
	return nil
}

func mediaURLFileHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res := mediaFilesReady(d); res != nil {
			return res, nil
		}
		url, res := blRequired(req, "url")
		if res != nil {
			return res, nil
		}
		saveTo, res := blRequired(req, "save_to")
		if res != nil {
			return res, nil
		}
		opts, res := mediaOptions(req, mediaDefaultMaxDim, "jpeg")
		if res != nil {
			return res, nil
		}
		src := media.Source{URL: url}
		if err := src.Validate(); err != nil {
			return errorf("%v", err), nil
		}
		out, res := mediaFetchAndSave(ctx, d, src, opts, saveTo, req.GetBool("overwrite", false))
		if res != nil {
			return res, nil
		}
		return jsonResult(out), nil
	}
}

type mediaBatchItem struct {
	Index  int    `json:"index"`
	OK     bool   `json:"ok"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
	Error  string `json:"error,omitempty"`
}

type mediaBatchResponse struct {
	Results   []mediaBatchItem `json:"results"`
	Succeeded int              `json:"succeeded"`
	Failed    int              `json:"failed"`
}

func mediaBatchHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res := mediaFilesReady(d); res != nil {
			return res, nil
		}
		var items []map[string]any
		if _, err := objectArg(req, "items", &items); err != nil {
			return errorf("%v", err), nil
		}
		if len(items) < 1 || len(items) > mediaBatchMax {
			return errorf("items must contain 1-%d entries (got %d)", mediaBatchMax, len(items)), nil
		}
		keepGoing := req.GetBool("continue_on_error", true)
		resp := mediaBatchResponse{Results: make([]mediaBatchItem, 0, len(items))}
		for i, args := range items {
			if i > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(mediaBatchGap):
				}
			}
			r := mediaBatchItem{Index: i}
			saved, res := mediaBatchOne(ctx, d, args)
			if res != nil {
				r.Error = mediaResultText(res)
				resp.Failed++
				resp.Results = append(resp.Results, r)
				if !keepGoing {
					break
				}
				continue
			}
			r.OK, r.Path, r.SHA256, r.Width, r.Height = true, saved.Path, saved.SHA256, saved.Width, saved.Height
			resp.Succeeded++
			resp.Results = append(resp.Results, r)
		}
		return jsonResult(resp), nil
	}
}

// mediaBatchOne validates and saves one batch item.
func mediaBatchOne(ctx context.Context, d deps.Deps, args map[string]any) (*mediaSavedResult, *mcp.CallToolResult) {
	if _, ok := args["url"]; ok {
		return nil, errorf("url is not accepted in a batch; use %s", mediaToolURLFile)
	}
	req := mcp.CallToolRequest{}
	req.Params.Name = mediaToolBatch
	req.Params.Arguments = args
	for _, k := range []string{"channel_id", "message_id", "save_to"} {
		if strings.TrimSpace(req.GetString(k, "")) == "" {
			return nil, errorf("%s is required", k)
		}
	}
	opts, res := mediaOptions(req, mediaDefaultMaxDim, "jpeg")
	if res != nil {
		return nil, res
	}
	src, res := mediaSourceFrom(ctx, d, req, mediaToolBatch)
	if res != nil {
		return nil, res
	}
	return mediaFetchAndSave(ctx, d, src, opts, req.GetString("save_to", ""), req.GetBool("overwrite", false))
}

// mediaResultText extracts the message from an error tool result.
func mediaResultText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			return tc.Text
		}
	}
	return "unknown error"
}

type mediaListResponse struct {
	Root      string            `json:"root"`
	Files     []media.FileEntry `json:"files"`
	Truncated bool              `json:"truncated,omitempty"`
}

func mediaListHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if d.MediaStore == nil {
			return errorf(mediaNoStoreTx), nil
		}
		files, truncated, err := d.MediaStore.List(req.GetString("subdir", ""), strings.TrimSpace(req.GetString("glob", "")))
		if err != nil {
			return mediaErr(err), nil
		}
		return jsonResult(mediaListResponse{Root: d.MediaStore.Root(), Files: files, Truncated: truncated}), nil
	}
}
