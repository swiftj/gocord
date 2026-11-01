package tools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

const (
	webhookCreateTool    = "discord_create_webhook"
	webhookSendTool      = "discord_send_webhook_message"
	webhookEditTool      = "discord_edit_webhook"
	webhookDeleteTool    = "discord_delete_webhook"
	personaRegisterTool  = "discord_persona_register"
	personaSendTool      = "discord_persona_send"
	webhookMaxContent    = 2000
	webhookMaxNameChars  = 80
	personaMaxDescChars  = 500
	personaWebhookName   = "MCP Personas" // Discord rejects webhook names containing "discord" or "clyde".
	webhookTokenRedacted = ""             // persona webhook tokens are resolved by the live client, never stored in clear here
)

var (
	personaNameRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
	webhookURLRe  = regexp.MustCompile(`^https://(?:(?:ptb|canary)\.)?(?:discord|discordapp)\.com/api/(?:v\d+/)?webhooks/(\d{15,20})/([A-Za-z0-9_-]{20,})$`)
)

// RegisterWebhookPersonaTools registers webhook CRUD and the persona registry/sender.
func RegisterWebhookPersonaTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(mcp.NewTool(webhookCreateTool,
		mcp.WithDescription("Create a webhook in a channel. The webhook token is never returned."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel Snowflake, #name, or mention.")),
		mcp.WithString("name", mcp.Required(), mcp.Description("Webhook name (1-80 chars; may not contain \"discord\" or \"clyde\")."), mcp.MinLength(1), mcp.MaxLength(webhookMaxNameChars)),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return webhookCreate(ctx, d, req), nil
	})

	s.AddTool(mcp.NewTool(webhookSendTool,
		mcp.WithDescription("Send a message through a webhook given by webhook_id or webhook_url (one is required). Content must be <= 2000 chars."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("webhook_id", mcp.Description("Webhook Snowflake (token resolved server-side).")),
		mcp.WithString("webhook_url", mcp.Description("Full https://discord.com/api/webhooks/{id}/{token} URL. Never echoed back.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Message text (<= 2000 chars)."), mcp.MaxLength(webhookMaxContent)),
		mcp.WithString("username", mcp.Description("Override display name (<= 80 chars).")),
		mcp.WithString("avatar_url", mcp.Description("Override avatar (https URL).")),
		mcp.WithString("thread_id", mcp.Description("Post into this thread of the webhook's channel.")),
		mcp.WithObject("embed", mcp.Description("Optional embed: {title, description, url, color, fields:[{name,value,inline}], footer, image_url}.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return webhookSend(ctx, d, req), nil
	})

	s.AddTool(mcp.NewTool(webhookEditTool,
		mcp.WithDescription("Rename a webhook or move it to another channel. Supports dry_run previews."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook Snowflake.")),
		mcp.WithString("name", mcp.Description("New name (1-80 chars)."), mcp.MaxLength(webhookMaxNameChars)),
		mcp.WithString("channel_id", mcp.Description("Move the webhook to this channel (Snowflake, #name, or mention).")),
		mcp.WithBoolean("dry_run", mcp.Description("Preview without changing anything."), mcp.DefaultBool(false)),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return webhookEdit(ctx, d, req), nil
	})

	s.AddTool(mcp.NewTool(webhookDeleteTool,
		mcp.WithDescription("Delete a webhook (and forget it if it backs a persona). Supports dry_run previews."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithString("webhook_id", mcp.Required(), mcp.Description("Webhook Snowflake.")),
		mcp.WithString("reason", mcp.Description("Audit log reason.")),
		mcp.WithBoolean("dry_run", mcp.Description("Preview without changing anything."), mcp.DefaultBool(false)),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return webhookDelete(ctx, d, req), nil
	})

	s.AddTool(mcp.NewTool(personaRegisterTool,
		mcp.WithDescription("Register or update a reusable persona (name, avatar) for discord_persona_send. Stored locally; no Discord call."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
		mcp.WithString("persona_name", mcp.Required(), mcp.Description("Key, lowercase [a-z0-9-]{1,32}."), mcp.Pattern(`^[a-z0-9-]{1,32}$`)),
		mcp.WithString("display_name", mcp.Required(), mcp.Description("Name shown on posts (1-80 chars)."), mcp.MinLength(1), mcp.MaxLength(webhookMaxNameChars)),
		mcp.WithString("avatar_url", mcp.Description("Avatar image (https URL).")),
		mcp.WithString("description", mcp.Description("What this persona is for (<= 500 chars)."), mcp.MaxLength(personaMaxDescChars)),
		mcp.WithString("default_channel_id", mcp.Description("Channel used when discord_persona_send omits channel_id.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return personaRegister(ctx, d, req), nil
	})

	s.AddTool(mcp.NewTool(personaSendTool,
		mcp.WithDescription("Post as a registered persona via an auto-provisioned channel webhook. Threads are supported via thread_id or a thread channel_id."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("persona_name", mcp.Required(), mcp.Description("Registered persona key.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Message text (<= 2000 chars)."), mcp.MaxLength(webhookMaxContent)),
		mcp.WithString("channel_id", mcp.Description("Target channel or thread; defaults to the persona's default channel.")),
		mcp.WithString("thread_id", mcp.Description("Reply inside this thread.")),
		mcp.WithObject("embed", mcp.Description("Optional embed: {title, description, url, color, fields:[{name,value,inline}], footer, image_url}.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return personaSend(ctx, d, req), nil
	})
}

// ---------- shared validation ----------

// webhookScope returns the guild used to resolve channel names (the configured
// default guild, or "" to search every guild).
func webhookScope(d deps.Deps) string {
	if d.Config != nil {
		return d.Config.DefaultGuildID
	}
	return ""
}

// webhookCheckName validates a webhook name or username override.
func webhookCheckName(field, name string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(name))
	if n < 1 || n > webhookMaxNameChars {
		return fmt.Errorf("%s must be 1-%d characters", field, webhookMaxNameChars)
	}
	low := strings.ToLower(name)
	for _, banned := range []string{"discord", "clyde"} {
		if strings.Contains(low, banned) {
			return fmt.Errorf("%s may not contain %q (Discord rejects it)", field, banned)
		}
	}
	return nil
}

// webhookCheckHTTPS validates an optional https URL.
func webhookCheckHTTPS(field, raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s must be an https:// URL", field)
	}
	return nil
}

// webhookCheckContent validates message content.
func webhookCheckContent(content string) error {
	if strings.TrimSpace(content) == "" {
		return errors.New("content is required")
	}
	if n := utf8.RuneCountInString(content); n > webhookMaxContent {
		return fmt.Errorf("content is %d characters; Discord allows at most %d", n, webhookMaxContent)
	}
	return nil
}

// webhookEmbed decodes the optional embed argument.
func webhookEmbed(req mcp.CallToolRequest) (*dto.EmbedInput, error) {
	var e dto.EmbedInput
	ok, err := objectArg(req, "embed", &e)
	if err != nil || !ok {
		return nil, err
	}
	return &e, nil
}

// webhookIDArg validates the webhook_id argument.
func webhookIDArg(req mcp.CallToolRequest) (string, error) {
	id := strings.TrimSpace(req.GetString("webhook_id", ""))
	if !dto.IsSnowflake(id) {
		return "", fmt.Errorf("webhook_id must be a Snowflake, got %q", id)
	}
	return id, nil
}

// webhookResolveThread resolves a thread reference and checks its type.
func webhookResolveThread(ctx context.Context, d deps.Deps, ref string) (*dto.ChannelInfo, error) {
	th, err := resolveChannel(ctx, d, webhookScope(d), ref)
	if err != nil {
		return nil, err
	}
	if th.Type != dto.ChannelThread {
		return nil, fmt.Errorf("thread_id %s is a %s channel, not a thread", th.ID, th.Type)
	}
	return th, nil
}

// webhookGuildScope builds the policy op for a webhook whose channel is unknown
// (Service has no GetWebhook): the target channel when given, else the
// default/only guild.
func webhookGuildScope(ctx context.Context, d deps.Deps, ch *dto.ChannelInfo) (string, error) {
	if ch != nil {
		return ch.GuildID, nil
	}
	return resolveGuildRef(ctx, d, "")
}

// ---------- webhook CRUD ----------

func webhookCreate(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	name := req.GetString("name", "")
	if err := webhookCheckName("name", name); err != nil {
		return errorResult(err)
	}
	ch, err := resolveChannel(ctx, d, webhookScope(d), req.GetString("channel_id", ""))
	if err != nil {
		return errorResult(err)
	}
	switch ch.Type {
	case dto.ChannelCategory, dto.ChannelThread:
		return errorf("cannot create a webhook in %s channel %s; use its parent text/forum channel", ch.Type, ch.ID)
	}
	if err := d.Guard.Check(channelOp(webhookCreateTool, ch, opWrite, false)); err != nil {
		return errorResult(err)
	}
	w, err := d.Discord.CreateWebhook(ctx, ch.ID, strings.TrimSpace(name))
	if err != nil {
		return errorResult(err)
	}
	return jsonResult(w)
}

func webhookSend(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	content := req.GetString("content", "")
	if err := webhookCheckContent(content); err != nil {
		return errorResult(err)
	}
	wreq := dto.WebhookMessageReq{Content: content, Username: req.GetString("username", ""), AvatarURL: req.GetString("avatar_url", "")}
	id := strings.TrimSpace(req.GetString("webhook_id", ""))
	rawURL := strings.TrimSpace(req.GetString("webhook_url", ""))
	switch {
	case id != "" && rawURL != "":
		return errorf("pass either webhook_id or webhook_url, not both")
	case id != "":
		if !dto.IsSnowflake(id) {
			return errorf("webhook_id must be a Snowflake")
		}
		wreq.WebhookID = id
	case rawURL != "":
		if !webhookURLRe.MatchString(rawURL) {
			// Never echo the URL: it contains the token.
			return errorf("webhook_url must look like https://discord.com/api/webhooks/{id}/{token}")
		}
		wreq.WebhookURL = rawURL
	default:
		return errorf("one of webhook_id or webhook_url is required")
	}
	if wreq.Username != "" {
		if err := webhookCheckName("username", wreq.Username); err != nil {
			return errorResult(err)
		}
	}
	if err := webhookCheckHTTPS("avatar_url", wreq.AvatarURL); err != nil {
		return errorResult(err)
	}
	embed, err := webhookEmbed(req)
	if err != nil {
		return errorResult(err)
	}
	wreq.Embed = embed

	op := guildOp(webhookSendTool, "", opWrite, false)
	if ref := req.GetString("thread_id", ""); strings.TrimSpace(ref) != "" {
		th, err := webhookResolveThread(ctx, d, ref)
		if err != nil {
			return errorResult(err)
		}
		wreq.ThreadID = th.ID
		op = channelOp(webhookSendTool, th, opWrite, false)
	}
	if err := d.Guard.Check(op); err != nil {
		return errorResult(err)
	}
	msg, err := d.Discord.SendWebhookMessage(ctx, wreq)
	if err != nil {
		return errorResult(webhookRedact(err, rawURL))
	}
	return jsonResult(msg)
}

// webhookRedact strips the webhook token from an error message.
func webhookRedact(err error, rawURL string) error {
	m := webhookURLRe.FindStringSubmatch(rawURL)
	if m == nil || !strings.Contains(err.Error(), m[2]) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), m[2], "***"))
}

func webhookEdit(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	id, err := webhookIDArg(req)
	if err != nil {
		return errorResult(err)
	}
	name := optString(req, "name")
	chRef := strings.TrimSpace(req.GetString("channel_id", ""))
	if (name == nil || *name == "") && chRef == "" {
		return errorf("nothing to change: pass name and/or channel_id")
	}
	var changes []string
	if name != nil && *name != "" {
		if err := webhookCheckName("name", *name); err != nil {
			return errorResult(err)
		}
		changes = append(changes, fmt.Sprintf("name → %q", strings.TrimSpace(*name)))
	}
	var ch *dto.ChannelInfo
	if chRef != "" {
		if ch, err = resolveChannel(ctx, d, webhookScope(d), chRef); err != nil {
			return errorResult(err)
		}
		if ch.Type == dto.ChannelCategory || ch.Type == dto.ChannelThread {
			return errorf("cannot move a webhook to %s channel %s", ch.Type, ch.ID)
		}
		changes = append(changes, fmt.Sprintf("channel → #%s (%s)", ch.Name, ch.ID))
	}
	guildID, err := webhookGuildScope(ctx, d, ch)
	if err != nil {
		return errorResult(err)
	}
	op := channelOp(webhookEditTool, ch, opDestructive, dryRun(req))
	op.GuildID = guildID
	if dryRun(req) {
		return dryRunResult(d, op, "webhook "+id, changes)
	}
	if err := d.Guard.Check(op); err != nil {
		return errorResult(err)
	}
	newName, newChannel := "", ""
	if name != nil {
		newName = strings.TrimSpace(*name)
	}
	if ch != nil {
		newChannel = ch.ID
	}
	w, err := d.Discord.EditWebhook(ctx, id, newName, newChannel)
	if err != nil {
		return errorResult(err)
	}
	if newChannel != "" {
		// A moved persona webhook no longer serves its old channel.
		if err := d.Store.DeletePersonaWebhook(ctx, id); err != nil {
			d.Log().Warn("forget moved persona webhook", "webhook_id", id, "error", err.Error())
		}
	}
	return jsonResult(w)
}

func webhookDelete(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	id, err := webhookIDArg(req)
	if err != nil {
		return errorResult(err)
	}
	guildID, err := webhookGuildScope(ctx, d, nil)
	if err != nil {
		return errorResult(err)
	}
	op := guildOp(webhookDeleteTool, guildID, opDestructive, dryRun(req))
	if dryRun(req) {
		return dryRunResult(d, op, "webhook "+id, []string{"delete webhook " + id + " (and its persona mapping, if any)"},
			"messages already sent by this webhook remain; persona sends will provision a new webhook")
	}
	if err := d.Guard.Check(op); err != nil {
		return errorResult(err)
	}
	if err := d.Discord.DeleteWebhook(ctx, id, req.GetString("reason", "")); err != nil {
		if !errors.Is(err, discord.ErrNotFound) {
			return errorResult(err)
		}
		_ = d.Store.DeletePersonaWebhook(ctx, id)
		return errorResult(err)
	}
	if err := d.Store.DeletePersonaWebhook(ctx, id); err != nil {
		return errorf("webhook deleted, but forgetting its persona mapping failed: %v", err)
	}
	return jsonResult(struct {
		Deleted   bool   `json:"deleted"`
		WebhookID string `json:"webhook_id"`
	}{true, id})
}

// ---------- personas ----------

func personaRegister(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	name := strings.TrimSpace(req.GetString("persona_name", ""))
	if !personaNameRe.MatchString(name) {
		return errorf("persona_name must match [a-z0-9-]{1,32}, got %q", name)
	}
	p := dto.Persona{
		Name:        name,
		DisplayName: strings.TrimSpace(req.GetString("display_name", "")),
		AvatarURL:   strings.TrimSpace(req.GetString("avatar_url", "")),
		Description: strings.TrimSpace(req.GetString("description", "")),
	}
	if err := webhookCheckName("display_name", p.DisplayName); err != nil {
		return errorResult(err)
	}
	if err := webhookCheckHTTPS("avatar_url", p.AvatarURL); err != nil {
		return errorResult(err)
	}
	if utf8.RuneCountInString(p.Description) > personaMaxDescChars {
		return errorf("description must be <= %d characters", personaMaxDescChars)
	}
	op := guildOp(personaRegisterTool, "", opWrite, false)
	if ref := strings.TrimSpace(req.GetString("default_channel_id", "")); ref != "" {
		// Snowflakes and mentions are stored without a Discord call (offline-safe).
		id := discord.ParseRef(ref).ID
		if id == "" {
			var err error
			if id, err = d.Discord.ResolveChannelID(ctx, webhookScope(d), ref); err != nil {
				return errorResult(err)
			}
		}
		p.DefaultChannelID = id
		op.ChannelID = id
	}
	if err := d.Guard.Check(op); err != nil {
		return errorResult(err)
	}
	_, err := d.Store.GetPersona(ctx, name)
	created := errors.Is(err, index.ErrNotFound)
	if err != nil && !created {
		return errorResult(err)
	}
	if err := d.Store.UpsertPersona(ctx, p); err != nil {
		return errorResult(err)
	}
	saved, err := d.Store.GetPersona(ctx, name)
	if err != nil {
		return errorResult(err)
	}
	return jsonResult(struct {
		Created bool         `json:"created"`
		Persona *dto.Persona `json:"persona"`
	}{created, saved})
}

// personaUnknown builds the "unknown persona" error listing registered ones.
func personaUnknown(ctx context.Context, d deps.Deps, name string) *mcp.CallToolResult {
	ps, err := d.Store.ListPersonas(ctx)
	if err != nil || len(ps) == 0 {
		return errorf("unknown persona %q: no personas are registered; call discord_persona_register first", name)
	}
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = p.Name
	}
	return errorf("unknown persona %q; registered personas: %s", name, strings.Join(names, ", "))
}

// personaWebhookFor returns the persona webhook for a (non-thread) channel,
// creating and storing one when missing. created reports a fresh webhook.
func personaWebhookFor(ctx context.Context, d deps.Deps, channelID string) (id string, created bool, err error) {
	id, _, err = d.Store.GetPersonaWebhook(ctx, channelID)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, index.ErrNotFound) {
		return "", false, err
	}
	w, err := d.Discord.CreateWebhook(ctx, channelID, personaWebhookName)
	if err != nil {
		return "", false, err
	}
	// The token is never exposed through MCP; the live client resolves it by webhook ID.
	if err := d.Store.PutPersonaWebhook(ctx, channelID, w.ID, webhookTokenRedacted); err != nil {
		return "", false, err
	}
	return w.ID, true, nil
}

type personaSendResult struct {
	Persona        string           `json:"persona"`
	ChannelID      string           `json:"channel_id"`
	ThreadID       string           `json:"thread_id,omitempty"`
	WebhookID      string           `json:"webhook_id"`
	WebhookCreated bool             `json:"webhook_created,omitempty"`
	Message        *dto.MessageItem `json:"message"`
}

func personaSend(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) *mcp.CallToolResult {
	name := strings.TrimSpace(req.GetString("persona_name", ""))
	content := req.GetString("content", "")
	if err := webhookCheckContent(content); err != nil {
		return errorResult(err)
	}
	embed, err := webhookEmbed(req)
	if err != nil {
		return errorResult(err)
	}
	p, err := d.Store.GetPersona(ctx, name)
	if errors.Is(err, index.ErrNotFound) {
		return personaUnknown(ctx, d, name)
	}
	if err != nil {
		return errorResult(err)
	}

	// Determine the target: explicit thread, else channel (which may itself be a thread).
	var target *dto.ChannelInfo
	if ref := strings.TrimSpace(req.GetString("thread_id", "")); ref != "" {
		if target, err = webhookResolveThread(ctx, d, ref); err != nil {
			return errorResult(err)
		}
	} else {
		ref := strings.TrimSpace(req.GetString("channel_id", ""))
		if ref == "" {
			ref = p.DefaultChannelID
		}
		if ref == "" {
			return errorf("channel_id is required: persona %q has no default_channel_id", p.Name)
		}
		if target, err = resolveChannel(ctx, d, webhookScope(d), ref); err != nil {
			return errorResult(err)
		}
	}
	parentID, threadID := target.ID, ""
	switch target.Type {
	case dto.ChannelThread:
		parentID, threadID = target.ParentID, target.ID
		if parentID == "" {
			return errorf("thread %s has no known parent channel", target.ID)
		}
	case dto.ChannelCategory:
		return errorf("%s is a category; pick a text channel or thread", target.ID)
	case dto.ChannelForum:
		return errorf("%s is a forum; pass thread_id of a forum post", target.ID)
	}
	if err := d.Guard.Check(channelOp(personaSendTool, target, opWrite, false)); err != nil {
		return errorResult(err)
	}

	whID, created, err := personaWebhookFor(ctx, d, parentID)
	if err != nil {
		return errorResult(err)
	}
	wreq := dto.WebhookMessageReq{WebhookID: whID, Content: content, Username: p.DisplayName, AvatarURL: p.AvatarURL, ThreadID: threadID, Embed: embed}
	msg, err := d.Discord.SendWebhookMessage(ctx, wreq)
	if err != nil && errors.Is(err, discord.ErrNotFound) && !created {
		// Unknown Webhook: it was deleted out-of-band. Forget it, recreate once, retry.
		d.Log().Info("persona webhook missing; recreating", "channel_id", parentID, "webhook_id", whID)
		if derr := d.Store.DeletePersonaWebhook(ctx, whID); derr != nil {
			return errorResult(derr)
		}
		if whID, created, err = personaWebhookFor(ctx, d, parentID); err != nil {
			return errorResult(err)
		}
		wreq.WebhookID = whID
		msg, err = d.Discord.SendWebhookMessage(ctx, wreq)
	}
	if err != nil {
		return errorResult(err)
	}
	return jsonResult(personaSendResult{Persona: p.Name, ChannelID: parentID, ThreadID: threadID, WebhookID: whID, WebhookCreated: created, Message: msg})
}
