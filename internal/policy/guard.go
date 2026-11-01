// Package policy implements the governance guard: read-only mode, guild and
// channel allow/block lists, bulk-delete limits, and dry-run previews.
// See specs/01-architecture-and-contracts.md §7.
package policy

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/dto"
)

// ErrDenied is wrapped by every DeniedError.
var ErrDenied = errors.New("policy denied")

// DeniedError explains why an operation was refused.
type DeniedError struct {
	Tool   string
	Reason string
}

func (e *DeniedError) Error() string { return fmt.Sprintf("policy denied %s: %s", e.Tool, e.Reason) }
func (e *DeniedError) Unwrap() error { return ErrDenied }

// Op describes an operation for policy evaluation.
type Op struct {
	Tool string
	// Write is true for any operation that changes Discord state (send, create,
	// edit, delete, react). Read-only mode blocks all writes that are not dry runs.
	Write bool
	// Destructive marks operations in the destructive set (see DestructiveTools).
	Destructive bool
	GuildID     string
	ChannelID   string
	// ParentChannelID is the parent of ChannelID when ChannelID is a thread.
	ParentChannelID string
	// MessageIDs is used for bulk-delete validation.
	MessageIDs []string
	DryRun     bool
}

// Bulk delete limits imposed by Discord.
const (
	BulkDeleteMin    = 2
	BulkDeleteMax    = 100
	BulkDeleteMaxAge = 14 * 24 * time.Hour
)

// DestructiveTools lists tools (or tool:action pairs) that MUST accept dry_run.
var DestructiveTools = map[string]bool{
	"discord_delete_message":                  true,
	"discord_bulk_delete_messages":            true,
	"discord_delete_channel":                  true,
	"discord_edit_channel":                    true,
	"discord_set_channel_permissions":         true,
	"discord_manage_roles:create":             true,
	"discord_manage_roles:edit":               true,
	"discord_manage_roles:delete":             true,
	"discord_manage_roles:assign":             true,
	"discord_manage_roles:revoke":             true,
	"discord_delete_webhook":                  true,
	"discord_edit_webhook":                    true,
	"discord_governance_audit:timeout_member": true,
	"discord_manage_threads:archive":          true,
	"discord_manage_threads:lock":             true,
}

// Guard evaluates operations against configured policy.
type Guard struct {
	readOnly        bool
	allowedGuilds   []string
	blockedGuilds   []string
	allowedChannels []string
	blockedChannels []string
	now             func() time.Time
}

// New builds a Guard from configuration. A nil cfg yields a permissive guard.
func New(cfg *config.Config) *Guard {
	g := &Guard{now: time.Now}
	if cfg != nil {
		g.readOnly = cfg.ReadOnly
		g.allowedGuilds = cfg.AllowedGuilds
		g.blockedGuilds = cfg.BlockedGuilds
		g.allowedChannels = cfg.AllowedChannels
		g.blockedChannels = cfg.BlockedChannels
	}
	return g
}

// SetClock overrides the time source (tests only).
func (g *Guard) SetClock(now func() time.Time) { g.now = now }

// ReadOnly reports whether read-only mode is on.
func (g *Guard) ReadOnly() bool { return g.readOnly }

// IsDestructive reports whether tool (or "tool:action") is in the destructive set.
func (g *Guard) IsDestructive(tool string) bool { return DestructiveTools[tool] }

// GuildAllowed reports whether a guild passes the allow/block lists (for filtering lists).
func (g *Guard) GuildAllowed(guildID string) bool {
	return g.listCheck(guildID, g.allowedGuilds, g.blockedGuilds) == ""
}

// ChannelAllowed reports whether a channel passes the allow/block lists.
func (g *Guard) ChannelAllowed(channelID string) bool {
	return g.listCheck(channelID, g.allowedChannels, g.blockedChannels) == ""
}

// Check evaluates op. It returns nil or a *DeniedError. The first denial wins.
func (g *Guard) Check(op Op) error {
	deny := func(format string, a ...any) error {
		return &DeniedError{Tool: op.Tool, Reason: fmt.Sprintf(format, a...)}
	}
	mutating := op.Write || op.Destructive

	// 1. Read-only (dry runs always allowed).
	if g.readOnly && mutating && !op.DryRun {
		return deny("read-only mode: %s is disabled", op.Tool)
	}
	// 2–3. Guild lists.
	if op.GuildID != "" {
		if r := g.listCheck(op.GuildID, g.allowedGuilds, g.blockedGuilds); r != "" {
			return deny("guild %s is %s", op.GuildID, r)
		}
	}
	// 4. Channel lists (thread checked together with its parent).
	for _, ch := range []string{op.ChannelID, op.ParentChannelID} {
		if ch == "" {
			continue
		}
		if slices.Contains(g.blockedChannels, ch) {
			return deny("channel %s is blocked", ch)
		}
	}
	if len(g.allowedChannels) > 0 && op.ChannelID != "" &&
		!slices.Contains(g.allowedChannels, op.ChannelID) &&
		(op.ParentChannelID == "" || !slices.Contains(g.allowedChannels, op.ParentChannelID)) {
		return deny("channel %s is not in the allowlist", op.ChannelID)
	}
	// 5. Bulk delete limits.
	if op.Tool == "discord_bulk_delete_messages" {
		n := len(op.MessageIDs)
		if n < BulkDeleteMin || n > BulkDeleteMax {
			return deny("bulk delete needs %d-%d message IDs, got %d", BulkDeleteMin, BulkDeleteMax, n)
		}
		cutoff := g.now().Add(-BulkDeleteMaxAge)
		for _, id := range op.MessageIDs {
			ts, err := dto.SnowflakeTime(id)
			if err != nil {
				return deny("invalid message ID %q", id)
			}
			if ts.Before(cutoff) {
				return deny("message %s is older than 14 days (created %s); delete it individually", id, dto.FormatTime(ts))
			}
		}
	}
	// 6. Unresolvable scope.
	if op.Destructive && op.GuildID == "" && op.ChannelID == "" {
		return deny("destructive operation has no guild or channel scope")
	}
	return nil
}

// Preview runs Check and builds a DryRunPreview. It never mutates anything.
func (g *Guard) Preview(op Op, target string, changes []string, warnings ...string) dto.DryRunPreview {
	op.DryRun = true
	p := dto.DryRunPreview{
		DryRun:   true,
		Action:   op.Tool,
		Target:   target,
		Changes:  changes,
		Warnings: warnings,
		Allowed:  true,
	}
	// Evaluate as a real run so the preview reports whether it would be allowed.
	real := op
	real.DryRun = false
	if err := g.Check(real); err != nil {
		p.Allowed = false
		var de *DeniedError
		if errors.As(err, &de) {
			p.DeniedReason = de.Reason
		} else {
			p.DeniedReason = err.Error()
		}
	}
	return p
}

func (g *Guard) listCheck(id string, allowed, blocked []string) string {
	if slices.Contains(blocked, id) {
		return "blocked"
	}
	if len(allowed) > 0 && !slices.Contains(allowed, id) {
		return "not in the allowlist"
	}
	return ""
}
