package main

// The bubbletea model: a filter input on top, a two-column body (the tree of
// tickets and their PRs on the left, the rendered description on the right)
// and a help footer. Modeled on asgotopr/asgoto: the input is focused before
// the program starts, every printable key filters, and the selected row is
// opened in the browser AFTER the TUI exits (quitting is what closes the
// popup).

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// ---- styles ----

var (
	stHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stTitle   = lipgloss.NewStyle().Bold(true) // the preview's title (the list has no bold)
	stCount   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stError   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	stTodo    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	stDoing   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	stOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stBad     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	stPRKey   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))

	// stRowSel is the selected row of the list: the selection's background,
	// never bold (the list has no bold).
	stRowSel = stSel.Bold(false)
)

// levelColors color a ticket's key by its depth: pink, blue, peach, mauve
// (the last two are 256-color: the 16 slots run out).
var levelColors = []string{"5", "4", "216", "183"}

func keyStyle(depth int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(levelColors[min(max(depth, 0), len(levelColors)-1)]))
}

// stateStyle colors an issue by its state: green in progress, red blocked,
// blue to do.
func stateStyle(state string) lipgloss.Style {
	switch state {
	case stateDoing:
		return stDoing
	case stateBlocked:
		return stBlocked
	default:
		return stTodo
	}
}

// toneStyle is the color of a word of a details line.
func toneStyle(tone int) lipgloss.Style {
	switch tone {
	case toneGreen:
		return stOK
	case toneYellow:
		return stWarn
	case toneRed:
		return stBad
	case toneBlue:
		return stTodo
	}
	return stDim
}

// flagsLine is a PR's words, each in its color, joined for the preview.
// base is the style they are drawn over.
func flagsLine(flags []prFlag, base lipgloss.Style) string {
	parts := make([]string, len(flags))
	for i, f := range flags {
		parts[i] = base.Foreground(toneStyle(f.tone).GetForeground()).Render(f.text)
	}
	return strings.Join(parts, base.Foreground(stDim.GetForeground()).Render(" · "))
}

// The list: a row's first line is its key, in its level's color, and its
// title, faint; its details line is dim, with the words that mean something
// in their colors, faint. Over the selection's background the dim words are
// a lighter grey (color 7), so they can still be read there.
var (
	stDetail    = stDim
	stDetailSel = stRowSel.Foreground(lipgloss.Color("7"))
)

// ---- key bindings ----

type keyMap struct {
	Nav      listNav
	Select   key.Binding
	Quit     key.Binding
	PrevUp   key.Binding
	PrevDown key.Binding
	Shrink   key.Binding
	Grow     key.Binding
	Copy     key.Binding
	Order    key.Binding
	Show     key.Binding
	Group    key.Binding
	Fold     key.Binding
	Shallow  key.Binding // shift+tab: the tree one level less deep
	Deeper   key.Binding // tab: one level more
	Filter   key.Binding
	Help     key.Binding
}

// ShortHelp is the help line: the tool's own actions, the panel's key and the
// quit keys. Moving, scrolling and resizing are in the expanded help, so the
// line stays short enough for a narrow popup (a cut line loses the quit keys
// first).
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Filter, k.Select, k.Help, k.Quit}
}

// FullHelp is the panel's list of keys, one column per group: the
// filter and the preview, the list, the tool's actions, help and quit.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Filter, k.PrevUp, k.Shrink},
		{k.Nav.Up, k.Nav.PageUp, k.Nav.Top},
		{k.Select, k.Copy, k.Order, k.Show, k.Group},
		{k.Fold, k.Shallow},
		{k.Help, k.Quit},
	}
}

func defaultKeys() keyMap {
	return keyMap{
		Nav:      defaultListNav(),
		Select:   key.NewBinding(key.WithKeys("enter", "ctrl+o"), key.WithHelp("enter", "open in browser")),
		Quit:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc/q", "quit")),
		PrevUp:   key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("⇧↑/⇧↓", "scroll the description")),
		PrevDown: key.NewBinding(key.WithKeys("shift+down")),
		Shrink:   key.NewBinding(key.WithKeys("shift+left"), key.WithHelp("⇧←/⇧→", "resize the list")),
		Grow:     key.NewBinding(key.WithKeys("shift+right")),
		Copy:     key.NewBinding(key.WithKeys("ctrl+y"), key.WithHelp("^y", "copy the key")),
		Order:    key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("^s", "order")),
		Show:     key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("^t", "show")),
		Group:    key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("^g", "group")),
		// space folds only while the filter is empty; otherwise it is text.
		Fold:    key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "fold/unfold (empty filter)")),
		Shallow: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇧tab/tab", "fold/unfold a level")),
		Deeper:  key.NewBinding(key.WithKeys("tab")),
		// Help-only entry: a binding without keys is disabled and the help
		// bubble would skip it. Nothing ever matches against it.
		Filter: key.NewBinding(key.WithKeys("type"), key.WithHelp("type", "filter")),
		Help:   helpBinding(true),
	}
}

// ---- model ----

type model struct {
	// data
	stacks    []stack
	sources   []source
	entries   []*entry
	pulls     []pull
	unlinked  map[string][]*pull // per stack, my PRs that name no ticket
	summaries []string           // parallel corpora, see filter.go
	keysC     []string
	metas     []string
	cache     issueCache

	// options
	order  orderMode
	prs    prsMode
	show   showMode
	group  groupMode
	rowsM  rowsMode
	titles titlesMode

	collapsed map[string]bool // the rows folded by hand, by foldKey, for this run
	depthNow  int             // the level the tree was folded to with tab/shift+tab, 0 for none

	// what the list says besides the trackers
	summ      summaries             // the short titles (summary.go)
	summTried map[string]bool       // the items sent this run, so a missing answer is not asked for again
	summBusy  bool                  // a call is running
	local     map[string]localState // my PRs' checkouts, by URL (local.go)

	// background refresh
	pending    int // sources still fetching
	fresh      map[string][]issue
	freshPulls map[string][]pull
	fetchErrs  []string
	refreshing bool
	netErr     string
	stale      bool // the last refresh failed: the list is the cached one (see status)

	// ui
	rows    []row
	cursor  int
	lineOf  []int // the first list line of each row: a row takes one line, or two with its details
	lines   int   // how many lines the rows take
	ti      textinput.Model
	listVP  viewport.Model
	prevVP  viewport.Model
	help    help.Model
	keys    keyMap
	width   int
	height  int
	split   int // the preview's share of the width, percent
	renders map[string]string
	prevKey string
	flash   flash // confirmation on the help line (flash.go)
	panel   panel // options and keys, over the frame while it is open (panel.go)

	// previewStyle is the glamour standard style ("dark"/"light"). It starts
	// as "dark" and flips when the terminal answers RequestBackgroundColor.
	previewStyle string

	openURL string // opened in the browser after quit ("" = none)
}

// newModel builds the model from the cached snapshot. stale starts the
// background refresh of every source from Init.
func newModel(stacks []stack, cache issueCache, stale bool) model {
	m := model{
		stacks:       stacks,
		sources:      sourcesOf(stacks),
		cache:        cache,
		refreshing:   stale,
		cursor:       -1, // the first ticket, not the stack's header (applyFilter)
		order:        parseOrder(loadSetting(stateDir(), "order")),
		prs:          parsePrs(loadSetting(stateDir(), "prs")),
		show:         parseShow(loadSetting(stateDir(), "show"), loadSetting(stateDir(), "merged")),
		group:        parseGroup(loadSetting(stateDir(), "group")),
		rowsM:        parseRows(loadSetting(stateDir(), "rows")),
		titles:       parseTitles(loadSetting(stateDir(), "titles")),
		collapsed:    map[string]bool{},
		summ:         loadSummaries(),
		summTried:    map[string]bool{},
		local:        map[string]localState{},
		ti:           newFilterInput("asgotoissues", "Search by title, key, status, repo, PR…"),
		listVP:       viewport.New(viewport.WithWidth(50), viewport.WithHeight(20)),
		prevVP:       viewport.New(viewport.WithWidth(40), viewport.WithHeight(17)),
		help:         help.New(),
		keys:         defaultKeys(),
		split:        loadSplit(stateDir()),
		renders:      map[string]string{},
		previewStyle: "dark",
		width:        94,
		height:       24,
	}
	if stale {
		m.pending = len(m.sources)
	}
	m.syncHelp()
	m.setEntries(cache.Issues, cache.Pulls)
	m.applyFilter()
	m.resize()
	m.renderList()
	return m
}

func (m *model) currentRow() *row {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].selectable() {
		return &m.rows[m.cursor]
	}
	return nil
}

// titleOf is the title a ticket's row shows: its short title when there is
// one and the option wants it, else the tracker's.
func (m *model) titleOf(e *entry) string {
	if m.titles == titlesShort {
		if s := m.summ[e.it.URL].Text; s != "" {
			return s
		}
	}
	return e.it.Summary
}

// pullTitle is titleOf for a PR.
func (m *model) pullTitle(p *pull) string {
	if m.titles == titlesShort {
		if s := m.summ[p.URL].Text; s != "" {
			return s
		}
	}
	return p.Title
}

// opts is how the rows are built now.
func (m *model) opts() treeOpts {
	return treeOpts{order: m.order, prs: m.prs, show: m.show, group: m.group, collapsed: m.collapsed}
}

// innerW is the width inside the frame's sides.
func (m *model) innerW() int { return max(20, m.width-2) }

// listW is the list's share of the main section; the divider and the preview
// take the rest.
func (m *model) listW() int { w, _ := splitWidths(m.innerW(), m.split); return w }

// detailsW is the preview's area, including the cell of padding on each
// side; prevW is the text width inside it.
func (m *model) detailsW() int { _, w := splitWidths(m.innerW(), m.split); return w }
func (m *model) prevW() int    { return max(10, m.detailsW()-2) }

// bodyH is the height of the main section: everything but the frame's own
// lines and the help.
func (m *model) bodyH() int { return max(1, m.height-frameRows-1) }

func (m *model) resize() {
	m.listVP.SetWidth(m.listW())
	m.listVP.SetHeight(m.bodyH())
	m.prevVP.SetWidth(m.prevW())
	m.syncPreviewHeight()
	m.help.SetWidth(max(0, m.width-4))
	sizeInput(&m.ti, m.width-4)
}

// syncPreviewHeight fits the body viewport under the header of the selected
// row: a PR's facts take more lines than a ticket's, and the frame shows
// only bodyH lines.
func (m *model) syncPreviewHeight() {
	hh := 0
	if r := m.currentRow(); r != nil {
		hh = lipgloss.Height(m.headerOf(r, m.prevW())) + 1 // and the blank line under it
	}
	m.prevVP.SetHeight(max(1, m.bodyH()-hh))
}

// resizeList moves the divider between the list and the preview by one step.
func (m *model) resizeList(grow bool) tea.Cmd {
	m.split = moveSplit(stateDir(), m.split, grow)
	m.resize()
	m.renderList()
	return m.updatePreview()
}

// setEntries takes a list and its PRs: the old snapshot's parents get their
// URLs, the PRs hang from their tickets, and the corpora follow.
func (m *model) setEntries(issues []issue, pulls []pull) {
	backfillParentURLs(issues, m.stacks)
	m.entries = buildEntries(issues)
	m.pulls = pulls
	m.unlinked = linkPulls(m.entries, m.pulls)
	m.summaries, m.keysC, m.metas = corpora(m.entries, m.titleOf)
}

func (m *model) applyFilter() {
	q := strings.TrimSpace(m.ti.Value())
	m.rows = buildRows(m.entries, m.pulls, m.unlinked, m.opts(), q, m.summaries, m.keysC, m.metas)
	if hasTerms(q) {
		m.cursor = firstHit(m.rows) // ranked: the best match is the first hit
		return
	}
	if m.cursor < 0 || m.cursor >= len(m.rows) || !m.rows[m.cursor].selectable() {
		m.cursor = firstIssue(m.rows)
	}
}

// keepCursorOn puts the cursor back on the row with the given identity, or
// on the first one when it is gone.
func (m *model) keepCursorOn(id string) {
	if id == "" {
		return
	}
	for i, r := range m.rows {
		if r.selectable() && r.id() == id {
			m.cursor = i
			return
		}
	}
	m.cursor = firstIssue(m.rows)
}

// fold folds or unfolds the row under the cursor: a ticket, a stack, the
// group of PRs without a ticket, a phase section (on a PR, its ticket in
// the tree and its section by phase). What hangs from it leaves the list or
// comes back, and the cursor sits on it. A row with nothing under it says
// so.
func (m *model) fold() tea.Cmd {
	r := m.currentRow()
	if r == nil {
		return m.flash.fail("nothing to fold")
	}
	target := m.cursor
	if r.kind == rowPull {
		target = -1
		for i := m.cursor - 1; i >= 0; i-- {
			k := m.rows[i].kind
			if m.group == groupTree && (k == rowIssue && m.rows[i].e.it.URL == r.parent || k == rowGroup) ||
				m.group == groupPhase && k == rowSection {
				target = i
				break
			}
		}
	}
	if target < 0 || !m.rows[target].kids {
		return m.flash.fail("nothing to fold")
	}
	key := foldKey(m.rows[target])
	id := m.rows[target].id()
	if m.collapsed[key] {
		delete(m.collapsed, key)
	} else {
		m.collapsed[key] = true
	}
	m.applyFilter()
	m.keepCursorOn(id)
	m.renderList()
	return m.updatePreview()
}

// foldTo folds the tree to one level less deep (shallower) or one more:
// all, then the deepest level, down to the roots alone, and back. It
// replaces the folds made by hand (a stack stays folded). With a query, or
// by phase, it does nothing: a search never hides a hit.
func (m *model) foldTo(shallower bool) tea.Cmd {
	if hasTerms(m.ti.Value()) || m.group != groupTree {
		return nil
	}
	open := treeOpts{order: m.order, prs: m.prs, show: m.show, group: groupTree}
	deep := deepest(buildTree(m.entries, m.pulls, m.unlinked, open, nil))
	if deep == 0 {
		return m.flash.fail("nothing to fold")
	}
	lvl := m.depthNow
	switch {
	case shallower && lvl == 0:
		lvl = deep
	case shallower:
		lvl = max(1, lvl-1)
	case lvl != 0:
		lvl++
		if lvl > deep {
			lvl = 0
		}
	}
	m.depthNow = lvl
	cur := m.currentID()
	folds := foldLevel(buildTree(m.entries, m.pulls, m.unlinked, open, nil), lvl)
	for k := range m.collapsed {
		if strings.HasPrefix(k, "stack:") {
			folds[k] = true
		}
	}
	m.collapsed = folds
	m.applyFilter()
	m.keepCursorOn(cur)
	m.renderList()
	msg := "all levels"
	if lvl > 0 {
		msg = "level " + strconv.Itoa(lvl)
	}
	return tea.Batch(m.flash.set(msg), m.updatePreview())
}

// currentID is the identity of the row under the cursor, "" without one.
func (m *model) currentID() string {
	if r := m.currentRow(); r != nil {
		return r.id()
	}
	return ""
}

// ---- options ----

// rowsMode is how many lines a ticket or a PR takes: two, with its details
// under the title, or one.
type rowsMode string

const (
	rowsTwo rowsMode = "two"
	rowsOne rowsMode = "one"
)

var rowsModes = []rowsMode{rowsTwo, rowsOne}

func parseRows(s string) rowsMode {
	if strings.TrimSpace(strings.ToLower(s)) == string(rowsOne) {
		return rowsOne
	}
	return rowsTwo
}

// options are the settings the panel offers: the order of every level of
// the tree, which PR rows show, what the list lists and how, how many lines
// a row takes, and which titles it shows.
func (m *model) options() []option {
	order := option{id: "order", label: "Order", key: "^s"}
	for i, o := range orderModes {
		order.values = append(order.values, string(o))
		if o == m.order {
			order.cur = i
		}
	}
	prs := option{id: "prs", label: "PRs"}
	for i, p := range prsModes {
		prs.values = append(prs.values, string(p))
		if p == m.prs {
			prs.cur = i
		}
	}
	show := option{id: "show", label: "Show", key: "^t"}
	for i, v := range showModes {
		show.values = append(show.values, string(v))
		if v == m.show {
			show.cur = i
		}
	}
	group := option{id: "group", label: "Group", key: "^g"}
	for i, v := range groupModes {
		group.values = append(group.values, string(v))
		if v == m.group {
			group.cur = i
		}
	}
	rows := option{id: "rows", label: "Rows", values: []string{"two lines", "one line"}}
	if m.rowsM == rowsOne {
		rows.cur = 1
	}
	titles := option{id: "titles", label: "Titles"}
	for i, v := range titlesModes {
		titles.values = append(titles.values, string(v))
		if v == m.titles {
			titles.cur = i
		}
	}
	return []option{order, prs, show, group, rows, titles}
}

// setOption changes a setting, remembers it and rebuilds the list around the
// row the cursor was on.
func (m *model) setOption(id string, v int) tea.Cmd {
	var more tea.Cmd
	switch id {
	case "order":
		m.order = orderModes[max(0, min(v, len(orderModes)-1))]
		saveSetting(stateDir(), "order", string(m.order))
	case "prs":
		m.prs = prsModes[max(0, min(v, len(prsModes)-1))]
		saveSetting(stateDir(), "prs", string(m.prs))
	case "show":
		m.show = showModes[max(0, min(v, len(showModes)-1))]
		saveSetting(stateDir(), "show", string(m.show))
	case "group":
		m.group = groupModes[max(0, min(v, len(groupModes)-1))]
		saveSetting(stateDir(), "group", string(m.group))
	case "rows":
		m.rowsM = rowsModes[max(0, min(v, len(rowsModes)-1))]
		saveSetting(stateDir(), "rows", string(m.rowsM))
	case "titles":
		m.titles = titlesModes[max(0, min(v, len(titlesModes)-1))]
		saveSetting(stateDir(), "titles", string(m.titles))
		m.summaries, m.keysC, m.metas = corpora(m.entries, m.titleOf)
		more = m.summarizeNext()
	default:
		return nil
	}
	m.syncHelp()
	cur := m.currentID()
	m.applyFilter()
	m.keepCursorOn(cur)
	m.renderList()
	return tea.Batch(m.flash.set(id+": "+m.optionValue(id)), m.updatePreview(), more)
}

func (m *model) optionValue(id string) string {
	switch id {
	case "prs":
		return string(m.prs)
	case "show":
		return string(m.show)
	case "group":
		return string(m.group)
	case "rows":
		return string(m.rowsM) + " line(s)"
	case "titles":
		return string(m.titles)
	}
	return string(m.order)
}

// syncHelp names, on the keys that cycle an option, the value the next
// press gives.
func (m *model) syncHelp() {
	opts := m.options()
	m.keys.Order.SetHelp("^s", "order by "+string(orderModes[nextValue(opts, "order")]))
	m.keys.Show.SetHelp("^t", "show "+string(showModes[nextValue(opts, "show")]))
	m.keys.Group.SetHelp("^g", "group by "+string(groupModes[nextValue(opts, "group")]))
}

// summarizeNext starts the next call of short titles, when the option wants
// them and something is missing; nothing while one runs.
func (m *model) summarizeNext() tea.Cmd {
	if m.titles != titlesShort || summarizerOff || m.summBusy {
		return nil
	}
	for _, batch := range pendingSummaries(m.entries, m.summ) {
		var send []summaryItem
		fresh := false
		for _, it := range batch {
			if it.Summary == "" && m.summTried[it.ID] {
				continue
			}
			if it.Summary == "" {
				fresh = true
			}
			send = append(send, it)
		}
		if !fresh {
			continue
		}
		for _, it := range send {
			m.summTried[it.ID] = true
		}
		m.summBusy = true
		return summarizeCmd(send)
	}
	return nil
}

// summarizeStartMsg asks Update to start the short titles (Init cannot keep
// what it marks).
type summarizeStartMsg struct{}

// ---- list rendering ----

func (m *model) renderList() {
	listW := m.listW()
	var b strings.Builder
	m.lineOf = m.lineOf[:0]
	m.lines = 0
	for i, r := range m.rows {
		m.lineOf = append(m.lineOf, m.lines)
		for _, l := range m.rowLines(r, i == m.cursor, listW) {
			if m.lines > 0 {
				b.WriteString("\n")
			}
			b.WriteString(l)
			m.lines++
		}
	}
	m.listVP.SetContent(b.String())
	m.ensureVisible()
}

// rowLines is a row as the lines it takes: one, or, for a ticket or a PR
// with two-line rows, its details under it.
func (m *model) rowLines(r row, selected bool, width int) []string {
	first := m.rowLine(r, selected, width)
	if m.rowsM != rowsTwo || !r.opens() {
		return []string{first}
	}
	return []string{first, m.detailLine(r, selected, width)}
}

// lineEnd is the line after the last one of row i.
func (m *model) lineEnd(i int) int {
	if i+1 < len(m.lineOf) {
		return m.lineOf[i+1]
	}
	return m.lines
}

// The tree's columns: a ticket at depth d has its fold arrow at arrowCol(d)
// and its key at keyCol(d); what it holds (its details, its PRs' keys, its
// sub-tickets' keys) starts 4 cells past its key, and its PR bullets and its
// sub-tickets' arrows 2 cells before that.
func arrowCol(d int) int { return 1 + 4*d }
func keyCol(d int) int   { return 3 + 4*d }

// arrow is the fold mark of a row that can fold: full-size triangles, ▶
// written as text so no terminal draws it as an emoji.
func arrow(folded bool) string {
	if folded {
		return "▶︎"
	}
	return "▼"
}

// lead is what a row's line has before its text: the gutter (the cursor's
// mark), the guides, the branch, the fold arrow or the PR's bullet, as
// cells. title says which of the row's two lines it is.
func (m *model) lead(r row, title bool) []string {
	var cells []string
	d := r.depth
	switch {
	case r.kind == rowPull && m.group == groupPhase:
		cells = make([]string, 2)
	case r.kind == rowIssue && m.group == groupPhase:
		cells = make([]string, 2)
	case r.kind == rowPull:
		cells = make([]string, keyCol(d))
	case title:
		cells = make([]string, keyCol(d))
	default:
		cells = make([]string, keyCol(d)+4)
	}
	for i := range cells {
		cells[i] = " "
	}
	set := func(i int, c string) {
		if i >= 0 && i < len(cells) {
			cells[i] = c
		}
	}
	if m.group == groupPhase {
		if !title {
			cells = append(cells, "    ")
		}
		return cells
	}
	for j, on := range r.rails {
		if on {
			set(arrowCol(j), "│")
		}
	}
	switch r.kind {
	case rowPull:
		if title {
			set(arrowCol(d), "○")
		}
	case rowIssue:
		if title {
			if d >= 1 {
				at := arrowCol(d - 1)
				set(at, map[bool]string{true: "└", false: "├"}[r.lastSib])
				end := arrowCol(d) - 1
				if !r.kids {
					end = arrowCol(d) + 1 // a leaf's branch runs through the arrow's place
				}
				for i := at + 1; i < end; i++ {
					set(i, "─")
				}
			}
			if r.kids {
				set(arrowCol(d), arrow(r.collapsed))
			}
		} else {
			if d >= 1 && !r.lastSib {
				set(arrowCol(d-1), "│")
			}
			if r.kidRail {
				set(arrowCol(d), "│")
			}
		}
	}
	return cells
}

// drawLead renders the lead: the gutter's mark on the selected row, the
// guides, arrows and bullets dim (lighter over the selection).
func drawLead(cells []string, selected bool) string {
	base, guide := lipgloss.NewStyle(), stDim
	if selected {
		base, guide = stRowSel, stDetailSel
	}
	var b strings.Builder
	for i, c := range cells {
		switch {
		case i == 0 && selected:
			b.WriteString(base.Render("▌"))
		case c == " " || c == "    ":
			b.WriteString(base.Render(c))
		default:
			b.WriteString(guide.Render(c))
		}
	}
	return b.String()
}

// rowLine is the first line of a row cut to width; the selected one is
// padded to it, so the highlight spans the list. The title comes last, so
// it is what a narrow list cuts.
func (m *model) rowLine(r row, selected bool, width int) string {
	base := lipgloss.NewStyle()
	if selected {
		base = stRowSel
	}
	dim := stDim
	if selected {
		dim = stDetailSel
	}
	var line string
	switch r.kind {
	case rowHeader:
		line = dim.Render(arrow(r.collapsed)) + base.Render(" ") + base.Foreground(stHeader.GetForeground()).Render(r.stack)
	case rowGroup:
		line = base.Render(" ") + dim.Render(arrow(r.collapsed)) + base.Render(" ") + dim.Render(r.text)
	case rowSection:
		label := "── " + r.text + " (" + strconv.Itoa(r.count) + ") "
		line = base.Render(" ") + dim.Render(arrow(r.collapsed)) + base.Render(" ") + dim.Render(label+strings.Repeat("─", max(4, 40-len(r.text))))
	case rowPull:
		line = drawLead(m.lead(r, true), selected) + m.pathOf(r, base) + m.pullKeyStyle(r, onBase(stPRKey, selected)).Render(strings.ToLower(r.p.Key))
		if m.rowsM == rowsOne {
			line += base.Render(" ") + m.detailWords(r, selected)
		}
		line += base.Render(" ") + titleStyle(base, r.ctx).Render(m.pullTitle(r.p))
	default:
		it := r.e.it
		ks := keyStyle(r.depth)
		if m.group == groupPhase {
			ks = keyStyle(len(r.path))
		}
		if r.ctx {
			ks = stDim
		}
		line = drawLead(m.lead(r, true), selected) + m.pathOf(r, base) + onBase(ks, selected).Render(strings.ToLower(it.Key))
		if m.rowsM == rowsOne {
			line += base.Render(" ") + m.detailWords(r, selected)
		}
		title := m.titleOf(r.e)
		ts := titleStyle(base, r.ctx)
		if r.match && len(r.idx) > 0 {
			line += base.Render(" ") + highlight(title, r.idx, ts)
		} else {
			line += base.Render(" ") + ts.Render(title)
		}
	}
	if selected {
		return selPadRow(truncate(line, width), width)
	}
	return truncate(line, width)
}

// titleStyle is a title's look: the foreground, faint, so the key stands
// out over it; dim for a row listed only to lead to a hit.
func titleStyle(base lipgloss.Style, ctx bool) lipgloss.Style {
	if ctx {
		return base.Foreground(stDim.GetForeground())
	}
	return base.Faint(true)
}

// onBase is a colored style as the row draws it: over the selection's
// background on the selected row.
func onBase(st lipgloss.Style, selected bool) lipgloss.Style {
	if selected {
		return stRowSel.Foreground(st.GetForeground())
	}
	return st
}

// selPadRow is selPad without bold: the list has none.
func selPadRow(s string, width int) string {
	if n := width - ansi.StringWidth(s); n > 0 {
		s += stRowSel.Render(strings.Repeat(" ", n))
	}
	return s
}

// pathOf is, in the phase view, the keys of the tickets a row hangs from,
// each in its level's color, joined by ›, before the row's own key.
func (m *model) pathOf(r row, base lipgloss.Style) string {
	if m.group != groupPhase || len(r.path) == 0 {
		return ""
	}
	selected := base.GetBackground() != lipgloss.NoColor{}
	var b strings.Builder
	for _, k := range r.path {
		b.WriteString(onBase(keyStyle(k.depth), selected).Render(strings.ToLower(k.key)))
		b.WriteString(titleStyle(base, false).Render(" › "))
	}
	return b.String()
}

// pullKeyStyle is the style of a PR's key: its own color, or the match
// style when the query is in it or in the branch, so the reader sees which
// PR matched.
func (m *model) pullKeyStyle(r row, base lipgloss.Style) lipgloss.Style {
	if q := strings.ToLower(strings.TrimSpace(m.ti.Value())); hasTerms(q) &&
		(strings.Contains(strings.ToLower(r.p.Key), q) || strings.Contains(strings.ToLower(r.p.Head), q)) {
		return matchOver(base)
	}
	return base
}

// detailLine is the second line of a ticket or a PR: its words, dim, with
// the ones that mean something in their colors, faint. It starts 4 cells
// past a ticket's key, under a PR's key.
func (m *model) detailLine(r row, selected bool, width int) string {
	line := drawLead(m.lead(r, false), selected) + m.detailWords(r, selected)
	if selected {
		return selPadRow(truncate(line, width), width)
	}
	return truncate(line, width)
}

// detailWords is what a details line says, most important first and the age
// always last. A ticket: its status, `no PR` when it goes on without one,
// `not yours` for a ghost. A PR: its state, what it needs, the counters of
// its checkout, the status of its ticket (by phase), the facts.
func (m *model) detailWords(r row, selected bool) string {
	dim := stDetail
	if selected {
		dim = stDetailSel
	}
	color := func(st lipgloss.Style) lipgloss.Style {
		return onBase(st, selected).Faint(true)
	}
	var parts []string
	word := func(f prFlag) {
		if f.tone == toneDim {
			parts = append(parts, dim.Render(f.text))
			return
		}
		parts = append(parts, color(toneStyle(f.tone)).Render(f.text))
	}
	if r.kind == rowPull {
		word(r.state)
		i := 0
		for ; i < r.needN; i++ { // what it needs; the facts come after the checkout
			word(r.flags[i])
		}
		if st, ok := m.local[r.p.URL]; ok {
			var cs []string
			for _, c := range localFlags(st) {
				cs = append(cs, color(lipgloss.NewStyle().Foreground(lipgloss.Color(c.color))).Render(c.text))
			}
			parts = append(parts, strings.Join(cs, dim.Render(" ")))
		}
		if m.group == groupPhase && r.pe != nil {
			parts = append(parts, color(stateStyle(r.pe.it.state())).Render(statusWord(r.pe.it)))
		}
		for ; i < len(r.flags); i++ {
			word(r.flags[i])
		}
	} else {
		it := r.e.it
		parts = append(parts, color(stateStyle(it.state())).Render(statusWord(it)))
		if r.noPR {
			parts = append(parts, dim.Render("no PR"))
		}
		if it.Ghost {
			parts = append(parts, dim.Render("not yours"))
		}
	}
	parts = append(parts, dim.Render(ageWord(r.last)))
	return strings.Join(parts, dim.Render(" · "))
}

// statusWord is a ticket's status as the details line says it, in lower
// case: the tracker's own status, or, for a GitHub issue (which has no
// workflow), the one its labels give (in progress, blocked), else open.
func statusWord(it issue) string {
	if it.Source == kindGitHub {
		switch it.state() {
		case stateDoing:
			return "in progress"
		case stateBlocked:
			return "blocked"
		}
		return "open"
	}
	return strings.ToLower(it.Status)
}

// ageWord is a row's last activity: today, else how long ago (3d, 2w).
func ageWord(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	now := time.Now()
	if y, mo, d := t.Local().Date(); y == now.Year() && mo == now.Month() && d == now.Day() {
		return "today"
	}
	return compactAge(t, now)
}

func (m *model) ensureVisible() {
	if m.cursor < 0 || m.cursor >= len(m.lineOf) {
		m.listVP.SetYOffset(0)
		return
	}
	// Scrolling up onto the first row of a group also reveals its header.
	top := withHeader(m.cursor, len(m.rows), func(i int) bool { return m.rows[i].kind == rowHeader })
	m.listVP.SetYOffset(scrollSpan(m.listVP.YOffset(), m.listVP.Height(), m.lines, m.lineOf[top], m.lineEnd(m.cursor)))
}

// ---- preview ----

func (m *model) updatePreview() tea.Cmd {
	r := m.currentRow()
	m.syncPreviewHeight()
	if r == nil || !r.opens() {
		m.clearPreview()
		return nil
	}
	key := rowPreviewKey(r, m.prevW())
	if !m.showRender(key) {
		return nil
	}
	return renderPreviewCmd(key, rowMarkdown(r), m.prevW(), m.previewStyle)
}

// ---- refresh plumbing ----

// finishRefresh swaps in the merged list. The snapshot is always persisted
// (failed sources keep their cached tickets and PRs), but FetchedAt only
// advances when every source succeeded so a partial failure revalidates on
// the next open.
func (m *model) finishRefresh() tea.Cmd {
	m.refreshing = false
	merged := mergeStacks(m.stacks, m.fresh, m.cache.Issues)
	pulls := mergePulls(m.stacks, m.freshPulls, m.cache.Pulls)
	fetchedAt := m.cache.FetchedAt
	if len(m.fetchErrs) == 0 {
		fetchedAt = time.Now()
	} else {
		m.netErr = strings.Join(m.fetchErrs, " · ")
		m.stale = true
	}
	m.cache = issueCache{FetchedAt: fetchedAt, Issues: merged, Pulls: pulls}
	saveCache(m.cache)
	m.fresh = nil
	m.freshPulls = nil
	m.fetchErrs = nil

	cur := m.currentID()
	m.setEntries(merged, pulls)
	m.applyFilter()
	m.keepCursorOn(cur)
	m.renderList()
	return tea.Batch(m.updatePreview(), localCmd(m.pulls), m.summarizeNext())
}

// ---- bubbletea ----

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, tea.RequestBackgroundColor, localCmd(m.pulls),
		func() tea.Msg { return summarizeStartMsg{} }}
	if m.refreshing {
		for _, s := range m.sources {
			cmds = append(cmds, fetchSourceCmd(s))
		}
	}
	// No preview yet: the size is not known, and a render at a made-up width is
	// one nobody sees. The first tea.WindowSizeMsg starts it, as in asgitlog.
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		m.renderList()
		return m, m.updatePreview()

	case sourceMsg:
		m.pending--
		if m.fresh == nil {
			m.fresh = map[string][]issue{}
			m.freshPulls = map[string][]pull{}
		}
		switch {
		case msg.err != nil:
			m.fetchErrs = append(m.fetchErrs, msg.err.Error())
		case strings.HasSuffix(msg.source, "/"+kindPulls):
			m.freshPulls[msg.source] = msg.pulls
		default:
			m.fresh[msg.source] = msg.issues
		}
		if m.pending > 0 {
			return m, nil
		}
		return m, m.finishRefresh()

	case tea.BackgroundColorMsg:
		return m, m.setPreviewStyle(glamourStyle(msg))

	case flashMsg:
		return m, m.flash.set(string(msg))

	case flashErrMsg:
		return m, m.flash.fail(string(msg))

	case clearFlashMsg:
		m.flash.clear(msg)
		return m, nil

	case previewMsg:
		m.handlePreview(msg)
		return m, nil

	case localMsg:
		m.local = msg.states
		m.renderList()
		return m, nil

	case summarizeStartMsg:
		return m, m.summarizeNext()

	case summariesMsg:
		m.summBusy = false
		if msg.err != nil {
			if !errors.Is(msg.err, errNoSummarizer) {
				m.netErr = "short titles: " + msg.err.Error()
			}
			return m, nil
		}
		if len(msg.written) == 0 {
			return m, m.summarizeNext()
		}
		for k, v := range msg.written {
			m.summ[k] = v
		}
		settleHashes(m.entries, m.summ, msg.written)
		saveSummaries(m.summ)
		cur := m.currentID()
		m.summaries, m.keysC, m.metas = corpora(m.entries, m.titleOf)
		m.applyFilter()
		m.keepCursorOn(cur)
		m.renderList()
		return m, m.summarizeNext()

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseWheelMsg:
		if m.panel.open {
			return m, nil
		}
		// Over the list the wheel moves the selection, as in asgitlog; anywhere
		// else it scrolls the preview.
		if m.overList(msg.X, msg.Y) {
			if k, ok := wheelKey(msg); ok {
				return m.handleKey(k)
			}
			return m, nil
		}
		m.prevVP, _ = m.prevVP.Update(msg)
		return m, nil

	case tea.MouseClickMsg:
		if m.panel.open {
			return m, nil
		}
		return m.handleClick(msg)

	case tea.PasteMsg:
		if m.panel.open {
			return m, nil // nothing is typed under the panel
		}
		return m.toInput(msg)

	default:
		// Whatever else the input takes (its own paste, the cursor's blink).
		return m.toInput(msg)
	}
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	m.netErr = "" // like a notice: the next key gives the help line back (status keeps the mark)
	switch {
	case msg.String() == "ctrl+c":
		return m, tea.Quit
	case m.panel.open:
		// The panel takes every key: esc closes it before anything else.
		if a := m.panel.update(msg, m.options()); a.id != "" {
			return m, m.setOption(a.id, a.value)
		}
		return m, nil
	case isHelpKey(msg):
		m.panel.toggle()
		return m, nil
	case msg.String() == "q" && m.ti.Value() == "":
		// q quits only while the filter is empty; otherwise it is text.
		return m, tea.Quit
	case key.Matches(msg, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Select):
		if r := m.currentRow(); r != nil && r.opens() {
			m.openURL = r.url()
			return m, tea.Quit
		}
		return m, m.flash.fail("nothing to open")
	case key.Matches(msg, m.keys.Copy):
		if r := m.currentRow(); r != nil && r.opens() {
			if r.kind == rowPull {
				return m, copyCmd("asgotoissues", "", r.p.URL)
			}
			return m, copyCmd("asgotoissues", "", r.e.it.Key)
		}
		return m, copyCmd("asgotoissues", "", "")
	case key.Matches(msg, m.keys.Order):
		return m, m.setOption("order", nextValue(m.options(), "order"))
	case key.Matches(msg, m.keys.Show):
		return m, m.setOption("show", nextValue(m.options(), "show"))
	case key.Matches(msg, m.keys.Group):
		return m, m.setOption("group", nextValue(m.options(), "group"))
	case key.Matches(msg, m.keys.Shallow):
		return m, m.foldTo(true)
	case key.Matches(msg, m.keys.Deeper):
		return m, m.foldTo(false)
	case key.Matches(msg, m.keys.Fold) && m.ti.Value() == "":
		return m, m.fold()
	case m.keys.Nav.matches(msg):
		page := m.listVP.Height()
		if m.rowsM == rowsTwo {
			page = max(1, page/2) // a page of rows, not of lines
		}
		m.cursor = m.keys.Nav.move(msg, m.cursor, len(m.rows), page,
			func(i int) bool { return m.rows[i].selectable() })
		m.renderList()
		return m, m.updatePreview()
	case key.Matches(msg, m.keys.Shrink):
		return m, m.resizeList(false)
	case key.Matches(msg, m.keys.Grow):
		return m, m.resizeList(true)
	case key.Matches(msg, m.keys.PrevUp):
		m.prevVP.ScrollUp(3)
		return m, nil
	case key.Matches(msg, m.keys.PrevDown):
		m.prevVP.ScrollDown(3)
		return m, nil
	}

	return m.toInput(msg)
}

// toInput hands a message to the filter input and, when that changed the
// query, filters again: a key, a paste from the terminal (tea.PasteMsg) or the
// input's own ctrl+v all come through here, so the list never lags behind
// what the input shows. A message that leaves the query alone moves nothing:
// the cursor stays on the row it was on.
func (m model) toInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	cur := m.currentID()
	cmd, changed := typeInto(&m.ti, msg)
	if !changed {
		return m, cmd
	}
	m.applyFilter()
	if !hasTerms(m.ti.Value()) {
		// Clearing the query rebuilt the rows; stay on the same ticket instead
		// of whatever now sits at the old cursor index.
		m.keepCursorOn(cur)
	}
	m.renderList()
	return m, tea.Batch(cmd, m.updatePreview())
}

// overList reports whether a screen cell is inside the list.
func (m *model) overList(x, y int) bool {
	return inList(x, y, listY, m.listW(), m.bodyH())
}

// handleClick moves the selection to the row under a left click on the list.
// It never opens anything: that stays on enter.
func (m model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft || !m.overList(msg.X, msg.Y) {
		return m, nil
	}
	line, ok := rowUnder(msg.Y, listY, m.listVP.YOffset(), m.lines)
	if !ok {
		return m, nil
	}
	i, ok := rowOfLine(line, m.lineOf)
	if !ok || !m.rows[i].selectable() || i == m.cursor {
		return m, nil
	}
	m.cursor = i
	m.renderList()
	return m, m.updatePreview()
}

// View declares the screen: alt screen and cell-motion mouse reports.
func (m model) View() tea.View { return popupView(m.render(), true) }

// render stacks the sections in one frame (see frame.go); the tests assert on
// it. There is no context line: the stacks already head their groups.
func (m model) render() string {
	w := m.width
	out := frameHead(w, withDevMark(m.status()), m.ti.View())
	pos := ""
	if m.currentRow() != nil {
		pos = scrollPos(&m.prevVP)
	}
	out = append(out, splitMain(m.listLines(), strings.Split(m.rightColumn(), "\n"),
		m.listW(), m.detailsW(), m.counter(), pos)...)
	out = append(out, framed(w, footLine(m.flash, m.netErr, "", m.help, m.keys, w-4)), hline(w, "╰", "╯", "", ""))
	if m.panel.open {
		keys := keyLines(m.help, m.keys, w-10)
		out = overlay(out, panelLines(m.options(), m.panel.cursor, keys, w-4, len(out)-2), w)
	}
	return strings.Join(out, "\n")
}

// counter is the matches/total count of my tickets, for the edge under the
// list: ghosts and PRs are context, not tickets.
func (m model) counter() string {
	n := 0
	for _, r := range m.rows {
		if r.kind == rowIssue && !r.e.it.Ghost && !r.ctx {
			n++
		}
	}
	total := 0
	for _, e := range m.entries {
		if !e.it.Ghost {
			total++
		}
	}
	return stCount.Render(strconv.Itoa(n) + "/" + strconv.Itoa(total))
}

// status is the refresh mark, for the edge over the input.
func (m model) status() string { return refreshMark(m.refreshing, m.stale) }

// listLines is the list as exactly bodyH lines of listW cells.
// leftColumn is the list, or the reason there is nothing to list. A fetch
// error is on the help line already.
func (m model) leftColumn() string {
	if len(m.rows) > 0 {
		return m.listVP.View()
	}
	reason := "No open issues"
	if len(m.entries) == 0 && m.refreshing {
		reason = "Loading issues from " + strconv.Itoa(len(m.sources)) + " source(s)…"
	}
	return emptyList("", m.ti.Value(), reason, m.listW())
}

func (m model) listLines() []string { return fitLines(m.leftColumn(), m.bodyH(), m.listW()) }

func (m model) rightColumn() string {
	w := m.prevW()
	r := m.currentRow()
	if r == nil {
		return "" // the list says why it is empty (leftColumn)
	}
	return m.headerOf(r, w) + "\n\n" + m.prevVP.View()
}
