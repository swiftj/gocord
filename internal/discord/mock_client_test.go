package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/local/discord-mcp/internal/dto"
)

func TestParseRef(t *testing.T) {
	cases := map[string]Ref{
		"100000000000000010":     {ID: "100000000000000010"},
		"<#100000000000000010>":  {ID: "100000000000000010"},
		"<@100000000000000050>":  {ID: "100000000000000050"},
		"<@!100000000000000050>": {ID: "100000000000000050"},
		"<@&100000000000000040>": {ID: "100000000000000040"},
		"#general":               {Name: "general"},
		"@alice":                 {Name: "alice"},
		" general ":              {Name: "general"},
	}
	for in, want := range cases {
		if got := ParseRef(in); got != want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestMockResolve(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	for _, ref := range []string{"#general", "general", "<#100000000000000010>", MockGeneralID} {
		id, err := m.ResolveChannelID(ctx, MockGuildID, ref)
		if err != nil || id != MockGeneralID {
			t.Errorf("ResolveChannelID(%q) = %q, %v", ref, id, err)
		}
	}
	if _, err := m.ResolveChannelID(ctx, MockGuildID, "#nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	m.Channels["100000000000000012"] = dto.ChannelInfo{ID: "100000000000000012", GuildID: MockGuildID, Name: "General", Type: dto.ChannelText}
	if _, err := m.ResolveChannelID(ctx, MockGuildID, "general"); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("want ErrAmbiguous, got %v", err)
	}
	if id, _ := m.ResolveUserID(ctx, MockGuildID, "@alice"); id != MockAliceID {
		t.Errorf("ResolveUserID = %q", id)
	}
	if id, _ := m.ResolveGuildID(ctx, "Test Guild"); id != MockGuildID {
		t.Errorf("ResolveGuildID = %q", id)
	}
}

func TestMockRecordingAndErrors(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	if _, err := m.ListChannels(ctx, MockGuildID); err != nil {
		t.Fatal(err)
	}
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("read should not be mutating")
	}
	if _, err := m.SendMessage(ctx, dto.SendMessageReq{ChannelID: MockGeneralID, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if n := len(m.MutatingCalls()); n != 1 {
		t.Fatalf("mutating calls = %d", n)
	}
	m.Err["DeleteChannel"] = ErrForbidden
	if err := m.DeleteChannel(ctx, MockGeneralID, ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("injected error not returned: %v", err)
	}
	if _, ok := m.Channels[MockGeneralID]; !ok {
		t.Fatal("channel deleted despite injected error")
	}
}

func TestMockAwaitInteraction(t *testing.T) {
	ctx := context.Background()
	m := NewMockClient()
	r, err := m.AwaitInteraction(ctx, dto.AwaitInteractionReq{ChannelID: MockGeneralID, PromptText: "Deploy?"})
	if err != nil || !r.TimedOut {
		t.Fatalf("want timeout, got %+v %v", r, err)
	}
	m.NextInteraction = &dto.InteractionResult{Selected: []string{"approve"}, UserID: MockAliceID}
	r, err = m.AwaitInteraction(ctx, dto.AwaitInteractionReq{ChannelID: MockGeneralID, PromptText: "Deploy?"})
	if err != nil || r.TimedOut || r.Selected[0] != "approve" || r.MessageID == "" {
		t.Fatalf("unexpected %+v %v", r, err)
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.AwaitInteraction(cctx, dto.AwaitInteractionReq{ChannelID: MockGeneralID}); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestMockReadMessagesNewestFirst(t *testing.T) {
	m := NewMockClient()
	msgs, err := m.ReadMessages(context.Background(), dto.ReadMessagesReq{ChannelID: MockGeneralID})
	if err != nil || len(msgs) != 2 || msgs[0].ID != "100000000000000101" {
		t.Fatalf("got %+v %v", msgs, err)
	}
}
