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

- `main.go`: flags (`-version`, `-dump`, `-query`, `-show`, `-order`), model
  construction, `tea.NewProgram`, post-quit browser open, `runDump` (it writes
  to an `io.Writer`, so the tests read what `-dump` prints: the tree, with
  the glyphs, the indent, the ghosts marked and the PRs with their flags).
- `config.go`: front-matter extraction from `asdev.local.md`, stack parsing
  (config order preserved via a `yaml.Node` walk), which trackers a stack
  lists (`issues:`), netrc + env credentials for Jira.
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
- `pull.go`: the `pull` struct (the cache's format for a PR), `flags` (what a
  PR needs, in the order the row shows them, and its level), `pullRefs` (the
  tickets and issues a PR names: strong in the branch, the title and the
  closing references, weak in the body).
- `pulls.go`: the pulls source: three searches per stack under its owners
  (open PRs I am involved in, my PRs merged in the last 30 days, and the
  URLs of the PRs whose review is asked of me), the viewer's login for
  `Mine`, node mapping, `carryMergeable` (a merge state GitHub has not
  computed yet keeps the cached one while the branches did not move).
- `tree.go`: the list as a tree: `linkPulls` (a PR under every ticket of its
  stack it names), `buildTree` (nesting by the parent's URL, PR rows under
  their ticket, the group of my PRs without a ticket, the query walk that
  keeps the ancestors of a hit), the order modes and the PRs modes,
  `ownAttention` (a ticket's level: the worst of its PRs and, in the tree,
  its descendants).
- `setting.go`: `loadSetting`/`saveSetting`: a setting the tool remembers
  between runs, one plain-text file each in the state dir. The same file in
  every tool of the family that needs it.
- `wiki.go`: Jira wiki markup → Markdown (headings, lists, code/noformat/
  quote blocks, tables, links, mono/bold/italic, mentions).
- `cache.go`: `issuecache.json` (issues and pulls) load/save (through
  `jsonfile.go`), 60s freshness debounce, and `stateDir()`, a wrapper over
  `stateDirFor` (`statedir.go`).
- `filter.go`: entries (a ticket and its PRs), the row kinds, corpora, fuzzy
  hits, `matchBonus` ranking; `buildRows` scores the hits and hands them to
  the tree.
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
- `frame.go`: the single-frame layout the pickers share: `frameHead`,
  `splitMain` (list and preview) and the section rows (`mainY`, `listY`,
  `frameRows`), drawn with the
  primitives of `border.go`. Copied, not imported: the same file ships in
  asgoto, asgotopr, asgotonotes, asgotosession and asgotochanged (all under
  github.com/asumaran), and there is no shared library. A pull request only
  needs to change it here; the maintainer ports the change to the other
  copies.
- `split.go`: the divider between the list and the preview: `loadSplit`,
  `saveSplit`, `stepSplit`, `splitWidths`, `moveSplit` (one step, remembered)
  and `sizePanes` (the list and the preview get their share of the main
  section). Copied, not imported, like `frame.go`: the same file ships in
  asgotopr, asgotonotes, asgotosession and asgotochanged.
- `ui.go`: the bubbletea model/Update/View, styles, the options (`order`,
  `prs`) and the rows (a glyph, the indent, the key, the flags of a PR). The
  browser is opened from `main.go` after the TUI quits (`openURL`,
  `openurl.go`).
- `preview.go`: glamour rendering as a `tea.Cmd`, per-(URL,width,updated)
  render cache, the instant non-glamour headers of a ticket (with its parent
  and whether every PR is merged) and of a PR (where it is, what it needs,
  the review, checks and merge facts); `rightColumn` puts one blank line
  under the header and `syncPreviewHeight` fits the body under it.

## Build & run

```bash
go build -o asgotoissues .    # plugin runs ./asgotoissues from the repo root
./asgotoissues -dump          # stacks + issues, no TTY (refreshes when stale)
./asgotoissues -dump -query x # the matches and their scores instead of the list
./asgotoissues -dump -show KEY # prints the wiki → Markdown conversion of KEY
./asgotoissues -dump -order attention # the tree in that order (created, updated, key, attention); not saved
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
  `shift+↑`/`shift+↓` only. The keys are listed in the panel.
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
  The options are the order of every level of the tree (`Order`, also cycled
  by `ctrl+s`, which names the next one on its key), which PR rows show
  (`PRs`: all, open, attention), how many lines a row takes (`Rows`: two
  lines, one line) and whether the merged work shows (`Merged`: show,
  hide); each is saved as a setting (`setting.go`) and the help line says
  `f1 options`.
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
  nested in the query): a whole issue with `ghost: true`, dim in the list,
  selectable, its preview says `not in your list`, never counted. A ghost
  sorts by the newest of its subtree, so an old epic sits with the work
  under it. A parent that is its own descendant stays a root.
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
- **What a PR needs** (`pull.flags`), derived from the fetched fields, never
  cached: `merged` and `closed` (dim) stop everything else; `conflicts`
  (`CONFLICTING` or merge state `DIRTY`), `changes requested`, `ci failed`,
  `base merged` (a stacked PR whose base branch is the head of a merged PR)
  are bad on my PR and a warning on someone else's; `review requested` is
  bad whoever's PR it is; `behind base` (merge state `BEHIND`, the base
  moved) and `ci pending` warn; `on #N` (stacked on an open PR) and `draft`
  say so; `approved` (ok, ready to merge) only when nothing blocks it and no
  check runs; `awaiting review` (warn) with `REVIEW_REQUIRED` or reviewers
  pending, never on a draft, never on a PR with no rule and no request (a
  personal repo must not read as waiting forever). Levels: bad 3 > ok 2 >
  warn 1 > none 0. A ticket's level (`ownAttention`, then the tree) is the
  worst of its PRs and its descendants' (`needs`: their flags that say
  something, once each, worst first, `mergeNeeds`), a blocked ticket without
  a PR is a warning, and a ticket whose every PR is merged reads `all
  merged` (ok: move it). The level orders the list (`attention`); the
  words are what the list shows. `mergeable`/`mergeStateStatus` `UNKNOWN` (GitHub computes them
  lazily) keep the cached values while `headRefOid`/`baseRefOid` did not
  move (`carryMergeable`).
- **Rows** (`rowLines`, `rowLine`, `pullLine`, `detailLine`): every
  selectable row starts with the gutter (two cells: the cursor's mark on the
  selected one), then the indent; the title comes last on its line, so it
  is what a narrow list cuts. There is no glyph column: what a row needs is
  said in words, in its details line (a ticket's collects its PRs' and its
  descendants'), so nothing has to be decoded. **The list is two tones**
  (`stDetail`, `stDetailSel`): a row's first line is plain (the key, the
  status and the flags carry no color there), its details line is dim, and
  under the selection the details are a lighter grey (color 7) over the
  background, because the dim tone is the background's own color and would
  vanish. The state and level colors belong to the preview header
  (`stateStyle`, `levelStyle`, `flagsLine`), not to the list. A ghost or a
  context row is dim on its first line too: it is not mine, or not a hit. The indent is the tree's own
  (`row.col`): a child row, a PR or a child ticket, starts where its
  parent's title starts (one space past the parent's key, from wherever the
  parent is set in), so a ticket, its details, its PRs and its children
  share one left edge. By default a ticket or a PR takes
  **two lines** (`rowsMode`, the `Rows` option): the title on the first, its
  details dim under it, so the titles and the glyphs stay what the eye
  scans. A ticket: `<indent><key> <summary>` (one space between
  the key and the summary, no shared key column; the key is one color for
  every ticket, `stKey`, dim for a ghost or a context row: the state is said
  in words, never by the key's color), then, aligned under the summary (one
  space past the key), `<status> · <type> · updated <age> · <not in your
  list> · <N PRs> · <all merged> · <needs>`. A PR: `<indent>↳ <repo#N>
  <title>`, then, under the title, `open · <flags> · by <author, when not
  me> · <age>`, the repo always in the key
  (`owner/repo#N` when two
  owners share a name, the rule of `qualifyClashingKeys`), the key in the
  match style when the query is in it or in the branch. With one-line rows
  a ticket is `<indent><key> [<status>] <summary>` (what its PRs need is on
  their rows) and a PR `<indent>↳ <repo#N> <flags · joined> <title>`.
  Headers and
  the group line always take one line. The list is drawn in lines and the
  rows know theirs (`lineOf`, `lines`): `ensureVisible` keeps the cursor's
  lines in view (`scrollSpan`, `listnav.go`), a click on either line selects
  the row (`rowOfLine`, `listmouse.go`), a page is a page of rows, and the
  selection covers both lines. `-dump` prints the same words after a
  ticket's line. The preview header's height varies (a PR has
  more facts); `syncPreviewHeight` fits the body under it on every selection
  change and resize.
- **Order** is a panel option and `ctrl+s` (`orderMode`): every level of
  the tree independently. `created` (default: newest first, `newerIssue`),
  `updated` desc, `key` (project, then number asc), `attention` (level desc,
  then blocked, doing, to do, then updated desc). Ties keep the fetched
  order; a saved value that is none of them reads as `created`; with a query
  the scores decide instead. A ticket's PR rows come before its child
  tickets, open before merged, newest activity first, always. `-dump -order`
  beats the saved setting and never writes it.
- **PRs option** (`prsMode`): `all`, `open` (merged rows hidden),
  `attention` (only PRs with a bad or warn flag).
- **Rows option** (`rowsMode`): `two` (default) or `one` line per ticket or
  PR; the one-line list holds twice the rows of a popup, the two-line one
  loses no title.
- **Folding** (`fold`, `m.collapsed`): `space` on a ticket, while the filter
  is empty (with text in it space is text, like `q`), folds its PRs and
  children away and marks it with `▸` in the gutter (the cell before the
  indent, so the key and the details stay put); space again unfolds it. On a PR row it folds the PR's
  ticket and the cursor moves there. A ticket with nothing under it flashes
  `nothing to fold`. Folds are by URL, for the run only, and a query shows
  everything (a search never hides a hit). A folded ticket still carries
  what its hidden PRs and children need.
- **Merged option** (`hideMerged`, saved as `merged`: `show`/`hide`): hide
  leaves out the merged PR rows everywhere and the tickets with nothing
  left under them (`node.done`: every PR merged, or none on a ghost, and
  every child done), so the list is the work that is not finished. A hidden
  ticket does not answer a query either. The counter still counts it in the
  total (`2/3`). `-dump` shows everything.
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
and flags of a PR, linking, the tree (nesting, ghosts, the query walk, the
order and PRs modes, attention), ranking, grouping, key handling, partial
refresh failure, View content). `TestMain` points `HERDR_PLUGIN_STATE_DIR`
at a temp dir so tests never touch the real cache; a test that presses a
key under the panel or saves a setting takes a temp dir of its own.

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
