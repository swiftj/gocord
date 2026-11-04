package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDefaults(t *testing.T) {
	c, err := Load(nil, envFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport != TransportStdio || c.HTTPAddr != DefaultHTTPAddr || c.DBPath != DefaultDBPath || c.ReadOnly || c.Profile != DefaultProfile {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestPrecedenceFlagOverEnv(t *testing.T) {
	env := envFrom(map[string]string{
		"DISCORD_MCP_TRANSPORT": "http",
		"DISCORD_MCP_READ_ONLY": "true",
		"DISCORD_BOT_TOKEN":     "env-token",
	})
	c, err := Load([]string{"--transport", "stdio", "--read-only=false"}, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport != TransportStdio {
		t.Errorf("transport = %q, flag should win", c.Transport)
	}
	if c.ReadOnly {
		t.Error("read-only flag=false should win over env=true")
	}
	if c.Token != "env-token" {
		t.Error("token should come from env")
	}
}

func TestInvalidTransport(t *testing.T) {
	if _, err := Load([]string{"--transport", "grpc"}, envFrom(nil)); err == nil {
		t.Fatal("expected error")
	}
}

func TestProfilesFile(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.json")
	if err := os.WriteFile(good, []byte(`{"profiles":{"ops":{"token_env":"OPS_TOKEN","default_guild_id":"100000000000000001","read_only":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load([]string{"--config", good, "--profile", "ops"}, envFrom(map[string]string{"OPS_TOKEN": "ops-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "ops-secret" || !c.ReadOnly || c.DefaultGuildID != "100000000000000001" {
		t.Fatalf("profile not applied: %+v", c)
	}
	if _, err := c.TokenForProfile("missing", envFrom(nil)); err == nil {
		t.Error("expected unknown profile error")
	}

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"profiles":{"ops":{"token":"literal"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load([]string{"--config", bad}, envFrom(nil)); !errors.Is(err, ErrInlineToken) {
		t.Fatalf("want ErrInlineToken, got %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	c, err := Load([]string{"--db-path", "~/x/index.db"}, envFrom(map[string]string{"HOME": "/home/u"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.DBPath != "/home/u/x/index.db" {
		t.Fatalf("db path = %q", c.DBPath)
	}
}

func TestMediaEnv(t *testing.T) {
	env := map[string]string{
		"HOME":                        "/Users/test",
		"GOCORD_MEDIA_MAX_BYTES":      "1048576",
		"GOCORD_MEDIA_ALLOW_ABSOLUTE": "${user_config.media_allow_absolute}", // unsubstituted placeholder
	}
	get := func(k string) string { return env[k] }
	c, err := Load(nil, get)
	if err != nil {
		t.Fatal(err)
	}
	if c.MediaMaxBytes != 1<<20 || c.MediaAllowAbsolute ||
		c.MediaDir != "/Users/test/Downloads/gocord" {
		t.Fatalf("config = %+v", c)
	}
	env["GOCORD_MEDIA_DIR"] = "~/Art"
	env["GOCORD_MEDIA_ALLOW_ABSOLUTE"] = "true"
	if c, err = Load(nil, get); err != nil || c.MediaDir != "/Users/test/Art" || !c.MediaAllowAbsolute {
		t.Fatalf("config = %+v, %v", c, err)
	}
	for k, v := range map[string]string{"GOCORD_MEDIA_MAX_BYTES": "lots", "GOCORD_MEDIA_ALLOW_ABSOLUTE": "maybe"} {
		old := env[k]
		env[k] = v
		if _, err := Load(nil, get); err == nil {
			t.Errorf("invalid %s must fail", k)
		}
		env[k] = old
	}
}
