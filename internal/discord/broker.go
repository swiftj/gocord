package discord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/dto"
)

// Broker implements the HITL await flow (spec 02 §3.2). Each pending request
// owns a random custom_id prefix "dmcp:<16-hex>"; components carry
// "dmcp:<16-hex>:<value>". The client's single InteractionCreate handler calls
// handle for every interaction.

const (
	customIDPrefix   = "dmcp"
	customIDMax      = 100
	maxButtonsPerRow = 5
	maxButtons       = 25
	maxSelectOptions = 25
	maxModalInputs   = 5
	modalOpenValue   = "__open"
	modalSubmitValue = "__modal"
	selectValue      = "__select"
	promptMaxChars   = 1800
)

// brokerAPI is the slice of Discord REST the broker needs; faked in tests.
type brokerAPI interface {
	send(ctx context.Context, channelID string, m *discordgo.MessageSend) (*discordgo.Message, error)
	respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error
	edit(ctx context.Context, e *discordgo.MessageEdit) error
}

// Broker routes component and modal interactions to pending awaiters.
type Broker struct {
	api brokerAPI
	log *slog.Logger
	// unit scales TimeoutSeconds (time.Second in production; tests shrink it).
	unit time.Duration
	now  func() time.Time

	mu      sync.Mutex
	waiters map[string]*waiter
}

type outcome struct {
	result dto.InteractionResult
	label  string
}

type waiter struct {
	prefix       string
	req          dto.AwaitInteractionReq
	allowedUsers map[string]bool
	allowedRoles map[string]bool
	done         chan outcome // buffered(1); first delivery wins
	once         sync.Once
}

func newBroker(api brokerAPI, log *slog.Logger) *Broker {
	if log == nil {
		log = slog.Default()
	}
	return &Broker{api: api, log: log, unit: time.Second, now: time.Now, waiters: map[string]*waiter{}}
}

func (w *waiter) deliver(o outcome) bool {
	ok := false
	w.once.Do(func() { w.done <- o; ok = true })
	return ok
}

func (w *waiter) authorized(i *discordgo.Interaction) bool {
	u := interactionUser(i)
	if u == nil || u.Bot {
		return false
	}
	if len(w.allowedUsers) == 0 && len(w.allowedRoles) == 0 {
		return i.Member != nil // any non-bot guild member
	}
	if w.allowedUsers[u.ID] {
		return true
	}
	if i.Member != nil {
		for _, r := range i.Member.Roles {
			if w.allowedRoles[r] {
				return true
			}
		}
	}
	return false
}

func interactionUser(i *discordgo.Interaction) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

// normalizeAwaitReq applies defaults and validates limits.
func normalizeAwaitReq(req dto.AwaitInteractionReq) (dto.AwaitInteractionReq, error) {
	if req.ChannelID == "" {
		return req, fmt.Errorf("channel_id is required")
	}
	if strings.TrimSpace(req.PromptText) == "" {
		return req, fmt.Errorf("prompt_text is required")
	}
	req.Mode = strings.ToLower(strings.TrimSpace(req.Mode))
	if req.Mode == "" {
		req.Mode = dto.ModeButtons
	}
	if len(req.Options) == 0 {
		if req.Mode == dto.ModeModal {
			req.Options = []dto.InteractionOption{{Label: "Response", Value: "response"}}
		} else {
			req.Options = []dto.InteractionOption{
				{Label: "Approve", Value: "approve", Style: "success"},
				{Label: "Reject", Value: "reject", Style: "danger"},
			}
		}
	}
	if req.TimeoutSeconds <= 0 {
		req.TimeoutSeconds = dto.DefaultInteractionTimeout
	}
	req.TimeoutSeconds = min(req.TimeoutSeconds, dto.MaxInteractionTimeout)

	seen := map[string]bool{}
	for i := range req.Options {
		o := &req.Options[i]
		o.Label = strings.TrimSpace(o.Label)
		o.Value = strings.TrimSpace(o.Value)
		if o.Value == "" {
			o.Value = strings.ToLower(strings.ReplaceAll(o.Label, " ", "_"))
		}
		if o.Label == "" {
			o.Label = o.Value
		}
		if o.Value == "" {
			return req, fmt.Errorf("option %d needs a label or value", i+1)
		}
		if seen[o.Value] {
			return req, fmt.Errorf("duplicate option value %q", o.Value)
		}
		seen[o.Value] = true
		// "dmcp:" + 16 hex + ":" + value must fit in 100 chars.
		if len(o.Value) > customIDMax-len(customIDPrefix)-18 {
			return req, fmt.Errorf("option value %q is too long (max %d chars)", o.Value, customIDMax-len(customIDPrefix)-18)
		}
	}
	switch req.Mode {
	case dto.ModeButtons:
		if len(req.Options) > maxButtons {
			return req, fmt.Errorf("at most %d buttons allowed, got %d", maxButtons, len(req.Options))
		}
	case dto.ModeSelect:
		if len(req.Options) > maxSelectOptions {
			return req, fmt.Errorf("at most %d select options allowed, got %d", maxSelectOptions, len(req.Options))
		}
		if req.MinValues <= 0 {
			req.MinValues = 1
		}
		if req.MaxValues <= 0 {
			req.MaxValues = req.MinValues
		}
		req.MaxValues = min(req.MaxValues, len(req.Options))
		req.MinValues = min(req.MinValues, req.MaxValues)
	case dto.ModeModal:
		if len(req.Options) > maxModalInputs {
			return req, fmt.Errorf("at most %d modal inputs allowed, got %d", maxModalInputs, len(req.Options))
		}
	default:
		return req, fmt.Errorf("invalid mode %q (want buttons, select, or modal)", req.Mode)
	}
	return req, nil
}

func buttonStyle(s string) discordgo.ButtonStyle {
	switch strings.ToLower(s) {
	case "secondary":
		return discordgo.SecondaryButton
	case "success":
		return discordgo.SuccessButton
	case "danger":
		return discordgo.DangerButton
	}
	return discordgo.PrimaryButton
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// buildComponents renders the message components for req.
func buildComponents(prefix string, req dto.AwaitInteractionReq, disabled bool) []discordgo.MessageComponent {
	switch req.Mode {
	case dto.ModeSelect:
		opts := make([]discordgo.SelectMenuOption, 0, len(req.Options))
		for _, o := range req.Options {
			opts = append(opts, discordgo.SelectMenuOption{Label: clip(o.Label, 100), Value: o.Value, Emoji: toComponentEmoji(o.Emoji)})
		}
		minV := req.MinValues
		return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{
				MenuType: discordgo.StringSelectMenu, CustomID: prefix + ":" + selectValue, Placeholder: "Choose…",
				MinValues: &minV, MaxValues: req.MaxValues, Options: opts, Disabled: disabled,
			},
		}}}
	case dto.ModeModal:
		return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Respond", Style: discordgo.PrimaryButton, CustomID: prefix + ":" + modalOpenValue, Disabled: disabled},
		}}}
	}
	var rows []discordgo.MessageComponent
	var row []discordgo.MessageComponent
	for _, o := range req.Options {
		row = append(row, discordgo.Button{
			Label: clip(o.Label, 80), Style: buttonStyle(o.Style), CustomID: prefix + ":" + o.Value,
			Emoji: toComponentEmoji(o.Emoji), Disabled: disabled,
		})
		if len(row) == maxButtonsPerRow {
			rows = append(rows, discordgo.ActionsRow{Components: row})
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: row})
	}
	return rows
}

func buildModal(prefix string, req dto.AwaitInteractionReq) *discordgo.InteractionResponse {
	rows := make([]discordgo.MessageComponent, 0, len(req.Options))
	for _, o := range req.Options {
		style := discordgo.TextInputShort
		if s := strings.ToLower(o.Style); s == "paragraph" || s == "long" {
			style = discordgo.TextInputParagraph
		}
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.TextInput{CustomID: o.Value, Label: clip(o.Label, 45), Style: style, Required: true, MaxLength: 4000},
		}})
	}
	return &discordgo.InteractionResponse{Type: discordgo.InteractionResponseModal, Data: &discordgo.InteractionResponseData{
		CustomID: prefix + ":" + modalSubmitValue, Title: clip(req.PromptText, 45), Components: rows,
	}}
}

func newPrefix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return customIDPrefix + ":" + hex.EncodeToString(b[:]), nil
}

// Await posts the prompt and blocks until an authorised response, timeout, or
// ctx cancellation. Timeout returns TimedOut=true and a nil error.
func (b *Broker) Await(ctx context.Context, req dto.AwaitInteractionReq) (*dto.InteractionResult, error) {
	req, err := normalizeAwaitReq(req)
	if err != nil {
		return nil, err
	}
	prefix, err := newPrefix()
	if err != nil {
		return nil, fmt.Errorf("await interaction: %w", err)
	}
	w := &waiter{prefix: prefix, req: req, allowedUsers: map[string]bool{}, allowedRoles: map[string]bool{}, done: make(chan outcome, 1)}
	for _, id := range req.AllowedUserIDs {
		w.allowedUsers[id] = true
	}
	for _, id := range req.AllowedRoleIDs {
		w.allowedRoles[id] = true
	}
	// Register before sending so an instant click cannot be lost.
	b.mu.Lock()
	b.waiters[prefix] = w
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.waiters, prefix)
		b.mu.Unlock()
	}()

	prompt := clip(req.PromptText, promptMaxChars)
	msg, err := b.api.send(ctx, req.ChannelID, &discordgo.MessageSend{
		Content: prompt, Components: buildComponents(prefix, req, false),
		AllowedMentions: &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeUsers, discordgo.AllowedMentionTypeRoles}},
	})
	if err != nil {
		return nil, err
	}

	timer := time.NewTimer(time.Duration(req.TimeoutSeconds) * b.unit)
	defer timer.Stop()
	select {
	case o := <-w.done:
		b.finish(msg, prefix, req, prompt, fmt.Sprintf("✅ %s by @%s", o.label, o.result.Username))
		r := o.result
		r.MessageID = msg.ID
		return &r, nil
	case <-timer.C:
		w.deliver(outcome{}) // reject late clicks
		b.finish(msg, prefix, req, prompt, "⌛ timed out")
		return &dto.InteractionResult{MessageID: msg.ID, TimedOut: true}, nil
	case <-ctx.Done():
		w.deliver(outcome{})
		b.finish(msg, prefix, req, prompt, "🚫 cancelled")
		return nil, ctx.Err()
	}
}

// finish disables the components and appends the outcome. It uses a fresh
// context so it still runs after the caller's ctx is cancelled.
func (b *Broker) finish(msg *discordgo.Message, prefix string, req dto.AwaitInteractionReq, prompt, line string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	content := prompt + "\n\n" + line
	comps := buildComponents(prefix, req, true)
	e := &discordgo.MessageEdit{ID: msg.ID, Channel: msg.ChannelID, Content: &content, Components: &comps}
	if err := b.api.edit(ctx, e); err != nil {
		b.log.Warn("hitl: failed to finalise prompt message", "message_id", msg.ID, "err", err)
	}
}

func ephemeral(text string) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: text, Flags: discordgo.MessageFlagsEphemeral}}
}

// handle processes one interaction. Interactions without a dmcp custom_id are
// ignored (left for other listeners).
func (b *Broker) handle(i *discordgo.Interaction) {
	var customID string
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		customID = i.MessageComponentData().CustomID
	case discordgo.InteractionModalSubmit:
		customID = i.ModalSubmitData().CustomID
	default:
		return
	}
	parts := strings.SplitN(customID, ":", 3)
	if len(parts) != 3 || parts[0] != customIDPrefix {
		return
	}
	prefix, value := parts[0]+":"+parts[1], parts[2]

	b.mu.Lock()
	w := b.waiters[prefix]
	b.mu.Unlock()
	if w == nil {
		b.reply(i, ephemeral("This request is no longer active."))
		return
	}
	if !w.authorized(i) {
		b.reply(i, ephemeral("You are not authorised to respond to this request."))
		return
	}
	u := interactionUser(i)
	res := dto.InteractionResult{UserID: u.ID, Username: u.Username, RespondedAt: dto.FormatTime(b.now())}
	label := ""

	switch {
	case i.Type == discordgo.InteractionMessageComponent && value == modalOpenValue && w.req.Mode == dto.ModeModal:
		b.reply(i, buildModal(prefix, w.req)) // opening the modal is the ACK
		return
	case i.Type == discordgo.InteractionModalSubmit:
		res.ModalFields = modalValues(i.ModalSubmitData().Components)
		label = "response submitted"
	case value == selectValue:
		res.Selected = slices.Clone(i.MessageComponentData().Values)
		var labels []string
		for _, v := range res.Selected {
			labels = append(labels, w.labelFor(v))
		}
		label = strings.Join(labels, ", ")
	default:
		if !w.hasValue(value) {
			b.reply(i, ephemeral("Unknown option."))
			return
		}
		res.Selected = []string{value}
		label = w.labelFor(value)
	}
	// ACK within 3 s; the awaiting goroutine then edits the message.
	b.reply(i, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate})
	if !w.deliver(outcome{result: res, label: label}) {
		b.log.Debug("hitl: late response ignored", "prefix", prefix)
	}
}

func (b *Broker) reply(i *discordgo.Interaction, r *discordgo.InteractionResponse) {
	if err := b.api.respond(i, r); err != nil {
		b.log.Warn("hitl: interaction response failed", "interaction_id", i.ID, "err", err)
	}
}

func (w *waiter) labelFor(v string) string {
	for _, o := range w.req.Options {
		if o.Value == v {
			return o.Label
		}
	}
	return v
}

func (w *waiter) hasValue(v string) bool {
	return slices.ContainsFunc(w.req.Options, func(o dto.InteractionOption) bool { return o.Value == v })
}

func modalValues(comps []discordgo.MessageComponent) map[string]string {
	out := map[string]string{}
	var walk func([]discordgo.MessageComponent)
	walk = func(cs []discordgo.MessageComponent) {
		for _, c := range cs {
			switch t := c.(type) {
			case *discordgo.ActionsRow:
				walk(t.Components)
			case discordgo.ActionsRow:
				walk(t.Components)
			case *discordgo.TextInput:
				out[t.CustomID] = t.Value
			case discordgo.TextInput:
				out[t.CustomID] = t.Value
			}
		}
	}
	walk(comps)
	return out
}

// sessionBrokerAPI adapts the live session to brokerAPI.
type sessionBrokerAPI struct{ c *Client }

func (a *sessionBrokerAPI) send(ctx context.Context, channelID string, m *discordgo.MessageSend) (*discordgo.Message, error) {
	s, err := a.c.sess()
	if err != nil {
		return nil, err
	}
	msg, err := s.ChannelMessageSendComplex(channelID, m, discordgo.WithContext(ctx))
	return msg, mapErr("send interaction prompt", err)
}

func (a *sessionBrokerAPI) respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	s, err := a.c.sess()
	if err != nil {
		return err
	}
	return mapErr("interaction respond", s.InteractionRespond(i, r))
}

func (a *sessionBrokerAPI) edit(ctx context.Context, e *discordgo.MessageEdit) error {
	s, err := a.c.sess()
	if err != nil {
		return err
	}
	_, err = s.ChannelMessageEditComplex(e, discordgo.WithContext(ctx))
	return mapErr("edit interaction prompt", err)
}
