package tools

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/dto"
	"github.com/local/discord-mcp/internal/mcp/deps"
)

func rpSeedPoll(m *discord.MockClient) {
	m.Polls[discord.MockFirstMessageID] = dto.PollResult{MessageID: discord.MockFirstMessageID, Question: "Ship?",
		Answers: []dto.PollAnswerTally{{ID: 1, Text: "Yes", Votes: 3}, {ID: 2, Text: "No", Votes: 3}, {ID: 3, Text: "Later", Votes: 1}}}
}

func TestReactionPollTools(t *testing.T) {
	blRunCases(t, []blCase{
		{
			tool: "discord_add_reaction", required: []string{"channel_id", "message_id", "emoji"},
			args: map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID, "emoji": "👍"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "AddReaction"); c.Args[0] != discord.MockGeneralID || c.Args[2] != "👍" {
					t.Errorf("AddReaction args = %v", c.Args)
				}
			},
		},
		{
			tool: "discord_add_multiple_reactions", required: []string{"channel_id", "message_id", "emojis"},
			args: map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID, "emojis": []any{"👍", "🎉", "<:party:100000000000000077>"}},
			check: func(t *testing.T, txt string, m *discord.MockClient, _ deps.Deps) {
				es := blLastCall(t, m, "AddMultipleReactions").Args[2].([]string)
				if len(es) != 3 || es[0] != "👍" || es[2] != "party:100000000000000077" {
					t.Errorf("emojis = %v", es)
				}
				if !strings.Contains(txt, `"ok":true`) {
					t.Errorf("result = %s", txt)
				}
			},
		},
		{
			tool: "discord_remove_reaction", required: []string{"channel_id", "message_id", "emoji"},
			args: map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID, "emoji": "👍", "user_id": "@alice"},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				if c := blLastCall(t, m, "RemoveReaction"); c.Args[3] != discord.MockAliceID {
					t.Errorf("RemoveReaction args = %v", c.Args)
				}
			},
		},
		{
			tool: "discord_create_poll", required: []string{"channel_id", "question", "answers"},
			args: map[string]any{"channel_id": "#general", "question": "Ship v1.2?", "answers": []any{"Yes", "No"}, "allow_multiselect": true},
			check: func(t *testing.T, _ string, m *discord.MockClient, _ deps.Deps) {
				r := blLastCall(t, m, "CreatePoll").Args[0].(dto.CreatePollReq)
				if r.ChannelID != discord.MockGeneralID || r.DurationHours != 24 || !r.AllowMultiselect || len(r.Answers) != 2 {
					t.Errorf("CreatePoll req = %+v", r)
				}
			},
		},
		{
			tool: "discord_get_poll_results", required: []string{"channel_id", "message_id"}, read: true, setup: rpSeedPoll,
			args: map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID},
			check: func(t *testing.T, txt string, _ *discord.MockClient, _ deps.Deps) {
				var p dto.PollResult
				_ = json.Unmarshal([]byte(txt), &p)
				if p.TotalVotes != 7 || len(p.Winners) != 2 || p.Winners[0] != "Yes" || p.Winners[1] != "No" {
					t.Errorf("poll = %+v", p)
				}
			},
		},
	})
}

func TestAddMultipleReactionsReportsPerEmojiFailures(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	m.Err["AddMultipleReactions"] = discord.ErrForbidden
	// Make the per-emoji fallback fail for every emoji too.
	m.Err["AddReaction"] = discord.ErrForbidden
	blWantErr(t, callTool(t, s, "discord_add_multiple_reactions", map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID, "emojis": []any{"👍", "🎉"}}), "no reactions added")

	// Batch call fails but the per-emoji fallback succeeds: every emoji is reported as added.
	delete(m.Err, "AddReaction")
	txt := blWantOK(t, callTool(t, s, "discord_add_multiple_reactions", map[string]any{"channel_id": "#general", "message_id": discord.MockFirstMessageID, "emojis": []any{"👍", "🎉"}}))
	if !strings.Contains(txt, `"added":["👍","🎉"]`) {
		t.Fatalf("result = %s", txt)
	}
}

func TestCreatePollValidation(t *testing.T) {
	s, m, _ := blSetup(t, nil)
	blWantErr(t, callTool(t, s, "discord_create_poll", map[string]any{"channel_id": "#general", "question": "Q", "answers": []any{"only"}}), "2-10")
	blWantErr(t, callTool(t, s, "discord_create_poll", map[string]any{"channel_id": "#general", "question": "Q", "answers": []any{"a", strings.Repeat("b", 56)}}), "max 55")
	blWantErr(t, callTool(t, s, "discord_create_poll", map[string]any{"channel_id": "#general", "question": strings.Repeat("q", 301), "answers": []any{"a", "b"}}), "max 300")
	blWantErr(t, callTool(t, s, "discord_create_poll", map[string]any{"channel_id": "#general", "question": "Q", "answers": []any{"a", "b"}, "duration_hours": 1000}), "duration_hours")
	if len(m.MutatingCalls()) != 0 {
		t.Fatal("validation failures must not mutate")
	}
}
