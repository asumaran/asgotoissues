# asgotoissues

A [herdr](https://github.com/asumaran/herdr) plugin popup that lists your open
issues from Jira and GitHub, grouped by stack as a tree (a sub-task under its
story, the story under its epic), with the pull requests linked to each ticket
and what each one needs from you, fuzzy search and a rendered preview of the
description. Selecting a ticket or a PR opens it in the browser.

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
to configure beyond `gh auth login`. It is needed by every stack with a
`github.org` (or `orgs`): those list GitHub issues when `issues:` says so,
and always get their pull requests from there, a Jira-only stack included.

## Usage

The list is a tree: under each stack a ticket hangs from its parent (a
sub-task from its story, the story from its epic), whatever the depth. A
parent that is not assigned to you (an epic of someone else's, a Done one) is
fetched all the same and shown dim, so your tickets sit where they belong;
its preview says `not in your list`. Under each ticket come the pull
requests that name it (in the branch, the title or the closing keywords; a
key merely mentioned in the body counts only when nothing else links), one
row per PR with the repo in the key, whatever repo it lives in, and a PR
that names several tickets appears under each. Your PRs that name no ticket
close their stack under `PRs without a ticket`.

A ticket or a PR takes two lines: the key and the title on the first, its
details dim under the title (a ticket: status, type, last update, how many
PRs; a PR: `open`, what it needs, the author when it is not you, the last
update). A child row starts where its parent's title starts. The `Rows` option in the panel puts everything on one line instead,
for a list twice as long on screen.

The list is two tones: a row's first line is plain, its details line is dim
(a lighter grey on the selected row), and colors are kept for the preview.
What a PR needs is written on its details line: `conflicts`, `changes requested`, `ci
failed`, `base merged` (it sits on a branch that was merged: retarget it),
`review requested` (of you), `behind base` (the base moved), `ci pending`,
`awaiting review`, `approved` (ready to merge), `on #N` (stacked on that
PR), `draft`, `merged`. A ticket's details line collects what its PRs and
every ticket under it need, worst first, so an epic shows the conflict three
levels down (`2 PRs · conflicts · awaiting review`); a ticket whose every PR
is merged says `all merged` (move it).

The filter input is focused on open, so just type. A query of several words matches them in any order (`login fix` finds "fix login flow"), and a word starting with `'` must occur as typed instead of fuzzily (`'dex`). Search is fuzzy over the
summary, the key (`2099` finds `PLAT-2099`, so does `plat-2099`; `12` finds
`tool#12`), and the status, type, project or repo, stack, parent key and
summary, labels (`uat`, `sub-task`, `shop`, `bug`) and the keys, numbers and
branches of the linked PRs (`8122` finds the ticket of `front#8122`).

| key | action |
| --- | --- |
| `enter`, `ctrl+o` | open the ticket or the PR in the browser |
| `ctrl+y` | copy the issue key (`PLAT-2099`, `tool#12`), or a PR's URL, to the clipboard; the help line confirms it |
| `ctrl+s` | cycle the order: `created`, `updated`, `key`, `attention` (remembered) |
| `space` (empty filter) | fold or unfold the ticket's PRs and children (on a PR, its ticket); a folded ticket shows `▸` at the left edge |
| `↑/↓`, `ctrl+p`/`ctrl+n` | move the cursor |
| PgDn/PgUp | move the cursor a page |
| `alt+↑`/`alt+↓`, Home/End | top or bottom of the list |
| `shift+↓`/`shift+↑`, mouse wheel over the preview | scroll the description |
| mouse wheel over the list | move the cursor |
| `f1` | open the panel with the options (`Order`; `PRs`: all, open, attention; `Rows`: two lines, one line; `Merged`: show, hide) and every key (`esc` closes it) |
| `shift+←`/`shift+→` | resize the list; the split is remembered (the list takes a quarter of the width by default) |
| click | select a row, on either of its lines (`enter` still opens it) |
| `esc`, `ctrl+c`, `q` with an empty filter | quit |

With a query the tree stays a tree: the tickets that match, with the parents
that lead to them kept dim as context and their PRs under them; the best
match comes first among its siblings, its stack on top, and the cursor
starts on it. Rows that match equally well stay in their usual order.

The order (`f1`, or `ctrl+s` to cycle it) applies to every level of the tree
on its own: `created` (newest first, the default), `updated`, `key`, or
`attention` (what needs you first, then blocked, in progress, to do). The
PRs shown are an option too: all, open only, or only the ones that need
something; so are the lines a row takes, and whether the merged work shows
(`Merged: hide` leaves out the merged PRs and the tickets with nothing left
under them, so the list is what is not finished). All four are remembered.

Pasting into the filter (a terminal paste or `ctrl+v`) filters like typing
does. A query of spaces only, or a bare `~` or `'`, is not a query yet: the
list stays as it is and the cursor does not move.

## Behavior notes

- Jira: one search per stack (`assignee = currentUser() AND
  statusCategory != Done ORDER BY updated DESC`), paginated up to 500
  tickets. Cloud uses `POST /rest/api/2/search/jql`, Server
  `POST /rest/api/2/search`; v2 is used on purpose so descriptions arrive
  as wiki markup, which is converted to Markdown for the preview.
- GitHub: the open issues assigned to you under the stack's owners, up to 500.
  Archived repositories are left out. The key is `repo#number`, and
  `owner/repo#number` when two owners have a repository with the same name.
  An issue's parent (a sub-issue's) and its parent's parent come along.
- Jira: a ticket's parent (the story of a sub-task, the epic of a story)
  comes with the search; the parents that are not yours are fetched with
  one more request per level, three at most. Jira Server/DC only has a
  parent for sub-tasks (an epic is an Epic Link field there), so the tree
  stops at the story.
- Pull requests: three searches per stack under its owners, the open PRs you
  are involved in, your PRs merged in the last 30 days (so a ticket whose PR
  just merged still shows it; closed-unmerged PRs are never fetched) and the
  open PRs whose review is asked of you. Whether a PR is yours is the login
  `gh` is signed in as. GitHub computes a PR's merge state lazily; an
  unknown one keeps the last known while the branches did not move.
- Within a stack, tickets are listed newest created first (descending key
  number within a project or repository) unless the order says otherwise;
  a ticket's PRs open first, then newest activity first.
- Everything is cached stale-while-revalidate in the plugin state dir: the
  popup renders instantly from the last snapshot while the trackers refresh
  concurrently in the background (skipped entirely when the snapshot is
  under 60s old). If a source fails (offline, expired token, `gh` logged
  out) its cached issues or PRs stay listed, the error takes the help line
  until the next key, the edge over the filter keeps a red `refresh failed`
  mark, and the snapshot is revalidated again on the next open.
- A configuration file that cannot be read keeps the popup from starting: the
  message stays on screen until `enter` (from a shell it is a plain error).
- A ticket's state is written, never told by the key's color: on the
  second line of its row (`[In Progress]` after the key with one-line rows)
  and in the preview, green while in progress, dim for to-do, red when
  blocked. In Jira that comes from the status category, and from a status
  name that contains "block". In GitHub it comes from the labels: one that
  contains "block", or one like `in progress`, `doing` or `wip`. What the
  ticket's PRs need from you is a different thing, written after it on the
  same line.

## Development

```bash
go build -o asgotoissues .          # local build (plugin runs ./asgotoissues from the repo root)
./asgotoissues -dump                # print stacks and issues (no TTY; refreshes when stale)
./asgotoissues -dump -query cart    # the matches and their scores instead of the list
./asgotoissues -dump -show PLAT-2099 # print an issue's description as Markdown
./asgotoissues -dump -order attention # the tree in that order (not saved)
./asgotoissues -version             # print the embedded version
go vet ./... && go test ./...
scripts/pty-check.py ./asgotoissues   # end-to-end TUI check on a pty (python3 + pyte)
herdr plugin link "$PWD"   # register the working copy (no build step)
```

Runtime state (`issuecache.json`, the divider's `split-columns`) lives in
`HERDR_PLUGIN_STATE_DIR`; standalone runs use the same directory
(`~/.local/state/herdr/plugins/asumaran.asgotoissues/`).

`ASGOTOISSUES_CONFIG` points at another configuration file.
`ASGOTOISSUES_OPENER` replaces the browser opener (a command line; the URL is
appended) and `ASGOTOISSUES_CLIPBOARD`
replaces the clipboard command (`pbcopy` on macOS, else `wl-copy`, `xclip` or
`xsel`); the tests use them to capture the URL and the copied key.
`ASGOTOISSUES_POPUP_WIDTH` / `ASGOTOISSUES_POPUP_HEIGHT` override the popup
size from the manifest.

## Releasing

`scripts/release.sh <X.Y.Z>` gates on a clean tree + green vet/build/test,
generates the CHANGELOG entry from commit subjects, syncs the manifest
version, commits, tags and publishes the GitHub release; CI then attaches
the `asgotoissues-<os>-<arch>` binaries (macOS and Linux, arm64 and amd64), the assets `fetch-binary.sh` downloads on installs.
