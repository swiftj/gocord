package discord

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/local/discord-mcp/internal/dto"
)

type fakeBrokerAPI struct {
	mu        sync.Mutex
	sent      chan *discordgo.MessageSend
	responses []*discordgo.InteractionResponse
	edits     []*discordgo.MessageEdit
	sendErr   error
}

func newFakeBrokerAPI() *fakeBrokerAPI {
	return &fakeBrokerAPI{sent: make(chan *discordgo.MessageSend, 4)}
}

func (f *fakeBrokerAPI) send(_ context.Context, channelID string, m *discordgo.MessageSend) (*discordgo.Message, error) {
	if f.sendErr != nil {
		return nil, f.sendErr
	}
	f.sent <- m
	return &discordgo.Message{ID: "100000000000000200", ChannelID: channelID}, nil
}

func (f *fakeBrokerAPI) respond(_ *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses = append(f.responses, r)
	return nil
}

func (f *fakeBrokerAPI) edit(_ context.Context, e *discordgo.MessageEdit) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, e)
	return nil
}

func (f *fakeBrokerAPI) snapshot() ([]*discordgo.InteractionResponse, []*discordgo.MessageEdit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*discordgo.InteractionResponse(nil), f.responses...), append([]*discordgo.MessageEdit(nil), f.edits...)
}

func testBroker(api brokerAPI) *Broker {
	b := newBroker(api, nil)
	b.unit = time.Millisecond // TimeoutSeconds become milliseconds
	return b
}

func firstCustomID(t *testing.T, m *discordgo.MessageSend) string {
	t.Helper()
	row := m.Components[0].(discordgo.ActionsRow)
	switch c := row.Components[0].(type) {
	case discordgo.Button:
		return c.CustomID
	case discordgo.SelectMenu:
		return c.CustomID
	}
	t.Fatalf("unexpected component %T", row.Components[0])
	return ""
}

func click(customID, userID string, roles ...string) *discordgo.Interaction {
	return &discordgo.Interaction{
		ID: "100000000000000300", Type: discordgo.InteractionMessageComponent,
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID, ComponentType: discordgo.ButtonComponent},
		Member: &discordgo.Member{User: &discordgo.User{ID: userID, Username: "user" + userID[len(userID)-2:]}, Roles: roles},
	}
}

type awaitRes struct {
	r   *dto.InteractionResult
	err error
}

func startAwait(ctx context.Context, b *Broker, req dto.AwaitInteractionReq) chan awaitRes {
	ch := make(chan awaitRes, 1)
	go func() {
		r, err := b.Await(ctx, req)
		ch <- awaitRes{r, err}
	}()
	return ch
}

func TestBrokerApproveFlow(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	done := startAwait(context.Background(), b, dto.AwaitInteractionReq{
		ChannelID: tGeneral, PromptText: "Deploy v1.2?", AllowedUserIDs: []string{tAlice}, TimeoutSeconds: 5000,
	})
	sent := <-api.sent
	row := sent.Components[0].(discordgo.ActionsRow)
	if len(row.Components) != 2 {
		t.Fatalf("want default Approve/Reject, got %d buttons", len(row.Components))
	}
	approve := row.Components[0].(discordgo.Button)
	reject := row.Components[1].(discordgo.Button)
	if approve.Label != "Approve" || approve.Style != discordgo.SuccessButton || reject.Style != discordgo.DangerButton {
		t.Fatalf("bad defaults: %+v %+v", approve, reject)
	}
	if !strings.HasPrefix(approve.CustomID, "dmcp:") || len(strings.Split(approve.CustomID, ":")[1]) != 16 {
		t.Fatalf("bad custom id %q", approve.CustomID)
	}

	// Unauthorised user gets an ephemeral rejection and the wait continues.
	b.handle(click(approve.CustomID, tBob))
	// Authorised click completes.
	b.handle(click(approve.CustomID, tAlice))

	res := <-done
	if res.err != nil {
		t.Fatal(res.err)
	}
	if res.r.TimedOut || res.r.UserID != tAlice || len(res.r.Selected) != 1 || res.r.Selected[0] != "approve" || res.r.MessageID == "" {
		t.Fatalf("result = %+v", res.r)
	}
	resps, edits := api.snapshot()
	if len(resps) != 2 || resps[0].Data == nil || resps[0].Data.Flags != discordgo.MessageFlagsEphemeral ||
		resps[1].Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("responses = %+v", resps)
	}
	if len(edits) != 1 || !strings.Contains(*edits[0].Content, "✅ Approve by @") {
		t.Fatalf("edits = %+v", edits)
	}
	for _, c := range (*edits[0].Components)[0].(discordgo.ActionsRow).Components {
		if !c.(discordgo.Button).Disabled {
			t.Fatal("components not disabled after completion")
		}
	}
	// A late click after completion is told the request is no longer active.
	b.handle(click(approve.CustomID, tAlice))
	resps, _ = api.snapshot()
	if last := resps[len(resps)-1]; last.Data == nil || !strings.Contains(last.Data.Content, "no longer active") {
		t.Fatalf("late click response = %+v", last)
	}
}

func TestBrokerRoleAuthorisation(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	done := startAwait(context.Background(), b, dto.AwaitInteractionReq{
		ChannelID: tGeneral, PromptText: "ok?", AllowedRoleIDs: []string{tModRole}, TimeoutSeconds: 5000,
	})
	id := firstCustomID(t, <-api.sent)
	b.handle(click(id, tBob, tModRole))
	if res := <-done; res.err != nil || res.r.UserID != tBob {
		t.Fatalf("res = %+v %v", res.r, res.err)
	}
}

func TestBrokerTimeout(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	start := time.Now()
	r, err := b.Await(context.Background(), dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "hello?", TimeoutSeconds: 30})
	if err != nil || !r.TimedOut || r.MessageID == "" {
		t.Fatalf("r=%+v err=%v", r, err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not honoured")
	}
	_, edits := api.snapshot()
	if len(edits) != 1 || !strings.Contains(*edits[0].Content, "⌛ timed out") {
		t.Fatalf("edits = %+v", edits)
	}
}

func TestBrokerContextCancel(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	ctx, cancel := context.WithCancel(context.Background())
	done := startAwait(ctx, b, dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "x", TimeoutSeconds: 100000})
	<-api.sent
	cancel()
	res := <-done
	if !errors.Is(res.err, context.Canceled) {
		t.Fatalf("err = %v", res.err)
	}
	_, edits := api.snapshot()
	if len(edits) != 1 {
		t.Fatalf("components not disabled on cancel: %+v", edits)
	}
}

func TestBrokerSelect(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	done := startAwait(context.Background(), b, dto.AwaitInteractionReq{
		ChannelID: tGeneral, PromptText: "pick", Mode: dto.ModeSelect, MaxValues: 2, TimeoutSeconds: 5000,
		Options: []dto.InteractionOption{{Label: "Red", Value: "red"}, {Label: "Blue", Value: "blue"}, {Label: "Green"}},
	})
	sent := <-api.sent
	menu := sent.Components[0].(discordgo.ActionsRow).Components[0].(discordgo.SelectMenu)
	if len(menu.Options) != 3 || menu.MaxValues != 2 || *menu.MinValues != 1 || menu.Options[2].Value != "green" {
		t.Fatalf("menu = %+v", menu)
	}
	i := click(menu.CustomID, tAlice)
	i.Data = discordgo.MessageComponentInteractionData{CustomID: menu.CustomID, ComponentType: discordgo.SelectMenuComponent, Values: []string{"red", "blue"}}
	b.handle(i)
	res := <-done
	if res.err != nil || strings.Join(res.r.Selected, ",") != "red,blue" {
		t.Fatalf("res = %+v %v", res.r, res.err)
	}
}

func TestBrokerModal(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	done := startAwait(context.Background(), b, dto.AwaitInteractionReq{
		ChannelID: tGeneral, PromptText: "Why?", Mode: dto.ModeModal, TimeoutSeconds: 5000,
		Options: []dto.InteractionOption{{Label: "Reason", Value: "reason", Style: "paragraph"}},
	})
	open := firstCustomID(t, <-api.sent)
	if !strings.HasSuffix(open, ":"+modalOpenValue) {
		t.Fatalf("respond button id %q", open)
	}
	b.handle(click(open, tAlice))
	resps, _ := api.snapshot()
	if len(resps) != 1 || resps[0].Type != discordgo.InteractionResponseModal {
		t.Fatalf("expected modal response, got %+v", resps)
	}
	submit := &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit,
		Data: discordgo.ModalSubmitInteractionData{CustomID: resps[0].Data.CustomID, Components: []discordgo.MessageComponent{
			&discordgo.ActionsRow{Components: []discordgo.MessageComponent{&discordgo.TextInput{CustomID: "reason", Value: "because"}}},
		}},
		Member: &discordgo.Member{User: &discordgo.User{ID: tAlice, Username: "alice"}},
	}
	b.handle(submit)
	res := <-done
	if res.err != nil || res.r.ModalFields["reason"] != "because" {
		t.Fatalf("res = %+v %v", res.r, res.err)
	}
}

func TestBrokerIgnoresForeignAndBots(t *testing.T) {
	api := newFakeBrokerAPI()
	b := testBroker(api)
	b.handle(click("other:thing", tAlice)) // not ours: no response
	i := click("dmcp:0123456789abcdef:approve", tAlice)
	i.Member.User.Bot = true
	b.handle(i) // unknown prefix → "no longer active"
	resps, _ := api.snapshot()
	if len(resps) != 1 {
		t.Fatalf("responses = %+v", resps)
	}
}

func TestNormalizeAwaitReq(t *testing.T) {
	many := make([]dto.InteractionOption, 26)
	for i := range many {
		many[i] = dto.InteractionOption{Label: "o", Value: strings.Repeat("v", i+1)}
	}
	tests := []struct {
		name string
		req  dto.AwaitInteractionReq
		ok   bool
	}{
		{"defaults", dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p"}, true},
		{"missing prompt", dto.AwaitInteractionReq{ChannelID: tGeneral}, false},
		{"bad mode", dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", Mode: "voice"}, false},
		{"too many buttons", dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", Options: many}, false},
		{"too many modal inputs", dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", Mode: dto.ModeModal, Options: many[:6]}, false},
		{"duplicate values", dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", Options: []dto.InteractionOption{{Value: "a"}, {Value: "a"}}}, false},
		{"value too long", dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", Options: []dto.InteractionOption{{Value: strings.Repeat("x", 90)}}}, false},
	}
	for _, tc := range tests {
		got, err := normalizeAwaitReq(tc.req)
		if (err == nil) != tc.ok {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if tc.name == "defaults" && (got.Mode != dto.ModeButtons || got.TimeoutSeconds != 300 || len(got.Options) != 2) {
			t.Errorf("defaults not applied: %+v", got)
		}
	}
	got, _ := normalizeAwaitReq(dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", TimeoutSeconds: 5000})
	if got.TimeoutSeconds != 900 {
		t.Errorf("timeout not clamped: %d", got.TimeoutSeconds)
	}
	// 12 buttons → rows of 5,5,2.
	twelve := many[:12]
	r, err := normalizeAwaitReq(dto.AwaitInteractionReq{ChannelID: tGeneral, PromptText: "p", Options: twelve})
	if err != nil {
		t.Fatal(err)
	}
	rows := buildComponents("dmcp:0123456789abcdef", r, false)
	if len(rows) != 3 || len(rows[2].(discordgo.ActionsRow).Components) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
}
