package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

const (
	interactionTool           = "discord_await_interaction"
	interactionMinTimeout     = 10
	interactionMaxOptions     = 25 // buttons (5 rows x 5) and select options
	interactionMaxModalFields = 5
	interactionMaxPrompt      = 2000
	interactionMaxValueChars  = 78 // custom_id "dmcp:<16hex>:<value>" must fit in 100 chars
)

var interactionStyles = []string{"primary", "secondary", "success", "danger"}

// RegisterInteractionTools registers discord_await_interaction.
func RegisterInteractionTools(s *server.MCPServer, d deps.Deps) {
	s.AddTool(mcp.NewTool(interactionTool,
		mcp.WithDescription("Post buttons, a select menu, or a modal form and block until an authorised user responds or timeout_seconds expires. A timeout is a normal result (timed_out: true)."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("channel_id", mcp.Required(), mcp.Description("Channel or thread (Snowflake, #name, or mention).")),
		mcp.WithString("prompt_text", mcp.Required(), mcp.Description("Question shown above the components (<= 2000 chars)."), mcp.MaxLength(interactionMaxPrompt)),
		mcp.WithString("mode", mcp.Description("Component type."), mcp.Enum(dto.ModeButtons, dto.ModeSelect, dto.ModeModal), mcp.DefaultString(dto.ModeButtons)),
		mcp.WithArray("options", mcp.Description("Choices (buttons/select, <= 25) or form fields (modal, <= 5). Default: Approve (success) / Reject (danger)."),
			mcp.MaxItems(interactionMaxOptions),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"label": map[string]any{"type": "string", "description": "Visible text (modal: input label)."},
					"value": map[string]any{"type": "string", "description": "Returned value (modal: field key). Defaults to a slug of label."},
					"style": map[string]any{"type": "string", "enum": interactionStyles},
					"emoji": map[string]any{"type": "string"},
				},
				"required": []string{"label"},
			})),
		mcp.WithArray("allowed_user_ids", mcp.Description("Users who may respond (Snowflake, @name, or mention)."), mcp.WithStringItems()),
		mcp.WithArray("allowed_role_ids", mcp.Description("Roles whose members may respond. If both lists are empty, any non-bot member may respond."), mcp.WithStringItems()),
		mcp.WithNumber("timeout_seconds", mcp.Description("How long to wait (10-900)."), mcp.Min(interactionMinTimeout), mcp.Max(dto.MaxInteractionTimeout), mcp.DefaultNumber(dto.DefaultInteractionTimeout)),
		mcp.WithNumber("min_values", mcp.Description("Select mode: minimum selections (default 1)."), mcp.Min(0), mcp.Max(interactionMaxOptions)),
		mcp.WithNumber("max_values", mcp.Description("Select mode: maximum selections (default 1)."), mcp.Min(1), mcp.Max(interactionMaxOptions)),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return interactionHandle(ctx, d, req)
	})
}

func interactionHandle(ctx context.Context, d deps.Deps, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	prompt := strings.TrimSpace(req.GetString("prompt_text", ""))
	if prompt == "" {
		return errorf("prompt_text is required"), nil
	}
	if n := utf8.RuneCountInString(prompt); n > interactionMaxPrompt {
		return errorf("prompt_text is %d characters; at most %d allowed", n, interactionMaxPrompt), nil
	}
	mode := strings.ToLower(strings.TrimSpace(req.GetString("mode", dto.ModeButtons)))
	if mode == "" {
		mode = dto.ModeButtons
	}
	if mode != dto.ModeButtons && mode != dto.ModeSelect && mode != dto.ModeModal {
		return errorf("mode must be one of buttons, select, modal; got %q", mode), nil
	}

	var opts []dto.InteractionOption
	if _, err := objectArg(req, "options", &opts); err != nil {
		return errorf("%v (want an array of {label, value, style, emoji})", err), nil
	}
	opts, err := interactionNormalizeOptions(mode, opts)
	if err != nil {
		return errorResult(err), nil
	}

	timeout := dto.DefaultInteractionTimeout
	if hasArg(req, "timeout_seconds") {
		timeout = req.GetInt("timeout_seconds", dto.DefaultInteractionTimeout)
		if timeout < interactionMinTimeout || timeout > dto.MaxInteractionTimeout {
			return errorf("timeout_seconds must be between %d and %d, got %d", interactionMinTimeout, dto.MaxInteractionTimeout, timeout), nil
		}
	}
	minV, maxV, err := interactionSelectBounds(req, mode, len(opts))
	if err != nil {
		return errorResult(err), nil
	}

	ch, err := resolveChannel(ctx, d, webhookScope(d), req.GetString("channel_id", ""))
	if err != nil {
		return errorResult(err), nil
	}
	switch ch.Type {
	case dto.ChannelCategory, dto.ChannelForum:
		return errorf("cannot post components in %s channel %s; use a text channel or thread", ch.Type, ch.ID), nil
	}

	var users, roles []string
	for _, ref := range req.GetStringSlice("allowed_user_ids", nil) {
		id, err := resolveUser(ctx, d, ch.GuildID, ref)
		if err != nil {
			return errorResult(fmt.Errorf("allowed_user_ids: %w", err)), nil
		}
		users = append(users, id)
	}
	for _, ref := range req.GetStringSlice("allowed_role_ids", nil) {
		id, err := resolveRole(ctx, d, ch.GuildID, ref)
		if err != nil {
			return errorResult(fmt.Errorf("allowed_role_ids: %w", err)), nil
		}
		roles = append(roles, id)
	}

	if err := d.Guard.Check(channelOp(interactionTool, ch, opWrite, false)); err != nil {
		return errorResult(err), nil
	}
	res, err := d.Discord.AwaitInteraction(ctx, dto.AwaitInteractionReq{
		ChannelID: ch.ID, PromptText: prompt, Mode: mode, Options: opts,
		AllowedUserIDs: users, AllowedRoleIDs: roles, TimeoutSeconds: timeout,
		MinValues: minV, MaxValues: maxV,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return errorf("interaction wait cancelled: %v", err), nil
		}
		return errorResult(err), nil
	}
	return jsonResult(res), nil
}

// interactionNormalizeOptions applies defaults and validates options for mode.
func interactionNormalizeOptions(mode string, opts []dto.InteractionOption) ([]dto.InteractionOption, error) {
	if len(opts) == 0 {
		if mode == dto.ModeModal {
			return []dto.InteractionOption{{Label: "Response", Value: "response"}}, nil
		}
		return []dto.InteractionOption{
			{Label: "Approve", Value: "approve", Style: "success"},
			{Label: "Reject", Value: "reject", Style: "danger"},
		}, nil
	}
	maxN, maxLabel := interactionMaxOptions, 80
	switch mode {
	case dto.ModeSelect:
		maxLabel = 100
	case dto.ModeModal:
		maxN, maxLabel = interactionMaxModalFields, 45
	}
	if len(opts) > maxN {
		return nil, fmt.Errorf("mode %s allows at most %d options, got %d", mode, maxN, len(opts))
	}
	seen := map[string]bool{}
	out := make([]dto.InteractionOption, len(opts))
	for i, o := range opts {
		o.Label = strings.TrimSpace(o.Label)
		if o.Label == "" {
			return nil, fmt.Errorf("options[%d].label is required", i)
		}
		if utf8.RuneCountInString(o.Label) > maxLabel {
			return nil, fmt.Errorf("options[%d].label exceeds %d characters", i, maxLabel)
		}
		o.Value = strings.TrimSpace(o.Value)
		if o.Value == "" {
			o.Value = interactionSlug(o.Label)
			if o.Value == "" {
				o.Value = fmt.Sprintf("option-%d", i+1)
			}
		}
		if len(o.Value) > interactionMaxValueChars {
			return nil, fmt.Errorf("options[%d].value exceeds %d bytes", i, interactionMaxValueChars)
		}
		if seen[o.Value] {
			return nil, fmt.Errorf("options[%d].value %q is duplicated; values must be unique", i, o.Value)
		}
		seen[o.Value] = true
		o.Style = strings.ToLower(strings.TrimSpace(o.Style))
		if mode == dto.ModeButtons {
			switch {
			case o.Style == "":
				o.Style = interactionDefaultStyle(o)
			case !containsFold(interactionStyles, o.Style):
				return nil, fmt.Errorf("options[%d].style must be one of %s", i, strings.Join(interactionStyles, ", "))
			}
		} else {
			o.Style = ""
		}
		out[i] = o
	}
	return out, nil
}

// interactionDefaultStyle maps Approve-like choices to success and Reject-like to danger.
func interactionDefaultStyle(o dto.InteractionOption) string {
	key := strings.ToLower(o.Value + " " + o.Label)
	switch {
	case strings.Contains(key, "approve"), strings.Contains(key, "accept"), strings.Contains(key, "yes"):
		return "success"
	case strings.Contains(key, "reject"), strings.Contains(key, "deny"), strings.Contains(key, "decline"):
		return "danger"
	}
	return "primary"
}

// interactionSlug lowercases s and keeps [a-z0-9_-], mapping spaces to '-'.
func interactionSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_'):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// interactionSelectBounds validates min_values/max_values (select mode only).
func interactionSelectBounds(req mcp.CallToolRequest, mode string, n int) (int, int, error) {
	hasMin, hasMax := hasArg(req, "min_values"), hasArg(req, "max_values")
	if mode != dto.ModeSelect {
		if hasMin || hasMax {
			return 0, 0, errors.New("min_values/max_values apply only to mode=select")
		}
		return 0, 0, nil
	}
	minV, maxV := req.GetInt("min_values", 1), req.GetInt("max_values", 1)
	if hasMin && !hasMax && minV > maxV {
		maxV = minV
	}
	if minV < 0 || maxV < 1 || minV > maxV || maxV > n {
		return 0, 0, fmt.Errorf("need 0 <= min_values <= max_values <= %d (number of options), got min=%d max=%d", n, minV, maxV)
	}
	return minV, maxV, nil
}
