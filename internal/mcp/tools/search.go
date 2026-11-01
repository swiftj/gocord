package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
	"github.com/local/discord-mcp/internal/policy"
)

const (
	searchToolName           = "discord_search_messages"
	searchDefaultBackfill    = 500
	searchMaxBackfill        = 5000
	searchBackfillPageSize   = 100
	searchToolDescriptionTxt = "Full-text search (FTS5: AND/OR/NOT, \"phrase\", prefix*) over the local message index of a guild. " +
		"Channels not yet indexed are first backfilled via REST, up to auto_backfill_limit messages per call."
)

// RegisterSearchTools registers discord_search_messages.
func RegisterSearchTools(s *server.MCPServer, d deps.Deps) {
	tool := mcp.NewTool(searchToolName,
		mcp.WithDescription(searchToolDescriptionTxt),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("guild_id", mcp.Required(), mcp.Description("Guild Snowflake or name.")),
		mcp.WithString("query", mcp.Required(), mcp.Description("FTS5 query, e.g. `release AND \"code freeze\"` or `deploy*`. Invalid syntax is searched as a phrase.")),
		mcp.WithString("channel_id", mcp.Description("Restrict to one channel, thread, or forum (Snowflake, #name, or mention). Includes thread replies.")),
		mcp.WithString("author_id", mcp.Description("Restrict to one author (Snowflake, @name, or mention).")),
		mcp.WithString("before", mcp.Description("Only messages before this Snowflake or ISO-8601 time.")),
		mcp.WithString("after", mcp.Description("Only messages after this Snowflake or ISO-8601 time. Also bounds the backfill.")),
		mcp.WithBoolean("has_attachments", mcp.Description("Only messages with (true) or without (false) attachments.")),
		mcp.WithNumber("limit", mcp.Description("Maximum hits (1-100)."), mcp.Min(1), mcp.Max(dto.MaxSearchLimit), mcp.DefaultNumber(dto.DefaultSearchLimit)),
		mcp.WithNumber("auto_backfill_limit", mcp.Description("Maximum messages fetched via REST across all channels before searching (0 disables backfill)."),
			mcp.Min(0), mcp.Max(searchMaxBackfill), mcp.DefaultNumber(searchDefaultBackfill)),
	)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return searchHandle(ctx, d, req)
	})
}

// searchTarget is one channel or thread whose messages are searched/backfilled.
type searchTarget struct {
	ID       string
	ParentID string // set for threads
}

// searchResponse is the tool output.
type searchResponse struct {
	Count            int             `json:"count"`
	Items            []dto.SearchHit `json:"items"`
	ChannelsSearched int             `json:"channels_searched"`
	IndexedChannels  int             `json:"indexed_channels"`
	Backfilled       int             `json:"backfilled"`
	Warning          string          `json:"warning,omitempty"`
}

func searchHandle(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := strings.TrimSpace(req.GetString("query", ""))
	if query == "" {
		return errorf("query is required"), nil
	}
	guildID, err := resolveGuild(ctx, d, req)
	if err != nil {
		return errorResult(err), nil
	}

	var ch *dto.ChannelInfo
	if ref := req.GetString("channel_id", ""); strings.TrimSpace(ref) != "" {
		if ch, err = resolveChannel(ctx, d, guildID, ref); err != nil {
			return errorResult(err), nil
		}
		if ch.GuildID != "" && ch.GuildID != guildID {
			return errorf("channel %s belongs to guild %s, not %s", ch.ID, ch.GuildID, guildID), nil
		}
	}
	var authorID string
	if ref := req.GetString("author_id", ""); strings.TrimSpace(ref) != "" {
		if authorID, err = resolveUser(ctx, d, guildID, ref); err != nil {
			return errorResult(err), nil
		}
	}
	before, err := cursorArg(req, "before")
	if err != nil {
		return errorf("%v", err), nil
	}
	after, err := cursorArg(req, "after")
	if err != nil {
		return errorf("%v", err), nil
	}
	limit := clamp(req.GetInt("limit", dto.DefaultSearchLimit), 1, dto.MaxSearchLimit)
	budget := clamp(req.GetInt("auto_backfill_limit", searchDefaultBackfill), 0, searchMaxBackfill)

	op := guildOp(searchToolName, guildID, opRead, false)
	if ch != nil {
		op = channelOp(searchToolName, ch, opRead, false)
		op.GuildID = guildID
	}
	if err := d.Guard.Check(op); err != nil {
		return errorResult(err), nil
	}

	targets, err := searchTargets(ctx, d, guildID, ch)
	if err != nil {
		return errorResult(err), nil
	}

	resp := searchResponse{ChannelsSearched: len(targets)}
	var warnings []string
	partial := 0
	var skipped []string
	for _, t := range targets {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		covered, n, berr := searchBackfillChannel(ctx, d, guildID, t, after, &budget)
		resp.Backfilled += n
		if berr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			d.Log().Warn("search backfill failed", "channel_id", t.ID, "error", berr.Error())
			skipped = append(skipped, t.ID)
			if errors.Is(berr, discord.ErrRateLimited) || errors.Is(berr, discord.ErrNotLoggedIn) {
				budget = 0 // stop hitting the API for the rest of this call
			}
			continue
		}
		if covered {
			resp.IndexedChannels++
		} else {
			partial++
		}
	}
	if partial > 0 {
		warnings = append(warnings, fmt.Sprintf("partial index: %d channels not fully backfilled", partial))
	}
	if len(skipped) > 0 {
		warnings = append(warnings, fmt.Sprintf("backfill skipped for %d unreadable channels: %s", len(skipped), strings.Join(skipped, ", ")))
	}

	q := dto.SearchQuery{
		GuildID: guildID, Query: query, AuthorID: authorID,
		Before: before, After: after, HasAttachments: optBool(req, "has_attachments"), Limit: limit,
	}
	if ch != nil {
		q.ChannelID = ch.ID
	}
	hits, warn, err := d.Store.SearchEx(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("search index: %w", err)
	}
	if warn != "" {
		warnings = append(warnings, warn)
	}
	resp.Items = searchFilterHits(d, hits, targets)
	resp.Count = len(resp.Items)
	resp.Warning = strings.Join(warnings, "; ")
	return jsonResult(resp), nil
}

// searchTargets returns the channels to backfill: the given channel (a forum
// expands to its threads, a category to its children), or every readable text,
// announcement, and forum-thread channel in the guild.
func searchTargets(ctx context.Context, d deps.Deps, guildID string, ch *dto.ChannelInfo) ([]searchTarget, error) {
	var out []searchTarget
	add := func(id, parent string) {
		if d.Guard.Check(policy.Op{Tool: searchToolName, ChannelID: id, ParentChannelID: parent}) != nil {
			return
		}
		out = append(out, searchTarget{ID: id, ParentID: parent})
	}
	addForum := func(forumID string) error {
		threads, err := d.Discord.ListForumThreads(ctx, forumID, true)
		if err != nil {
			return err
		}
		for _, th := range threads {
			add(th.ID, forumID)
		}
		return nil
	}

	if ch != nil {
		switch ch.Type {
		case dto.ChannelForum:
			return out, addForum(ch.ID)
		case dto.ChannelThread:
			add(ch.ID, ch.ParentID)
			return out, nil
		case dto.ChannelCategory:
			// fall through to the guild scan, restricted to children
		default:
			add(ch.ID, "")
			return out, nil
		}
	}

	channels, err := d.Discord.ListChannels(ctx, guildID)
	if err != nil {
		return nil, err
	}
	for _, c := range channels {
		if ch != nil && c.ParentID != ch.ID {
			continue
		}
		switch c.Type {
		case dto.ChannelText, dto.ChannelAnnouncement:
			add(c.ID, "")
		case dto.ChannelForum:
			if err := addForum(c.ID); err != nil {
				if errors.Is(err, discord.ErrForbidden) {
					continue
				}
				return nil, err
			}
		}
	}
	return out, nil
}

// searchBackfillChannel pages backwards through one channel until it is fully
// indexed, the shared budget is spent, or the pages are older than the after
// bound. It reports whether the index now covers the requested window and how
// many messages were fetched.
func searchBackfillChannel(ctx context.Context, d deps.Deps, guildID string, t searchTarget, after string, budget *int) (bool, int, error) {
	oldest, newest, full, err := d.Store.Coverage(ctx, t.ID)
	if err != nil {
		return false, 0, err
	}
	if full || searchCoversAfter(oldest, after) {
		return true, 0, nil
	}
	fetched := 0
	for *budget > 0 {
		page := min(searchBackfillPageSize, *budget)
		msgs, err := d.Discord.ReadMessages(ctx, dto.ReadMessagesReq{ChannelID: t.ID, Limit: page, Before: oldest})
		if err != nil {
			return false, fetched, err
		}
		if len(msgs) == 0 {
			if err := d.Store.UpdateCoverage(ctx, t.ID, guildID, oldest, newest, true); err != nil {
				return false, fetched, err
			}
			return true, fetched, nil
		}
		for i := range msgs {
			if msgs[i].ChannelID == "" {
				msgs[i].ChannelID = t.ID
			}
		}
		if err := d.Store.UpsertMessages(ctx, guildID, t.ParentID, msgs); err != nil {
			return false, fetched, err
		}
		fetched += len(msgs)
		*budget -= len(msgs)
		pageOldest, pageNewest := msgs[0].ID, msgs[0].ID
		for _, m := range msgs[1:] {
			if searchSnowLess(m.ID, pageOldest) {
				pageOldest = m.ID
			}
			if searchSnowLess(pageNewest, m.ID) {
				pageNewest = m.ID
			}
		}
		if newest == "" || searchSnowLess(newest, pageNewest) {
			newest = pageNewest
		}
		if oldest == "" || searchSnowLess(pageOldest, oldest) {
			oldest = pageOldest
		} else {
			// The API did not move backwards; treat the channel as exhausted.
			if err := d.Store.UpdateCoverage(ctx, t.ID, guildID, oldest, newest, true); err != nil {
				return false, fetched, err
			}
			return true, fetched, nil
		}
		if err := d.Store.UpdateCoverage(ctx, t.ID, guildID, oldest, newest, false); err != nil {
			return false, fetched, err
		}
		if searchCoversAfter(oldest, after) {
			return true, fetched, nil
		}
	}
	return false, fetched, nil
}

// searchCoversAfter reports whether an index reaching back to oldest already
// covers every message newer than the after bound.
func searchCoversAfter(oldest, after string) bool {
	return oldest != "" && after != "" && !searchSnowLess(after, oldest)
}

// searchSnowLess compares Snowflakes numerically.
func searchSnowLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// searchFilterHits drops hits from channels the policy forbids (e.g. Gateway-indexed
// messages in a blocked channel) unless they belong to an allowed target.
func searchFilterHits(d deps.Deps, hits []dto.SearchHit, targets []searchTarget) []dto.SearchHit {
	allowedTarget := make(map[string]bool, len(targets))
	for _, t := range targets {
		allowedTarget[t.ID] = true
	}
	out := make([]dto.SearchHit, 0, len(hits))
	for _, h := range hits {
		if allowedTarget[h.ChannelID] || d.Guard.ChannelAllowed(h.ChannelID) {
			out = append(out, h)
		}
	}
	return out
}
