# CLAUDE.md

Guidance for working in this repository.

## What this is

`asgotoissues` is a herdr plugin popup that lists the user's open issues (Jira
tickets and GitHub issues) across every stack configured in
`~/.claude/asdev.local.md`, grouped by stack, with fuzzy search and a
glamour-rendered preview of the description. Selecting an issue opens it in
the browser. Open, pick, exit: the same lifecycle as `asgotopr` and `asgoto`,
which this repo is modeled on.

Distributed as a herdr plugin (`herdr plugin install asumaran/asgotoissues`; the
manifest's `[[build]]` runs `scripts/fetch-binary.sh`). Each GitHub Release
attaches the `asgotoissues-<os>-<arch>` binaries (macOS and Linux, arm64 and amd64). There is no published library.

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
  (config order preserved via a `yaml.Node` walk), which trackers a stack
  lists (`issues:`), netrc + env credentials for Jira.
- `provider.go` — the tracker-neutral side: the `issue` struct, the
  `provider` interface (`kind`, `fetch`), `sourcesOf` (one source per tracker
  of a stack), `mergeStacks` (fresh-or-cached per source), tea.Cmd plumbing.
- `jira.go` — the Jira provider: per-stack search (cloud
  `/rest/api/2/search/jql` with `nextPageToken`, server `/rest/api/2/search`
  with `startAt`), issue mapping.
- `github.go` — the GitHub provider: issue searches through `gh api graphql`
  (`ghRun` is the seam tests replace), issue mapping, state from labels.
- `wiki.go` — Jira wiki markup → Markdown (headings, lists, code/noformat/
  quote blocks, tables, links, mono/bold/italic, mentions).
- `cache.go` — state dir resolution, `issuecache.json` load/save, 60s
  freshness debounce.
- `filter.go` — entries, corpora, fuzzy hits, `matchBonus` ranking, row
  building, header-skipping navigation.
- `match.go` — `findTight`, the fuzzy matcher with one correction: it is
  greedy (first candidate for each rune, left to right), so a query that
  occurs in one piece could still match scattered letters before it. When the
  query occurs whole, that occurrence is the match, for the highlight and for
  the score. The same file in every tool of the family.
- `text.go` — `truncate`, `padRight`, `padLeft`: fitting text, styled or not,
  into cells. The same file in every tool of the family.
- `statedir.go` — `stateDirFor`: the state dir herdr injects, or a fixed path
  under the config home when the tool runs on its own. The same file in every
  tool of the family that keeps state.
- `age.go` — `compactAge` (`5m`, `3h`, `2d`, `6w`, `2y`) for a list column and
  `relTime` (`3h ago`) for a sentence. The same file in every tool of the
  family that shows an age.
- `markdown.go` — `renderMarkdown` (glamour with a fixed style, never
  auto-detected), `glamourStyle` and `setPreviewStyle`. The same file in every
  tool of the family that renders Markdown.
- `listmouse.go` — `inList`, `rowUnder`, `wheelKey`: the mouse over the list.
  The wheel goes through the same code as the arrows; a click moves the
  cursor and never opens anything. The same file in every tool of the family.
- `prompt.go` — the filter input: its prompt (with the tool's name only outside
  herdr's popup), the placeholder, the `(dev)` mark on the edge over the
  input. The same
  file in every tool of the family.
- `helpfoot.go` — the help at the foot: the key that expands it, its height
  and its lines cut to the width. The same file in every tool of the family.
- `listnav.go` — `listNav`: the keys that move the cursor through a list and
  where each one takes it, group headers skipped. The same file in every tool
  of the family.
- `highlight.go` — `highlight`/`highlightFrom`, `matchOver`, `onSel`,
  `selPad` and the `stSel`/`stMatch` styles: how a match and the selected row
  look. The same file in every tool of the family.
- `frame.go` — the single-frame layout shared by the family: `hline`, `fit`,
  `framed`, `frameHead`, `splitMain`, `scrollPos` and the section rows (`mainY`,
  `listY`, `frameRows`, each with or without the optional context line).
- `split.go` — the divider between the list and the preview: `loadSplit`,
  `saveSplit`, `stepSplit`, `splitWidths`. The file is copied, not imported:
  the same one ships in asgotochanged, asgotosession, asgotonotes and asgotopr (all
  under github.com/asumaran), and there is no shared library. A pull request
  only needs to change it here; the maintainer ports the change to the other
  copies.
- `ui.go` — the bubbletea model/Update/View, styles, `openInBrowser`.
- `preview.go` — glamour rendering as a `tea.Cmd`, per-(URL,width,updated)
  render cache, instant non-glamour header.

## Build & run

```bash
go build -o asgotoissues .    # plugin runs ./asgotoissues from the repo root
./asgotoissues -dump          # stacks + issues, no TTY (refreshes when stale)
./asgotoissues -dump -query x # additionally prints filter scores
./asgotoissues -dump -show KEY # prints the wiki → Markdown conversion of KEY
go vet ./... && go test ./...
scripts/pty-check.py ./asgotoissues   # end-to-end TUI check on a pty (python3 + pyte)
herdr plugin link "$PWD"   # link does NOT run [[build]]; go build yourself
```

Keybinding (user config): `prefix+t` / `ctrl+alt+t` → `plugin_action`
`asumaran.asgotoissues.open` → `scripts/open-pane.sh` → `herdr plugin pane open`.

## Behaviour / decisions

- **Layout**: one rounded frame of sections split by shared edges, the layout
  asgitlog introduced and every picker of the family follows (`frame.go`, the
  same file in each repo): the filter input (the border over it carries the
  refresh mark), the main section (list and preview split by a divider; its bottom edge carries the
  matches/total counter under the list and, while the preview overflows, its
  scroll position on the right), and the help. A context line on top is only for what the
  rest of the screen cannot say (asgitlog: repo and branch); a title is not
  context, so there is none here. The stacks already head their groups in the
  list. The list starts on screen row `listY`, one cell in from the left side,
  which is what the click-to-row math uses. Errors and notices take the help
  line.
- **Moving through the list** is the same in every tool of the family and
  comes from `listnav.go` (the same file in each repo): arrows or
  `ctrl+p`/`ctrl+n` a row, `pgup`/`pgdn` a page, `alt+↑`/`alt+↓` or
  `home`/`end` the ends. `home`/`end` are taken from the filter input's caret
  on purpose (`←`/`→` and `ctrl+e` still move it). The preview scrolls with
  `shift+↑`/`shift+↓` only. The keys are listed in the expanded help.
- **The filter input** comes from `prompt.go` (the same file in every tool of
  the family). Inside herdr's popup the prompt is the arrow alone, because the
  pane's title (`[[panes]] title` in the manifest, the tool's name) already
  says which tool it is, and a placeholder says what the filter searches. Run
  on its own the prompt carries the tool's name. A build that is not a release
  says `(dev)` on the edge over the input, never inside the
  prompt.
  herdr sets `HERDR_PLUGIN_ENTRYPOINT_ID` for a plugin pane; that is how the
  two cases are told apart.
- **Help**: the line at the foot shows the tool's own actions, `? help` and the
  quit keys; `?` expands it into every key in columns and the main section
  gives way (`helpfoot.go`, the same file in every tool of the family). `?`
  expands only while the filter is empty, otherwise it is text, like `q`;
  `f1` always does; `esc` folds the help before it quits. Moving, scrolling
  and resizing live in the expanded help only, so the folded line stays short
  enough for a narrow popup. A message (error, notice) takes the help's place
  on one line.
- **Filter matches** look the same in every tool of the family and come from
  one place, `highlight.go` (the same file in each repo; it also owns `stSel`
  and `stMatch`): a match is the match color plus an underline on top of the
  style the text already has, and the selected row shows them too. That row
  is never one big `stSel.Render` around styled text, because the reset that
  ends a match would cut the background: every piece is rendered over `stSel`
  (`highlight(s, idx, stSel)`) and `selPad` fills the rest. Do not write a
  local highlighter.
- **Resizable list**: `shift+←/→` move the divider in 5% steps, as in
  asgitlog. The setting is the PREVIEW's share of the width, clamped to
  30-85 and saved as `split-columns` in the state dir; the default is 75
  (list 25%, preview 75%), the same in every picker of the family. Rows
  must degrade for a narrow list instead of truncating their last columns.
  `↑/↓` are left out of the help line so `esc/q quit` still fits next to
  `resize`.
- **Mouse**: the wheel follows the pointer, as in asgitlog: over the list
  (`overList`) it moves the selection through the same code as the arrow keys,
  anywhere else it scrolls the description. A left click on an issue row moves
  the selection and never opens anything.
- **Providers**: a tracker is a `provider` that returns normalized `issue`s.
  Whatever is tracker-specific is settled at fetch time and stored on the
  issue: `State` (todo, doing, blocked) drives the colors, `Meta` holds the
  preview's meta parts when the Jira fields do not cover them, `BodyFormat`
  says whether the description is wiki markup or already Markdown. The UI,
  the filter and the preview never ask which tracker an issue came from. To
  add a tracker: implement `provider`, give it a kind, handle the kind in
  `sourcesOf` and `stackTrackers`.
- **The cache file is a contract**: `issue`'s JSON tags are the format of
  `issuecache.json` and of the fixtures in `scripts/pty-check.py`. New fields
  are `omitempty` and an issue without them must keep working (no `Source`
  means Jira, no `State` falls back to the Jira status fields), so a snapshot
  written by an older version still renders.
- **Config source is asdev's file, on purpose**: one place to declare a
  stack for both the Claude plugin and this picker. Only
  `stacks.<name>.issues`, `stacks.<name>.jira.{base_url,type,email,api_token_env,username}`
  and `stacks.<name>.github.{org,orgs}` are read. Without `issues:` a stack
  lists Jira when it has a `jira.base_url`, so a config that only knows about
  Jira needs no changes.
- **GitHub goes through `gh`**, like asgotopr: no token handling here, and
  the user is whoever `gh` is logged in as. A stack lists the open issues
  assigned to that user under its owners (`assignee:@me`), one search for
  all of them.
- **Jira auth**: `~/.netrc` (by host) beats the env var, because the plugin
  process may not inherit shell rc exports. Cloud = basic (email + token);
  server = bearer PAT unless `username` is set.
- **Jira API v2, not v3**: v3 returns descriptions as ADF JSON; v2 returns wiki
  markup, which `wiki.go` converts well enough for a preview. `currentUser()`
  works with basic auth on Cloud (it does not with some server PAT setups;
  the asdev skill uses `account_id` for that reason, but here the query is
  fixed and the user is always the token owner).
- **Caching**: stale-while-revalidate; first frame always renders from
  `issuecache.json`; snapshots fresher than 60s skip the refresh. Sources
  (one per tracker of a stack) refresh concurrently, one tea.Cmd each; a
  failed source keeps its cached issues and the snapshot's `FetchedAt` is NOT advanced, so the next open
  retries. Errors take the help line, never a modal; the refresh mark sits
  on the edge over the input.
- **Selection is deliberately just "open in browser"** (`open` on macOS,
  `xdg-open` elsewhere, `ASGOTOISSUES_OPENER` override), executed after quit
  because quitting closes the popup. Jumping to a checkout / creating a
  worktree for an issue was considered and rejected for v1.
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
- Ordering: stacks in config order; issues within a stack by `created`
  desc (`newerIssue`), falling back to key number, then `updated`.
- Search corpus: summary + key/number + status/type/project/stack/parent
  and the provider's meta parts (repo, labels); exact key +30, exact number
  +20 (the part after the last `-` or `#`), key prefix with `-` or `#` +10, exact
  stack/project +10, key hit +2.
- **A query makes the list a search result**: tickets are ranked, best match
  first, the stack that holds it on top with its tickets kept together, and
  the cursor sits on the first one (`rank` in `rank.go`, the same file in
  every picker of the family). Equal scores go to the newer `updated`. A score
  says how good the match is and nothing about the length of the text
  (`match.go`). Without a query the order is the one above.

## Testing

Unit tests cover the pure logic (config parsing, netrc, time parsing,
merge fallback per source, the GitHub provider against a fake `ghRun`,
issues cached by older versions, wiki conversion, ranking, grouping, key handling, partial
refresh failure, View content). `TestMain` points `HERDR_PLUGIN_STATE_DIR`
at a temp dir so tests never touch the real cache. For end-to-end TUI
verification without a TTY, drive the binary in a pty (answer OSC 10/11 +
CSI 6n + DA1 queries, replay keystrokes, set `ASGOTOISSUES_OPENER` to a script
that logs argv) — see asgoto's `scripts/demo/driver.py`.

For end-to-end verification without a TTY, `scripts/pty-check.py ./asgotoissues`
(python3 + `pyte`) spawns the binary on a pty, answers the terminal queries,
replays keystrokes and asserts on pyte-rendered frames, in a throwaway sandbox
(fake `HOME`, synthetic `ASGOTOISSUES_CONFIG`, empty netrc, a fresh synthetic
ticket cache so nothing is fetched, a logging stub as `ASGOTOISSUES_OPENER`). The v2 renderer repaints with scroll regions, which pyte ignores, so the
driver forces a full redraw (pty resize + SIGWINCH) before reading a frame.

## Commits & branches

- Conventional Commits: `type(scope): description`.
- Never mention AI tooling in commits, PRs, or any repo-visible text.
- Default branch is `main`. Don't commit, tag, or push unless explicitly
  asked (releasing is an explicit, separate request).

## Releasing

`scripts/release.sh <X.Y.Z>` — clean-tree + vet/build/test gate, CHANGELOG
generation from commit subjects, manifest version sync, commit + tag + GitHub
release; CI (`.github/workflows/release.yml`) attaches the `asgotoissues-<os>-<arch>` binaries (macOS and Linux, arm64 and amd64).
Releasing never touches the linked plugin's `./asgotoissues`; rebuild locally to
keep testing dev code.
