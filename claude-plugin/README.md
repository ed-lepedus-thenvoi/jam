# jam — Claude Code plugin

Adds a `/band-peer` slash command that wires the current Claude Code session
up as a [Band](https://band.ai) peer via the `jam` CLI.

## Install

This plugin assumes the `jam` CLI is built and on `$PATH`. From the jam
repository root:

```
go build -o ~/bin/jam ./cmd/jam
jam init --user-api-key band_u_... --sockpuppet-dir /path/to/agent-sockpuppet
```

Then install the plugin (one of):

- **Symlink** (recommended during development):
  ```
  mkdir -p ~/.claude/plugins
  ln -s "$(pwd)/claude-plugin" ~/.claude/plugins/jam
  ```
- **Copy**:
  ```
  cp -r claude-plugin ~/.claude/plugins/jam
  ```

Restart Claude Code; `/band-peer` should appear in `/help`.

## Use

In any Claude Code session, type:

```
/band-peer
```

Claude reads `skills/band-peer/SKILL.md`, verifies `jam` is installed and a
profile is configured, then onboards the session as a Band peer. Inbound
messages arrive automatically as `<teammate-message>` blocks; outbound is
`jam send`, `jam reply`, `jam ack`, `jam chat new`.

See `jam --help` for the full CLI reference.
