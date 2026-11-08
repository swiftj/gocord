package tools

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/mcp/resources"
	"github.com/local/discord-mcp/internal/policy"
)

// RegisterMessageTools registers send/read/edit/delete/bulk-delete message tools.
func RegisterMessageTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool("discord_send",
		"Send text, an embed, and/or up to 10 local files to a channel or thread as the bot. To post as a persona, use discord_persona_send. Content must be at most 2000 characters.",
		blMutating(false, false),
		append([]mcp.ToolOption{
			mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
			mcp.WithString("content", mcp.MaxLength(blMaxContent), mcp.Description("Message text (may be omitted when embed or a file is given).")),
			mcp.WithObject("embed", mcp.Description("Embed: {title, description, url, color, fields:[{name,value,inline}], footer, image_url}.")),
			mcp.WithString("reply_to_message_id", mcp.Description("Message Snowflake to reply to.")),
		}, attachParams()...)...,
	), msgSendHandler(d))

	s.AddTool(blTool("discord_read_messages",
		"Read recent messages (newest first) with mentions resolved. Use at most one of before/after/around (Snowflake or ISO-8601).",
		blReadOnly(),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithInteger("limit", mcp.Min(1), mcp.Max(100), mcp.DefaultNumber(50), mcp.Description("Messages to return (1-100).")),
		mcp.WithString("before", mcp.Description("Only messages before this Snowflake or timestamp.")),
		mcp.WithString("after", mcp.Description("Only messages after this Snowflake or timestamp.")),
		mcp.WithString("around", mcp.Description("Messages around this Snowflake or timestamp.")),
	), msgReadHandler(d))

	s.AddTool(blTool("discord_edit_message",
		"Edit the content of a message the bot authored. Content must be at most 2000 characters.",
		blMutating(false, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Message Snowflake.")),
		mcp.WithString("content", mcp.Required(), mcp.MaxLength(blMaxContent), mcp.Description("New content.")),
	), msgEditHandler(d))

	s.AddTool(blTool("discord_delete_message",
		"Delete a single message. Supports dry_run.",
		blMutating(true, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Message Snowflake.")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		blDryRunParam(),
	), msgDeleteHandler(d))

	s.AddTool(blTool("discord_bulk_delete_messages",
		"Bulk delete 2-100 messages in one channel. Messages must be less than 14 days old.",
		blMutating(true, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithArray("message_ids", mcp.Required(), mcp.MinItems(policy.BulkDeleteMin), mcp.MaxItems(policy.BulkDeleteMax),
			mcp.WithStringItems(), mcp.Description("Message Snowflakes (2-100).")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		blDryRunParam(),
	), msgBulkDeleteHandler(d))
}

func msgSendHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_send"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content := req.GetString("content", "")
		if res := blCheckContent(content); res != nil {
			return res, nil
		}
		var embed dto.EmbedInput
		hasEmbed, err := objectArg(req, "embed", &embed)
		if err != nil {
			return errorf("%v", err), nil
		}
		files, res := attachFiles(d, req)
		if res != nil {
			return res, nil
		}
		if strings.TrimSpace(content) == "" && !hasEmbed && len(files) == 0 {
			return errorf("content is required (or pass embed, file_path or file_paths)"), nil
		}
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opWrite, false)
		if res != nil {
			return res, nil
		}
		sr := dto.SendMessageReq{ChannelID: ch.ID, Content: content, FilePaths: files,
			ReplyToMessageID: strings.TrimSpace(req.GetString("reply_to_message_id", ""))}
		if hasEmbed {
			sr.Embed = &embed
		}
		prep, res := attachPrepare(ctx, d, req, files, ch.GuildID)
		if res != nil {
			return res, nil
		}
		defer prep.Close()
		sr.FilePaths = prep.Paths
		msg, err := d.Discord.SendMessage(ctx, sr)
		if err != nil {
			return errorResult(err), nil
		}
		return attachResult(jsonResult(msg), prep), nil
	}
}

func msgReadHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_read_messages"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		rr := dto.ReadMessagesReq{Limit: clamp(req.GetInt("limit", 50), 1, 100)}
		set := 0
		for _, c := range []struct {
			key string
			dst *string
		}{{"before", &rr.Before}, {"after", &rr.After}, {"around", &rr.Around}} {
			v, err := cursorArg(req, c.key)
			if err != nil {
				return errorf("%v", err), nil
			}
			if v != "" {
				*c.dst = v
				set++
			}
		}
		if set > 1 {
			return errorf("pass at most one of before, after, around"), nil
		}
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opRead, false)
		if res != nil {
			return res, nil
		}
		rr.ChannelID = ch.ID
		msgs, err := d.Discord.ReadMessages(ctx, rr)
		if err != nil {
			return errorResult(err), nil
		}
		parent := ""
		if ch.Type == dto.ChannelThread {
			parent = ch.ParentID
		}
		if d.Store != nil && len(msgs) > 0 {
			if err := d.Store.UpsertMessages(ctx, ch.GuildID, parent, msgs); err != nil {
				d.Log().Warn("read_messages: index upsert failed", "channel_id", ch.ID, "error", err)
			}
		}
		mr := resources.NewMentionResolver(d.Discord, ch.GuildID)
		for i := range msgs {
			msgs[i].Content = mr.Resolve(ctx, msgs[i].Content)
		}
		next := ""
		if len(msgs) == rr.Limit && rr.Around == "" {
			ids := make([]string, len(msgs))
			for i, m := range msgs {
				ids[i] = m.ID
			}
			slices.SortFunc(ids, msgCmpSnowflake)
			if rr.After != "" {
				next = ids[len(ids)-1] // continue forward with after=
			} else {
				next = ids[0] // continue backward with before=
			}
		}
		return listResult(msgs, next), nil
	}
}

func msgCmpSnowflake(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

func msgEditHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_edit_message"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		msgID, res := blRequired(req, "message_id")
		if res != nil {
			return res, nil
		}
		content, res := blRequired(req, "content")
		if res != nil {
			return res, nil
		}
		if res := blCheckContent(content); res != nil {
			return res, nil
		}
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opWrite, false)
		if res != nil {
			return res, nil
		}
		msg, err := d.Discord.EditMessage(ctx, ch.ID, msgID, content)
		if err != nil {
			if errors.Is(err, discord.ErrForbidden) {
				return errorf("cannot edit message %s: only messages authored by the bot can be edited", msgID), nil
			}
			return errorResult(err), nil
		}
		return jsonResult(msg), nil
	}
}

func msgDeleteHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_delete_message"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dry := dryRun(req)
		msgID, res := blRequired(req, "message_id")
		if res != nil {
			return res, nil
		}
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opDestructive, dry)
		if res != nil {
			return res, nil
		}
		reason := req.GetString("reason", "")
		if dry {
			change := fmt.Sprintf("delete message %s in %s", msgID, blLabel(ch))
			if reason != "" {
				change += " (reason: " + reason + ")"
			}
			return dryRunResult(d, channelOp(tool, ch, opDestructive, true), blLabel(ch), []string{change}), nil
		}
		if err := d.Discord.DeleteMessage(ctx, ch.ID, msgID, reason); err != nil {
			return errorResult(err), nil
		}
		if d.Store != nil {
			if err := d.Store.MarkDeleted(ctx, ch.ID, msgID); err != nil {
				d.Log().Debug("delete_message: index mark deleted failed", "error", err)
			}
		}
		return jsonResult(map[string]any{"ok": true, "channel_id": ch.ID, "deleted": msgID}), nil
	}
}

func msgBulkDeleteHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_bulk_delete_messages"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dry := dryRun(req)
		var ids []string
		for _, id := range req.GetStringSlice("message_ids", nil) {
			id = strings.TrimSpace(id)
			if id != "" && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		for _, id := range ids {
			if !dto.IsSnowflake(id) {
				return errorf("message_ids: %q is not a message Snowflake", id), nil
			}
		}
		ref, res := blRequired(req, "channel_id")
		if res != nil {
			return res, nil
		}
		ch, err := resolveChannel(ctx, d, "", ref)
		if err != nil {
			return errorResult(err), nil
		}
		op := channelOp(tool, ch, opDestructive, dry)
		op.MessageIDs = ids
		if err := d.Guard.Check(op); err != nil {
			return errorResult(err), nil
		}
		reason := req.GetString("reason", "")
		if dry {
			change := fmt.Sprintf("bulk delete %d messages in %s: %s", len(ids), blLabel(ch), strings.Join(ids, ", "))
			if reason != "" {
				change += " (reason: " + reason + ")"
			}
			op.DryRun = true
			return dryRunResult(d, op, blLabel(ch), []string{change}), nil
		}
		if err := d.Discord.BulkDeleteMessages(ctx, ch.ID, ids, reason); err != nil {
			return errorResult(err), nil
		}
		if d.Store != nil {
			for _, id := range ids {
				if err := d.Store.MarkDeleted(ctx, ch.ID, id); err != nil {
					d.Log().Debug("bulk_delete: index mark deleted failed", "error", err)
				}
			}
		}
		return jsonResult(map[string]any{"ok": true, "channel_id": ch.ID, "deleted_count": len(ids), "deleted": ids}), nil
	}
}
