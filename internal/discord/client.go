package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/index"
)

// Client is the live discordgo-backed implementation of Service.
//
// The server starts without a token: until Login succeeds every method returns
// an error wrapping ErrNotLoggedIn. Gateway events feed the resolver cache, the
// FTS5 indexer (through a bounded worker queue), the HITL broker, and any
// OnEvent subscribers.
//
// Gateway intents: Guilds | GuildMessages | GuildMembers | MessageContent |
// GuildMessageReactions | GuildScheduledEvents | GuildModeration.
// MessageContent and GuildMembers are PRIVILEGED intents and must be enabled
// for the bot in the Discord Developer Portal (Bot → Privileged Gateway Intents).
type Client struct {
	cfg      *config.Config
	store    *index.Store
	log      *slog.Logger
	resolver *Resolver
	broker   *Broker
	http     *http.Client
	telem    *rateLimitTelemetry

	mu      sync.RWMutex
	session *discordgo.Session
	profile string
	botID   string
	botName string

	hooksMu          sync.RWMutex
	eventHooks       []func(kind, guildID, channelID string)
	interactionHooks []func(*discordgo.InteractionCreate)

	tokensMu      sync.Mutex
	webhookTokens map[string]string // webhookID -> token; never logged or returned

	ingest        chan ingestJob
	events        chan eventJob
	droppedIngest atomic.Int64
	droppedEvents atomic.Int64
	stop          chan struct{}
	wg            sync.WaitGroup
	closeOnce     sync.Once
}

var _ Service = (*Client)(nil)

// GatewayIntents are the intents requested on Login (spec 01 §8).
const GatewayIntents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages | discordgo.IntentsGuildMembers |
	discordgo.IntentsMessageContent | discordgo.IntentsGuildMessageReactions |
	discordgo.IntentsGuildScheduledEvents | discordgo.IntentGuildModeration

// Queue capacities for the Gateway → indexer and Gateway → subscriber workers.
const (
	ingestQueueSize = 1024
	eventQueueSize  = 1024
)

// Resource-subscription event kinds passed to OnEvent callbacks.
const (
	EventMessageCreate = "message_create"
	EventChannelCreate = "channel_create"
	EventChannelUpdate = "channel_update"
	EventChannelDelete = "channel_delete"
	EventThreadCreate  = "thread_create"
	EventThreadUpdate  = "thread_update"
	EventThreadDelete  = "thread_delete"
	EventRoleCreate    = "role_create"
	EventRoleUpdate    = "role_update"
	EventRoleDelete    = "role_delete"
)

type ingestJob struct {
	del       bool
	guildID   string
	parentID  string
	msg       dto.MessageItem
	channelID string
	ids       []string
}

type eventJob struct{ kind, guildID, channelID string }

// NewClient builds a Client. store may be nil (indexing disabled); logger nil
// means slog.Default(). Call Close to stop the background workers.
func NewClient(cfg *config.Config, store *index.Store, logger *slog.Logger) *Client {
	if cfg == nil {
		cfg = &config.Config{Profile: config.DefaultProfile}
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := &Client{
		cfg:           cfg,
		store:         store,
		log:           logger,
		webhookTokens: map[string]string{},
		ingest:        make(chan ingestJob, ingestQueueSize),
		events:        make(chan eventJob, eventQueueSize),
		stop:          make(chan struct{}),
	}
	c.telem = &rateLimitTelemetry{log: logger}
	c.http = &http.Client{Timeout: 30 * time.Second, Transport: &telemetryTransport{base: http.DefaultTransport, t: c.telem}}
	c.resolver = NewResolver(c)
	c.broker = newBroker(&sessionBrokerAPI{c: c}, logger)
	c.wg.Add(2)
	go c.ingestWorker()
	go c.eventWorker()
	return c
}

// Close stops the Gateway session and background workers. It is idempotent.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		s := c.session
		c.session = nil
		c.mu.Unlock()
		if s != nil {
			err = s.Close()
		}
		close(c.stop)
		c.wg.Wait()
	})
	return err
}

// OnEvent registers a non-blocking subscriber for message create and
// channel/role/thread create/update/delete events (used for MCP resource
// subscriptions). Callbacks run on a single worker goroutine; when its queue is
// full, events are dropped with a warning rather than blocking the Gateway.
func (c *Client) OnEvent(fn func(kind string, guildID, channelID string)) {
	if fn == nil {
		return
	}
	c.hooksMu.Lock()
	defer c.hooksMu.Unlock()
	c.eventHooks = append(c.eventHooks, fn)
}

// OnInteraction registers an additional InteractionCreate listener. The client
// registers exactly one Gateway handler and fans out to the HITL broker first,
// then to these listeners.
func (c *Client) OnInteraction(fn func(*discordgo.InteractionCreate)) {
	if fn == nil {
		return
	}
	c.hooksMu.Lock()
	defer c.hooksMu.Unlock()
	c.interactionHooks = append(c.interactionHooks, fn)
}

// RateLimitInfo is the last observed rate-limit state.
type RateLimitInfo struct {
	Route      string
	Bucket     string
	Remaining  int
	ResetAfter time.Duration
	At         time.Time
}

// LastRateLimit returns the most recently observed rate-limit headers.
func (c *Client) LastRateLimit() RateLimitInfo { return c.telem.last() }

// ---------- session ----------

func (c *Client) sess() (*discordgo.Session, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.session == nil {
		return nil, fmt.Errorf("no active Discord session (set DISCORD_BOT_TOKEN or call discord_login): %w", ErrNotLoggedIn)
	}
	return c.session, nil
}

// reqOpts returns per-request options: the caller's context and an optional
// audit-log reason.
func reqOpts(ctx context.Context, reason string) []discordgo.RequestOption {
	opts := []discordgo.RequestOption{discordgo.WithContext(ctx)}
	if reason = strings.TrimSpace(reason); reason != "" {
		opts = append(opts, discordgo.WithAuditLogReason(reason))
	}
	return opts
}

// Login opens a Gateway session with token (an empty token with a configured
// profile uses that profile's token_env), replacing any previous session.
func (c *Client) Login(ctx context.Context, token, profile string) (*dto.SessionInfo, error) {
	if profile == "" {
		profile = c.cfg.Profile
	}
	if profile == "" {
		profile = config.DefaultProfile
	}
	token = strings.TrimSpace(token)
	if token == "" && c.cfg.Profiles != nil {
		if _, ok := c.cfg.Profiles[profile]; ok {
			t, err := c.cfg.TokenForProfile(profile, nil)
			if err != nil {
				return nil, fmt.Errorf("login: %v: %w", err, ErrNotLoggedIn)
			}
			token = t
		}
	}
	if token == "" {
		return nil, fmt.Errorf("login: empty token: %w", ErrNotLoggedIn)
	}
	if !strings.HasPrefix(token, "Bot ") {
		token = "Bot " + token
	}

	s, err := discordgo.New(token)
	if err != nil {
		return nil, fmt.Errorf("login: create session: %w", err)
	}
	s.Client = c.http
	s.StateEnabled = false
	s.ShouldReconnectOnError = true
	s.ShouldRetryOnRateLimit = true
	s.MaxRestRetries = 3
	s.Identify.Intents = GatewayIntents
	c.addHandlers(s)

	opened := make(chan error, 1)
	go func() { opened <- s.Open() }()
	select {
	case err = <-opened:
	case <-ctx.Done():
		go func() {
			// Wait for Open to finish, then close whatever it opened (Open's error is irrelevant here).
			if err := <-opened; err != nil {
				c.log.Debug("gateway open finished after login was cancelled", "err", err)
			}
			_ = s.Close()
		}()
		return nil, ctx.Err()
	}
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("login: open gateway: %w", mapErr("login", err))
	}

	me, err := s.User("@me", discordgo.WithContext(ctx))
	if err != nil {
		_ = s.Close()
		return nil, mapErr("login: fetch bot user", err)
	}

	c.mu.Lock()
	old := c.session
	c.session = s
	c.profile = profile
	c.botID, c.botName = me.ID, me.Username
	c.mu.Unlock()
	if old != nil {
		if err := old.Close(); err != nil {
			c.log.Warn("closing previous session", "err", err)
		}
	}
	c.resolver.Reset()

	guilds, err := c.restUserGuilds(ctx)
	if err != nil {
		c.log.Warn("login: listing guilds failed", "err", err)
	}
	for _, g := range guilds {
		c.resolver.applyUserGuild(g)
	}
	c.log.Info("discord session opened", "profile", profile, "bot_user_id", me.ID, "guilds", len(guilds))
	return &dto.SessionInfo{Profile: profile, BotUserID: me.ID, BotUsername: me.Username, GuildCount: len(guilds)}, nil
}

// BotUserID returns the logged-in bot's user ID ("" before Login). Tools use it
// (via an optional interface) to enforce role-hierarchy checks.
func (c *Client) BotUserID() string { return c.botUserID() }

func (c *Client) botUserID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.botID
}

// ---------- error mapping ----------

// Discord JSON error codes that mean "forbidden".
const (
	codeMissingAccess      = 50001
	codeMissingPermissions = 50013
	codeCannotEditOthers   = 50005
)

// mapErr converts discordgo errors into sentinel-wrapped errors that keep
// Discord's message. It never includes request headers (which carry the token).
func mapErr(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", op, err)
	}
	var rl *discordgo.RateLimitError
	if errors.As(err, &rl) {
		retry := time.Duration(0)
		if rl.RateLimit != nil && rl.TooManyRequests != nil {
			retry = rl.RetryAfter
		}
		return fmt.Errorf("%s: retry after %s: %w", op, retry, ErrRateLimited)
	}
	var rest *discordgo.RESTError
	if errors.As(err, &rest) && rest.Response != nil {
		msg := rest.Response.Status
		code := 0
		if rest.Message != nil {
			code = rest.Message.Code
			if rest.Message.Message != "" {
				msg = rest.Message.Message
			}
		}
		detail := msg
		if code != 0 {
			detail = fmt.Sprintf("%s (code %d)", msg, code)
		}
		switch {
		case rest.Response.StatusCode == http.StatusNotFound:
			return fmt.Errorf("%s: %s: %w", op, detail, ErrNotFound)
		case rest.Response.StatusCode == http.StatusTooManyRequests:
			return fmt.Errorf("%s: %s: %w", op, detail, ErrRateLimited)
		case rest.Response.StatusCode == http.StatusForbidden || code == codeMissingPermissions || code == codeMissingAccess:
			return fmt.Errorf("%s: %s: %w", op, detail, ErrForbidden)
		case rest.Response.StatusCode == http.StatusUnauthorized:
			return fmt.Errorf("%s: invalid or revoked bot token (%s): %w", op, detail, ErrNotLoggedIn)
		}
		return fmt.Errorf("%s: discord HTTP %d: %s", op, rest.Response.StatusCode, detail)
	}
	if errors.Is(err, discordgo.ErrUnauthorized) {
		return fmt.Errorf("%s: invalid bot token: %w", op, ErrNotLoggedIn)
	}
	if strings.Contains(err.Error(), "4004") { // gateway close: authentication failed
		return fmt.Errorf("%s: authentication failed: %w", op, ErrNotLoggedIn)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// ---------- rate-limit telemetry ----------

type rateLimitTelemetry struct {
	log  *slog.Logger
	mu   sync.Mutex
	info RateLimitInfo
}

func (t *rateLimitTelemetry) last() RateLimitInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.info
}

func (t *rateLimitTelemetry) observe(method, path string, resp *http.Response) {
	h := resp.Header
	route := method + " " + sanitizeRoute(path)
	bucket := h.Get("X-RateLimit-Bucket")
	remStr := h.Get("X-RateLimit-Remaining")
	resetStr := h.Get("X-RateLimit-Reset-After")
	if resp.StatusCode == http.StatusTooManyRequests {
		t.log.Warn("discord rate limited", "route", route, "bucket", bucket,
			"retry_after", h.Get("Retry-After"), "scope", h.Get("X-RateLimit-Scope"),
			"global", h.Get("X-RateLimit-Global") != "")
	}
	if remStr == "" && resetStr == "" {
		return
	}
	rem, _ := strconv.Atoi(remStr)
	resetF, _ := strconv.ParseFloat(resetStr, 64)
	reset := time.Duration(resetF * float64(time.Second))
	t.mu.Lock()
	t.info = RateLimitInfo{Route: route, Bucket: bucket, Remaining: rem, ResetAfter: reset, At: time.Now()}
	t.mu.Unlock()
	t.log.Debug("discord rate limit", "route", route, "remaining", rem, "reset_after", reset.String(), "bucket", bucket)
}

// sanitizeRoute replaces Snowflakes with ":id" and redacts webhook/interaction
// tokens so routes are safe to log.
func sanitizeRoute(path string) string {
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if i >= 2 && (parts[i-2] == "webhooks" || parts[i-2] == "interactions") && p != "" && p != "messages" {
			parts[i] = "***"
			continue
		}
		if dto.IsSnowflake(p) {
			parts[i] = ":id"
		}
	}
	return strings.Join(parts, "/")
}

type telemetryTransport struct {
	base http.RoundTripper
	t    *rateLimitTelemetry
}

func (tt *telemetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := tt.base.RoundTrip(req)
	if err == nil && resp != nil {
		tt.t.observe(req.Method, req.URL.Path, resp)
	}
	return resp, err
}

// ---------- Gateway handlers ----------

func (c *Client) addHandlers(s *discordgo.Session) {
	s.AddHandler(c.onReady)
	s.AddHandler(c.onGuildCreate)
	s.AddHandler(c.onGuildUpdate)
	s.AddHandler(c.onGuildDelete)
	s.AddHandler(c.onChannelCreate)
	s.AddHandler(c.onChannelUpdate)
	s.AddHandler(c.onChannelDelete)
	s.AddHandler(c.onThreadCreate)
	s.AddHandler(c.onThreadUpdate)
	s.AddHandler(c.onThreadDelete)
	s.AddHandler(c.onThreadListSync)
	s.AddHandler(c.onRoleCreate)
	s.AddHandler(c.onRoleUpdate)
	s.AddHandler(c.onRoleDelete)
	s.AddHandler(c.onMemberAdd)
	s.AddHandler(c.onMemberUpdate)
	s.AddHandler(c.onMemberRemove)
	s.AddHandler(c.onMembersChunk)
	s.AddHandler(c.onMessageCreate)
	s.AddHandler(c.onMessageUpdate)
	s.AddHandler(c.onMessageDelete)
	s.AddHandler(c.onMessageDeleteBulk)
	s.AddHandler(c.onInteractionCreate) // exactly one InteractionCreate handler
	s.AddHandler(c.onRateLimit)
}

func (c *Client) onReady(_ *discordgo.Session, e *discordgo.Ready) {
	for _, g := range e.Guilds {
		c.resolver.applyGuild(g)
	}
}

func (c *Client) onGuildCreate(_ *discordgo.Session, e *discordgo.GuildCreate) {
	c.resolver.applyGuild(e.Guild)
}

func (c *Client) onGuildUpdate(_ *discordgo.Session, e *discordgo.GuildUpdate) {
	c.resolver.applyGuild(e.Guild)
}

func (c *Client) onGuildDelete(_ *discordgo.Session, e *discordgo.GuildDelete) {
	if e.Guild != nil && !e.Unavailable {
		c.resolver.removeGuild(e.ID)
	}
}

func (c *Client) onChannelCreate(_ *discordgo.Session, e *discordgo.ChannelCreate) {
	c.resolver.applyChannel(e.Channel)
	c.emit(EventChannelCreate, e.GuildID, e.ID)
}

func (c *Client) onChannelUpdate(_ *discordgo.Session, e *discordgo.ChannelUpdate) {
	c.resolver.applyChannel(e.Channel)
	c.emit(EventChannelUpdate, e.GuildID, e.ID)
}

func (c *Client) onChannelDelete(_ *discordgo.Session, e *discordgo.ChannelDelete) {
	c.resolver.removeChannel(e.ID)
	c.emit(EventChannelDelete, e.GuildID, e.ID)
}

func (c *Client) onThreadCreate(_ *discordgo.Session, e *discordgo.ThreadCreate) {
	c.resolver.applyChannel(e.Channel)
	c.emit(EventThreadCreate, e.GuildID, e.ID)
}

func (c *Client) onThreadUpdate(_ *discordgo.Session, e *discordgo.ThreadUpdate) {
	c.resolver.applyChannel(e.Channel)
	c.emit(EventThreadUpdate, e.GuildID, e.ID)
}

func (c *Client) onThreadDelete(_ *discordgo.Session, e *discordgo.ThreadDelete) {
	c.resolver.removeChannel(e.ID)
	c.emit(EventThreadDelete, e.GuildID, e.ID)
}

func (c *Client) onThreadListSync(_ *discordgo.Session, e *discordgo.ThreadListSync) {
	c.resolver.applyThreadListSync(e)
}

func (c *Client) onRoleCreate(_ *discordgo.Session, e *discordgo.GuildRoleCreate) {
	if e.GuildRole == nil {
		return
	}
	c.resolver.applyRole(e.GuildID, e.Role)
	c.emit(EventRoleCreate, e.GuildID, "")
}

func (c *Client) onRoleUpdate(_ *discordgo.Session, e *discordgo.GuildRoleUpdate) {
	if e.GuildRole == nil {
		return
	}
	c.resolver.applyRole(e.GuildID, e.Role)
	c.emit(EventRoleUpdate, e.GuildID, "")
}

func (c *Client) onRoleDelete(_ *discordgo.Session, e *discordgo.GuildRoleDelete) {
	c.resolver.removeRole(e.RoleID)
	c.emit(EventRoleDelete, e.GuildID, "")
}

func (c *Client) onMemberAdd(_ *discordgo.Session, e *discordgo.GuildMemberAdd) {
	if e.Member != nil {
		c.resolver.applyMember(e.GuildID, e.Member)
	}
}

func (c *Client) onMemberUpdate(_ *discordgo.Session, e *discordgo.GuildMemberUpdate) {
	if e.Member != nil {
		c.resolver.applyMember(e.GuildID, e.Member)
	}
}

func (c *Client) onMemberRemove(_ *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if e.Member != nil && e.User != nil {
		c.resolver.removeMember(e.GuildID, e.User.ID)
	}
}

func (c *Client) onMembersChunk(_ *discordgo.Session, e *discordgo.GuildMembersChunk) {
	for _, m := range e.Members {
		c.resolver.applyMember(e.GuildID, m)
	}
}

func (c *Client) onMessageCreate(_ *discordgo.Session, e *discordgo.MessageCreate) {
	if e.Message == nil {
		return
	}
	c.indexMessage(e.Message)
	c.emit(EventMessageCreate, e.GuildID, e.ChannelID)
}

func (c *Client) onMessageUpdate(_ *discordgo.Session, e *discordgo.MessageUpdate) {
	if e.Message == nil || e.Author == nil { // partial update (e.g. embed unfurl only)
		return
	}
	c.indexMessage(e.Message)
}

func (c *Client) onMessageDelete(_ *discordgo.Session, e *discordgo.MessageDelete) {
	if e.Message == nil {
		return
	}
	c.enqueue(ingestJob{del: true, channelID: e.ChannelID, ids: []string{e.ID}})
}

func (c *Client) onMessageDeleteBulk(_ *discordgo.Session, e *discordgo.MessageDeleteBulk) {
	if len(e.Messages) == 0 {
		return
	}
	c.enqueue(ingestJob{del: true, channelID: e.ChannelID, ids: e.Messages})
}

func (c *Client) onInteractionCreate(_ *discordgo.Session, e *discordgo.InteractionCreate) {
	if e == nil || e.Interaction == nil {
		return
	}
	c.broker.handle(e.Interaction)
	c.hooksMu.RLock()
	hooks := c.interactionHooks
	c.hooksMu.RUnlock()
	for _, fn := range hooks {
		fn(e)
	}
}

func (c *Client) onRateLimit(_ *discordgo.Session, e *discordgo.RateLimit) {
	if e == nil || e.TooManyRequests == nil {
		return
	}
	c.log.Warn("discord rate limit hit; retrying", "route", sanitizeRoute(urlPath(e.URL)),
		"bucket", e.Bucket, "retry_after", e.RetryAfter.String())
}

func urlPath(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if j := strings.Index(u, "/"); j >= 0 {
			u = u[j:]
		}
	}
	if i := strings.Index(u, "?"); i >= 0 {
		u = u[:i]
	}
	return u
}

// indexMessage converts and enqueues a guild message for indexing.
func (c *Client) indexMessage(m *discordgo.Message) {
	if m.GuildID == "" { // DMs are never indexed
		return
	}
	c.resolver.noteUser(m.Author)
	c.noteMessageMember(m)
	item := toMessageItem(m, c.resolver)
	c.enqueue(ingestJob{guildID: m.GuildID, parentID: c.resolver.threadParent(m.ChannelID), msg: item})
}

// enqueue never blocks the Gateway goroutine: when the buffer is full the job is
// dropped and counted.
func (c *Client) enqueue(j ingestJob) {
	if c.store == nil {
		return
	}
	select {
	case c.ingest <- j:
	default:
		n := c.droppedIngest.Add(1)
		c.log.Warn("index queue full; dropping gateway message", "dropped_total", n, "channel_id", j.channelID+j.msg.ChannelID)
	}
}

func (c *Client) ingestWorker() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stop:
			return
		case j := <-c.ingest:
			c.applyIngest(j)
		}
	}
}

func (c *Client) applyIngest(j ingestJob) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if j.del {
		for _, id := range j.ids {
			if err := c.store.MarkDeleted(ctx, j.channelID, id); err != nil {
				c.log.Warn("index: mark deleted failed", "channel_id", j.channelID, "message_id", id, "err", err)
			}
		}
		return
	}
	if err := c.store.UpsertMessage(ctx, j.guildID, j.parentID, j.msg); err != nil {
		c.log.Warn("index: upsert failed", "channel_id", j.msg.ChannelID, "message_id", j.msg.ID, "err", err)
	}
}

// emit queues a subscriber notification without blocking.
func (c *Client) emit(kind, guildID, channelID string) {
	c.hooksMu.RLock()
	n := len(c.eventHooks)
	c.hooksMu.RUnlock()
	if n == 0 {
		return
	}
	select {
	case c.events <- eventJob{kind: kind, guildID: guildID, channelID: channelID}:
	default:
		d := c.droppedEvents.Add(1)
		c.log.Warn("event queue full; dropping subscriber notification", "kind", kind, "dropped_total", d)
	}
}

func (c *Client) eventWorker() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stop:
			return
		case ev := <-c.events:
			c.hooksMu.RLock()
			hooks := c.eventHooks
			c.hooksMu.RUnlock()
			for _, fn := range hooks {
				c.safeCall(func() { fn(ev.kind, ev.guildID, ev.channelID) })
			}
		}
	}
}

func (c *Client) safeCall(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			c.log.Error("event subscriber panicked", "panic", fmt.Sprint(r))
		}
	}()
	fn()
}
