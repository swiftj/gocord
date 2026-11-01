package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func noEnv(string) string { return "" }

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--version"}, &out, &errb, noEnv); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if got := out.String(); got != "discord-mcp dev (none, unknown)\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--help"}, &out, &errb, noEnv); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out.String(), "--transport") {
		t.Fatalf("usage missing flags: %q", out.String())
	}
}

func TestListToolsJSON(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--list-tools-json"}, &out, &errb, noEnv); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	var tools []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(out.Bytes(), &tools); err != nil {
		t.Fatalf("invalid JSON %q: %v", out.String(), err)
	}
	for i := 1; i < len(tools); i++ {
		if tools[i-1].Name > tools[i].Name {
			t.Fatalf("tools not sorted: %q > %q", tools[i-1].Name, tools[i].Name)
		}
	}
}

func TestInvalidTransport(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--transport", "carrier-pigeon"}, &out, &errb, noEnv); code != 2 {
		t.Fatalf("exit %d, want 2", code)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must stay clean, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "invalid transport") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestInvalidTransportFromEnv(t *testing.T) {
	var out, errb bytes.Buffer
	env := func(k string) string {
		if k == "DISCORD_MCP_TRANSPORT" {
			return "smoke-signals"
		}
		return ""
	}
	if code := run(nil, &out, &errb, env); code != 2 || out.Len() != 0 {
		t.Fatalf("exit %d, stdout %q", code, out.String())
	}
}

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]string{"debug": "DEBUG", "WARN": "WARN", "error": "ERROR", "": "INFO", "bogus": "INFO"} {
		if got := parseLevel(in).String(); got != want {
			t.Errorf("parseLevel(%q) = %s", in, got)
		}
	}
}
