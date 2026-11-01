---
paths: [".github/**", "ecosystem/**", ".goreleaser.yaml", "Makefile"]
---

# Release & Security Rules

Full spec: `specs/03-claude-ecosystem.md` (ecosystem) and `specs/04-release-and-ci.md` (CI/release).

- **Zero hardcoded secrets:** never write a bot token, webhook URL, client secret, or real guild/user Snowflake into any file. Reference secrets only as `${ENV_VAR}` (`.mcp.json`), `${user_config.*}` (`.dxt` manifest, with `sensitive: true`), or `${{ secrets.* }}` (Actions). CI needs only `GITHUB_TOKEN`. Docs and examples use `YOUR_BOT_TOKEN` and synthetic `1000000000000000xx` IDs.
- **Static, portable binaries:** every shipped binary is built with `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=…"` for exactly `darwin/arm64`, `darwin/amd64`, `linux/amd64`, and `linux/arm64`. Do not introduce steps that link against system libraries or require glibc. Race tests (`CGO_ENABLED=1 -race`) are test-only and never produce release artifacts.
- **Hook and packaging scripts:** POSIX `sh`/`bash` with `set -euo pipefail` (packaging) or always-`exit 0` (hooks). They never echo tokens and never write to stdout from hooks. They work on both the macOS (BSD) and Linux (GNU) userlands: avoid `sed -i` without a suffix, `readlink -f`, and `date -d`.
- **Supply chain:** pin GitHub Actions to commit SHAs before the first public release. Use `permissions:` least privilege (`contents: read` for CI, `contents: write` only in release). Releases publish SHA-256 `checksums.txt` covering the tarballs and `.dxt` bundles.
