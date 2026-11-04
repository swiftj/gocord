package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
		"DISCORD_MCP_TRANSPORT":      "http",
		"DISCORD_MCP_READ_ONLY":      "true",
		"DISCORD_MCP_ALLOWED_GUILDS": "100000000000000001, 100000000000000002",
		"DISCORD_BOT_TOKEN":          "env-token",
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
	want := []string{"100000000000000001", "100000000000000002"}
	if !reflect.DeepEqual(c.AllowedGuilds, want) {
		t.Errorf("allowed guilds = %v", c.AllowedGuilds)
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
	if err := os.WriteFile(good, []byte(`{"profiles":{"ops":{"token_env":"OPS_TOKEN","allowed_guilds":["100000000000000001"],"read_only":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load([]string{"--config", good, "--profile", "ops"}, envFrom(map[string]string{"OPS_TOKEN": "ops-secret"}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "ops-secret" || !c.ReadOnly || len(c.AllowedGuilds) != 1 {
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

func TestMediaAndLegendKeeperEnv(t *testing.T) {
	env := map[string]string{
		"GOCORD_MEDIA_ALLOWED_HOSTS": "*.example.com, img.test",
		"GOCORD_MEDIA_MAX_BYTES":     "1048576",
		"LEGENDKEEPER_API_KEY":       "lk_test_key",
		"LEGENDKEEPER_PROJECT_ID":    "${user_config.legendkeeper_project_id}", // unsubstituted placeholder
		"XDG_CACHE_HOME":             "/tmp/cache",
	}
	c, err := Load(nil, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if len(c.MediaAllowedHosts) != 2 || c.MediaMaxBytes != 1<<20 || c.LegendKeeperAPIKey != "lk_test_key" ||
		c.LegendKeeperProjectID != "" || c.MediaCacheDir != "/tmp/cache/gocord" {
		t.Fatalf("config = %+v", c)
	}
	env["GOCORD_MEDIA_MAX_BYTES"] = "lots"
	if _, err := Load(nil, func(k string) string { return env[k] }); err == nil {
		t.Fatal("invalid GOCORD_MEDIA_MAX_BYTES must fail")
	}
}
