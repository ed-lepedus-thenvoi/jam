# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`jam` is a CLI that lets Claude Code sessions (and other tooling) coordinate via the [Band](https://band.ai) platform. Inbound messages arrive as `<teammate-message>` blocks in the next turn; outbound is `jam send` / `jam reply`. The WebSocket bridge runs **in-process** (`jam daemon start` re-execs `jam internal-bridge` and detaches) — there's no separate daemon binary and no Elixir runtime to install.

See `README.md` for user-facing install/usage docs and the full command surface.

## Common commands

```
mise exec -- go test ./...                          # full unit suite
mise exec -- go test ./internal/cli -run TestSend   # one package's tests, filtered
mise exec -- go build -o /tmp/jam ./cmd/jam         # binary to /tmp/jam for smoke testing
```

`mise.toml` pins Go via mise; `mise exec --` is the canonical wrapper. There is no separate lint/format step in CI — `go vet`/`gofmt` if you want to be tidy locally.

Releases tag-driven: pushing `v*` triggers `.github/workflows/release.yml` (GoReleaser builds darwin+linux × amd64+arm64, uploads to the GitHub Release, then PATCHes the Homebrew formula in `ed-lepedus-thenvoi/homebrew-tap`). Cross-repo formula write needs the `HOMEBREW_TAP_GITHUB_TOKEN` secret (PAT with `repo` scope on the tap).

## Architecture

**Single binary, two modes.** `cmd/jam/main.go` is the only entrypoint. `jam daemon start` execs `os.Executable() internal-bridge` with Band credentials in env vars and `SysProcAttr.Setsid = true`, then exits — the child runs as the long-lived bridge. Same binary, different cobra subcommand.

**The bridge** (`internal/bridge`) is jam's in-process replacement for the (now retired) Elixir agent-sockpuppet. It connects to `wss://<host>/api/v1/socket` using `github.com/nshafer/phx` (Phoenix Channels client), joins `agent_rooms:<id>` / `agent_contacts:<id>` / `chat_room:<id>` channels, and on each `message_created` event:
1. POSTs `mark_processing`
2. Resolves the sender's full `owner/handle` via the in-bridge `peerCache` (refresh-on-miss; synthetic self entry so own-mentions resolve without an API hop)
3. Rewrites any `@[[uuid]]` platform-substitution tokens in the content text to `@owner/handle` so what Claude reads matches what `jam send` accepts on the way out
4. Renders the notify template (Go `text/template`; defaults to a jam-flavored prompt; override via `JAM_NOTIFY_TEMPLATE` env var)
5. Appends the result to `~/.claude/teams/<team>/inboxes/<teammate>.json`

That inbox JSON is what Claude Code reads to inject `<teammate-message>` blocks. The schema is in `internal/inbox`. `band.<field>` carries structured fields (`chat_id`, `message_id`, `sender_*`, `content`) so `jam reply`/`jam ack`/`jam inbox` can act on it programmatically.

**Profiles.** `internal/config` reads `~/.config/jam/profiles/<name>.json`. Default profile is `default`; `--profile NAME` (persistent root flag) or `JAM_PROFILE` env selects others. Every CLI subcommand calls `getProfile()` (closure in `cli/root.go`) to resolve. `jam init --profile staging --base-url ... --user-api-key band_u_...` is how a non-default profile gets created.

**Per-cwd session state.** `internal/session` writes `~/.config/jam/sessions/<profile>/<scope>.json` where `scope = filepath.Base(cwd) + "-" + sha1(cwd)[:8]`. Each session record holds the provisioned agent's id, api key, handle, PID, and log path. This is what `jam daemon status` reads, what `jam daemon stop` deletes, what `jam agent prune` walks looking for dead PIDs.

**Lifecycle invariants.**
- Per-cwd agents are *ephemeral*. `jam daemon stop` force-deletes them (`band.DeleteAgent(id, true)`) since they accumulate execution history and a non-force delete 422s. Use `jam daemon stop --keep` to preserve the agent for `jam daemon restart` later.
- `jam daemon start` is idempotent: if a session record exists and its PID is alive, it's a no-op print. Crashes are recovered by `jam agent prune` (force-deletes orphans whose PIDs are dead).

**Band API client** (`internal/band`) is a thin HTTP wrapper. The same client type talks to both `/api/v1/me/...` (user-scoped, registers/deletes/lists agents — needs the user API key) and `/api/v1/agent/...` (agent-scoped — needs an agent API key). Construct with the right key per call site.

**Mentions are the entire UX foundation.** Band requires every outbound message to contain at least one resolved `@`-mention or it 422s — even in 2-person chats. `jam send`/`jam reply` parse `@owner/handle` patterns from text via `extractHandles`, resolve via `/agent/peers`, and populate both `mention.name` (short) and `mention.handle` (full) so the platform's substitution picks the unique form. The sender's own handle is silently skipped during resolution (you're not in your own peers). `resolveMentions` in `internal/cli/messaging.go` retries once on miss to paper over platform peer-index propagation lag.

## Testing patterns

**Outside-in BDD with empirical verification each loop.** Every slice was driven by writing a failing acceptance test first (`httptest.Server` + stub command + assertions on stdout/stderr/exit code/filesystem), confirming red baseline, implementing to green, then smoke-testing the built binary against real staging before committing. Tests do NOT hit the network; they mock Band via `httptest`.

**The daemon test harness** (`internal/cli/daemon_test.go`'s `daemonHarness`) spawns a stub bridge as `sh -c "echo '[Socket] Connected as test-stub'; sleep 60"` instead of re-execing jam. The `SpawnSockpuppet` factory (`Env.SpawnSockpuppet`, type `sockpuppet.Spawner`) is the seam that makes this possible. Tests reap their stubs via `cmd.Process.Wait()` in a goroutine to avoid zombies (which would make `kill -0` return true and break alive-checks).

**Bridge tests** (`internal/bridge/inbound_test.go`) similarly mock `/agent/peers` and `/api/v1/agent/chats/.../processing` via `httptest`. The bridge's `peerCache` accepts an `*band.Client` so tests inject one pointed at the test server.

## Repo workflow gotchas

- The companion **Claude Code plugin marketplace** lives in a separate repo (`ed-lepedus-thenvoi/jam-marketplace`). The `band-peer` skill there should bump version in lockstep with significant CLI changes that affect the skill's instructions. `jam plugin install` shells out to `claude plugin marketplace add` + `claude plugin install`.
- `internal/sockpuppet` is misnamed (legacy from when the daemon was the Elixir agent-sockpuppet). It currently just builds the self-exec `exec.Cmd` for the in-process Go bridge. Don't grow it; the package name is kept only to avoid churn at call sites.
- `band-peer-bootstrap.md` at the repo root is the original 90-line prompt that pre-dated this CLI. Kept as a historical reference for what `jam onboard` + the `band-peer` skill replaced. Don't expand it.
