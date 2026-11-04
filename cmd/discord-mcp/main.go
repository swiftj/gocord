// Command discord-mcp is a Model Context Protocol server for Discord.
//
// stdout carries JSON-RPC frames in stdio mode: the only direct stdout writes
// are --version, --help, and the hidden --list-tools-json. Logs go to stderr.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/local/discord-mcp/internal/config"
	"github.com/local/discord-mcp/internal/discord"
	"github.com/local/discord-mcp/internal/index"
	"github.com/local/discord-mcp/internal/lkbridge"
	"github.com/local/discord-mcp/internal/mcp"
	"github.com/local/discord-mcp/internal/media"
	"github.com/local/discord-mcp/internal/policy"
)

// Injected at build time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var version, commit, date = "dev", "none", "unknown"

const usage = `discord-mcp — Model Context Protocol server for Discord

Usage:
  discord-mcp [flags]

Flags (each also settable via the env var shown):
  --token string            Discord bot token                (DISCORD_BOT_TOKEN; prefer the env var)
  --config string           JSON profiles file               (DISCORD_MCP_CONFIG)
  --profile string          active profile (default "default") (DISCORD_MCP_PROFILE)
  --transport string        stdio | http (default "stdio")   (DISCORD_MCP_TRANSPORT)
  --http-addr string        HTTP listen address (default "127.0.0.1:8080") (DISCORD_MCP_HTTP_ADDR)
  --db-path string          index DB path or :memory: (default ":memory:") (DISCORD_MCP_DB_PATH)
  --read-only               block write and destructive tools (DISCORD_MCP_READ_ONLY)
  --allowed-guilds string   comma-separated guild IDs        (DISCORD_MCP_ALLOWED_GUILDS)
  --blocked-guilds string   comma-separated guild IDs        (DISCORD_MCP_BLOCKED_GUILDS)
  --allowed-channels string comma-separated channel IDs      (DISCORD_MCP_ALLOWED_CHANNELS)
  --blocked-channels string comma-separated channel IDs      (DISCORD_MCP_BLOCKED_CHANNELS)
  --default-guild string    guild used when a tool omits guild_id (DISCORD_MCP_DEFAULT_GUILD_ID)
  --log-level string        debug | info | warn | error (default "info") (DISCORD_MCP_LOG_LEVEL)
  --version                 print version and exit
  -h, --help                show this help

Media and LegendKeeper (environment only):
  GOCORD_MEDIA_ALLOWED_HOSTS  extra image hosts, comma-separated ("*.example.com" allowed)
  GOCORD_MEDIA_MAX_BYTES      download cap (default 26214400, 25 MB)
  LEGENDKEEPER_API_KEY        enables the LegendKeeper bridge tools
  LEGENDKEEPER_PROJECT_ID     default LegendKeeper project
  LEGENDKEEPER_BASE_URL       API base URL override

The MessageContent and GuildMembers privileged intents must be enabled for the
bot in the Discord Developer Portal.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, os.Getenv))
}

// run is the testable entrypoint. It returns the process exit code.
func run(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	cfg, err := config.Load(args, getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, usage)
			return 0
		}
		fmt.Fprintf(stderr, "discord-mcp: %v\n\n%s", err, usage)
		return 2
	}
	if cfg.ShowVersion {
		fmt.Fprintf(stdout, "discord-mcp %s (%s, %s)\n", version, commit, date)
		return 0
	}

	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: parseLevel(cfg.LogLevel)}))
	slog.SetDefault(logger)
	// Route any stray stdlib log output (e.g. from dependencies) to stderr.
	log.SetOutput(stderr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dbPath := cfg.DBPath
	if cfg.ListToolsJSON {
		dbPath = index.MemoryPath // never touch the user's DB just to list tools
	}
	store, err := index.Open(ctx, dbPath)
	if err != nil {
		logger.Error("open index", "path", dbPath, "err", err)
		return 1
	}
	defer func() {
		if err := store.Close(); err != nil {
			logger.Warn("close index", "err", err)
		}
	}()

	client := discord.NewClient(cfg, store, logger)
	defer func() {
		if err := client.Close(); err != nil {
			logger.Warn("close discord client", "err", err)
		}
	}()

	lkb, err := lkbridge.New(lkbridge.Config{
		APIKey: cfg.LegendKeeperAPIKey, DefaultProject: cfg.LegendKeeperProjectID, BaseURL: cfg.LegendKeeperBaseURL,
		CacheDir: cfg.MediaCacheDir, Logger: logger,
	})
	if err != nil {
		logger.Error("LegendKeeper bridge disabled", "err", err)
	}
	s, notifier := mcp.New(mcp.Deps{
		Discord: client, Store: store, Guard: policy.New(cfg), Config: cfg, Logger: logger,
		Media:        media.New(client, media.Config{ExtraHosts: cfg.MediaAllowedHosts, MaxBytes: cfg.MediaMaxBytes, Logger: logger}),
		LegendKeeper: lkb,
	}, version)
	client.OnEvent(notifier.HandleEvent) // resource subscriptions (spec 02 §4)

	if cfg.ListToolsJSON {
		return listToolsJSON(s, stdout, stderr)
	}

	if cfg.Token != "" {
		loginCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		info, err := client.Login(loginCtx, cfg.Token, cfg.Profile)
		cancel()
		if err != nil {
			logger.Error("auto-login failed; serving without a Discord session (use discord_login)", "err", err)
		} else {
			logger.Info("logged in", "profile", info.Profile, "bot", info.BotUsername, "guilds", info.GuildCount)
		}
	} else {
		logger.Info("no DISCORD_BOT_TOKEN configured; call discord_login to connect")
	}

	switch cfg.Transport {
	case config.TransportHTTP:
		err = serveHTTP(ctx, s, cfg.HTTPAddr, logger)
	default:
		err = serveStdio(ctx, s, os.Stdin, stdout, stderr)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("server stopped", "err", err)
		return 1
	}
	return 0
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	}
	return slog.LevelInfo
}

type toolEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func listToolsJSON(s *server.MCPServer, stdout, stderr io.Writer) int {
	tools := s.ListTools()
	out := make([]toolEntry, 0, len(tools))
	for name, t := range tools {
		out = append(out, toolEntry{Name: name, Description: t.Tool.Description})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	b, err := json.Marshal(out)
	if err != nil {
		fmt.Fprintf(stderr, "discord-mcp: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

func serveStdio(ctx context.Context, s *server.MCPServer, stdin io.Reader, stdout, stderr io.Writer) error {
	ss := server.NewStdioServer(s)
	ss.SetErrorLogger(log.New(stderr, "mcp-stdio: ", log.LstdFlags))
	return ss.Listen(ctx, stdin, stdout)
}

func serveHTTP(ctx context.Context, s *server.MCPServer, addr string, logger *slog.Logger) error {
	h := server.NewStreamableHTTPServer(s, server.WithEndpointPath("/mcp"))
	mux := http.NewServeMux()
	mux.Handle("/mcp", h)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("streamable HTTP transport listening", "addr", addr, "endpoint", "/mcp")
		errCh <- srv.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	logger.Info("shutting down HTTP transport")
	shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = h.Shutdown(shutCtx)
	return srv.Shutdown(shutCtx)
}
