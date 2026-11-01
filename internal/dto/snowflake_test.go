package dto

import (
	"testing"
	"time"
)

func TestSnowflakeRoundTrip(t *testing.T) {
	ts := time.Date(2026, 11, 1, 14, 55, 0, 0, time.UTC)
	id := SnowflakeFromTime(ts)
	got, err := SnowflakeTime(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(ts) {
		t.Fatalf("round trip: got %v want %v", got, ts)
	}
}

func TestNormalizeCursor(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
		same    bool
	}{
		{"", false, true},
		{"100000000000000010", false, true},
		{"2026-11-01T14:55:00Z", false, false},
		{"2026-11-01", false, false},
		{"yesterday", true, false},
	}
	for _, c := range cases {
		got, err := NormalizeCursor(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("%q: err=%v wantErr=%v", c.in, err, c.wantErr)
			continue
		}
		if c.same && got != c.in {
			t.Errorf("%q: got %q, want unchanged", c.in, got)
		}
		if !c.wantErr && c.in != "" && !IsSnowflake(got) {
			t.Errorf("%q: result %q is not a snowflake", c.in, got)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := Truncate("héllo", 10); got != "héllo" {
		t.Errorf("short string changed: %q", got)
	}
	if got := Truncate("héllo world", 5); got != "héllo…[truncated 6 chars]" {
		t.Errorf("got %q", got)
	}
}
