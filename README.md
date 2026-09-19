# gotojira

A [herdr](https://github.com/asumaran/herdr) plugin popup that lists the Jira
tickets assigned to you (open ones, not in the Done status category) across
every Jira site you have configured, grouped by stack, with fuzzy search and
a rendered preview of the description. Selecting a ticket opens it in the
browser.

Sibling of [gotopr](https://github.com/asumaran/gotopr) (open GitHub PRs) and
[herdr-goto](https://github.com/asumaran/herdr-goto) (herdr workspaces): same
open-pick-exit popup pattern, same fuzzy search feel, but the universe is
your Jira backlog.

## Install

```
herdr plugin install asumaran/gotojira
```

The manifest's `[[build]]` runs `scripts/fetch-binary.sh`, which downloads the
release binary matching the manifest version and falls back to `go build`
(`GOTOJIRA_BUILD_FROM_SOURCE=1` skips the download). Requires herdr >= 0.7.5.

Bind a key to the `open` action in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = ["prefix+t", "ctrl+alt+t"]
type = "plugin_action"
command = "asumaran.gotojira.open"
description = "gotojira (Jira ticket switcher)"
```

## Configuration

gotojira reads the same file the [asdev](https://github.com/asumaran/asdev)
Claude Code plugin uses, `~/.claude/asdev.local.md` (override with
`GOTOJIRA_CONFIG`). Only the `jira` block of each stack matters; stacks
without one are skipped:

```yaml
---
stacks:
  work:
    jira:
      type: cloud                       # default; "server" for Jira Server / DC
      base_url: https://org.atlassian.net
      email: you@org.com                # basic-auth user (cloud)
      api_token_env: JIRA_TOKEN_WORK    # env var holding the API token / PAT
  legacy:
    jira:
      type: server
      base_url: https://jira.internal
      api_token_env: JIRA_TOKEN_LEGACY  # bearer PAT (add `username:` for basic auth)
---
```

Credentials are resolved per stack from `~/.netrc` first (`machine
org.atlassian.net login you@org.com password <token>`), then from the env var
named in `api_token_env`. netrc is preferred because it works no matter how
the plugin process was spawned; env vars from your shell rc may not reach it.

## Usage

The filter input is focused on open — just type. Search is fuzzy over the
summary, the key (`2099` finds `PLAT-2099`, so does `plat-2099`), and the
status / type / project / stack / parent key (`uat`, `sub-task`, `shop`).
`↑/↓` (or `ctrl+p`/`ctrl+n`) move between tickets, `shift+↓`/`shift+↑` (or
PgDn/PgUp) scroll the description, `enter` opens the ticket in the browser,
`esc` closes.

## Behavior notes

- Data comes from one search per stack (`assignee = currentUser() AND
  statusCategory != Done ORDER BY updated DESC`), paginated up to 500
  tickets per stack. Within a stack, tickets are listed newest created
  first (descending key number within a project). Cloud uses `POST /rest/api/2/search/jql`, Server
  `POST /rest/api/2/search`; v2 is used on purpose so descriptions arrive
  as wiki markup, which is converted to Markdown for the preview.
- Everything is cached stale-while-revalidate in the plugin state dir: the
  popup renders instantly from the last snapshot while the stacks refresh
  concurrently in the background (skipped entirely when the snapshot is
  under 60s old). If a stack fails (offline, expired token) its cached
  tickets stay listed, the error shows dimmed in the footer, and the
  snapshot is revalidated again on the next open.
- Keys are colored by status: green while in progress, dim for to-do, red
  when the status name contains "block".

## Development

```bash
go build -o gotojira .          # local build (plugin runs ./gotojira from the repo root)
./gotojira -dump                # print stacks and tickets (no TTY; refreshes when stale)
./gotojira -dump -query cart    # additionally print filter scores for a query
./gotojira -dump -show PLAT-2099 # print a ticket's wiki → Markdown conversion
go vet ./... && go test ./...
scripts/pty-check.py ./gotojira   # end-to-end TUI check on a pty (python3 + pyte)
herdr plugin link ~/Developer/gotojira   # register the working copy (no build step)
```

Runtime state (`issuecache.json`) lives in `HERDR_PLUGIN_STATE_DIR`;
standalone runs fall back to `~/.config/herdr/gotojira-tui/`.
`GOTOJIRA_OPEN_CMD` replaces the browser opener (tests use it to capture the
URL).

## Releasing

`scripts/release.sh <X.Y.Z>` gates on a clean tree + green vet/build/test,
generates the CHANGELOG entry from commit subjects, syncs the manifest
version, commits, tags and publishes the GitHub release; CI then attaches
`gotojira-darwin-arm64`, the asset `fetch-binary.sh` downloads on installs.
