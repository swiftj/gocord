package discord

import "testing"

func TestDecodeWithoutComponents(t *testing.T) {
	body := []byte(`[{"id":"100000000000000100","channel_id":"100000000000000010","content":"hi","flags":32768,
		"author":{"id":"100000000000000050","username":"alice"},
		"components":[{"type":20,"id":1}],
		"referenced_message":{"id":"100000000000000099","content":"ref","components":[{"type":20}]},
		"message_snapshots":[{"message":{"content":"fwd","components":[{"type":20}]}}]}]`)
	var out []*struct {
		ID      string
		Content string
		Flags   int
	}
	if err := decodeWithoutComponents(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Content != "hi" || out[0].Flags != 32768 {
		t.Fatalf("out = %+v", out[0])
	}
	if !isUnknownComponent(errString("json unmarshal: unknown component type: 20")) || isUnknownComponent(nil) {
		t.Fatal("isUnknownComponent")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
