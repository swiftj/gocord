package discord

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// The resolver keeps an in-memory cache of guilds, channels (including active
// threads), roles, and members. Gateway events update it through the apply*
// methods; on a cache miss it falls back to REST through resolverREST. The
// cache never touches the network itself, so tests drive it with synthetic
// discordgo event structs.

type cachedGuild struct {
	ID          string
	Name        string
	OwnerID     string
	MemberCount int
}

type cachedChannel struct {
	ID       string
	GuildID  string
	Name     string
	Type     discordgo.ChannelType
	ParentID string
	Position int
	Topic    string
	NSFW     bool
	Archived bool
}

type cachedRole struct {
	ID       string
	GuildID  string
	Name     string
	Position int
}

type cachedMember struct {
	UserID     string
	GuildID    string
	Username   string
	GlobalName string
	Nick       string
	Bot        bool
	Roles      []string
}

// resolverREST is the REST fallback used on cache misses. *Client implements it;
// tests may pass nil (no fallback) or a fake.
type resolverREST interface {
	restGuild(ctx context.Context, guildID string) (*discordgo.Guild, error)
	restUserGuilds(ctx context.Context) ([]*discordgo.UserGuild, error)
	restChannel(ctx context.Context, channelID string) (*discordgo.Channel, error)
	restGuildChannels(ctx context.Context, guildID string) ([]*discordgo.Channel, error)
	restGuildMember(ctx context.Context, guildID, userID string) (*discordgo.Member, error)
	restSearchMembers(ctx context.Context, guildID, query string) ([]*discordgo.Member, error)
	restUser(ctx context.Context, userID string) (*discordgo.User, error)
}

// Resolver maps user-supplied references to Snowflakes.
type Resolver struct {
	mu       sync.RWMutex
	guilds   map[string]*cachedGuild
	channels map[string]*cachedChannel
	roles    map[string]*cachedRole              // roleID -> role
	members  map[string]map[string]*cachedMember // guildID -> userID -> member
	users    map[string]string                   // userID -> username (from any source)

	rest resolverREST
}

// NewResolver returns an empty cache. rest may be nil (no REST fallback).
func NewResolver(rest resolverREST) *Resolver {
	r := &Resolver{rest: rest}
	r.Reset()
	return r
}

// Reset clears every cached entry (used when switching bot identity).
func (r *Resolver) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.guilds = map[string]*cachedGuild{}
	r.channels = map[string]*cachedChannel{}
	r.roles = map[string]*cachedRole{}
	r.members = map[string]map[string]*cachedMember{}
	r.users = map[string]string{}
}

// ---------- cache mutation (Gateway events) ----------

func (r *Resolver) applyGuild(g *discordgo.Guild) {
	if g == nil || g.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cg := r.guilds[g.ID]
	if cg == nil {
		cg = &cachedGuild{ID: g.ID}
		r.guilds[g.ID] = cg
	}
	if g.Name != "" {
		cg.Name = g.Name
	}
	if g.OwnerID != "" {
		cg.OwnerID = g.OwnerID
	}
	if g.MemberCount > 0 {
		cg.MemberCount = g.MemberCount
	} else if g.ApproximateMemberCount > 0 {
		cg.MemberCount = g.ApproximateMemberCount
	}
	for _, c := range g.Channels {
		r.putChannelLocked(g.ID, c)
	}
	for _, t := range g.Threads {
		r.putChannelLocked(g.ID, t)
	}
	for _, role := range g.Roles {
		r.putRoleLocked(g.ID, role)
	}
	for _, m := range g.Members {
		r.putMemberLocked(g.ID, m)
	}
}

func (r *Resolver) applyUserGuild(g *discordgo.UserGuild) {
	if g == nil || g.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	cg := r.guilds[g.ID]
	if cg == nil {
		cg = &cachedGuild{ID: g.ID}
		r.guilds[g.ID] = cg
	}
	cg.Name = g.Name
	if g.ApproximateMemberCount > 0 {
		cg.MemberCount = g.ApproximateMemberCount
	}
}

func (r *Resolver) removeGuild(guildID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.guilds, guildID)
	for id, c := range r.channels {
		if c.GuildID == guildID {
			delete(r.channels, id)
		}
	}
	for id, role := range r.roles {
		if role.GuildID == guildID {
			delete(r.roles, id)
		}
	}
	delete(r.members, guildID)
}

func (r *Resolver) applyChannel(c *discordgo.Channel) {
	if c == nil || c.ID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putChannelLocked(c.GuildID, c)
}

func (r *Resolver) putChannelLocked(guildID string, c *discordgo.Channel) {
	if c == nil || c.ID == "" {
		return
	}
	if guildID == "" {
		guildID = c.GuildID
	}
	cc := &cachedChannel{
		ID: c.ID, GuildID: guildID, Name: c.Name, Type: c.Type, ParentID: c.ParentID,
		Position: c.Position, Topic: c.Topic, NSFW: c.NSFW,
	}
	if c.ThreadMetadata != nil {
		cc.Archived = c.ThreadMetadata.Archived
	}
	r.channels[c.ID] = cc
}

func (r *Resolver) removeChannel(channelID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.channels, channelID)
}

func (r *Resolver) applyThreadListSync(e *discordgo.ThreadListSync) {
	if e == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range e.Threads {
		r.putChannelLocked(e.GuildID, t)
	}
}

func (r *Resolver) applyRole(guildID string, role *discordgo.Role) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putRoleLocked(guildID, role)
}

func (r *Resolver) putRoleLocked(guildID string, role *discordgo.Role) {
	if role == nil || role.ID == "" {
		return
	}
	r.roles[role.ID] = &cachedRole{ID: role.ID, GuildID: guildID, Name: role.Name, Position: role.Position}
}

func (r *Resolver) removeRole(roleID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.roles, roleID)
}

func (r *Resolver) applyMember(guildID string, m *discordgo.Member) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.putMemberLocked(guildID, m)
}

func (r *Resolver) putMemberLocked(guildID string, m *discordgo.Member) {
	if m == nil || m.User == nil || m.User.ID == "" {
		return
	}
	if guildID == "" {
		guildID = m.GuildID
	}
	gm := r.members[guildID]
	if gm == nil {
		gm = map[string]*cachedMember{}
		r.members[guildID] = gm
	}
	gm[m.User.ID] = &cachedMember{
		UserID: m.User.ID, GuildID: guildID, Username: m.User.Username, GlobalName: m.User.GlobalName,
		Nick: m.Nick, Bot: m.User.Bot, Roles: slices.Clone(m.Roles),
	}
	r.users[m.User.ID] = m.User.Username
}

func (r *Resolver) removeMember(guildID, userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if gm := r.members[guildID]; gm != nil {
		delete(gm, userID)
	}
}

func (r *Resolver) noteUser(u *discordgo.User) {
	if u == nil || u.ID == "" || u.Username == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.users[u.ID] = u.Username
}

// ---------- cache lookups ----------

func (r *Resolver) channel(id string) (cachedChannel, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.channels[id]
	if !ok {
		return cachedChannel{}, false
	}
	return *c, true
}

func (r *Resolver) guild(id string) (cachedGuild, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	g, ok := r.guilds[id]
	if !ok {
		return cachedGuild{}, false
	}
	return *g, true
}

// threadParent returns the parent channel of a thread, or "" when channelID is
// not a known thread.
func (r *Resolver) threadParent(channelID string) string {
	c, ok := r.channel(channelID)
	if !ok || !isThreadType(c.Type) {
		return ""
	}
	return c.ParentID
}

func (r *Resolver) roleName(id string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	role, ok := r.roles[id]
	if !ok {
		return "", false
	}
	return role.Name, true
}

func (r *Resolver) channelName(id string) (string, bool) {
	c, ok := r.channel(id)
	return c.Name, ok
}

func (r *Resolver) userName(id string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.users[id]
	return n, ok
}

// ---------- resolution ----------

type candidate struct {
	id    string
	label string // e.g. "#general in Test Guild"
}

func refNotFound(kind, ref string) error { return fmt.Errorf("%s %q: %w", kind, ref, ErrNotFound) }

func ambiguous(kind, name string, hits []candidate) error {
	slices.SortFunc(hits, func(a, b candidate) int { return cmpStr(a.id, b.id) })
	parts := make([]string, len(hits))
	for i, h := range hits {
		parts[i] = h.id
		if h.label != "" {
			parts[i] += " (" + h.label + ")"
		}
	}
	return fmt.Errorf("%s %q matches %s: %w", kind, name, strings.Join(parts, ", "), ErrAmbiguous)
}

func (r *Resolver) guildCandidates(name string) []candidate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var hits []candidate
	for _, g := range r.guilds {
		if NameMatches(name, g.Name) {
			hits = append(hits, candidate{id: g.ID, label: g.Name})
		}
	}
	return hits
}

func (r *Resolver) channelCandidates(guildID, name string) []candidate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var hits []candidate
	for _, c := range r.channels {
		if guildID != "" && c.GuildID != guildID {
			continue
		}
		if NameMatches(name, c.Name) {
			label := "#" + c.Name
			if g := r.guilds[c.GuildID]; g != nil && g.Name != "" {
				label += " in " + g.Name
			}
			hits = append(hits, candidate{id: c.ID, label: label})
		}
	}
	return hits
}

func (r *Resolver) memberCandidates(guildID, name string) []candidate {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]bool{}
	var hits []candidate
	for gid, gm := range r.members {
		if guildID != "" && gid != guildID {
			continue
		}
		for _, m := range gm {
			if seen[m.UserID] {
				continue
			}
			if NameMatches(name, m.Username) || (m.GlobalName != "" && NameMatches(name, m.GlobalName)) ||
				(m.Nick != "" && NameMatches(name, m.Nick)) {
				seen[m.UserID] = true
				hits = append(hits, candidate{id: m.UserID, label: "@" + m.Username})
			}
		}
	}
	return hits
}

func pick(kind, name string, hits []candidate) (string, error) {
	switch len(hits) {
	case 0:
		return "", refNotFound(kind, name)
	case 1:
		return hits[0].id, nil
	default:
		return "", ambiguous(kind, name, hits)
	}
}

// ResolveGuildID resolves a guild ID, mention-free name, or raw Snowflake.
func (r *Resolver) ResolveGuildID(ctx context.Context, guildRef string) (string, error) {
	ref := ParseRef(guildRef)
	if ref.ID != "" {
		if _, ok := r.guild(ref.ID); ok {
			return ref.ID, nil
		}
		if r.rest != nil {
			g, err := r.rest.restGuild(ctx, ref.ID)
			if err == nil {
				r.applyGuild(g)
				return ref.ID, nil
			}
			if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
				return "", err
			}
		}
		return "", refNotFound("guild", ref.ID)
	}
	if ref.Name == "" {
		return "", refNotFound("guild", guildRef)
	}
	hits := r.guildCandidates(ref.Name)
	if len(hits) == 0 && r.rest != nil {
		gs, err := r.rest.restUserGuilds(ctx)
		if err != nil {
			return "", err
		}
		for _, g := range gs {
			r.applyUserGuild(g)
		}
		hits = r.guildCandidates(ref.Name)
	}
	return pick("guild", ref.Name, hits)
}

// ResolveChannelID resolves a channel or thread within guildID ("" = any guild).
func (r *Resolver) ResolveChannelID(ctx context.Context, guildID, channelRef string) (string, error) {
	ref := ParseRef(channelRef)
	if ref.ID != "" {
		if c, ok := r.channel(ref.ID); ok && (guildID == "" || c.GuildID == guildID) {
			return ref.ID, nil
		}
		if r.rest != nil {
			c, err := r.rest.restChannel(ctx, ref.ID)
			if err == nil {
				r.applyChannel(c)
				if guildID == "" || c.GuildID == guildID {
					return ref.ID, nil
				}
			} else if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
				return "", err
			}
		}
		return "", refNotFound("channel", ref.ID)
	}
	if ref.Name == "" {
		return "", refNotFound("channel", channelRef)
	}
	hits := r.channelCandidates(guildID, ref.Name)
	if len(hits) == 0 && r.rest != nil && guildID != "" {
		cs, err := r.rest.restGuildChannels(ctx, guildID)
		if err != nil {
			return "", err
		}
		for _, c := range cs {
			r.applyChannel(c)
		}
		hits = r.channelCandidates(guildID, ref.Name)
	}
	return pick("channel", ref.Name, hits)
}

// ResolveUserID resolves a guild member (guildID "" = any cached guild).
func (r *Resolver) ResolveUserID(ctx context.Context, guildID, userRef string) (string, error) {
	ref := ParseRef(userRef)
	if ref.ID != "" {
		if r.isMember(guildID, ref.ID) {
			return ref.ID, nil
		}
		if r.rest != nil {
			var err error
			if guildID != "" {
				var m *discordgo.Member
				if m, err = r.rest.restGuildMember(ctx, guildID, ref.ID); err == nil {
					r.applyMember(guildID, m)
					return ref.ID, nil
				}
			} else {
				var u *discordgo.User
				if u, err = r.rest.restUser(ctx, ref.ID); err == nil {
					r.noteUser(u)
					return ref.ID, nil
				}
			}
			if !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrForbidden) {
				return "", err
			}
		}
		return "", refNotFound("user", ref.ID)
	}
	if ref.Name == "" {
		return "", refNotFound("user", userRef)
	}
	hits := r.memberCandidates(guildID, ref.Name)
	if len(hits) == 0 && r.rest != nil && guildID != "" {
		ms, err := r.rest.restSearchMembers(ctx, guildID, ref.Name)
		if err != nil {
			return "", err
		}
		for _, m := range ms {
			r.applyMember(guildID, m)
		}
		hits = r.memberCandidates(guildID, ref.Name)
	}
	return pick("user", ref.Name, hits)
}

func (r *Resolver) isMember(guildID, userID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if guildID != "" {
		_, ok := r.members[guildID][userID]
		return ok
	}
	for _, gm := range r.members {
		if _, ok := gm[userID]; ok {
			return true
		}
	}
	return false
}

func isThreadType(t discordgo.ChannelType) bool {
	return t == discordgo.ChannelTypeGuildPublicThread || t == discordgo.ChannelTypeGuildPrivateThread ||
		t == discordgo.ChannelTypeGuildNewsThread
}
