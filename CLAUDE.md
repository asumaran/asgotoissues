# CLAUDE.md

Guidance for working in this repository.

## What this is

`asgotoissues` is a herdr plugin popup that lists the user's open issues (Jira
tickets and GitHub issues) across every stack configured in
`~/.claude/asdev.local.md`, grouped by stack as a tree (a sub-task under its
story, the story under its epic, even when the epic is not the user's), with
the pull requests linked to each ticket and what each one needs (conflicts,
changes requested, a review, a merge), with fuzzy search and a
glamour-rendered preview of the description. Selecting a row opens it in the
browser. Open, pick, exit: the same lifecycle as `asgotopr` and `asgoto`,
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

- `main.go`: flags (`-version`, `-dump`, `-query`, `-show`, `-order`,
  `-summarize`), model construction, `tea.NewProgram`, post-quit browser
  open, `runDump` (it writes to an `io.Writer`, so the tests read what
  `-dump` prints: the list as the popup draws it, every ticket and PR, in
  plain text, with the local counters read synchronously), `summarizeAll`.
- `asdevconfig.go`: reading `asdev.local.md`: front-matter extraction,
  every stack in config order (a `yaml.Node` walk), netrc + env credentials
  for Jira (`resolveCredential`). The same file in every tool of the family
  that reads the asdev config (asmeta too).
- `config.go`: which trackers a stack lists (`issues:`, `parseStacks`,
  `issueStacks`); `ASGOTOISSUES_CONFIG` names another config file.
- `provider.go`: the tracker-neutral side: the `issue` struct (with its
  parent's key, summary and URL, and `Ghost` for one fetched as context), the
  `provider` interface (`kind`, `fetch` returning `fetched`: issues or PRs),
  `sourcesOf` (one source per tracker of a stack, plus the pulls source of a
  stack with GitHub owners), `mergeStacks` and `mergePulls` (fresh-or-cached
  per source), `backfillParentURLs` (an old snapshot's parents get their
  URL), tea.Cmd plumbing.
- `jira.go`: the Jira provider: per-stack search (cloud
  `/rest/api/2/search/jql` with `nextPageToken`, server `/rest/api/2/search`
  with `startAt`), issue mapping, and the parents the list does not hold
  fetched with `key in (…)` level by level (`ghosts`, three levels at most).
- `github.go`: the GitHub provider: issue searches through `gh api graphql`
  (`ghRun`, in `ghrun.go`, is the seam tests replace; `ghSearch` pages any
  node type), issue mapping, state from labels, the parent and the parent's
  parent nested in the query and turned into ghosts.
- `pull.go`: the `pull` struct (the cache's format for a PR), what its row
  says about it, each word with a level (attention) and a tone (its color):
  `state` (merged, closed, draft, changes requested, approved, in review),
  `needs` (conflicts, ci failed, to answer, base merged, review requested)
  and `facts` (behind <base>, stacked on #N, ci pending, by author);
  `attention` is the worst of them. `pullRefs` (the tickets and issues a PR
  names: strong in the branch, the title and the closing references, weak
  in the body).
- `pulls.go`: the pulls source: three searches per stack under its owners
  (open PRs I am involved in, my PRs merged in the last 30 days, and the
  URLs of the PRs whose review is asked of me), the viewer's login for
  `Mine`, node mapping (`ToAnswer`: a review thread not resolved whose last
  comment is not mine), `ghBehind` (how many commits of its base an open PR
  lacks: `compare(headRef:)` takes the head as an argument, so one aliased
  query per chunk of PRs after the searches; its failure leaves zero),
  `carryMergeable` (a merge state GitHub has not computed yet keeps the
  cached one while the branches did not move).
- `tree.go`: the list as a tree: `linkPulls` (a PR under every ticket of
  its stack it names), `grow` (the forest: nesting by the parent's URL,
  each node settled: its own level, `done`, `shown` under the show mode,
  its best score), `buildTree` (the rows: the guides of every row, PR rows
  under their ticket, the group of my PRs without a ticket, the query walk
  that keeps the ancestors of a hit, folded tickets, groups and stacks),
  `buildPhase` (the phase view), `foldLevel`/`deepest` (the folds of a
  level), the show, group, order and PRs modes, `ownLevel` (a ticket's
  level: its own PRs', nothing inherited).
- `local.go`: the local work on my open PRs: their checkouts under the
  roots (`ASGOTOISSUES_CHECKOUTS`, else `~/Developer`: each repo's
  worktrees, matched by the origin's owner/repo and the branch), read as
  the prompt counts them (`↑` commits on top of the PR's head, `+`
  staged, `!` unstaged, `?` untracked), in the background; `gitRun` is the
  seam the tests replace.
- `summary.go`: the short Spanish titles: `summaries.json` (by URL, with a
  hash of the title, the start of the body and the parent's summary),
  `pendingSummaries` (per stack, parents first, the summarized ancestors as
  context), the prompt, `summarize` (the CLI's JSON envelope or a bare
  array), `settleHashes`, the `Titles` option; `summarizeRun` runs
  `claude -p` with Haiku (`ASGOTOISSUES_SUMMARIZER` replaces it) and is
  the seam the tests replace.
- `setting.go`: `loadSetting`/`saveSetting`: a setting the tool remembers
  between runs, one plain-text file each in the state dir. The same file in
  every tool of the family that needs it.
- `wiki.go`: Jira wiki markup → Markdown (headings, lists, code/noformat/
  quote blocks, tables, links, mono/bold/italic, mentions).
- `cache.go`: `issuecache.json` (issues and pulls) load/save (through
  `jsonfile.go`), 60s freshness debounce, and `stateDir()`, a wrapper over
  `stateDirFor` (`statedir.go`).
- `filter.go`: entries (a ticket and its PRs), the row kinds (header,
  group, section, issue, pull) and what a row carries to be drawn (its
  guides, its path by phase), corpora (the title the row shows first, the
  tracker's with the metadata), fuzzy hits, `matchBonus` ranking;
  `buildRows` scores the hits and hands them to the tree.
- `match.go`: `findTight`/`tighten`, the fuzzy matcher with one correction: it
  is greedy (first candidate for each rune, left to right), so a query that
  occurs in one piece could still match scattered letters before it. When the
  query occurs whole, that occurrence is the match, for the highlight and for
  the score. `hasTerms` says whether a query searches for anything: spaces and
  a bare `~` or `'` do not, so they never filter, rank or move the cursor. The
  same file in every tool of the family.
- `text.go`: `truncate`, `padRight`, `padLeft`: fitting text, styled or not,
  into cells. `errorBlock` is an error for a preview: every line of it cut to
  the width, in the error color. The same file in every tool of the family.
- `statedir.go`: `stateDirFor`: the state dir herdr injects
  (`HERDR_PLUGIN_STATE_DIR`) or, when the tool runs on its own, the same
  directory worked out
  (`${XDG_STATE_HOME:-~/.local/state}/herdr/plugins/asumaran.asgotoissues`),
  so the popup and a run from the shell share settings and caches. The same
  file in every tool of the family.
- `age.go`: `compactAge` (`5m`, `3h`, `2d`, `6w`, `2y`) for a list column and
  `relTime` (`3h ago`) for a sentence. The same file in every tool of the
  family that shows an age.
- `markdown.go`: `renderMarkdown` (glamour with a fixed style, never
  auto-detected), `glamourStyle` and `setPreviewStyle`, plus the preview's
  side of a render: `previewMsg` (a finished render and the style it used),
  `showRender` (points the preview at a render and reports whether it has to
  be started), `clearPreview`, and `handlePreview` (keeps a finished render,
  shows it when it is still the one awaited, and drops one rendered before the
  style flipped). The same file in every tool of the family that renders
  Markdown.
- `listmouse.go`: `inList`, `rowUnder`, `rowOfLine`, `wheelKey`: the mouse
  over the list. The wheel goes through the same code as the arrows; a click
  moves the cursor and never opens anything (`rowOfLine` finds the row when
  rows take more than one line). The same file in every tool of the family.
- `prompt.go`: the filter input: its prompt (with the tool's name only outside
  herdr's popup), the placeholder, the `(dev)` mark on the edge over the
  input. `typeInto` hands a message to the input and reports whether the query
  changed: a key, a terminal paste and the input's own `ctrl+v` all edit it,
  and the caller filters again only when it did. The same file in every tool
  of the family.
- `helpfoot.go`: the line at the foot and the key that opens the panel.
  `footLine` is what the foot shows: a flash first, then a notice in the error
  color, else the help cut to the width; with a context (`info`, styled with
  `stInfo` and fitted to `footRoom`) the flash, the notice or the context on
  the left and the panel's key alone on the right (`panelHint`, taken from
  the tool's own `ShortHelp`). This tool has no context, so its foot is the
  help. The same file in every tool of the family.
- `panel.go`: the panel `f1` opens over the frame: options to change in
  place and every key under them (`option`, `panel`, `panelLines`,
  `overlay`). The same file in every tool of the family.
- `listnav.go`: `listNav`: the keys that move the cursor through a list and
  where each one takes it, group headers skipped. `scrollTo` keeps the cursor
  in view together with the row `withHeader` names: the header of its group
  when that is the row right above; `scrollSpan` is the same for rows that
  take more than one line (the offset is in lines). `emptyList` is what a list says instead of
  rows: the error that kept it from loading, in the error color, `No matches`,
  or the tool's own reason. The same file in every tool of the family.
- `highlight.go`: `highlight`/`highlightFrom`, `matchOver`, `onSel`,
  `selPad` and the `stSel`/`stMatch` styles: how a match and the selected row
  look. The same file in every tool of the family.
- `flash.go`: `flash`, `flashMsg`, `flashErrMsg`, `clearFlashMsg`: a word that
  takes the help line for a moment: a confirmation in green (`flash.set`), or
  a key that could do nothing (`nothing to copy`) in the error color
  (`flash.fail`). The same file in every tool of the family.
- `clipboard.go`: `copyCmd`: feeds a text to the system clipboard and reports
  it with a `flashMsg`, or with a `flashErrMsg` when there is nothing to copy
  or the copy fails; `ASGOTOISSUES_CLIPBOARD` replaces the command. The same
  file in every tool of the family.
- `border.go`: `hline`, `framed`, `fit`, `scrollPos`: the primitives the frame
  is drawn with (an edge with texts set into it, a line between the frame's
  sides, the position a scrolled viewport reports on an edge). `fitLines` is
  content as exactly so many lines of a width, and `popupView` is the
  `tea.View` every tool returns: the alt screen and, while the mouse is on,
  cell-motion mouse reports. The same file in every tool of the family.
- `fatal.go`: `fatal(tool, msg)`: an error that keeps the tool from starting.
  In herdr's popup the message is held until enter, because the pane closes
  with the process and takes stderr with it; in a shell it is plain stderr and
  exit 1. The same file in every tool of the family that needs it.
- `refreshmark.go`: `refreshMark`: what the edge over the input says about a
  list that is fetched in the background and shown from a cache meanwhile:
  `refreshing…` while the fetch runs and, after one that failed, a standing
  `refresh failed` in the error color. The same file in every tool of the
  family that needs it.
- `rank.go`: `rank`: with a query the list is a search result, best score
  first; in a grouped list the groups go by their best item and keep their
  items together, and equal scores keep the list's own order. The same file in
  every tool of the family that ranks its matches.
- `openurl.go`: `openURL`: hands a URL to the browser. On macOS a Chrome that
  is already up gets a new tab in its front window, else `open`; `xdg-open`
  elsewhere; `ASGOTOISSUES_OPENER` (`opener.go`) replaces all of it. The same
  file in every tool of the family that opens one.
- `opener.go`: `openerArgv`: the command `<TOOL>_OPENER` names, as words, or
  nothing when the variable is unset and the tool's own default applies. The
  value is a command line, not a path: `code -n` and a wrapper with flags both
  work, a path with spaces does not. The same file in every tool of the family
  that opens something.
- `ghrun.go`: `ghRun`: running the GitHub CLI. A failure is said the way gh
  said it (the first line of its stderr), and a missing gh reads `gh not found
  (install the GitHub CLI)`; the stdout gh printed comes back with the error
  (a GraphQL response with errors still carries its data). The tests replace
  it. The same file in every tool of the family that runs gh.
- `jsonfile.go`: `readJSONFile`, `writeJSONFile`, `writeFileAtomic`: a JSON
  cache in the state dir. A file that is missing or does not parse reads as
  nothing, and a write goes through a temporary file and a rename, so a popup
  closed mid-write, or two of them writing at once, never leave half a file
  for the next run. The same file in every tool of the family that keeps one.
- `folds.go`: the saved folds (`folds.json`, through `jsonfile.go`):
  `loadFolds`/`saveFolds` (the rows folded by hand and the `tab`/`shift+tab`
  level) and `m.pruneFolds()` (a fold whose row no longer exists, dropped
  after a complete refresh only). Not shared with the family: the other
  pickers do not persist folds.
- `frame.go`: the single-frame layout the pickers share: `frameHead`,
  `splitMain` (list and preview side by side, the columns layout) and
  `stackMain` (list over preview, split by a horizontal divider, the rows
  layout: bounded to exactly the preview's own row budget, unlike
  `splitMain`'s implicit bound by zipping list and preview line for line)
  and the section rows (`mainY`, `listY`, `frameRows`), drawn with the
  primitives of `border.go`. Copied, not imported: the same file ships in
  asgoto, asgotopr, asgotonotes, asgotosession and asgotochanged (all under
  github.com/asumaran), and there is no shared library; `stackMain` is this
  repo's own addition, not yet ported to the others. A pull request only
  needs to change it here; the maintainer ports the change to the other
  copies.
- `split.go`: the divider between the list and the preview, one file per
  layout: `loadSplit`, `saveSplit`, `stepSplit`, `splitWidths`, `moveSplit`
  (one step, remembered; all four take the file and the default as
  parameters, unlike the family's own copy) and `sizePanes` (the list and
  the preview get their share of the main section). Copied, not imported,
  like `frame.go`: the same file ships in asgotopr, asgotonotes,
  asgotosession and asgotochanged, without this repo's per-layout split.
- `ui.go`: the bubbletea model/Update/View, styles, the options (`order`,
  `prs`, `show`, `group`, `rows`, `titles`, `layout`), the effective layout
  and its geometry (`columns`, `listW`/`listH`, `detailsW`/`detailsH`,
  `resizeList`), folding (`fold`, `foldTo`) and
  the rows (`lead`: the gutter, the guides, the branch, the arrow or the
  bullet; the key in its level's color; the title faint; `detailWords`).
  The browser is opened from `main.go` after the TUI quits (`openURL`,
  `openurl.go`).
- `preview.go`: glamour rendering as a `tea.Cmd`, per-(URL,width,updated)
  render cache, the instant non-glamour headers of a ticket (its title, the
  short one under it, its parent) and of a PR (where it is, its state and
  what it needs, the review, checks and merge facts), and of a stack, group
  or section (the counts the list leaves out); `rightColumn` puts one blank
  line under the header and `syncPreviewHeight` fits the body under it.

## Build & run

```bash
go build -o asgotoissues .    # plugin runs ./asgotoissues from the repo root
./asgotoissues -dump          # stacks + issues, no TTY (refreshes when stale)
./asgotoissues -dump -query x # the matches and their scores instead of the list
./asgotoissues -dump -show KEY # prints the wiki → Markdown conversion of KEY
./asgotoissues -dump -order attention # the tree in that order (created, updated, key, attention); not saved
./asgotoissues -dump -summarize # write the missing short titles first (runs claude -p)
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
  scroll position on the right), and the help. The foot carries a context only where the
  rest of the screen cannot say it (asgitlog: repo and branch); a title is not
  context, so there is none here and the foot is the help. The stacks already head their groups in the
  list. The list starts on screen row `listY`, one cell in from the left side,
  which is what the click-to-row math uses. Errors and notices take the help
  line.
- **Moving through the list** is the same in every tool of the family and
  comes from `listnav.go` (the same file in each repo): arrows or
  `ctrl+p`/`ctrl+n` a row, `pgup`/`pgdn` a page, `alt+↑`/`alt+↓` or
  `home`/`end` the ends. `home`/`end` are taken from the filter input's caret
  on purpose (`←`/`→` and `ctrl+e` still move it). The preview scrolls with
  the mouse wheel only: `shift+` arrows resize the divider. The keys are listed in the panel.
- **The filter input** comes from `prompt.go` (the same file in every tool of
  the family). Inside herdr's popup the prompt is the arrow alone, because the
  pane's title (`[[panes]] title` in the manifest, the tool's name) already
  says which tool it is, and a placeholder says what the filter searches. Run
  on its own the prompt carries the tool's name. A build that is not a release
  says `(dev)` on the edge over the input, never inside the
  prompt.
  herdr sets `HERDR_PLUGIN_ENTRYPOINT_ID` for a plugin pane; that is how the
  two cases are told apart.
  Whatever reaches the input goes through `toInput`: a key, a paste from the
  terminal (`tea.PasteMsg`) and the input's own `ctrl+v` filter the list the
  same way (`typeInto`), and a message that leaves the query alone (a caret
  move, the blink) never moves the cursor. A paste under the open panel is
  dropped. A query made only of spaces, or a bare `~` or `'`, is not a query
  (`hasTerms`): it does not filter, rank or move the cursor.
- **Help and options**: the line at the foot shows the tool's own actions,
  the panel's key and the quit keys (`helpfoot.go`). `f1` opens the panel (`panel.go`, the same file in
  every tool of the family): the options on top, to change with `←`/`→` or
  `space`, and every key in columns under them, laid out by bubbles' `help`
  from `FullHelp()`. The panel is spliced over the middle of the frame, which
  keeps its size; while it is open it takes every key and the mouse, and `esc`
  closes it before it does anything else. `?` is not a help key: the filter
  has the focus, so it is text. Moving, scrolling and resizing are listed in
  the panel only, so the help line stays short enough for a narrow popup. A
  message takes the help line's place (`footLine`): a flash for a moment (a
  confirmation in green, a key that could do nothing in the error color),
  else an error or a notice in the error color.
  The options are the order of every level of the tree (`Order`, also
  cycled by `ctrl+s`, which names the next one on its key), which PR rows
  show (`PRs`: all, open, attention), what the list lists (`Show`: working,
  pending, all; `ctrl+t`), how (`Group`: tree, phase; `ctrl+g`), how many
  lines a row takes (`Rows`: two lines, one line) and which titles it shows
  (`Titles`: short, original); each is saved as a setting (`setting.go`) and
  the help line says `f1 options`. `Layout` (below) is one of them, on `ctrl+l` too.
- **Layout option** (`layoutMode`, saved as `layout`, `ctrl+l`, `m.columns()`):
  `columns` (default, today's side by side) or `rows` (the preview under a
  full-width list, split by a horizontal divider). `ctrl+l` cycles it like
  `^s`/`^t`/`^g`: it is in `FullHelp` (the panel's key list) and next to the
  option in the panel, but not on the foot's short help line. The *effective*
  layout (`m.columns()`) falls back from a saved `columns` to `rows` on a
  narrow terminal (`minColumnsW`, 60 cells, asgitlog's own floor), and from a
  saved `rows` to `columns` when the body is under 5 lines (a list of 2, the
  divider, and a preview of 2 do not fit); neither fallback touches the saved
  setting. `setOption` calls `m.resize()` before the shared `renderList()`/
  `updatePreview()` tail, so the viewports do not keep the old layout's sizes
  until the next `tea.WindowSizeMsg`. In `rows`, the list takes the full
  width (`listW() == innerW()`) and the preview's vertical budget
  (`detailsH()`) is `split-rows`' percent of the body, clamped to a floor of
  8 lines (less on a short body) and a ceiling that leaves the list at least
  2 lines plus the divider; a header (a PR's can run 4-5 lines) taller than
  that budget is cut, the same way a header taller than `bodyH` already was
  in `columns` (`stackMain`, `frame.go`, bounds the preview to exactly
  `detailsH` rows, dropping the rest, like `splitMain` does implicitly by
  zipping list and preview line for line).
- **Filter matches** look the same in every tool of the family and come from
  one place, `highlight.go` (the same file in each repo; it also owns `stSel`
  and `stMatch`): a match is the match color plus an underline on top of the
  style the text already has, and the selected row shows them too. That row
  is never one big `stSel.Render` around styled text, because the reset that
  ends a match would cut the background: every piece is rendered over `stSel`
  (`highlight(s, idx, stSel)`) and `selPad` fills the rest. Do not write a
  local highlighter.
- **Resizable list**: the arrows of the divider's own axis move it, in the
  *effective* layout (`resizeKey`): `shift+←/→` in columns, `shift+↑/↓` in
  rows (up and left shrink the list; the other pair does nothing). It moves
  (`resizeList`) in 5% steps, as in asgitlog, one split per layout so
  switching layouts never disturbs the other's: the setting is the
  PREVIEW's share, clamped to 30-85 and saved as `split-columns` (list 25%,
  preview 75% by default, the same in every picker of the family) or
  `split-rows` (50% by default: tree rows take two lines, so asgitlog's own
  70% would leave about two tickets in view). `loadSplit`/`saveSplit`/
  `moveSplit` (`split.go`) take the file and the default as parameters for
  this reason. Rows must degrade for a narrow list instead of truncating
  their last columns.
- **Mouse**: the wheel follows the pointer, as in asgitlog: over the list
  (`overList`) it moves the selection through the same code as the arrow keys,
  anywhere else it scrolls the description. A left click on an issue row moves
  the selection and never opens anything.
- **The list is a tree** (`tree.go`): under each stack a ticket hangs from
  the ticket its `ParentURL` names (never a display key: GitHub keys are
  qualified per list), whatever the depth. Jira's `parent` field gives the
  story of a sub-task and the epic of a story (Server/DC: sub-tasks only, an
  epic is an Epic Link custom field there); GitHub's `parent` gives a
  sub-issue's parent. A parent that is not in the user's list (an epic
  assigned to someone else, a Done one) is fetched as a **ghost** (Jira: one
  `key in (…)` request per level, three at most; GitHub: the parent's parent
  nested in the query): a whole issue with `ghost: true`, a row like any
  other whose details say `not yours`, selectable, its preview says `not in
  your list`, never counted. A ghost sorts by the newest of its subtree, so
  an old epic sits with the work under it (its age is its own). A parent that is its own descendant stays a root.
- **PRs come from GitHub and are linked locally** (`pulls.go`, `linkPulls`).
  Every stack with `github.org`/`orgs` has a pulls source, whether or not
  its `issues:` lists GitHub (a Jira stack's PRs live in its org). Three
  searches under the owners: the open PRs I am involved in
  (`involves:@me`), my PRs merged in the last 30 days (a ticket whose PR
  just merged still shows it; closed-unmerged ones are noise and never
  fetched), and, URL only, the open PRs whose review is asked of me
  (`review-requested:@me`: `involves` does not cover it, and a request to a
  team cannot be told from the PR's reviewer list). A PR carries `Refs`
  (strong: ticket keys in the head branch and the title, where Jira itself
  reads them; the issues its closing keywords name; GitHub's
  `closingIssuesReferences`) and `WeakRefs` (keys in the body, as text or as
  `/browse/` links: a stacked PR says "depends on ESHOP-2707", a body lists
  related tickets). A PR hangs from every ticket **of its stack** whose
  `ref()` (the key; `owner/repo#N` for a GitHub issue) is in its strong refs,
  and only when none is does the weak set count. One PR under several
  tickets and several PRs under one both follow; a PR's rows are told apart
  by `row.id()` (the parent's URL and its own). My PRs that name no ticket
  close their stack under `PRs without a ticket`; other people's are dropped.
  A GraphQL response with errors is still read (`ghSearch`): a field that
  fails on one node must not kill the source.
- **What a PR says** (`pull.go`), derived from the fetched fields, never
  cached: its **state** (`merged` green, `closed` and `draft` dim,
  `changes requested` red, `approved` green, else `in review` yellow),
  what it **needs** (`conflicts`, `ci failed`, `base merged`: a stacked PR
  whose base branch is the head of a merged PR; red on my PR, yellow on
  someone else's; `to answer`, on my PR, a review thread not resolved whose
  last comment is not mine; `review requested`, of me, red whoever's PR it
  is), and the **facts**, dim (`behind <base>`: the head lacks commits of
  its base, yellow when the repo requires an up-to-date branch,
  `mergeStateStatus BEHIND`; `stacked on #N`: its base is the head of that
  open PR; `ci pending`; `by <author>` when it is not me). No counts: the
  user wants to know that a branch is behind or a review waits, not how
  much. Levels, for the `attention` order: bad 3 > ok 2 > warn 1 > none 0;
  an approval is ok only while nothing blocks it and no check runs, a PR
  that waits for a review is a warning. **Nothing is inherited**: a
  ticket's level is its own PRs' (`ownLevel`), and a blocked ticket of mine
  without a PR is a warning; a parent never says what its children or
  their PRs need, the rows under it do. `mergeable`/`mergeStateStatus`
  `UNKNOWN` (GitHub computes them lazily) keep the cached values while
  `headRefOid`/`baseRefOid` did not move (`carryMergeable`).
- **Local work** (`local.go`): my open PRs with a checkout on this machine
  say what it holds that GitHub has not seen, as the shell prompt and
  Claude Code's statusline count it, each hidden at zero: `↑n` (commits on
  top of the PR's head, 256-color 212), `+n` staged (84), `!n` unstaged
  (228), `?n` untracked (245), after what the PR needs. Only the PR's row
  says them.
- **Rows** (`rowLines`, `rowLine`, `lead`, `detailLine`, `detailWords`):
  a tree drawn as Textual's (harlequin's catalog): a ticket at depth d has
  its fold arrow (`▼`, `▶` folded, written with U+FE0E so no terminal
  makes it an emoji) at `1 + 4·d` and its key two cells after it; a child
  ticket hangs from its parent's arrow on a branch (`├── ▼ key`, `└──` for
  the last sibling ticket), a leaf's branch runs on through the arrow's
  place (`├──── key`) so the keys of siblings line up; a `│` runs down from
  a ticket's arrow to its sub-tickets and between siblings. A ticket's
  details line and everything it holds start 4 cells past its key: its
  PRs are not branches but the items of its list, `○ repo#N title`, the
  `○` in its sub-tickets' arrow column, their details under their key. The
  first line: the key in lower case in its level's color (ANSI 5, 4,
  256-color 216, 183 from level 3 on; a PR's ANSI 14), the title (the
  short one, `summary.go`, when there is one) faint. The details, most
  important first and the age last: a ticket's status (lower case, green
  in progress, red blocked, blue to do, faint; a GitHub issue's from its
  labels), `no PR` (a started ticket of mine with neither PRs nor
  children), `not yours` (a ghost), the age of its last activity (its own
  update or its PRs', `today`, else `3d`, `2w`); a PR's state, needs,
  local counters, facts, age. Dim words are ANSI 8, and 7 over the
  selection. **No bold anywhere in the list**, the selection included
  (`stRowSel`). A context row (an ancestor listed for a hit) is dim. With
  one-line rows the details go between the key and the title. The list is
  drawn in lines and the rows know theirs (`lineOf`, `lines`):
  `ensureVisible` keeps the cursor's lines in view (`scrollSpan`,
  `listnav.go`), a click on either line selects the row (`rowOfLine`,
  `listmouse.go`), a page is a page of rows. `-dump` prints the same lines.
  The preview header's height varies; `syncPreviewHeight` fits the body
  under it on every selection change and resize.
- **Order** is a panel option and `ctrl+s` (`orderMode`): every level of
  the tree independently. `created` (default: newest first, `newerIssue`),
  `updated` desc (the last activity, a ticket's PRs' included), `key`
  (project, then number asc), `attention` (its own level desc,
  then blocked, doing, to do, then updated desc). Ties keep the fetched
  order; a saved value that is none of them reads as `created`; with a query
  the scores decide instead. A ticket's PR rows come before its child
  tickets, open before merged, newest activity first, always. `-dump -order`
  beats the saved setting and never writes it.
- **PRs option** (`prsMode`): `all`, `open` (merged rows hidden),
  `attention` (only PRs at warn or worse).
- **Rows option** (`rowsMode`): `two` (default) or `one` line per ticket or
  PR.
- **Show option** (`showMode`, saved as `show`, `ctrl+t`): `working`
  (default: a ticket in progress or with an open PR, a parent only as the
  container of children it lists, and the open PRs), `pending` (everything
  not finished: merged PRs and tickets with nothing left under them out)
  and `all`. It replaced `Merged: show | hide`: a saved `merged=hide` reads
  as pending. The counter still counts every ticket in the total.
- **Group option** (`groupMode`, saved as `group`, `ctrl+g`): `tree` or
  `phase` (`buildPhase`): per stack, a section per PR state (`PR in
  review`, `draft PR`, `PRs merged`) with one row per PR (a PR under two
  tickets once per ticket) carrying the path of its tickets (`shop-51 ›
  shop-62 › web-app#7811`, each key in its level's color), and
  `no PR` with my tickets that have neither PRs nor children; its details
  add the status of the PR's ticket. Empty sections are left out; the
  show mode, the PRs mode and a query apply.
- **Folding** (`fold`, `foldTo`, `m.collapsed` by `foldKey`): `space`, while
  the filter is empty (with text in it space is text), folds the row under
  the cursor: a ticket (its PRs and children), a stack to its header, the
  group of PRs without a ticket, a phase section; on a PR its ticket (by
  phase, its section) and the cursor moves there; a row with nothing under
  it flashes `nothing to fold`. The headers, groups and sections are
  selectable for this (`enter` flashes `nothing to open`, `ctrl+y`
  `nothing to copy`); their preview counts the stack's tickets by state
  and its PRs by phase. `shift+tab` folds the tree a level shallower (all,
  then the deepest level, down to the roots alone), `tab` a level deeper
  and back to all (`foldLevel`, `deepest`): a level replaces the folds made
  by hand (a folded stack stays); it flashes `level N`. A query shows
  everything (a search never hides a hit); the folds apply again once it is
  cleared.
- **Folds are saved** (`folds.go`), one file, `folds.json`
  (`{"level": n, "keys": [...]}`, `keys` sorted for a deterministic file),
  written atomically (`writeJSONFile`, `jsonfile.go`): `newModel` loads
  `collapsed` and `depthNow` from it, `fold()` and `foldTo()` save both
  after every change. It is a snapshot, not a depth policy: a level is
  restored exactly (plus where `tab`/`shift+tab` continue from next), and a
  ticket that arrives later (a refresh, or a fresh fetch after a restart)
  shows unfolded, the same as it does today after a live refresh;
  `foldTo` clamps a saved level deeper than the current tree to its
  `deepest` before acting on it. A fold made by hand after a level keeps the
  level as the point `tab` continues from, as before. `m.pruneFolds()`
  drops a fold whose row no longer exists: a structural key (`stack:<name>`,
  `group:<name>`, `section:<name>:<phase>` for each of `buildPhase`'s phase
  names) only when it is still built from the stacks configured now, a
  ticket's URL only when some entry (a ghost included) still holds it —
  nothing is split on `:`, a key is only ever checked against this valid
  set. Renaming a stack in the YAML, or a phase in the code, drops its fold;
  that is accepted. Pruning is the *model*'s job, not `saveFolds`', and it
  runs (and saves) only from `finishRefresh` when every source answered (no
  `fetchErrs`): a cache read alone, or a partial refresh, trusts nothing
  enough to prune, so a source that failed never costs its tickets their
  folds.
- **Short titles** (`summary.go`, `Titles: short | original`): one plain
  Spanish sentence of 6 to 14 words per ticket and per PR, written by Haiku
  through `claude -p` in the background, a child as a part of its parent's
  goal (the request goes per stack as the tree, the summarized ancestors as
  read-only context), cached in `summaries.json` by URL with the hash of
  what it was written from (a child's includes its parent's summary). Until
  one arrives, or when the CLI fails (the error takes the help line once;
  a CLI that is not installed says nothing),
  the row shows the tracker's title; `original` never calls the CLI. Both
  titles are searched; the preview keeps the tracker's with the short one
  under it (`≈`). It sends titles and the start of bodies to Anthropic
  through the user's Claude Code account.
- **Providers**: a tracker is a `provider` that returns normalized `issue`s.
  Whatever is tracker-specific is settled at fetch time and stored on the
  issue: `State` (todo, doing, blocked) drives the colors, `Meta` holds the
  preview's meta parts when the Jira fields do not cover them, `BodyFormat`
  says whether the description is wiki markup or already Markdown. The UI,
  the filter and the preview never ask which tracker an issue came from. To
  add a tracker: implement `provider`, give it a kind, handle the kind in
  `sourcesOf` and `stackTrackers`.
- **The cache file is a contract**: `issue`'s and `pull`'s JSON tags are the
  format of `issuecache.json` and of the fixtures in `scripts/pty-check.py`.
  New fields are `omitempty` and an issue without them must keep working (no
  `Source` means Jira, no `State` falls back to the Jira status fields, no
  `parent_url` is derived from the parent key and the stack's site for Jira
  (`backfillParentURLs`), no `pulls` is a list without PRs), so a snapshot
  written by an older version still renders.
- **Config source is asdev's file, on purpose**: one place to declare a
  stack for both the Claude plugin and this picker. Only
  `stacks.<name>.issues`, `stacks.<name>.jira.{base_url,type,email,api_token_env,username}`
  and `stacks.<name>.github.{org,orgs}` are read. Without `issues:` a stack
  lists Jira when it has a `jira.base_url`, so a config that only knows about
  Jira needs no changes.
- **GitHub goes through `gh`**, like asgotopr: no token handling here, and
  the user is whoever `gh` is logged in as (`viewer { login }` is what makes
  a PR mine). A stack lists the open issues assigned to that user under its
  owners (`assignee:@me`), one search for all of them; its PRs take three
  more.
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
  (one per tracker of a stack, plus its pulls source) refresh concurrently,
  one tea.Cmd each; a failed source keeps its cached issues or PRs (a pulls
  error reads `<stack> PRs: …`) and the snapshot's `FetchedAt` is NOT
  advanced, so the next open retries; a source that answers with nothing
  clears its part. Errors take the help line, never a modal; the refresh mark sits
  on the edge over the input.
- **A failed refresh** keeps the cached list on screen. The error (`netErr`,
  the failed sources joined) takes the help line through the shared
  `footLine`, in the error color, until the next key gives the help back; the
  edge over the input keeps a red `refresh failed` mark (`refreshMark`,
  `m.stale`) for as long as the list is the cached one, so the state outlives
  the message. An error that keeps a list from loading at all is shown in the
  list in the error color in every tool of the family (`emptyList`); here a
  fetch error never empties the list, so it has no such case. A config that
  cannot be read keeps the tool from starting and goes through the shared
  `fatal` (`fatal.go`): held until enter in the popup, stderr and exit 1 in a
  shell.
- **Selection is deliberately just "open in browser"**, a ticket, a ghost or
  a PR alike, executed after quit because quitting closes the popup. `openURL` (`openurl.go`, the same file
  in every tool that opens the browser) prefers an AppleScript `make new tab`
  in Chrome's front window when Chrome is running with a window, because
  `open <url>` lets Chrome pick its `profile.last_used`, which is not the
  last focused window. It falls back to `open` (`xdg-open` off macOS);
  `ASGOTOISSUES_OPENER` replaces the whole thing. Jumping to a checkout / creating a
  worktree for an issue was considered and rejected for v1. `enter` and
  `ctrl+o` both open.
- **Copy**: `ctrl+y` copies the issue key (`it.Key`), or a PR's URL on a PR
  row, with `copyCmd` (the shared `clipboard.go`) and the help line flashes
  `copied <key>`
  (`flash.go`), ahead of any network error. A key that could do nothing
  (`nothing to copy`, `copy failed: ...`, `nothing to open`) flashes in the
  error color instead of green (`flash.fail`, `flashErrMsg`). `ASGOTOISSUES_CLIPBOARD` replaces
  the clipboard command (the tests point it at a stub).
- **Never query the terminal behind bubbletea's back**: `Init` issues
  `tea.RequestBackgroundColor()` and the `tea.BackgroundColorMsg` reply picks
  the glamour style ("dark"/"light"). Frames before the reply use "dark";
  when the style flips, the render cache is dropped and the current preview
  re-renders. Don't call glamour's `WithAutoStyle` or lipgloss's
  `HasDarkBackground` from inside the program: the reply races bubbletea's
  input reader and ends up typed into the filter as literal "rgb:..." text.
- **Alt screen and mouse mode** are declared per frame in `View()`; there is
  no `tea.WithAltScreen` program option in v2. `View()` builds the
  `tea.View` from `render()`, which holds the frame text and is what the
  tests assert on.
- Ordering: stacks in config order; each level of a stack's tree by the
  order option (`created` by default: `newerIssue`, falling back to key
  number, then `updated`).
- Search corpus: summary + key/number + status/type/project/stack/parent
  key and summary/the keys, numbers and branches of the linked PRs and the
  provider's meta parts (repo, labels), kept as shown and matched
  against the query as typed (the matcher folds case itself, and its offsets
  are bytes into the string the row highlights; the bonuses compare whole
  words whatever their case); exact key +30, exact number
  +20 (the part after the last `-` or `#`), key prefix with `-` or `#` +10,
  exact key or number of a linked PR +20, exact stack/project +10, key hit +2.
- **A query keeps the tree**: a ticket is a hit when it matches, the
  ancestors that lead to a hit stay as context rows (dim, never a hit, no
  highlight), PR rows stay under the hits, siblings go by the best score in
  their subtree, the stacks by their best hit (`rank` in `rank.go`, the same
  file in every picker of the family), and the cursor sits on the first hit
  (`firstHit`). Ties keep the tree's own order. A score says how good the
  match is and nothing about the length of the text (`match.go`). Without a
  query the order is the one above.

## Testing

Unit tests cover the pure logic (config parsing, netrc, time parsing,
merge fallback per source, the GitHub and pulls providers against a fake
`ghRun` (`fakeGh` answers by the `q=` argument, so it cannot validate the
GraphQL document: a live `-dump` against a stale, isolated state dir is the
check that the queries are accepted), the Jira ghosts against an `httptest`
server, issues cached by older versions, wiki conversion, the references
and the state, needs and facts of a PR, linking, the tree (nesting,
ghosts, the query walk, the order, PRs and show modes, the guides, the
phase view, the folds of a level, own levels), the local counters against
a fake `gitRun`, the short titles against a fake `summarizeRun` (the
batches, the hashes, the answers the CLI gives), ranking, grouping, key
handling, partial refresh failure, View content: the rows as drawn, their
colors, no bold), the layout's effective fallback and its geometry (`listW`,
`listH`, `detailsH`, `stackMain`) at several sizes, the saved folds
(round-trip, pruning after a complete refresh only, the level clamped to the
tree's own deepest). `TestMain` points `HERDR_PLUGIN_STATE_DIR` at a temp dir
so tests never touch the real cache, `ASGOTOISSUES_CHECKOUTS` at it too so
no real checkout is read, turns the summarizer off and lists everything
(`defaultShow`); a test that presses a key under the panel, saves a setting,
or folds or unfolds a row (which now also persists to `folds.json`) takes a
temp dir of its own, so one test's fold or setting never leaks into the
next's fresh model. The pty sandbox saves `show=all` and
`titles=original` and sets `ASGOTOISSUES_NO_SUMMARIES`.

For end-to-end verification without a TTY, `scripts/pty-check.py ./asgotoissues`
(python3 + `pyte`) spawns the binary on a pty, answers the terminal queries,
replays keystrokes and asserts on pyte-rendered frames, in a throwaway sandbox
(fake `HOME`, synthetic `ASGOTOISSUES_CONFIG`, empty netrc, a fresh synthetic
cache of tickets, a ghost parent and PRs so nothing is fetched, logging stubs
as `ASGOTOISSUES_OPENER` and `ASGOTOISSUES_CLIPBOARD`). The v2 renderer repaints with scroll regions, which pyte ignores, so the
driver forces a full redraw (pty resize + SIGWINCH) before reading a frame.

## Commits & branches

- Conventional Commits: `type(scope): description` (feat, fix, chore, docs,
  style, refactor, test, perf).
- Never mention AI tooling in commits, PRs, or any repo-visible text as the
  author of changes.
- Default branch is `main`. Don't commit, tag, or push unless explicitly
  asked (releasing is an explicit, separate request).

## Releasing

`scripts/release.sh <X.Y.Z>`: clean-tree + vet/build/test gate, CHANGELOG
generation from commit subjects, manifest version sync, commit + tag + GitHub
release; CI (`.github/workflows/release.yml`) attaches the `asgotoissues-<os>-<arch>` binaries (macOS and Linux, arm64 and amd64).
Releasing never touches the linked plugin's `./asgotoissues`; rebuild locally to
keep testing dev code.
