// Package policy implements the governance guard: read-only mode, bulk-delete
// limits, and dry-run previews.
// See specs/01-architecture-and-contracts.md §7.
package policy

import (
	"errors"
	"fmt"
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
	readOnly bool
	now      func() time.Time
}

// New builds a Guard from configuration. A nil cfg yields a permissive guard.
func New(cfg *config.Config) *Guard {
	g := &Guard{now: time.Now}
	if cfg != nil {
		g.readOnly = cfg.ReadOnly
	}
	return g
}

// SetClock overrides the time source (tests only).
func (g *Guard) SetClock(now func() time.Time) { g.now = now }

// ReadOnly reports whether read-only mode is on.
func (g *Guard) ReadOnly() bool { return g.readOnly }

// IsDestructive reports whether tool (or "tool:action") is in the destructive set.
func (g *Guard) IsDestructive(tool string) bool { return DestructiveTools[tool] }

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
	// 2. Bulk delete limits.
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
	// 3. Unresolvable scope.
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
