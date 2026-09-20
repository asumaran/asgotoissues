# asgotoissues

A [herdr](https://github.com/asumaran/herdr) plugin popup that lists your open
issues from Jira and GitHub, grouped by stack, with fuzzy search and a rendered
preview of the description. Selecting an issue opens it in the browser.

Sibling of [asgotopr](https://github.com/asumaran/asgotopr) (open GitHub PRs) and
[asgoto](https://github.com/asumaran/asgoto) (herdr workspaces): same
open-pick-exit popup pattern, same fuzzy search feel, but the universe is
your backlog.

## Install

```
herdr plugin install asumaran/asgotoissues
```

The manifest's `[[build]]` runs `scripts/fetch-binary.sh`, which downloads the
release binary matching the manifest version and falls back to `go build`
(`ASGOTOISSUES_BUILD_FROM_SOURCE=1` skips the download). Requires herdr >= 0.7.5.

Bind a key to the `open` action in `~/.config/herdr/config.toml`:

```toml
[[keys.command]]
key = ["prefix+t", "ctrl+alt+t"]
type = "plugin_action"
command = "asumaran.asgotoissues.open"
description = "asgotoissues (issue switcher)"
```

## Configuration

asgotoissues reads the same file the [asdev](https://github.com/asumaran/asdev)
Claude Code plugin uses, `~/.claude/asdev.local.md` (override with
`ASGOTOISSUES_CONFIG`). Each stack is one group of the list. Its `issues:` key
names the trackers to query, `jira`, `github` or both. A stack without the key
lists Jira when it has a `jira.base_url`, and is skipped otherwise:

```yaml
---
stacks:
  work:
    issues: [jira, github]
    jira:
      type: cloud                       # default; "server" for Jira Server / DC
      base_url: https://org.atlassian.net
      email: you@org.com                # basic-auth user (cloud)
      api_token_env: JIRA_TOKEN_WORK    # env var holding the API token / PAT
    github:
      org: acme                         # an org or a user; `orgs:` takes a list
  personal:
    issues: [github]
    github:
      org: your-login
  legacy:                               # no `issues:` key: Jira only
    jira:
      type: server
      base_url: https://jira.internal
      api_token_env: JIRA_TOKEN_LEGACY  # bearer PAT (add `username:` for basic auth)
---
```

Jira credentials are resolved per stack from `~/.netrc` first (`machine
org.atlassian.net login you@org.com password <token>`), then from the env var
named in `api_token_env`. netrc is preferred because it works no matter how
the plugin process was spawned; env vars from your shell rc may not reach it.

GitHub goes through the [gh](https://cli.github.com) CLI, so there is nothing
to configure beyond `gh auth login`. It is only needed by stacks that list
`github`.

## Usage

The filter input is focused on open, so just type. A query of several words matches them in any order (`login fix` finds "fix login flow"), and a word starting with `'` must occur as typed instead of fuzzily (`'dex`). Search is fuzzy over the
summary, the key (`2099` finds `PLAT-2099`, so does `plat-2099`; `12` finds
`tool#12`), and the status, type, project or repo, stack, parent key and
labels (`uat`, `sub-task`, `shop`, `bug`).
`↑/↓` (or `ctrl+p`/`ctrl+n`) move between issues, PgDn/PgUp move a page,
`alt+↑`/`alt+↓` (or Home/End) go to the top or the bottom of the list,
`shift+↓`/`shift+↑` scroll the description, `?` (while the filter is empty) or
`f1` expands the help line into every key, the mouse wheel moves the selection over
the list and scrolls the description anywhere else, `shift+←`/`shift+→` resize the list (the split is
remembered; the list takes a quarter of the width by default), `enter` opens the
issue in the browser, a click selects an issue, and `esc` closes (so does
`q` while the filter is empty).

With a query the list is a search result: the best match comes first, with
its group on top, and the cursor starts on it. Rows that match equally well
stay in their usual order, most recently updated first.

## Behavior notes

- Jira: one search per stack (`assignee = currentUser() AND
  statusCategory != Done ORDER BY updated DESC`), paginated up to 500
  tickets. Cloud uses `POST /rest/api/2/search/jql`, Server
  `POST /rest/api/2/search`; v2 is used on purpose so descriptions arrive
  as wiki markup, which is converted to Markdown for the preview.
- GitHub: the open issues assigned to you under the stack's owners, up to 500.
  Archived repositories are left out. The key is `repo#number`, and
  `owner/repo#number` when two owners have a repository with the same name.
- Within a stack, issues are listed newest created first (descending key
  number within a project or repository).
- Everything is cached stale-while-revalidate in the plugin state dir: the
  popup renders instantly from the last snapshot while the trackers refresh
  concurrently in the background (skipped entirely when the snapshot is
  under 60s old). If a tracker fails (offline, expired token, `gh` logged
  out) its cached issues stay listed, the error takes the help line, and the
  snapshot is revalidated again on the next open.
- Keys are colored by state: green while in progress, dim for to-do, red when
  blocked. In Jira that comes from the status category, and from a status
  name that contains "block". In GitHub it comes from the labels: one that
  contains "block", or one like `in progress`, `doing` or `wip`.

## Development

```bash
go build -o asgotoissues .          # local build (plugin runs ./asgotoissues from the repo root)
./asgotoissues -dump                # print stacks and issues (no TTY; refreshes when stale)
./asgotoissues -dump -query cart    # additionally print filter scores for a query
./asgotoissues -dump -show PLAT-2099 # print an issue's description as Markdown
go vet ./... && go test ./...
scripts/pty-check.py ./asgotoissues   # end-to-end TUI check on a pty (python3 + pyte)
herdr plugin link "$PWD"   # register the working copy (no build step)
```

Runtime state (`issuecache.json`) lives in `HERDR_PLUGIN_STATE_DIR`;
standalone runs fall back to `~/.config/herdr/asgotoissues-tui/`.
`ASGOTOISSUES_OPEN_CMD` replaces the browser opener (tests use it to capture the
URL).

## Releasing

`scripts/release.sh <X.Y.Z>` gates on a clean tree + green vet/build/test,
generates the CHANGELOG entry from commit subjects, syncs the manifest
version, commits, tags and publishes the GitHub release; CI then attaches
the `asgotoissues-<os>-<arch>` binaries (macOS and Linux, arm64 and amd64), the assets `fetch-binary.sh` downloads on installs.
