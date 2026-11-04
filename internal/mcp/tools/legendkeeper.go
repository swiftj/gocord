package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/lkbridge"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/media"
	"github.com/local/discord-mcp/internal/policy"
)

// LegendKeeper bridge tools (spec 05 §4).
const (
	lkToolMessage = "discord_media_to_legendkeeper"
	lkToolURL     = "fetch_url_to_legendkeeper"
	lkToolBatch   = "discord_batch_media_to_legendkeeper"
	lkBatchMax    = 25
)

// lkBatchGap spaces batch items to respect Discord's and LegendKeeper's rate limits.
var lkBatchGap = 250 * time.Millisecond

// RegisterLegendKeeperTools registers the Discord → LegendKeeper bridge tools.
// They always register; without LEGENDKEEPER_API_KEY they report "not configured".
func RegisterLegendKeeperTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool(lkToolMessage,
		"Copy a message attachment or author avatar (e.g. a Tupperbox character's art) into a LegendKeeper asset library, server-side. "+
			"Returns the asset URL for use in LegendKeeper image blocks; identical bytes already uploaded to the project are reused.",
		lkAnnotations(),
		append(append(mediaSourceParams(true), lkTargetParams()...), lkTransformParams()...)...,
	), lkSingleHandler(d, lkToolMessage))

	s.AddTool(blTool(lkToolURL,
		"Copy an image from an allowlisted URL into a LegendKeeper asset library, server-side. Returns the asset URL.",
		lkAnnotations(),
		append(append([]mcp.ToolOption{
			mcp.WithString("url", mcp.Required(), mcp.Description("Direct image URL on an allowlisted host.")),
		}, lkTargetParams()...), lkTransformParams()...)...,
	), lkSingleHandler(d, lkToolURL))

	s.AddTool(blTool(lkToolBatch,
		"Copy up to 25 Discord attachments/avatars into LegendKeeper in one call, sequentially. "+
			"Each item takes the discord_media_to_legendkeeper fields; lk_project and lk_folder_path here are per-item defaults.",
		lkAnnotations(),
		mcp.WithArray("items", mcp.Required(), mcp.MinItems(1), mcp.MaxItems(lkBatchMax),
			mcp.Items(map[string]any{"type": "object", "required": []string{"channel_id", "message_id"}}),
			mcp.Description("Items: {channel_id, message_id, attachment_index|attachment_id|avatar, name?, description?, lk_folder_id?, lk_folder_path?, max_dimension?, format?, quality?}.")),
		mcp.WithString("lk_project", mcp.Description("Default LegendKeeper project ID for items (else LEGENDKEEPER_PROJECT_ID).")),
		mcp.WithString("lk_folder_path", mcp.Description("Default folder path for items, e.g. \"Campaign/Characters\"; created if missing.")),
		mcp.WithBoolean("continue_on_error", mcp.DefaultBool(true), mcp.Description("Keep going after a failed item (default true).")),
	), lkBatchHandler(d))
}

func lkAnnotations() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true), // dedupe returns the existing asset
		mcp.WithOpenWorldHintAnnotation(true),
	}
}

func lkTargetParams() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithString("lk_project", mcp.Description("LegendKeeper project ID (default: LEGENDKEEPER_PROJECT_ID).")),
		mcp.WithString("lk_folder_id", mcp.Description("Asset folder ID to upload into (exclusive with lk_folder_path).")),
		mcp.WithString("lk_folder_path", mcp.Description("Asset folder path such as \"Campaign/Characters\"; missing folders are created.")),
		mcp.WithString("name", mcp.MaxLength(lkbridge.MaxNameLen), mcp.Description("Asset name (max 120 characters; default: the source filename stem).")),
		mcp.WithString("description", mcp.Description("Asset description.")),
	}
}

func lkTransformParams() []mcp.ToolOption {
	return []mcp.ToolOption{
		mcp.WithNumber("max_dimension", mcp.Min(0), mcp.Max(mediaMaxDimension), mcp.DefaultNumber(0),
			mcp.Description("Scale the longest edge down to this many pixels; 0 (default) keeps the original size.")),
		mcp.WithString("format", mcp.Enum(mediaFormatOriginal, "jpeg", "png", "webp"), mcp.DefaultString(mediaFormatOriginal),
			mcp.Description("Upload encoding; \"original\" (default) uploads the untouched bytes. \"webp\" uploads PNG.")),
		mcp.WithNumber("quality", mcp.Min(1), mcp.Max(100), mcp.DefaultNumber(media.DefaultQuality), mcp.Description("JPEG quality (1-100).")),
	}
}

// lkCheck reports a not-configured bridge or read-only mode.
func lkCheck(d deps.Deps, tool string) *mcp.CallToolResult {
	if d.LegendKeeper == nil {
		return errorf("%v", lkbridge.ErrNotConfigured)
	}
	if d.Media == nil {
		return errorf(mediaNotConfiguredTx)
	}
	if d.Guard.ReadOnly() {
		return errorResult(&policy.DeniedError{Tool: tool, Reason: "read-only mode blocks uploads to LegendKeeper"})
	}
	return nil
}

func lkSingleHandler(d deps.Deps, tool string) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res := lkCheck(d, tool); res != nil {
			return res, nil
		}
		out, res := lkTransfer(ctx, d, req, tool)
		if res != nil {
			return res, nil
		}
		return jsonResult(out), nil
	}
}

// lkTransfer fetches one source and uploads it.
func lkTransfer(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string) (*lkbridge.Result, *mcp.CallToolResult) {
	opts, res := mediaOptions(req, 0, mediaFormatOriginal)
	if res != nil {
		return nil, res
	}
	src, res := mediaSourceFrom(ctx, d, req, tool)
	if res != nil {
		return nil, res
	}
	if tool == lkToolMessage && src.URL != "" {
		return nil, errorf("url is not accepted here; use %s", lkToolURL)
	}
	f, err := d.Media.Fetch(ctx, src, opts)
	if err != nil {
		return nil, mediaErr(err)
	}
	out, err := d.LegendKeeper.Upload(ctx, lkbridge.Request{
		Project:     req.GetString("lk_project", ""),
		FolderID:    strings.TrimSpace(req.GetString("lk_folder_id", "")),
		FolderPath:  strings.TrimSpace(req.GetString("lk_folder_path", "")),
		Name:        req.GetString("name", ""),
		Description: req.GetString("description", ""),
		Media:       f,
	})
	if err != nil {
		return nil, errorf("%v", err)
	}
	return out, nil
}

type lkBatchItem struct {
	Index   int    `json:"index"`
	OK      bool   `json:"ok"`
	AssetID string `json:"asset_id,omitempty"`
	URL     string `json:"url,omitempty"`
	Dedup   bool   `json:"deduplicated,omitempty"`
	Error   string `json:"error,omitempty"`
}

type lkBatchResponse struct {
	Results   []lkBatchItem `json:"results"`
	Succeeded int           `json:"succeeded"`
	Failed    int           `json:"failed"`
}

func lkBatchHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if res := lkCheck(d, lkToolBatch); res != nil {
			return res, nil
		}
		var items []map[string]any
		if _, err := objectArg(req, "items", &items); err != nil {
			return errorf("%v", err), nil
		}
		if len(items) < 1 || len(items) > lkBatchMax {
			return errorf("items must contain 1-%d entries (got %d)", lkBatchMax, len(items)), nil
		}
		defProject := req.GetString("lk_project", "")
		defPath := req.GetString("lk_folder_path", "")
		keepGoing := req.GetBool("continue_on_error", true)

		resp := lkBatchResponse{Results: make([]lkBatchItem, 0, len(items))}
		for i, args := range items {
			if i > 0 {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(lkBatchGap):
				}
			}
			if _, ok := args["url"]; ok {
				return errorf("items[%d]: url is not accepted in a batch; use %s", i, lkToolURL), nil
			}
			if _, ok := args["lk_project"]; !ok && defProject != "" {
				args["lk_project"] = defProject
			}
			_, hasPath := args["lk_folder_path"]
			_, hasID := args["lk_folder_id"]
			if !hasPath && !hasID && defPath != "" {
				args["lk_folder_path"] = defPath
			}
			itemReq := mcp.CallToolRequest{}
			itemReq.Params.Name = lkToolMessage
			itemReq.Params.Arguments = args
			r := lkBatchItem{Index: i}
			out, res := lkTransfer(ctx, d, itemReq, lkToolBatch)
			if res != nil {
				r.Error = lkResultText(res)
				resp.Failed++
				resp.Results = append(resp.Results, r)
				if !keepGoing {
					break
				}
				continue
			}
			r.OK, r.AssetID, r.URL, r.Dedup = true, out.AssetID, out.URL, out.Deduplicated
			resp.Succeeded++
			resp.Results = append(resp.Results, r)
		}
		return jsonResult(resp), nil
	}
}

// lkResultText extracts the message from an error tool result.
func lkResultText(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			return tc.Text
		}
	}
	return fmt.Sprintf("%v", res.Content)
}
