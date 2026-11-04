// Package config parses flags, environment variables, and an optional JSON
// profiles file. Precedence: flag > env > config file > default.
// See specs/01-architecture-and-contracts.md §6.
package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Transport values.
const (
	TransportStdio = "stdio"
	TransportHTTP  = "http"
)

// Profile is a named bot identity. Tokens are referenced by env var name only.
type Profile struct {
	TokenEnv        string   `json:"token_env"`
	AllowedGuilds   []string `json:"allowed_guilds,omitempty"`
	BlockedGuilds   []string `json:"blocked_guilds,omitempty"`
	AllowedChannels []string `json:"allowed_channels,omitempty"`
	BlockedChannels []string `json:"blocked_channels,omitempty"`
	ReadOnly        *bool    `json:"read_only,omitempty"`
	DefaultGuildID  string   `json:"default_guild_id,omitempty"`
}

// Config is the fully resolved runtime configuration.
type Config struct {
	Token           string `json:"-"` // never serialised or logged
	ConfigPath      string
	Profile         string
	Profiles        map[string]Profile
	Transport       string
	HTTPAddr        string
	DBPath          string
	ReadOnly        bool
	AllowedGuilds   []string
	BlockedGuilds   []string
	AllowedChannels []string
	BlockedChannels []string
	DefaultGuildID  string
	LogLevel        string
	ShowVersion     bool
	ListToolsJSON   bool // hidden: print tool list as JSON (used by build-dxt.sh)

	// Media (env only): the download cap in bytes (0 = default).
	MediaMaxBytes int64
	// MediaDir is the root that save_to paths resolve under (GOCORD_MEDIA_DIR,
	// default ~/Downloads/gocord). MediaAllowAbsolute permits absolute save_to
	// paths outside it (GOCORD_MEDIA_ALLOW_ABSOLUTE).
	MediaDir           string
	MediaAllowAbsolute bool
}

// Defaults.
const (
	DefaultProfile  = "default"
	DefaultHTTPAddr = "127.0.0.1:8080"
	DefaultDBPath   = ":memory:"
	DefaultLogLevel = "info"
	DefaultMediaDir = "~/Downloads/gocord"
)

// ErrInlineToken is returned when a profiles file contains a literal token.
var ErrInlineToken = errors.New("profiles file must not contain literal tokens; use token_env")

// Load parses args (typically os.Args[1:]) using getenv for environment lookup.
// Pass os.Getenv in production; tests pass a map-backed func.
func Load(args []string, getenv func(string) string) (*Config, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	fs := flag.NewFlagSet("discord-mcp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var (
		token, cfgPath, profile, transport, httpAddr, dbPath string
		allowedG, blockedG, allowedC, blockedC, defGuild     string
		logLevel                                             string
		readOnly                                             bool
		c                                                    Config
	)
	fs.StringVar(&token, "token", "", "Discord bot token (prefer DISCORD_BOT_TOKEN)")
	fs.StringVar(&cfgPath, "config", "", "JSON profiles file")
	fs.StringVar(&profile, "profile", "", "active profile")
	fs.StringVar(&transport, "transport", "", "stdio|http")
	fs.StringVar(&httpAddr, "http-addr", "", "HTTP listen address")
	fs.StringVar(&dbPath, "db-path", "", "index DB path or :memory:")
	fs.BoolVar(&readOnly, "read-only", false, "block write and destructive tools")
	fs.StringVar(&allowedG, "allowed-guilds", "", "comma-separated guild IDs")
	fs.StringVar(&blockedG, "blocked-guilds", "", "comma-separated guild IDs")
	fs.StringVar(&allowedC, "allowed-channels", "", "comma-separated channel IDs")
	fs.StringVar(&blockedC, "blocked-channels", "", "comma-separated channel IDs")
	fs.StringVar(&defGuild, "default-guild", "", "default guild ID")
	fs.StringVar(&logLevel, "log-level", "", "debug|info|warn|error")
	fs.BoolVar(&c.ShowVersion, "version", false, "print version and exit")
	fs.BoolVar(&c.ListToolsJSON, "list-tools-json", false, "print tools as JSON and exit")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	pick := func(flagName, flagVal, envName, def string) string {
		if set[flagName] {
			return flagVal
		}
		if v := getenv(envName); v != "" {
			return v
		}
		return def
	}

	c.ConfigPath = pick("config", cfgPath, "DISCORD_MCP_CONFIG", "")
	c.Profile = pick("profile", profile, "DISCORD_MCP_PROFILE", DefaultProfile)
	if c.ConfigPath != "" {
		profiles, err := loadProfiles(expandHome(c.ConfigPath, getenv))
		if err != nil {
			return nil, err
		}
		c.Profiles = profiles
	}
	p, hasProfile := c.Profiles[c.Profile]

	// Token: flag > env DISCORD_BOT_TOKEN > profile token_env.
	c.Token = pick("token", token, "DISCORD_BOT_TOKEN", "")
	if c.Token == "" && hasProfile && p.TokenEnv != "" {
		c.Token = getenv(p.TokenEnv)
	}

	c.Transport = strings.ToLower(pick("transport", transport, "DISCORD_MCP_TRANSPORT", TransportStdio))
	if c.Transport != TransportStdio && c.Transport != TransportHTTP {
		return nil, fmt.Errorf("invalid transport %q (want stdio or http)", c.Transport)
	}
	c.HTTPAddr = pick("http-addr", httpAddr, "DISCORD_MCP_HTTP_ADDR", DefaultHTTPAddr)
	c.DBPath = pick("db-path", dbPath, "DISCORD_MCP_DB_PATH", DefaultDBPath)
	if c.DBPath != DefaultDBPath {
		c.DBPath = expandHome(c.DBPath, getenv)
	}
	c.LogLevel = pick("log-level", logLevel, "DISCORD_MCP_LOG_LEVEL", DefaultLogLevel)

	// Read-only: flag > env > profile > false.
	switch {
	case set["read-only"]:
		c.ReadOnly = readOnly
	case getenv("DISCORD_MCP_READ_ONLY") != "":
		v, err := strconv.ParseBool(getenv("DISCORD_MCP_READ_ONLY"))
		if err != nil {
			return nil, fmt.Errorf("DISCORD_MCP_READ_ONLY: %w", err)
		}
		c.ReadOnly = v
	case hasProfile && p.ReadOnly != nil:
		c.ReadOnly = *p.ReadOnly
	}

	list := func(flagName, flagVal, envName string, fromProfile []string) []string {
		if set[flagName] {
			return splitList(flagVal)
		}
		if v := getenv(envName); v != "" {
			return splitList(v)
		}
		return fromProfile
	}
	c.AllowedGuilds = list("allowed-guilds", allowedG, "DISCORD_MCP_ALLOWED_GUILDS", p.AllowedGuilds)
	c.BlockedGuilds = list("blocked-guilds", blockedG, "DISCORD_MCP_BLOCKED_GUILDS", p.BlockedGuilds)
	c.AllowedChannels = list("allowed-channels", allowedC, "DISCORD_MCP_ALLOWED_CHANNELS", p.AllowedChannels)
	c.BlockedChannels = list("blocked-channels", blockedC, "DISCORD_MCP_BLOCKED_CHANNELS", p.BlockedChannels)
	c.DefaultGuildID = pick("default-guild", defGuild, "DISCORD_MCP_DEFAULT_GUILD_ID", p.DefaultGuildID)

	// A host that leaves an optional "${user_config.x}" unsubstituted means unset.
	env := func(name string) string {
		v := strings.TrimSpace(getenv(name))
		if strings.HasPrefix(v, "${") {
			return ""
		}
		return v
	}
	if v := env("GOCORD_MEDIA_MAX_BYTES"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("GOCORD_MEDIA_MAX_BYTES: want a positive integer, got %q", v)
		}
		c.MediaMaxBytes = n
	}
	c.MediaDir = env("GOCORD_MEDIA_DIR")
	if c.MediaDir == "" {
		c.MediaDir = DefaultMediaDir
	}
	c.MediaDir = expandHome(c.MediaDir, getenv)
	if v := env("GOCORD_MEDIA_ALLOW_ABSOLUTE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("GOCORD_MEDIA_ALLOW_ABSOLUTE: %w", err)
		}
		c.MediaAllowAbsolute = b
	}

	return &c, nil
}

// TokenForProfile returns the token for a named profile via its token_env.
func (c *Config) TokenForProfile(name string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	p, ok := c.Profiles[name]
	if !ok {
		return "", fmt.Errorf("unknown profile %q", name)
	}
	if p.TokenEnv == "" {
		return "", fmt.Errorf("profile %q has no token_env", name)
	}
	t := getenv(p.TokenEnv)
	if t == "" {
		return "", fmt.Errorf("profile %q: env var %s is empty", name, p.TokenEnv)
	}
	return t, nil
}

func loadProfiles(path string) (map[string]Profile, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: path is the operator-supplied --config file
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	// Reject literal tokens anywhere in a profile.
	var raw struct {
		Profiles map[string]map[string]json.RawMessage `json:"profiles"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	for name, fields := range raw.Profiles {
		if _, bad := fields["token"]; bad {
			return nil, fmt.Errorf("profile %q: %w", name, ErrInlineToken)
		}
	}
	var parsed struct {
		Profiles map[string]Profile `json:"profiles"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return parsed.Profiles, nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func expandHome(p string, getenv func(string) string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home := getenv("HOME")
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		return filepath.Join(home, strings.TrimPrefix(p, "~"))
	}
	return p
}
