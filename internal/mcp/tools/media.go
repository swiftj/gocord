package tools

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/media"
)

// Media tool limits (spec 05 §3).
const (
	mediaMaxChunk        = 1_000_000
	mediaDefaultMaxDim   = 1600
	mediaMaxDimension    = 8192
	mediaToolFetch       = "discord_fetch_media"
	mediaToolAvatar      = "discord_get_member_avatar"
	mediaFormatOriginal  = "original"
	mediaNotConfiguredTx = "media fetching is not configured on this server"
)

// RegisterMediaTools registers discord_get_member_avatar, discord_fetch_media,
// and the local-file tools (fetch_url_to_file, discord_batch_fetch_media, media_list_files).
func RegisterMediaTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool(mediaToolAvatar,
		"Get a member's avatar URLs and display name. avatar_url is the account avatar; guild_avatar_url is the server-specific one when set.",
		blReadOnly(),
		mcp.WithString("guild_id", mcp.Description("Guild Snowflake or name. Optional when a default guild is configured or the bot is in one guild.")),
		mcp.WithString("user_id", mcp.Required(), mcp.Description("User Snowflake, @name, or mention.")),
		mcp.WithNumber("size", mcp.DefaultNumber(dto.DefaultAvatarSize),
			mcp.Description("Requested pixel size: 128, 256, 512, 1024, 2048, or 4096.")),
	), mediaAvatarHandler(d))

	s.AddTool(blTool(mediaToolFetch,
		"Download an image (a message attachment, a message author's avatar, or an allowlisted URL), resized and re-encoded. "+
			"Returns it inline as base64 (chunked via offset/length over 1,000,000 bytes), or with save_to writes it under the local media root and returns the path.",
		mediaSaveAnnotations(),
		append(mediaSourceParams(false),
			mcp.WithNumber("max_dimension", mcp.Min(0), mcp.Max(mediaMaxDimension), mcp.DefaultNumber(mediaDefaultMaxDim),
				mcp.Description("Scale the longest edge down to this many pixels (never up); 0 keeps the original size.")),
			mcp.WithString("format", mcp.Enum("jpeg", "png", "webp", mediaFormatOriginal), mcp.DefaultString("jpeg"),
				mcp.Description("Output encoding. \"original\" returns the untouched bytes and ignores max_dimension; \"webp\" is returned as PNG (no pure-Go WebP encoder).")),
			mcp.WithNumber("quality", mcp.Min(1), mcp.Max(100), mcp.DefaultNumber(media.DefaultQuality), mcp.Description("JPEG quality (1-100).")),
			mcp.WithNumber("offset", mcp.Min(0), mcp.Description("Byte offset into the encoded payload, for chunked retrieval.")),
			mcp.WithNumber("length", mcp.Min(1), mcp.Max(mediaMaxChunk), mcp.Description("Bytes to return from offset (max 1,000,000).")),
			mediaSaveToParam(false), mediaOverwriteParam(),
		)...,
	), mediaFetchHandler(d))

	registerMediaFileTools(s, d)
}

// mediaSourceParams are the image-source parameters shared by the fetch and
// batch tools. messageRequired makes channel_id and message_id required.
func mediaSourceParams(messageRequired bool) []mcp.ToolOption {
	msgOpt := func(desc string) []mcp.PropertyOption {
		if messageRequired {
			return []mcp.PropertyOption{mcp.Required(), mcp.Description(desc)}
		}
		return []mcp.PropertyOption{mcp.Description(desc)}
	}
	params := []mcp.ToolOption{
		mcp.WithString("channel_id", msgOpt("Channel or thread holding the message (Snowflake, #name, or mention).")...),
		mcp.WithString("message_id", msgOpt("Message Snowflake.")...),
		mcp.WithNumber("attachment_index", mcp.Min(0), mcp.Description("0-based index of the message attachment.")),
		mcp.WithString("attachment_id", mcp.Description("Attachment Snowflake (alternative to attachment_index).")),
		mcp.WithBoolean("avatar", mcp.Description("Use the message author's avatar; for webhook messages this is the per-message art (e.g. a Tupperbox character).")),
	}
	if !messageRequired {
		params = append(params, mcp.WithString("url",
			mcp.Description("Direct image URL on an allowlisted host, instead of a message source.")))
	}
	return params
}

// mediaSourceFrom builds a media.Source from the request, resolving the
// channel and checking read policy for message sources.
func mediaSourceFrom(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string) (media.Source, *mcp.CallToolResult) {
	src := media.Source{
		MessageID:    strings.TrimSpace(req.GetString("message_id", "")),
		AttachmentID: strings.TrimSpace(req.GetString("attachment_id", "")),
		Avatar:       req.GetBool("avatar", false),
		URL:          strings.TrimSpace(req.GetString("url", "")),
	}
	if hasArg(req, "attachment_index") {
		n := req.GetInt("attachment_index", -1)
		src.AttachmentIndex = &n
	}
	chRef := strings.TrimSpace(req.GetString("channel_id", ""))
	if chRef != "" {
		ch, err := resolveChannel(ctx, d, "", chRef)
		if err != nil {
			return src, errorResult(err)
		}
		if err := d.Guard.Check(channelOp(tool, ch, opRead, false)); err != nil {
			return src, errorResult(err)
		}
		src.ChannelID = ch.ID
	}
	if err := src.Validate(); err != nil {
		return src, errorf("%v", err)
	}
	return src, nil
}

// mediaOptions reads max_dimension/format/quality. defDim and defFormat are
// the tool's defaults. Without an explicit format, an image extension on
// save_to picks it (.png → png, .jpg → jpeg, .gif/.webp → original).
func mediaOptions(req mcp.CallToolRequest, defDim int, defFormat string) (media.FetchOptions, *mcp.CallToolResult) {
	format := strings.ToLower(strings.TrimSpace(req.GetString("format", "")))
	if format == "" {
		format = defFormat
		switch strings.ToLower(filepath.Ext(req.GetString("save_to", ""))) {
		case ".png":
			format = "png"
		case ".jpg", ".jpeg":
			format = "jpeg"
		case ".gif", ".webp":
			format = mediaFormatOriginal
		}
	}
	if !slices.Contains([]string{"jpeg", "png", "webp", mediaFormatOriginal}, format) {
		return media.FetchOptions{}, errorf("format must be jpeg, png, webp, or original (got %q)", format)
	}
	dim := req.GetInt("max_dimension", defDim)
	if dim < 0 || dim > mediaMaxDimension {
		return media.FetchOptions{}, errorf("max_dimension must be 0-%d (got %d)", mediaMaxDimension, dim)
	}
	q := req.GetInt("quality", media.DefaultQuality)
	if q < 1 || q > 100 {
		return media.FetchOptions{}, errorf("quality must be 1-100 (got %d)", q)
	}
	if format == mediaFormatOriginal {
		return media.FetchOptions{}, nil
	}
	return media.FetchOptions{MaxDimension: dim, Format: format, Quality: q, StripGIFToFirstFrame: true}, nil
}

// mediaErr maps media errors to tool results.
func mediaErr(err error) *mcp.CallToolResult {
	if errors.Is(err, media.ErrInvalidSource) || errors.Is(err, media.ErrUnsupportedType) || errors.Is(err, media.ErrTooLarge) ||
		errors.Is(err, media.ErrPath) || errors.Is(err, media.ErrExists) {
		return errorf("%v", err)
	}
	return errorResult(err)
}

type mediaFetchResponse struct {
	Mime       string `json:"mime"`
	Filename   string `json:"filename"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
	TotalBytes int    `json:"total_bytes"`
	Offset     int    `json:"offset"`
	Length     int    `json:"length"`
	EOF        bool   `json:"eof"`
	SHA256     string `json:"sha256"`
	Base64     string `json:"base64"`
	Hint       string `json:"hint,omitempty"`
}

func mediaFetchHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if d.Media == nil {
			return errorf(mediaNotConfiguredTx), nil
		}
		opts, res := mediaOptions(req, mediaDefaultMaxDim, "jpeg")
		if res != nil {
			return res, nil
		}
		offset := req.GetInt("offset", 0)
		length := req.GetInt("length", 0)
		if offset < 0 {
			return errorf("offset must be >= 0"), nil
		}
		if hasArg(req, "length") && (length < 1 || length > mediaMaxChunk) {
			return errorf("length must be 1-%d (got %d)", mediaMaxChunk, length), nil
		}
		saveTo := strings.TrimSpace(req.GetString("save_to", ""))
		if saveTo != "" && (hasArg(req, "offset") || hasArg(req, "length")) {
			return errorf("offset/length apply to inline base64; omit them with save_to"), nil
		}
		if saveTo != "" && d.MediaStore == nil {
			return errorf(mediaNoStoreTx), nil
		}
		src, res := mediaSourceFrom(ctx, d, req, mediaToolFetch)
		if res != nil {
			return res, nil
		}
		if saveTo != "" {
			out, res := mediaFetchAndSave(ctx, d, src, opts, saveTo, req.GetBool("overwrite", false))
			if res != nil {
				return res, nil
			}
			return jsonResult(out.Saved), nil
		}
		f, err := d.Media.FetchCached(ctx, src, opts)
		if err != nil {
			return mediaErr(err), nil
		}
		resp, res := mediaSlice(f, offset, length, hasArg(req, "offset") || hasArg(req, "length"))
		if res != nil {
			return res, nil
		}
		return jsonResult(resp), nil
	}
}

// mediaSlice cuts [offset, offset+length) out of the encoded payload. With no
// explicit paging a payload over mediaMaxChunk returns its first chunk and a hint.
func mediaSlice(f *media.Fetched, offset, length int, paged bool) (*mediaFetchResponse, *mcp.CallToolResult) {
	total := len(f.Bytes)
	if offset > total {
		return nil, errorf("offset %d is past the end of the %d-byte payload", offset, total)
	}
	if length <= 0 {
		length = mediaMaxChunk
	}
	end := min(offset+length, total)
	sum := sha256.Sum256(f.Bytes)
	resp := &mediaFetchResponse{
		Mime: f.ContentType, Filename: f.Filename, Width: f.Width, Height: f.Height,
		TotalBytes: total, Offset: offset, Length: end - offset, EOF: end == total,
		SHA256: hex.EncodeToString(sum[:]), Base64: base64.StdEncoding.EncodeToString(f.Bytes[offset:end]),
	}
	if !resp.EOF && !paged {
		resp.Hint = fmt.Sprintf("payload is %d bytes; this is the first %d. Call again with the same arguments plus offset=%d (and length up to %d) "+
			"until eof is true, then concatenate the decoded chunks and check sha256. Lower max_dimension or use format jpeg for a smaller payload.",
			total, resp.Length, end, mediaMaxChunk)
	}
	return resp, nil
}

func mediaAvatarHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		size := req.GetInt("size", dto.DefaultAvatarSize)
		if !slices.Contains(dto.AvatarSizes, size) {
			return errorf("size must be one of 128, 256, 512, 1024, 2048, 4096 (got %d)", size), nil
		}
		ref, res := blRequired(req, "user_id")
		if res != nil {
			return res, nil
		}
		guildID, err := resolveGuild(ctx, d, req)
		if err != nil {
			return errorResult(err), nil
		}
		if err := d.Guard.Check(guildOp(mediaToolAvatar, guildID, opRead, false)); err != nil {
			return errorResult(err), nil
		}
		userID, err := resolveUser(ctx, d, guildID, ref)
		if err != nil {
			return errorResult(err), nil
		}
		p, err := d.Discord.GetMember(ctx, guildID, userID)
		if err != nil {
			return errorResult(err), nil
		}
		out := dto.MemberAvatar{
			UserID:         p.UserID,
			DisplayName:    dto.DisplayName(p.Nick, p.GlobalName, p.Username),
			AvatarURL:      dto.UserAvatarURL(p.UserID, p.Avatar, p.Discriminator, size),
			GuildAvatarURL: dto.GuildAvatarURL(guildID, p.UserID, p.GuildAvatar, size),
		}
		effective := p.Avatar
		if p.GuildAvatar != "" {
			effective = p.GuildAvatar
		}
		out.Animated = dto.IsAnimatedHash(effective)
		return jsonResult(out), nil
	}
}
