# CLAUDE.md

Guidance for working in this repository.

## What this is

`gotojira` is a herdr plugin popup that lists the Jira tickets assigned to
the user (open, not Done) across every stack configured in
`~/.claude/asdev.local.md`, grouped by stack, with fuzzy search and a
glamour-rendered preview of the description (wiki markup → Markdown).
Selecting a ticket opens it in the browser. Open, pick, exit — same
lifecycle as `gotopr` and `herdr-goto`, which this repo is modeled on.

Distributed as a herdr plugin (`herdr plugin install asumaran/gotojira`; the
manifest's `[[build]]` runs `scripts/fetch-binary.sh`). Each GitHub Release
attaches `gotojira-darwin-arm64`. There is no published library.

## Stack & layout

Go single module, single `package main`, static binary. TUI: Bubble Tea v2 +
bubbles v2 (`textinput`, `viewport`, `key`, `help`), lipgloss v2,
`sahilm/fuzzy` for matching, glamour v2 for the preview, `gopkg.in/yaml.v3`
for the config. The charm v2 modules are imported under their canonical
`charm.land/<name>/v2` paths (the `github.com/charmbracelet/<name>/v2`
spelling is rejected by `go get`).
Files are split by concern but everything stays in `package main`:

- `main.go` — flags (`-version`, `-dump`, `-query`, `-show`), model
  construction, `tea.NewProgram`, post-quit browser open, `runDump`.
- `config.go` — front-matter extraction from `asdev.local.md`, stack parsing
  (config order preserved via a `yaml.Node` walk), netrc + env credentials.
- `jira.go` — per-stack search (cloud `/rest/api/2/search/jql` with
  `nextPageToken`, server `/rest/api/2/search` with `startAt`), issue
  mapping, `mergeStacks` (fresh-or-cached per stack), tea.Cmd plumbing.
- `wiki.go` — Jira wiki markup → Markdown (headings, lists, code/noformat/
  quote blocks, tables, links, mono/bold/italic, mentions).
- `cache.go` — state dir resolution, `issuecache.json` load/save, 60s
  freshness debounce.
- `filter.go` — entries, corpora, fuzzy hits, `matchBonus` ranking, row
  building, header-skipping navigation.
- `ui.go` — the bubbletea model/Update/View, styles, `openInBrowser`.
- `preview.go` — glamour rendering as a `tea.Cmd`, per-(URL,width,updated)
  render cache, instant non-glamour header.

## Build & run

```bash
go build -o gotojira .    # plugin runs ./gotojira from the repo root
./gotojira -dump          # stacks + tickets, no TTY (refreshes when stale)
./gotojira -dump -query x # additionally prints filter scores
./gotojira -dump -show KEY # prints the wiki → Markdown conversion of KEY
go vet ./... && go test ./...
scripts/pty-check.py ./gotojira   # end-to-end TUI check on a pty (python3 + pyte)
herdr plugin link ~/Developer/gotojira   # link does NOT run [[build]]; go build yourself
```

Keybinding (user config): `prefix+t` / `ctrl+alt+t` → `plugin_action`
`asumaran.gotojira.open` → `scripts/open-pane.sh` → `herdr plugin pane open`.

## Behaviour / decisions

- **Config source is asdev's file, on purpose**: one place to declare a
  stack's Jira site for both the Claude plugin and this picker. Only
  `stacks.<name>.jira.{base_url,type,email,api_token_env,username}` is read.
- **Auth**: `~/.netrc` (by host) beats the env var, because the plugin
  process may not inherit shell rc exports. Cloud = basic (email + token);
  server = bearer PAT unless `username` is set.
- **API v2, not v3**: v3 returns descriptions as ADF JSON; v2 returns wiki
  markup, which `wiki.go` converts well enough for a preview. `currentUser()`
  works with basic auth on Cloud (it does not with some server PAT setups;
  the asdev skill uses `account_id` for that reason, but here the query is
  fixed and the user is always the token owner).
- **Caching**: stale-while-revalidate; first frame always renders from
  `issuecache.json`; snapshots fresher than 60s skip the refresh. Stacks
  refresh concurrently (one tea.Cmd each); a failed stack keeps its cached
  tickets and the snapshot's `FetchedAt` is NOT advanced, so the next open
  retries. Errors surface dimmed in the footer, never as a modal.
- **Selection is deliberately just "open in browser"** (`open` on macOS,
  `xdg-open` elsewhere, `GOTOJIRA_OPEN_CMD` override), executed after quit
  because quitting closes the popup. Jumping to a checkout / creating a
  worktree for a ticket was considered and rejected for v1.
- **Never query the terminal behind bubbletea's back**: `Init` issues
  `tea.RequestBackgroundColor()` and the `tea.BackgroundColorMsg` reply picks
  the glamour style ("dark"/"light"). Frames before the reply use "dark";
  when the style flips, the render cache is dropped and the current preview
  re-renders. Don't call glamour's `WithAutoStyle` or lipgloss's
  `HasDarkBackground` from inside the program: the reply races bubbletea's
  input reader and ends up typed into the filter as literal "rgb:..." text.
- **View**: `View()` returns a `tea.View` built from `render()`, which holds
  the frame text and is what the tests assert on. The alt screen is declared
  per frame there; there is no `tea.WithAltScreen` program option in v2.
- Ordering: stacks in config order; tickets within a stack by `created`
  desc (`newerIssue`), falling back to key number, then `updated`.
- Search corpus: summary + key/number + status/type/project/stack/parent
  metas; exact key +30, exact number +20, key prefix with dash +10, exact
  stack/project +10, key hit +2, ties broken by newer `updated`.

## Testing

Unit tests cover the pure logic (config parsing, netrc, time parsing,
merge fallback, wiki conversion, ranking, grouping, key handling, partial
refresh failure, View content). `TestMain` points `HERDR_PLUGIN_STATE_DIR`
at a temp dir so tests never touch the real cache. For end-to-end TUI
verification without a TTY, drive the binary in a pty (answer OSC 10/11 +
CSI 6n + DA1 queries, replay keystrokes, set `GOTOJIRA_OPEN_CMD` to a script
that logs argv) — see herdr-goto's `scripts/demo/driver.py`.

For end-to-end verification without a TTY, `scripts/pty-check.py ./gotojira`
(python3 + `pyte`) spawns the binary on a pty, answers the terminal queries,
replays keystrokes and asserts on pyte-rendered frames, in a throwaway sandbox
(fake `HOME`, synthetic `GOTOJIRA_CONFIG`, empty netrc, a fresh synthetic
ticket cache so nothing is fetched, a logging stub as `GOTOJIRA_OPEN_CMD`). The v2 renderer repaints with scroll regions, which pyte ignores, so the
driver forces a full redraw (pty resize + SIGWINCH) before reading a frame.

## Commits & branches

- Conventional Commits: `type(scope): description`.
- Never mention AI tooling in commits, PRs, or any repo-visible text.
- Default branch is `main`. Don't commit, tag, or push unless explicitly
  asked (releasing is an explicit, separate request).

## Releasing

`scripts/release.sh <X.Y.Z>` — clean-tree + vet/build/test gate, CHANGELOG
generation from commit subjects, manifest version sync, commit + tag + GitHub
release; CI (`.github/workflows/release.yml`) attaches `gotojira-darwin-arm64`.
Releasing never touches the linked plugin's `./gotojira`; rebuild locally to
keep testing dev code.
