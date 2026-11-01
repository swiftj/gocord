package tools

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

// Poll limits imposed by Discord.
const (
	rpMaxQuestion  = 300
	rpMaxAnswer    = 55
	rpMinAnswers   = 2
	rpMaxAnswers   = 10
	rpMaxEmojis    = 20
	rpMaxPollHours = 768
)

// RegisterReactionPollTools registers reaction and native poll tools.
func RegisterReactionPollTools(s *server.MCPServer, d deps.Deps) {
	emojiDesc := mcp.Description("Unicode emoji, :name:, or name:id for a custom emoji.")

	s.AddTool(blTool("discord_add_reaction",
		"Add an emoji reaction to a message as the bot.",
		blMutating(false, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Message Snowflake.")),
		mcp.WithString("emoji", mcp.Required(), emojiDesc),
	), rpAddHandler(d))

	s.AddTool(blTool("discord_add_multiple_reactions",
		"Add up to 20 emoji reactions to a message in order, reporting per-emoji failures.",
		blMutating(false, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Message Snowflake.")),
		mcp.WithArray("emojis", mcp.Required(), mcp.MinItems(1), mcp.MaxItems(rpMaxEmojis), mcp.WithStringItems(),
			mcp.Description("Emojis to add in order (unicode, :name:, or name:id).")),
	), rpAddMultipleHandler(d))

	s.AddTool(blTool("discord_remove_reaction",
		"Remove an emoji reaction from a message; omit user_id to remove the bot's own reaction.",
		blMutating(false, true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Message Snowflake.")),
		mcp.WithString("emoji", mcp.Required(), emojiDesc),
		mcp.WithString("user_id", mcp.Description("User whose reaction to remove (Snowflake, @name, or mention).")),
	), rpRemoveHandler(d))

	s.AddTool(blTool("discord_create_poll",
		"Create a native Discord poll with 2-10 answers. Question max 300 chars, each answer max 55.",
		blMutating(false, false),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("question", mcp.Required(), mcp.MaxLength(rpMaxQuestion), mcp.Description("Poll question.")),
		mcp.WithArray("answers", mcp.Required(), mcp.MinItems(rpMinAnswers), mcp.MaxItems(rpMaxAnswers),
			mcp.WithStringItems(mcp.MaxLength(rpMaxAnswer)), mcp.Description("Answer texts (2-10).")),
		mcp.WithInteger("duration_hours", mcp.Min(1), mcp.Max(rpMaxPollHours), mcp.DefaultNumber(24), mcp.Description("Poll duration in hours (1-768).")),
		mcp.WithBoolean("allow_multiselect", mcp.DefaultBool(false), mcp.Description("Allow voting for multiple answers.")),
	), rpCreatePollHandler(d))

	s.AddTool(blTool("discord_get_poll_results",
		"Get a poll's current vote tallies, total votes, winners (ties list several), and whether it is finalized.",
		blReadOnly(),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread Snowflake, #name, or mention.")),
		mcp.WithString("message_id", mcp.Required(), mcp.Description("Poll message Snowflake.")),
	), rpPollResultsHandler(d))
}

// rpEmoji normalises an emoji argument.
func rpEmoji(e string) string {
	e = strings.TrimSpace(e)
	// <:name:id> or <a:name:id> → name:id (the form Discord's reaction API expects).
	if strings.HasPrefix(e, "<") && strings.HasSuffix(e, ">") {
		inner := strings.TrimSuffix(strings.TrimPrefix(e, "<"), ">")
		inner = strings.TrimPrefix(inner, "a")
		e = strings.TrimPrefix(inner, ":")
	}
	return e
}

// rpTarget resolves channel_id + message_id with a write policy check.
func rpTarget(ctx context.Context, d deps.Deps, req mcp.CallToolRequest, tool string, kind int) (*dto.ChannelInfo, string, *mcp.CallToolResult) {
	msgID, res := blRequired(req, "message_id")
	if res != nil {
		return nil, "", res
	}
	ch, res := blChannel(ctx, d, req, "channel_id", tool, kind, false)
	if res != nil {
		return nil, "", res
	}
	return ch, msgID, nil
}

func rpAddHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_add_reaction"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		emoji := rpEmoji(req.GetString("emoji", ""))
		if emoji == "" {
			return errorf("emoji is required"), nil
		}
		ch, msgID, res := rpTarget(ctx, d, req, tool, opWrite)
		if res != nil {
			return res, nil
		}
		if err := d.Discord.AddReaction(ctx, ch.ID, msgID, emoji); err != nil {
			return errorResult(err), nil
		}
		return jsonResult(map[string]any{"ok": true, "channel_id": ch.ID, "message_id": msgID, "emoji": emoji}), nil
	}
}

func rpAddMultipleHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_add_multiple_reactions"
	type failure struct {
		Emoji string `json:"emoji"`
		Error string `json:"error"`
	}
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var emojis []string
		for _, e := range req.GetStringSlice("emojis", nil) {
			if e = rpEmoji(e); e != "" {
				emojis = append(emojis, e)
			}
		}
		if len(emojis) == 0 {
			return errorf("emojis must contain at least one emoji"), nil
		}
		if len(emojis) > rpMaxEmojis {
			return errorf("at most %d emojis per call, got %d", rpMaxEmojis, len(emojis)), nil
		}
		ch, msgID, res := rpTarget(ctx, d, req, tool, opWrite)
		if res != nil {
			return res, nil
		}
		// Fast path: one batched call. On failure, retry one by one so each
		// failing emoji is reported individually (re-adding a reaction is idempotent).
		var added []string
		var failed []failure
		if err := d.Discord.AddMultipleReactions(ctx, ch.ID, msgID, emojis); err == nil {
			added = emojis
		} else {
			for _, e := range emojis {
				if ctx.Err() != nil {
					failed = append(failed, failure{e, ctx.Err().Error()})
					continue
				}
				if err := d.Discord.AddReaction(ctx, ch.ID, msgID, e); err != nil {
					failed = append(failed, failure{e, rpErrorText(err)})
				} else {
					added = append(added, e)
				}
			}
		}
		if len(added) == 0 {
			parts := make([]string, len(failed))
			for i, f := range failed {
				parts[i] = f.Emoji + ": " + f.Error
			}
			return errorf("no reactions added: %s", strings.Join(parts, "; ")), nil
		}
		return jsonResult(struct {
			OK        bool      `json:"ok"`
			ChannelID string    `json:"channel_id"`
			MessageID string    `json:"message_id"`
			Added     []string  `json:"added"`
			Failed    []failure `json:"failed,omitempty"`
		}{len(failed) == 0, ch.ID, msgID, added, failed}), nil
	}
}

// rpErrorText returns the actionable text errorResult would produce.
func rpErrorText(err error) string {
	r := errorResult(err)
	for _, c := range r.Content {
		if tc, ok := mcp.AsTextContent(c); ok {
			return tc.Text
		}
	}
	return err.Error()
}

func rpRemoveHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_remove_reaction"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		emoji := rpEmoji(req.GetString("emoji", ""))
		if emoji == "" {
			return errorf("emoji is required"), nil
		}
		ch, msgID, res := rpTarget(ctx, d, req, tool, opWrite)
		if res != nil {
			return res, nil
		}
		uid := ""
		if ref := strings.TrimSpace(req.GetString("user_id", "")); ref != "" {
			id, err := resolveUser(ctx, d, ch.GuildID, ref)
			if err != nil {
				return errorResult(err), nil
			}
			uid = id
		}
		if err := d.Discord.RemoveReaction(ctx, ch.ID, msgID, emoji, uid); err != nil {
			return errorResult(err), nil
		}
		out := map[string]any{"ok": true, "channel_id": ch.ID, "message_id": msgID, "emoji": emoji}
		if uid != "" {
			out["user_id"] = uid
		}
		return jsonResult(out), nil
	}
}

func rpCreatePollHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_create_poll"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		q, res := blRequired(req, "question")
		if res != nil {
			return res, nil
		}
		if n := utf8.RuneCountInString(q); n > rpMaxQuestion {
			return errorf("question is %d characters; max %d", n, rpMaxQuestion), nil
		}
		var answers []string
		for _, a := range req.GetStringSlice("answers", nil) {
			a = strings.TrimSpace(a)
			if a == "" {
				return errorf("answers cannot contain empty strings"), nil
			}
			if n := utf8.RuneCountInString(a); n > rpMaxAnswer {
				return errorf("answer %q is %d characters; max %d", a, n, rpMaxAnswer), nil
			}
			answers = append(answers, a)
		}
		if len(answers) < rpMinAnswers || len(answers) > rpMaxAnswers {
			return errorf("answers must have %d-%d entries, got %d", rpMinAnswers, rpMaxAnswers, len(answers)), nil
		}
		hours := req.GetInt("duration_hours", 24)
		if hours < 1 || hours > rpMaxPollHours {
			return errorf("duration_hours must be 1-%d, got %d", rpMaxPollHours, hours), nil
		}
		ch, res := blChannel(ctx, d, req, "channel_id", tool, opWrite, false)
		if res != nil {
			return res, nil
		}
		msg, err := d.Discord.CreatePoll(ctx, dto.CreatePollReq{ChannelID: ch.ID, Question: q, Answers: answers,
			DurationHours: hours, AllowMultiselect: req.GetBool("allow_multiselect", false)})
		if err != nil {
			return errorResult(err), nil
		}
		return jsonResult(msg), nil
	}
}

func rpPollResultsHandler(d deps.Deps) server.ToolHandlerFunc {
	const tool = "discord_get_poll_results"
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ch, msgID, res := rpTarget(ctx, d, req, tool, opRead)
		if res != nil {
			return res, nil
		}
		p, err := d.Discord.GetPollResults(ctx, ch.ID, msgID)
		if err != nil {
			return errorResult(err), nil
		}
		rpFillTotals(p)
		return jsonResult(p), nil
	}
}

// rpFillTotals derives TotalVotes and Winners (ties → several) when the service left them empty.
func rpFillTotals(p *dto.PollResult) {
	sum, best := 0, 0
	for _, a := range p.Answers {
		sum += a.Votes
		best = max(best, a.Votes)
	}
	if p.TotalVotes == 0 {
		p.TotalVotes = sum
	}
	if len(p.Winners) == 0 && best > 0 {
		for _, a := range p.Answers {
			if a.Votes == best {
				p.Winners = append(p.Winners, a.Text)
			}
		}
	}
}
