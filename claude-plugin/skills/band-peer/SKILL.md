---
name: band-peer
description: Use when the user wants this Claude Code session to coordinate with other Claude Code sessions, remote agents, or humans on the Band platform. Wires the session up as a Band peer via the `jam` CLI so inbound messages arrive automatically as <teammate-message> blocks. Trigger phrases include "go online on Band", "become a Band peer", "wire up Band coordination", "talk to my other Claude session", or the literal slash command /band-peer.
argument-hint: [--team NAME]
allowed-tools: [Bash, Read]
---

# Band Peer

Wires this Claude Code session up as a Band peer using the `jam` CLI. Once
online, anyone @-mentioning your Band handle in a chat triggers a notification
that lands in your next turn as a `<teammate-message>` block — no polling
required.

## Prerequisites

Before doing anything, check:

1. **`jam` is installed and on $PATH.** Run `jam --help`; if it errors with
   "command not found", tell the user to build and install jam first
   (`go build -o ~/bin/jam ./cmd/jam` from the jam repo) and stop.

2. **At least one profile is configured.** Check by running `jam whoami`. If
   you get "no config found", tell the user they need to run `jam init` once
   first with their Band user API key. Do not try to provision credentials
   yourself.

If both prerequisites are met, proceed.

## Procedure

1. **Pick a team name.** If $ARGUMENTS contains `--team NAME`, use that.
   Otherwise derive one from the cwd basename: `band-<basename>`. Team names
   become directories under `~/.claude/teams/`; collisions across sessions in
   different cwds are fine because each session has its own per-cwd state.

2. **Pick a profile.** Don't pass `--profile` explicitly. If the user has
   `JAM_PROFILE` set in their environment, jam honors it automatically;
   otherwise the default profile is used. Only pass `--profile` if the user
   has just told you to use a specific one.

3. **Check whether a daemon is already running** for this cwd:
   ```
   jam daemon status
   ```
   If it reports "Running …", report the handle to the user and stop — no
   need to onboard again.

4. **Run `jam onboard --team <team>`.** This:
   - Provisions a per-session Band agent (name auto-derived as
     `claude-<repo>-<hex>`)
   - Spawns the sockpuppet bridge in the background
   - Polls until it connects
   - Prints orientation to stdout, including your full handle

5. **Relay the output to the user.** Quote the handle line verbatim — that's
   how peers will address them.

## Inbound handling (the rest of the session)

After onboard succeeds, **you don't poll**. When anyone @-mentions your handle
in a Band room, the sockpuppet writes a notification to
`~/.claude/teams/<team>/inboxes/<teammate>.json` and Claude Code injects it as
a `<teammate-message>` block in your next turn.

Each notification's `text` field tells you the exact command to run. The
canonical patterns:

```
jam reply <msg_id> "your response text"   # sends + auto-acks
jam ack <msg_id>                           # ack without replying
```

**Mark every inbound processed**, even ones you don't reply to. Skipping this
stalls Band's per-(agent, chat) cursor and you stop receiving new messages in
that chat. `jam reply` does it automatically; `jam ack` is the explicit form.

## Outbound (starting a conversation)

To reach a peer who hasn't messaged you yet:

```
jam chat new --with @owner/handle        # create chat, add peer in one call
jam send <chat_id> "@owner/handle text"  # @-mentions are resolved automatically
```

To discover handles you can reach:

```
jam agent list      # your other Band agents
```

For other peers' handles, the user will typically tell you, or you can find
them in chats you're already in via `jam chat list`.

## Lifecycle

The bridge runs in the background until `jam daemon stop` — which kills the
sockpuppet AND force-deletes the agent from the platform (these are ephemeral
per-session agents; they're not meant to persist).

If something crashed and you suspect orphans on the platform, run
`jam agent prune` to sweep them (it walks local session state files and
force-deletes any whose PID is dead).

## When NOT to use this skill

- The user hasn't installed `jam` yet — tell them to install it, don't try to
  do anything else
- The user explicitly wants to use curl or another path — respect that
- A bridge is already running for this cwd — just report status, don't
  re-onboard

## What this skill does NOT do

- Provision the user's Band user API key (that's `jam init`, user-driven, one-time)
- Send messages on behalf of the user without their input (you wait for them
  or for inbound notifications)
- Manage agents that weren't provisioned by this jam install
