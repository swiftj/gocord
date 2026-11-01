package mcp

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/discord"
)

// DefaultDebounce is the minimum interval between notifications for one URI (spec 02 §4).
const DefaultDebounce = 2 * time.Second

// Notifier tracks resources/subscribe state per session and turns Gateway
// events into debounced notifications/resources/updated messages sent only
// to the sessions subscribed to the affected URI.
type Notifier struct {
	mu       sync.Mutex
	subs     map[string]map[string]struct{} // uri -> session IDs
	pending  map[string]bool                // uri -> timer armed
	last     map[string]time.Time           // uri -> last send
	debounce time.Duration
	send     func(sessionID, uri string)
	now      func() time.Time
	after    func(time.Duration, func())
}

func newNotifier(send func(sessionID, uri string)) *Notifier {
	return &Notifier{
		subs:     map[string]map[string]struct{}{},
		pending:  map[string]bool{},
		last:     map[string]time.Time{},
		debounce: DefaultDebounce,
		send:     send,
		now:      time.Now,
		after:    func(d time.Duration, f func()) { time.AfterFunc(d, f) },
	}
}

// hooks returns mcp-go hooks that maintain the subscription table.
func (n *Notifier) hooks() *server.Hooks {
	h := &server.Hooks{}
	h.AddAfterSubscribe(func(ctx context.Context, _ any, req *mcp.SubscribeRequest, _ *mcp.EmptyResult) {
		if sid := sessionID(ctx); sid != "" {
			n.Subscribe(sid, req.Params.URI)
		}
	})
	h.AddAfterUnsubscribe(func(ctx context.Context, _ any, req *mcp.UnsubscribeRequest, _ *mcp.EmptyResult) {
		if sid := sessionID(ctx); sid != "" {
			n.Unsubscribe(sid, req.Params.URI)
		}
	})
	h.AddOnUnregisterSession(func(_ context.Context, s server.ClientSession) {
		n.DropSession(s.SessionID())
	})
	return h
}

func sessionID(ctx context.Context) string {
	if s := server.ClientSessionFromContext(ctx); s != nil {
		return s.SessionID()
	}
	return ""
}

// Subscribe records that sessionID wants updates for uri.
func (n *Notifier) Subscribe(sessionID, uri string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.subs[uri] == nil {
		n.subs[uri] = map[string]struct{}{}
	}
	n.subs[uri][sessionID] = struct{}{}
}

// Unsubscribe removes one subscription.
func (n *Notifier) Unsubscribe(sessionID, uri string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	delete(n.subs[uri], sessionID)
	if len(n.subs[uri]) == 0 {
		delete(n.subs, uri)
	}
}

// DropSession removes every subscription held by a session.
func (n *Notifier) DropSession(sessionID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for uri, set := range n.subs {
		delete(set, sessionID)
		if len(set) == 0 {
			delete(n.subs, uri)
		}
	}
}

// HandleEvent maps a Gateway event (see discord.Event* kinds) to affected URIs.
// Message creates touch the channel transcript; channel/thread/role changes touch
// the guild map (and a deleted channel's transcript).
func (n *Notifier) HandleEvent(kind, guildID, channelID string) {
	var prefixes []string
	switch kind {
	case discord.EventMessageCreate:
		if channelID != "" {
			prefixes = append(prefixes, "discord://channels/"+channelID+"/transcript")
		}
	case discord.EventChannelDelete, discord.EventThreadDelete:
		if channelID != "" {
			prefixes = append(prefixes, "discord://channels/"+channelID+"/transcript")
		}
		fallthrough
	default:
		if guildID != "" {
			prefixes = append(prefixes, "discord://guilds/"+guildID+"/map")
		}
	}
	for _, uri := range n.matching(prefixes) {
		n.schedule(uri)
	}
}

// matching returns subscribed URIs equal to a prefix or extending it with a query string.
func (n *Notifier) matching(prefixes []string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for uri := range n.subs {
		for _, p := range prefixes {
			if uri == p || strings.HasPrefix(uri, p+"?") {
				out = append(out, uri)
				break
			}
		}
	}
	return out
}

func (n *Notifier) schedule(uri string) {
	n.mu.Lock()
	if n.pending[uri] {
		n.mu.Unlock()
		return
	}
	wait := n.debounce - n.now().Sub(n.last[uri])
	if wait <= 0 {
		n.mu.Unlock()
		n.flush(uri)
		return
	}
	n.pending[uri] = true
	n.mu.Unlock()
	n.after(wait, func() { n.flush(uri) })
}

func (n *Notifier) flush(uri string) {
	n.mu.Lock()
	n.pending[uri] = false
	n.last[uri] = n.now()
	sessions := make([]string, 0, len(n.subs[uri]))
	for sid := range n.subs[uri] {
		sessions = append(sessions, sid)
	}
	n.mu.Unlock()
	for _, sid := range sessions {
		n.send(sid, uri)
	}
}
