package dto

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// DiscordEpochMillis is the Discord epoch (2015-01-01T00:00:00Z) in unix milliseconds.
const DiscordEpochMillis int64 = 1420070400000

// SnowflakeTime returns the creation time encoded in a Snowflake.
func SnowflakeTime(id string) (time.Time, error) {
	n, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid snowflake %q: %w", id, err)
	}
	return time.UnixMilli(int64(n>>22) + DiscordEpochMillis).UTC(), nil
}

// SnowflakeFromTime returns the smallest Snowflake for the given instant.
func SnowflakeFromTime(t time.Time) string {
	ms := t.UnixMilli() - DiscordEpochMillis
	if ms < 0 {
		ms = 0
	}
	return strconv.FormatUint(uint64(ms)<<22, 10) //nolint:gosec // G115: ms is clamped to >= 0 above
}

// IsSnowflake reports whether s looks like a Discord Snowflake (all digits, 15–20 chars).
func IsSnowflake(s string) bool {
	if len(s) < 15 || len(s) > 20 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// NormalizeCursor accepts a Snowflake or an ISO-8601 / RFC 3339 timestamp
// (or YYYY-MM-DD) and returns a Snowflake. Empty input returns "".
func NormalizeCursor(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || IsSnowflake(s) {
		return s, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return SnowflakeFromTime(t), nil
		}
	}
	return "", fmt.Errorf("invalid cursor %q: expected Snowflake or ISO-8601 timestamp", s)
}

// FormatTime renders t as an RFC 3339 UTC string, or "" for the zero time.
func FormatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// Truncate shortens s to at most max runes, appending "…[truncated N chars]".
func Truncate(s string, max int) string {
	n := utf8.RuneCountInString(s)
	if n <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + fmt.Sprintf("…[truncated %d chars]", n-max)
}
