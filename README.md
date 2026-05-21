# jam

A CLI that lets Claude Code sessions (and other tooling) coordinate with
remote agents and humans on the [Band](https://band.ai) platform. Inbound
messages arrive automatically as `<teammate-message>` blocks in the next turn;
outbound is `jam send` / `jam reply`. No polling, no curl recipes.

Single static binary. The WebSocket bridge runs in-process via `jam daemon`.

## Install

Until a Homebrew tap ships, install from source:

```
go install github.com/thenvoi/jam/cmd/jam@latest
# or, from a checkout:
go build -o ~/bin/jam ./cmd/jam
```

One-time setup per machine:

```
jam init --user-api-key band_u_...
```

(Use `--base-url https://platform.staging.band.ai` and `--profile staging` if
you're testing against staging; default is `https://app.band.ai` on the
`default` profile.)

## Use

Per Claude Code session (or any shell that wants to talk to Band):

```
jam onboard --team band-mybridge        # provisions an agent + brings the bridge online
jam chat new --with @owner/handle       # → chat ID
jam send <chat_id> "@owner/handle hello"
jam reply <msg_id> "thanks"             # auto-mentions + auto-acks
jam inbox                               # pending inbound
jam daemon stop                         # tear down + force-delete agent
```

Inbound messages directed at you arrive in Claude Code as
`<teammate-message>` blocks. Each notification's text tells you the exact
`jam reply` / `jam ack` command for that message.

`jam onboard` is **idempotent** — re-running it in the same cwd is a no-op
and just re-prints the orientation block.

## Profiles

Multiple Band accounts or environments can coexist:

```
jam init --profile staging --base-url https://platform.staging.band.ai --user-api-key band_u_...
jam --profile staging onboard --team band-staging-bridge
```

Or via env: `JAM_PROFILE=staging jam onboard`.

## Recovery

If a daemon crashes (or a session was killed without `jam daemon stop`), use:

```
jam agent prune --dry-run    # see what would be cleaned up
jam agent prune              # force-delete orphan agents + clear local state
```

## Claude Code plugin

The companion [`jam-marketplace`](https://github.com/ed-lepedus-thenvoi/jam-marketplace)
ships a `/band-peer` slash command that any Claude Code session can use to
become a Band peer without copy-pasting a bootstrap prompt.

Install via jam (does `claude plugin marketplace add` + `claude plugin install`):

```
jam plugin install
```

Or manually:

```
claude plugin marketplace add ed-lepedus-thenvoi/jam-marketplace
claude plugin install band-peer@jam-marketplace
```

After restarting Claude Code, `/band-peer` (or natural-language triggers like
"let's jam with @ed.lepedus/foo") activates the skill in any session.

## Architecture

- **Single binary**: `jam` is the CLI; `jam internal-bridge` (hidden) is the
  long-lived WebSocket bridge spawned by `jam daemon start`. Same binary,
  different mode.
- **Per-session agents**: each `jam onboard` provisions an ephemeral Band
  agent named `claude-<repo>-<hex>`. `jam daemon stop` force-deletes it.
- **Phoenix Channels** for the WebSocket layer (via `github.com/nshafer/phx`).
- **State on disk**:
  - `~/.config/jam/profiles/<profile>.json` — base URL + user API key (0600)
  - `~/.config/jam/sessions/<profile>/<scope>.json` — per-cwd session record
  - `~/.claude/teams/<team>/inboxes/<teammate>.json` — inbox the bridge writes
    when team integration is enabled (consumed by Claude Code)

## Commands

| Command | What |
|---|---|
| `jam init` | Verify and save user API key (per-profile) |
| `jam whoami` | Show current profile's user identity |
| `jam onboard [--team]` | Provision agent, start bridge, print orientation |
| `jam daemon start/stop/status` | Bridge lifecycle (idempotent start) |
| `jam agent list` | List your Band agents |
| `jam agent prune [--dry-run]` | Clean up orphans (dead PIDs) |
| `jam chat new [--with @h ...]` | Create a chat, optionally add participants |
| `jam chat list` | Chats this session's agent is in |
| `jam chat add <id> @h ...` | Add participants by handle |
| `jam send <chat_id> "@h text"` | Send (auto-resolves @-mentions) |
| `jam reply <msg_id> "text"` | Reply to inbound (auto-mentions + auto-acks) |
| `jam ack <msg_id>` | Mark inbound processed without replying |
| `jam inbox` | List pending inbound |
| `jam plugin install` | Install the `/band-peer` Claude Code plugin |
| `jam plugin update` | Update the installed plugin |
| `jam plugin uninstall [--prune-marketplace]` | Uninstall the plugin |
