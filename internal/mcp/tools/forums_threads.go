package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// ftAutoArchive lists the auto-archive durations Discord accepts (minutes).
var ftAutoArchive = []int{60, 1440, 4320, 10080}

// RegisterForumThreadTools registers forum channel/post tools and discord_manage_threads.
func RegisterForumThreadTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool("discord_create_forum_channel",
		"Create a forum channel with optional available tags (max 20) and a default reaction.",
		blMutating(false, false),
		blGuildParam(),
		mcp.WithString("name", mcp.Required(), mcp.MaxLength(100), mcp.Description("Forum name.")),
		mcp.WithString("topic", mcp.MaxLength(4096), mcp.Description("Forum guidelines shown to posters.")),
		mcp.WithArray("tags", mcp.MaxItems(20), mcp.WithStringItems(mcp.MaxLength(20)), mcp.Description("Available tag names (max 20).")),
		mcp.WithString("category_id", mcp.Description("Parent category (Snowflake or name).")),
		mcp.WithString("default_reaction", mcp.Description("Default reaction emoji for posts.")),
	), ftCreateForumHandler(d))

	s.AddTool(blTool("discord_create_forum_post",
		"Start a forum post (thread) with an initial message and optional tags. Unknown tag names return the available tags.",
		blMutating(false, false),
		mcp.WithString("forum_channel_id", mcp.Required(), mcp.Description("Forum channel Snowflake, #name, or mention.")),
		mcp.WithString("title", mcp.Required(), mcp.MaxLength(100), mcp.Description("Post title.")),
		mcp.WithString("content", mcp.Required(), mcp.MaxLength(blMaxContent), mcp.Description("Starter message (max 2000 characters).")),
		mcp.WithArray("tag_names", mcp.MaxItems(5), mcp.WithStringItems(), mcp.Description("Tags to apply (max 5).")),
	), ftCreatePostHandler(d))

	s.AddTool(blTool("discord_get_forum_post",
		"Get a forum post's starter message and replies.",
		blReadOnly(),
		mcp.WithString("thread_id", mcp.Required(), mcp.Description("Forum thread Snowflake or name.")),
		mcp.WithInteger("limit", mcp.Min(1), mcp.Max(100), mcp.DefaultNumber(50), mcp.Description("Max replies (1-100).")),
	), ftGetPostHandler(d))

	s.AddTool(blTool("discord_reply_to_forum",
		"Post a reply inside a forum thread. Content must be at most 2000 characters.",
		blMutating(false, false),
		mcp.WithString("thread_id", mcp.Required(), mcp.Description("Forum thread Snowflake or name.")),
		mcp.WithString("content", mcp.Required(), mcp.MaxLength(blMaxContent), mcp.Description("Reply text.")),
	), ftReplyHandler(d))

	s.AddTool(blTool("discord_list_forum_threads",
		"List threads in a forum channel; archived threads are included only when include_archived is true.",
		blReadOnly(),
		mcp.WithString("forum_channel_id", mcp.Required(), mcp.Description("Forum channel Snowflake, #name, or mention.")),
		mcp.WithBoolean("include_archived", mcp.DefaultBool(false), mcp.Description("Include archived threads.")),
	), ftListThreadsHandler(d))

	s.AddTool(blTool("discord_manage_threads",
		"Create a public/private thread (standalone or from a message), or archive/unarchive/lock/unlock one. dry_run previews archive/lock.",
		blMutating(true, false),
		mcp.WithString("action", mcp.Required(), mcp.Enum("create", "archive", "unarchive", "lock", "unlock"), mcp.Description("Operation to perform.")),
		mcp.WithString("channel_id", mcp.Description("Parent channel for create (Snowflake, #name, or mention).")),
		mcp.WithString("thread_id", mcp.Description("Thread to modify (archive/unarchive/lock/unlock).")),
		mcp.WithString("message_id", mcp.Description("Start the thread from this message (create).")),
		mcp.WithString("name", mcp.MaxLength(100), mcp.Description("Thread name (create).")),
		mcp.WithBoolean("private", mcp.DefaultBool(false), mcp.Description("Create a private thread.")),
		mcp.WithInteger("auto_archive_minutes", func(m map[string]any) { m["enum"] = ftAutoArchive },
			mcp.Description("Auto-archive after inactivity: 60, 1440, 4320, or 10080 minutes.")),
		blDryRunParam(),
	), ftManageThreadsHandler(d))
}

func ftCreateForumHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_create_forum_channel"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		name, res := blRequired(req, "name")
		if res != nil {
			return res, nil
		}
		var tags []string
		for _, t := range req.GetStringSlice("tags", nil) {
			if t = strings.TrimSpace(t); t != "" && !containsFold(tags, t) {
				tags = append(tags, t)
			}
		}
		if len(tags) > 20 {
			return errorf("a forum can have at most 20 tags, got %d", len(tags)), nil
		}
		gid, res := blGuild(ctx, d, req, tool, opWrite, false)
		if res != nil {
			return res, nil
		}
		parent, res := blCategory(ctx, d, req, gid)
		if res != nil {
			return res, nil
		}
		ch, err := d.Discord.CreateForumChannel(ctx, dto.CreateForumReq{GuildID: gid, Name: name,
			Topic: req.GetString("topic", ""), Tags: tags, ParentID: parent,
			DefaultReaction: strings.TrimSpace(req.GetString("default_reaction", ""))})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(ch), nil
	}
}

// ftForum resolves a forum channel argument and checks policy.
func ftForum(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string, kind int) (*dto.ChannelInfo, *mcp.CallToolResult) {
	ch, res := blChannel(ctx, d, req, "forum_channel_id", tool, kind, false)
	if res != nil {
		return nil, res
	}
	if ch.Type != dto.ChannelForum {
		return nil, errorf("forum_channel_id %s is a %s channel, not a forum", blLabel(ch), ch.Type)
	}
	return ch, nil
}

// ftThread resolves a thread argument and checks policy.
func ftThread(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string, kind int, dry bool) (*dto.ChannelInfo, *mcp.CallToolResult) {
	ch, res := blChannel(ctx, d, req, "thread_id", tool, kind, dry)
	if res != nil {
		return nil, res
	}
	if ch.Type != dto.ChannelThread {
		return nil, errorf("thread_id %s is a %s channel, not a thread", blLabel(ch), ch.Type)
	}
	return ch, nil
}

func ftCreatePostHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_create_forum_post"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		title, res := blRequired(req, "title")
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
		forum, res := ftForum(ctx, d, req, tool, opWrite)
		if res != nil {
			return res, nil
		}
		var tags []string
		for _, t := range req.GetStringSlice("tag_names", nil) {
			if t = strings.TrimSpace(t); t != "" && !containsFold(tags, t) {
				tags = append(tags, t)
			}
		}
		if len(tags) > 5 {
			return errorf("a forum post can have at most 5 tags, got %d", len(tags)), nil
		}
		t, err := d.Discord.CreateForumPost(ctx, dto.CreateForumPostReq{ForumChannelID: forum.ID, Title: title, Content: content, TagNames: tags})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(t), nil
	}
}

func ftGetPostHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_get_forum_post"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		th, res := ftThread(ctx, d, req, tool, opRead, false)
		if res != nil {
			return res, nil
		}
		post, err := d.Discord.GetForumPost(ctx, th.ID, clamp(req.GetInt("limit", 50), 1, 100))
		if err != nil {
			return errorResult(err), nil
		}
		if d.Store != nil {
			var msgs []dto.MessageItem
			if post.Starter != nil {
				msgs = append(msgs, *post.Starter)
			}
			msgs = append(msgs, post.Replies...)
			if err := d.Store.UpsertMessages(ctx, th.GuildID, th.ParentID, msgs); err != nil {
				d.Log().Warn("get_forum_post: index upsert failed", "thread_id", th.ID, "error", err)
			}
		}
		return jsonResult(post), nil
	}
}

func ftReplyHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_reply_to_forum"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		content, res := blRequired(req, "content")
		if res != nil {
			return res, nil
		}
		if res := blCheckContent(content); res != nil {
			return res, nil
		}
		th, res := ftThread(ctx, d, req, tool, opWrite, false)
		if res != nil {
			return res, nil
		}
		msg, err := d.Discord.ReplyToForum(ctx, th.ID, content)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(msg), nil
	}
}

func ftListThreadsHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_list_forum_threads"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		forum, res := ftForum(ctx, d, req, tool, opRead)
		if res != nil {
			return res, nil
		}
		ts, err := d.Discord.ListForumThreads(ctx, forum.ID, req.GetBool("include_archived", false))
		if err != nil {
			return errorResult(err), nil
		}
		return listResult(ts, ""), nil
	}
}

func ftManageThreadsHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_manage_threads"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		action := req.GetString("action", "")
		dry := dryRun(req)
		switch action {
		case "create":
			return ftCreateThread(ctx, d, req, tool), nil
		case "archive", "unarchive", "lock", "unlock":
		default:
			return errorf("action must be one of create, archive, unarchive, lock, unlock; got %q", action), nil
		}
		kind := opWrite
		if action == "archive" || action == "lock" {
			kind = opDestructive
		}
		if !hasArg(req, "thread_id") && hasArg(req, "channel_id") {
			return errorf("%s needs thread_id (channel_id is only used for create)", action), nil
		}
		th, res := ftThread(ctx, d, req, tool, kind, dry)
		if res != nil {
			return res, nil
		}
		t, f := true, false
		var archived, locked *bool
		var change string
		switch action {
		case "archive":
			archived, change = &t, "archived: → true"
		case "unarchive":
			archived, change = &f, "archived: → false"
		case "lock":
			locked, change = &t, "locked: → true"
		case "unlock":
			locked, change = &f, "locked: → false"
		}
		if dry {
			if cur := ftCurrentThread(ctx, d, th); cur != nil {
				switch action {
				case "archive", "unarchive":
					change = fmt.Sprintf("archived: %t → %t", cur.Archived, *archived)
				default:
					change = fmt.Sprintf("locked: %t → %t", cur.Locked, *locked)
				}
			}
			label := fmt.Sprintf("thread %q (%s)", th.Name, th.ID)
			return dryRunResult(d, channelOp(tool, th, kind, true), label, []string{label + " " + change}), nil
		}
		out, err := d.Discord.ModifyThread(ctx, th.ID, archived, locked)
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(out), nil
	}
}

// ftCurrentThread fetches a thread's current state via its parent (best effort, read-only).
func ftCurrentThread(ctx context.Context, d deps.Deps, th *dto.ChannelInfo) *dto.ThreadInfo {
	if th.ParentID == "" {
		return nil
	}
	ts, err := d.Discord.ListForumThreads(ctx, th.ParentID, true)
	if err != nil {
		return nil
	}
	for i := range ts {
		if ts[i].ID == th.ID {
			return &ts[i]
		}
	}
	return nil
}

func ftCreateThread(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string) *mcp.CallToolResult {
	name, res := blRequired(req, "name")
	if res != nil {
		return res
	}
	minutes := 0
	if hasArg(req, "auto_archive_minutes") {
		minutes = req.GetInt("auto_archive_minutes", 0)
		ok := false
		for _, m := range ftAutoArchive {
			ok = ok || m == minutes
		}
		if !ok {
			return errorf("auto_archive_minutes must be one of 60, 1440, 4320, 10080; got %d", minutes)
		}
	}
	ch, res := blChannel(ctx, d, req, "channel_id", tool, opWrite, false)
	if res != nil {
		return res
	}
	switch ch.Type {
	case dto.ChannelText, dto.ChannelAnnouncement:
	default:
		return errorf("cannot create a thread in %s (%s channel); use a text or announcement channel, or discord_create_forum_post for forums", blLabel(ch), ch.Type)
	}
	private := req.GetBool("private", false)
	msgID := strings.TrimSpace(req.GetString("message_id", ""))
	if private && msgID != "" {
		return errorf("private threads cannot be started from a message; omit message_id or private")
	}
	t, err := d.Discord.CreateThread(ctx, dto.CreateThreadReq{ChannelID: ch.ID, MessageID: msgID, Name: name,
		Private: private, AutoArchiveMinutes: minutes})
	if err != nil {
		return errorResult(err)
	}
	return jsonResult(t)
}
