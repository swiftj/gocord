package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

const (
	tupToolList     = "discord_list_tuppers"
	tupDefaultLimit = 100
	tupMaxLimit     = 500
	accToolCheck    = "discord_check_access"
)

// RegisterTupperAccessTools registers discord_list_tuppers and discord_check_access.
func RegisterTupperAccessTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(blTool(tupToolList,
		"List webhook identities (Tupperbox/PluralKit characters, persona webhooks) seen in a guild or channel, newest first, with each one's latest avatar URL. "+
			"Backfills unindexed channels like discord_search_messages. Avatar URLs come from the latest message: fetch or bridge them promptly.",
		blReadOnly(),
		mcp.WithString("guild_id", mcp.Description("Guild Snowflake or name. Optional when a default guild is configured or the bot is in one guild.")),
		mcp.WithString("channel_id", mcp.Description("Restrict to one channel, forum, or category (Snowflake, #name, or mention). Includes threads.")),
		mcp.WithString("since", mcp.Description("Only messages after this Snowflake or ISO-8601 time. Also bounds the backfill.")),
		mcp.WithNumber("limit", mcp.Min(1), mcp.Max(tupMaxLimit), mcp.DefaultNumber(tupDefaultLimit), mcp.Description("Maximum identities (1-500).")),
	), tupListHandler(d))

	s.AddTool(blTool(accToolCheck,
		"Diagnose which channels the bot can view and read history in, computed from its roles and channel overwrites, with a one-line fix for each blocked channel.",
		blReadOnly(),
		mcp.WithString("guild_id", mcp.Description("Guild Snowflake or name. Optional when a default guild is configured or the bot is in one guild.")),
		mcp.WithArray("channel_ids", mcp.WithStringItems(), mcp.MaxItems(500),
			mcp.Description("Channels to check (Snowflake, #name, or mention). Default: every text, announcement, and forum channel.")),
	), accCheckHandler(d))
}

type tupListResponse struct {
	Tuppers    []dto.TupperInfo `json:"tuppers"`
	Backfilled int              `json:"backfilled"`
	Warning    string           `json:"warning,omitempty"`
}

func tupListHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		guildID, err := resolveGuild(ctx, d, req)
		if err != nil {
			return errorResult(err), nil
		}
		since, err := cursorArg(req, "since")
		if err != nil {
			return errorf("%v", err), nil
		}
		limit := clamp(req.GetInt("limit", tupDefaultLimit), 1, tupMaxLimit)
		var ch *dto.ChannelInfo
		op := guildOp(tupToolList, guildID, opRead, false)
		if ref := strings.TrimSpace(req.GetString("channel_id", "")); ref != "" {
			if ch, err = resolveChannel(ctx, d, guildID, ref); err != nil {
				return errorResult(err), nil
			}
			op = channelOp(tupToolList, ch, opRead, false)
			op.GuildID = guildID
		}
		if err := d.Guard.Check(op); err != nil {
			return errorResult(err), nil
		}
		targets, err := searchTargets(ctx, d, guildID, ch)
		if err != nil {
			return errorResult(err), nil
		}
		resp := tupListResponse{}
		budget := searchDefaultBackfill
		var skipped []string
		for _, t := range targets {
			_, n, berr := searchBackfillChannel(ctx, d, guildID, t, since, &budget)
			resp.Backfilled += n
			if berr != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				d.Log().Warn("tupper backfill failed", "channel_id", t.ID, "error", berr.Error())
				skipped = append(skipped, t.ID)
				if errors.Is(berr, discord.ErrRateLimited) || errors.Is(berr, discord.ErrNotLoggedIn) {
					budget = 0
				}
			}
		}
		if len(skipped) > 0 {
			resp.Warning = fmt.Sprintf("backfill skipped for %d unreadable channels (see discord_check_access): %s", len(skipped), strings.Join(skipped, ", "))
		}
		q := dto.TupperQuery{GuildID: guildID, After: since, Limit: limit}
		if ch != nil && ch.Type != dto.ChannelCategory {
			q.ChannelID = ch.ID
		}
		rows, err := d.Store.ListWebhookAuthors(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("list tuppers: %w", err)
		}
		allowed := map[string]bool{}
		for _, t := range targets {
			allowed[t.ID] = true
		}
		resp.Tuppers = []dto.TupperInfo{}
		for _, r := range rows {
			// A category filter is applied here: only its child channels count.
			if ch != nil && !allowed[r.LastSeenChannelID] {
				continue
			}
			resp.Tuppers = append(resp.Tuppers, r)
		}
		return jsonResult(resp), nil
	}
}

// ---------- access diagnostics ----------

var (
	accView    = permissionBits["ViewChannel"]
	accHistory = permissionBits["ReadMessageHistory"]
	accAdmin   = permissionBits["Administrator"]
	accAll     = int64(-1)
)

type accResponse struct {
	Channels []dto.ChannelAccess `json:"channels"`
	Summary  dto.AccessSummary   `json:"summary"`
}

func accCheckHandler(d deps.Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		guildID, err := resolveGuild(ctx, d, req)
		if err != nil {
			return errorResult(err), nil
		}
		if err := d.Guard.Check(guildOp(accToolCheck, guildID, opRead, false)); err != nil {
			return errorResult(err), nil
		}
		channels, err := d.Discord.ListChannels(ctx, guildID)
		if err != nil {
			return errorResult(err), nil
		}
		byID := make(map[string]dto.ChannelInfo, len(channels))
		for _, c := range channels {
			byID[c.ID] = c
		}
		var targets []dto.ChannelInfo
		if raw, ok := req.GetArguments()["channel_ids"]; ok && raw != nil {
			refs, ok := raw.([]any)
			if !ok {
				return errorf("channel_ids must be an array of strings"), nil
			}
			for _, r := range refs {
				ref, ok := r.(string)
				if !ok {
					return errorf("channel_ids must be an array of strings"), nil
				}
				ch, err := resolveChannel(ctx, d, guildID, ref)
				if err != nil {
					return errorResult(err), nil
				}
				if ch.Type == dto.ChannelThread { // threads take their parent's permissions
					if p, ok := byID[ch.ParentID]; ok {
						ch = &p
					}
				}
				if full, ok := byID[ch.ID]; ok {
					ch = &full
				}
				targets = append(targets, *ch)
			}
		} else {
			for _, c := range channels {
				switch c.Type {
				case dto.ChannelText, dto.ChannelAnnouncement, dto.ChannelForum:
					targets = append(targets, c)
				}
			}
		}

		bot, err := d.Discord.GetMember(ctx, guildID, "")
		if err != nil {
			return errorResult(err), nil
		}
		roles, err := d.Discord.ListRoles(ctx, guildID)
		if err != nil {
			return errorResult(err), nil
		}
		info, err := d.Discord.GetGuildInfo(ctx, guildID)
		if err != nil {
			return errorResult(err), nil
		}
		a := newAccCalc(guildID, bot, roles, info.OwnerID)

		resp := accResponse{Channels: []dto.ChannelAccess{}}
		for _, c := range targets {
			row := a.check(c, byID)
			if row.CanView && row.CanReadHistory {
				resp.Summary.Readable++
			} else {
				resp.Summary.Blocked++
			}
			resp.Channels = append(resp.Channels, row)
		}
		return jsonResult(resp), nil
	}
}

// accCalc computes the bot's effective channel permissions (Discord's
// documented algorithm: base role permissions, then @everyone, role, and
// member overwrites).
type accCalc struct {
	guildID  string
	botID    string
	botRoles map[string]bool
	base     int64
	roleName map[string]string
	botRole  string // the role a fix should name
}

func newAccCalc(guildID string, bot *dto.MemberProfile, roles []dto.RoleInfo, ownerID string) *accCalc {
	a := &accCalc{guildID: guildID, botID: bot.UserID, botRoles: map[string]bool{}, roleName: map[string]string{}}
	for _, id := range bot.Roles {
		a.botRoles[id] = true
	}
	var best *dto.RoleInfo
	for i, r := range roles {
		a.roleName[r.ID] = r.Name
		bits, _ := strconv.ParseInt(r.Permissions, 10, 64)
		if r.ID == guildID || a.botRoles[r.ID] {
			a.base |= bits
		}
		if a.botRoles[r.ID] && r.ID != guildID {
			// Prefer the bot's managed integration role, else its highest role.
			if best == nil || (r.Managed && !best.Managed) || (r.Managed == best.Managed && r.Position > best.Position) {
				best = &roles[i]
			}
		}
	}
	if best != nil {
		a.botRole = best.Name
	}
	if a.base&accAdmin != 0 || (ownerID != "" && ownerID == bot.UserID) {
		a.base = accAll
	}
	return a
}

func (a *accCalc) effective(c dto.ChannelInfo) int64 {
	if a.base == accAll {
		return accAll
	}
	perms := a.base
	var allow, deny int64
	for _, o := range c.Overwrites {
		if o.Type == "role" && o.ID == a.guildID {
			perms = perms&^o.Deny | o.Allow
		}
	}
	for _, o := range c.Overwrites {
		if o.Type == "role" && o.ID != a.guildID && a.botRoles[o.ID] {
			allow |= o.Allow
			deny |= o.Deny
		}
	}
	perms = perms&^deny | allow
	for _, o := range c.Overwrites {
		if o.Type == "member" && o.ID == a.botID {
			perms = perms&^o.Deny | o.Allow
		}
	}
	return perms
}

func (a *accCalc) check(c dto.ChannelInfo, byID map[string]dto.ChannelInfo) dto.ChannelAccess {
	perms := a.effective(c)
	row := dto.ChannelAccess{ID: c.ID, Name: c.Name, CanView: perms&accView != 0}
	row.CanReadHistory = row.CanView && perms&accHistory != 0
	var cat *dto.ChannelInfo
	if p, ok := byID[c.ParentID]; ok && c.ParentID != "" {
		cat = &p
		row.Category = p.Name
	}
	if !row.CanView {
		row.Missing = append(row.Missing, "ViewChannel")
	}
	if perms&accHistory == 0 {
		row.Missing = append(row.Missing, "ReadMessageHistory")
	}
	if len(row.Missing) > 0 {
		row.Fix = a.fix(c, cat, row.Missing)
	}
	return row
}

// fix names where to add an overwrite: the category when the channel's
// overwrites are synced with it, else the channel itself.
func (a *accCalc) fix(c dto.ChannelInfo, cat *dto.ChannelInfo, missing []string) string {
	role := a.botRole
	if role == "" {
		role = "the bot's role"
	} else {
		role = fmt.Sprintf("role %q", role)
	}
	where := fmt.Sprintf("channel #%s", c.Name)
	if cat != nil && accSynced(c, *cat) {
		where = fmt.Sprintf("category %q (#%s is synced with it)", cat.Name, c.Name)
	}
	fix := fmt.Sprintf("In %s, add a permission overwrite for %s allowing %s.", where, role, strings.Join(missing, " and "))
	for _, o := range c.Overwrites {
		if o.Type == "member" && o.ID == a.botID && o.Deny&(accView|accHistory) != 0 {
			return fix + " Also remove the member overwrite that denies the bot directly."
		}
	}
	return fix
}

// accSynced reports whether a channel's overwrites equal its category's.
func accSynced(c, cat dto.ChannelInfo) bool {
	if len(c.Overwrites) != len(cat.Overwrites) {
		return false
	}
	key := func(o dto.PermissionOverwrite) string {
		return fmt.Sprintf("%s|%s|%d|%d", o.Type, o.ID, o.Allow, o.Deny)
	}
	a := make([]string, 0, len(c.Overwrites))
	b := make([]string, 0, len(cat.Overwrites))
	for i := range c.Overwrites {
		a = append(a, key(c.Overwrites[i]))
		b = append(b, key(cat.Overwrites[i]))
	}
	sort.Strings(a)
	sort.Strings(b)
	return strings.Join(a, ",") == strings.Join(b, ",")
}
