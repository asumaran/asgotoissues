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
sub-task from its story, the story from its epic), whatever the depth, drawn
with guides from the parent's fold arrow (`├── ▼ shop-62`). A parent that
is not assigned to you (an epic of someone else's, a Done one) is fetched all
the same, so your tickets sit where they belong; its details say `not
yours`. Under each ticket come, as a list, the pull requests that name it (in
the branch, the title or the closing keywords; a key merely mentioned in the
body counts only when nothing else links), one `○ repo#N` per PR, whatever
repo it lives in, and a PR that names several tickets appears under each.
Your PRs that name no ticket close their stack under `PRs without a ticket`.

```
▼ acme
 ▼ shop-51 Corregir los datos estructurados de todas las marcas
 │     in progress · 1w
 ├── ▼ shop-62 Emitir las migas de pan desde la plantilla de categoría
 │   │     in progress · today
 │   │   ○ web-app#8233 Migas de pan: la plantilla genera la lista
 │   │     draft · ci failed · to answer · today
 │   └──── shop-66 Definir qué esquema manda cuando el CMS trae uno
 │             blocked · no PR · 2w
```

A ticket or a PR takes two lines: its key (in its level's color) and its
title, then its details, most important first and the age of its last
activity last. A ticket says its own status (green in progress, red blocked,
blue to do), `no PR` when it is started and has neither PRs nor children,
`not yours` for a parent that is not yours. A PR says its state (`in
review`, `draft`, `approved`, `changes requested`, `merged`), what it needs
from you in red (`conflicts`, `ci failed`, `to answer`: a review comment
waits for your reply, `base merged`: it sits on a branch that was merged,
retarget it, `review requested`: of you), what your checkout of its branch
holds that GitHub has not seen, the way the shell prompt counts it (`↑2`
commits to push, `+1` staged, `!3` unstaged, `?1` untracked), and the facts
(`behind master`: the base moved, `stacked on #8027`, `by someone`). Nothing
is inherited: a ticket never repeats what its PRs or its children say.

The titles are short Spanish summaries, 6 to 14 words, a child's written as
a part of its parent's goal so the tree reads top-down (the preview keeps
the full title). They are written by Claude Haiku through the Claude Code
CLI (`claude -p`) in the background, only for new or edited items, and
cached; until one arrives the row shows the tracker's title. This sends the
titles and the start of the descriptions of your tickets and PRs to
Anthropic through your Claude Code account: set `Titles: original` in the
panel (`f1`) to never do it.

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
| `ctrl+t` | cycle what is listed: `working`, `pending`, `all` (remembered) |
| `ctrl+g` | group as a tree or by phase (remembered) |
| `ctrl+l` | switch the layout: preview beside the list (columns) or under it (rows); remembered |
| `space` (empty filter) | fold or unfold the row: a ticket, a stack, the PRs without a ticket, a phase (on a PR, its ticket); remembered between runs |
| `shift+tab` / `tab` | fold the whole tree one level shallower / deeper: all, then down to the roots alone, and back; remembered too |
| `↑/↓`, `ctrl+p`/`ctrl+n` | move the cursor |
| PgDn/PgUp | move the cursor a page |
| `alt+↑`/`alt+↓`, Home/End | top or bottom of the list |
| mouse wheel over the preview | scroll the description |
| mouse wheel over the list | move the cursor |
| `f1` | open the panel with the options (`Order`; `PRs`: all, open, attention; `Show`; `Group`; `Rows`: two lines, one line; `Titles`: short, original; `Layout`: columns, rows) and every key (`esc` closes it) |
| `shift+←`/`shift+→` (columns), `shift+↑`/`shift+↓` (rows) | resize the list of the current layout; each layout's split is remembered on its own (the list takes a quarter of the width by default in columns, half the body in rows) |
| click | select a row, on either of its lines (`enter` still opens it) |
| `esc`, `ctrl+c`, `q` with an empty filter | quit |

With a query the tree stays a tree: the tickets that match, with the parents
that lead to them kept dim as context and their PRs under them; the best
match comes first among its siblings, its stack on top, and the cursor
starts on it. Rows that match equally well stay in their usual order.

The order (`f1`, or `ctrl+s` to cycle it) applies to every level of the tree
on its own: `created` (newest first, the default), `updated` (last activity,
a ticket's PRs included), `key`, or `attention` (what needs you first, then
blocked, in progress, to do). What is listed is an option too (`ctrl+t`):
`working` (the default: tickets in progress or with an open PR, their
parents as containers, and the open PRs), `pending` (everything not
finished: blocked and not started tickets too) or `all`. By phase (`ctrl+g`)
the list is a section per PR state, `PR in review`, `draft PR`, `PRs merged`,
each PR with the path of its tickets on one line (`shop-51 › shop-62 ›
web-app#8233`), and `no PR` with the tickets that have none. The PRs
shown (all, open, the ones that need something), the lines a row takes and
the titles are options as well. All are remembered.

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
- A ticket's status is written in lower case and colored by its category:
  green in progress, red blocked, blue to do. In Jira that comes from the
  status category, and from a status name that contains "block". In GitHub
  it comes from the labels: one that contains "block", or one like `in
  progress`, `doing` or `wip`; the word is then `in progress`, `blocked` or
  `open`.
- `behind <base>` and `to answer` take one more query per refresh (the
  commits a head lacks from its base cannot ride in the search); if it
  fails, the rows just do not say it.
- Your checkouts are found under `~/Developer` (each repo's worktrees, so
  `~/wt/...` too), matched by the origin's owner/repo and the branch;
  `ASGOTOISSUES_CHECKOUTS` (paths joined by `:`) replaces the roots.

## Development

```bash
go build -o asgotoissues .          # local build (plugin runs ./asgotoissues from the repo root)
./asgotoissues -dump                # print stacks and issues (no TTY; refreshes when stale)
./asgotoissues -dump -query cart    # the matches and their scores instead of the list
./asgotoissues -dump -show PLAT-2099 # print an issue's description as Markdown
./asgotoissues -dump -order attention # the tree in that order (not saved)
./asgotoissues -dump -summarize     # write the missing short titles first
./asgotoissues -version             # print the embedded version
go vet ./... && go test ./...
scripts/pty-check.py ./asgotoissues   # end-to-end TUI check on a pty (python3 + pyte)
herdr plugin link "$PWD"   # register the working copy (no build step)
```

Runtime state (`issuecache.json`, `summaries.json`, the dividers
(`split-columns`, `split-rows`), the folded rows and the `tab`/`shift+tab`
level (`folds.json`), the options) lives in
`HERDR_PLUGIN_STATE_DIR`; standalone runs use the same directory
(`~/.local/state/herdr/plugins/asumaran.asgotoissues/`).

`ASGOTOISSUES_CONFIG` points at another configuration file.
`ASGOTOISSUES_OPENER` replaces the browser opener (a command line; the URL is
appended) and `ASGOTOISSUES_CLIPBOARD`
replaces the clipboard command (`pbcopy` on macOS, else `wl-copy`, `xclip` or
`xsel`); the tests use them to capture the URL and the copied key.
`ASGOTOISSUES_POPUP_WIDTH` / `ASGOTOISSUES_POPUP_HEIGHT` override the popup
size from the manifest. `ASGOTOISSUES_SUMMARIZER` replaces the command that
writes the short titles (it reads `{"items": [...]}` on stdin and answers a
JSON list of `{"id", "summary"}`), `ASGOTOISSUES_NO_SUMMARIES` turns it off,
and `ASGOTOISSUES_CHECKOUTS` sets where your checkouts are looked for.

## Releasing

`scripts/release.sh <X.Y.Z>` gates on a clean tree + green vet/build/test,
generates the CHANGELOG entry from commit subjects, syncs the manifest
version, commits, tags and publishes the GitHub release; CI then attaches
the `asgotoissues-<os>-<arch>` binaries (macOS and Linux, arm64 and amd64), the assets `fetch-binary.sh` downloads on installs.
