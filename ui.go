package main

// The bubbletea model: a filter input on top, a two-column body (the tree of
// tickets and their PRs on the left, the rendered description on the right)
// and a help footer. Modeled on asgotopr/asgoto: the input is focused before
// the program starts, every printable key filters, and the selected row is
// opened in the browser AFTER the TUI exits (quitting is what closes the
// popup).

import (
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
	stHeader  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	stDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stTitle   = lipgloss.NewStyle().Bold(true)
	stKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	stCount   = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stError   = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	stTodo    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stDoing   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stBlocked = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	stOK      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	stWarn    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	stBad     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// stateStyle colors an issue by its state: green in progress, red blocked,
// dim to do.
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

// levelStyle colors what a PR needs: red when it is mine to do, yellow when
// it waits, green when it is ready, dim otherwise.
func levelStyle(level int) lipgloss.Style {
	switch level {
	case levelBad:
		return stBad
	case levelWarn:
		return stWarn
	case levelOK:
		return stOK
	}
	return stDim
}

// flagsLine is a PR's flags, each in its color, joined for the preview.
// base is the style they are drawn over.
func flagsLine(flags []prFlag, base lipgloss.Style) string {
	parts := make([]string, len(flags))
	for i, f := range flags {
		parts[i] = base.Foreground(levelStyle(f.level).GetForeground()).Render(f.text)
	}
	return strings.Join(parts, base.Foreground(stDim.GetForeground()).Render(" · "))
}

// flagTexts is what the flags say, for a line in one tone.
func flagTexts(flags []prFlag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = f.text
	}
	return out
}

// The list is two tones, so the eye scans titles: the first line of a row is
// plain, the details under it are dim. Over the selection's background the
// details are a lighter grey, so they can still be read there.
var (
	stDetail    = stDim
	stDetailSel = stSel.Foreground(lipgloss.Color("7")).Bold(false)
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
	Fold     key.Binding
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
		{k.Select, k.Copy, k.Order, k.Fold},
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
		// space folds only while the filter is empty; otherwise it is text.
		Fold: key.NewBinding(key.WithKeys("space"), key.WithHelp("space", "fold/unfold (empty filter)")),
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
	order      orderMode
	prs        prsMode
	rowsM      rowsMode
	hideMerged bool // leave out the merged PRs and the tickets with nothing left under them

	collapsed map[string]bool // the tickets folded by hand, by URL, for this run

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
		order:        parseOrder(loadSetting(stateDir(), "order")),
		prs:          parsePrs(loadSetting(stateDir(), "prs")),
		rowsM:        parseRows(loadSetting(stateDir(), "rows")),
		hideMerged:   loadSetting(stateDir(), "merged") == "hide",
		collapsed:    map[string]bool{},
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
		hh = lipgloss.Height(headerOf(r, m.prevW())) + 1 // and the blank line under it
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
	m.summaries, m.keysC, m.metas = corpora(m.entries)
}

func (m *model) applyFilter() {
	q := strings.TrimSpace(m.ti.Value())
	m.rows = buildRows(m.entries, m.pulls, m.unlinked, m.order, m.prs, m.hideMerged, m.collapsed, q, m.summaries, m.keysC, m.metas)
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

// fold folds or unfolds the ticket under the cursor (a PR's ticket when the
// cursor is on a PR): its PRs and children leave the list or come back, and
// the cursor sits on the ticket. A ticket with nothing under it says so.
func (m *model) fold() tea.Cmd {
	r := m.currentRow()
	if r == nil {
		return m.flash.fail("nothing to fold")
	}
	url := r.url()
	if r.kind == rowPull {
		url = r.parent
	}
	var target *row
	for i := range m.rows {
		if m.rows[i].kind == rowIssue && m.rows[i].e.it.URL == url {
			target = &m.rows[i]
			break
		}
	}
	if target == nil || !target.kids {
		return m.flash.fail("nothing to fold")
	}
	id := target.id()
	if m.collapsed[url] {
		delete(m.collapsed, url)
	} else {
		m.collapsed[url] = true
	}
	m.applyFilter()
	m.keepCursorOn(id)
	m.renderList()
	return m.updatePreview()
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
// the tree, which PR rows show, and how many lines a row takes.
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
	rows := option{id: "rows", label: "Rows", values: []string{"two lines", "one line"}}
	if m.rowsM == rowsOne {
		rows.cur = 1
	}
	merged := option{id: "merged", label: "Merged", values: []string{"show", "hide"}}
	if m.hideMerged {
		merged.cur = 1
	}
	return []option{order, prs, rows, merged}
}

// setOption changes a setting, remembers it and rebuilds the list around the
// row the cursor was on.
func (m *model) setOption(id string, v int) tea.Cmd {
	switch id {
	case "order":
		m.order = orderModes[max(0, min(v, len(orderModes)-1))]
		saveSetting(stateDir(), "order", string(m.order))
	case "prs":
		m.prs = prsModes[max(0, min(v, len(prsModes)-1))]
		saveSetting(stateDir(), "prs", string(m.prs))
	case "rows":
		m.rowsM = rowsModes[max(0, min(v, len(rowsModes)-1))]
		saveSetting(stateDir(), "rows", string(m.rowsM))
	case "merged":
		m.hideMerged = v == 1
		saveSetting(stateDir(), "merged", m.optionValue("merged"))
	default:
		return nil
	}
	m.syncHelp()
	cur := m.currentID()
	m.applyFilter()
	m.keepCursorOn(cur)
	m.renderList()
	return tea.Batch(m.flash.set(id+": "+m.optionValue(id)), m.updatePreview())
}

func (m *model) optionValue(id string) string {
	switch id {
	case "prs":
		return string(m.prs)
	case "rows":
		return string(m.rowsM) + " line(s)"
	case "merged":
		if m.hideMerged {
			return "hide"
		}
		return "show"
	}
	return string(m.order)
}

// syncHelp names, on the order key, the order the next press gives.
func (m *model) syncHelp() {
	next := orderModes[nextValue(m.options(), "order")]
	m.keys.Order.SetHelp("^s", "order by "+string(next))
}

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
	if m.rowsM != rowsTwo || !r.selectable() {
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

// rowLine is the first line of a row cut to width; the selected one is
// padded to it, so the highlight spans the list. A selectable row starts
// with the gutter (the cursor's mark), then its indent, then its key and,
// one space on, its title, which comes last, so it is what a narrow list
// cuts.
func (m *model) rowLine(r row, selected bool, width int) string {
	switch r.kind {
	case rowHeader:
		return truncate(stHeader.Render(r.stack), width)
	case rowGroup:
		return truncate(stDim.Render("  "+r.text), width)
	case rowPull:
		return m.pullLine(r, selected, width)
	}
	it := r.e.it
	indent := strings.Repeat(" ", r.col)
	// The first line is plain, the key included: the state is said in words,
	// on the second line or, with one-line rows, right after the key. A ghost
	// or a context row is dim: it is not one of mine, or not a hit.
	keyStyle := lipgloss.NewStyle()
	if r.e.it.Ghost || r.ctx {
		keyStyle = stDim
	}
	status := func(base lipgloss.Style) string {
		if m.rowsM == rowsTwo {
			return ""
		}
		return base.Render("[" + it.Status + "] ")
	}
	// A folded ticket carries its mark in the gutter, so the key stays put.
	gutter := "  "
	switch {
	case selected && r.collapsed:
		gutter = "▌▸"
	case selected:
		gutter = "▌ "
	case r.collapsed:
		gutter = " ▸"
	}
	if selected {
		return selPad(truncate(stSel.Render(gutter+indent+it.Key+" ")+status(stSel)+highlight(it.Summary, r.idx, stSel), width), width)
	}
	title := it.Summary
	switch {
	case r.match && len(r.idx) > 0:
		title = highlight(title, r.idx, lipgloss.NewStyle())
	case it.Ghost || r.ctx:
		title = stDim.Render(title)
	}
	return truncate(gutter+indent+keyStyle.Render(it.Key)+" "+status(lipgloss.NewStyle())+title, width)
}

// pullLine is a PR's first line: the gutter, the indent and, with two-line
// rows, `↳ repo#N title` (the rest goes under the title); with one-line
// rows `↳ repo#N`, its flags and its title.
func (m *model) pullLine(r row, selected bool, width int) string {
	indent := strings.Repeat(" ", r.col)
	base := lipgloss.NewStyle()
	if selected {
		base = stSel
	}
	var line string
	if m.rowsM == rowsTwo {
		line = base.Render(indent+"↳ ") + m.pullKeyStyle(r, base).Render(r.p.Key) + base.Render(" "+r.p.Title)
	} else {
		// One line: the flags between the key and the title, dim, so the
		// line still reads as key and title.
		detail := stDetail
		if selected {
			detail = stDetailSel
		}
		line = base.Render(indent+"↳ ") + m.pullKeyStyle(r, base).Render(r.p.Key)
		if len(r.flags) > 0 {
			line += base.Render("  ") + detail.Render(strings.Join(flagTexts(r.flags), " · "))
		}
		line += base.Render("  " + r.p.Title)
	}
	if selected {
		return selPad(truncate(stSel.Render("▌ ")+line, width), width)
	}
	return truncate("  "+line, width)
}

// pullKeyStyle is the style of a PR's key: plain, like the line, or the
// match style when the query is in it or in the branch, so the reader sees
// which PR matched.
func (m *model) pullKeyStyle(r row, base lipgloss.Style) lipgloss.Style {
	if q := strings.ToLower(strings.TrimSpace(m.ti.Value())); hasTerms(q) &&
		(strings.Contains(strings.ToLower(r.p.Key), q) || strings.Contains(strings.ToLower(r.p.Head), q)) {
		return matchOver(base)
	}
	return base
}

// detailLine is the second line of a ticket or a PR with two-line rows,
// aligned under the title (one space past the key) and in one dim tone, so
// the titles stay what the eye scans: a ticket's status, type, last update,
// how many PRs it has and what they and its descendants' need, in words
// (or that it is not in my list); a PR's state, flags, author when it is
// not me, and last update. Under the selection the tone is a lighter grey,
// readable over its background.
func (m *model) detailLine(r row, selected bool, width int) string {
	detail, gutter := stDetail, "  "
	if selected {
		detail, gutter = stDetailSel, stSel.Render("▌ ")
	}
	lead := strings.Repeat(" ", r.col)
	var parts []string
	if r.kind == rowPull {
		p := r.p
		lead += "  " + strings.Repeat(" ", ansi.StringWidth(p.Key)+1) // under the title, past "↳ " and the key
		if p.State == prOpen && !p.Draft {
			parts = append(parts, "open")
		}
		parts = append(parts, flagTexts(r.flags)...)
		if !p.Mine && p.Author != "" {
			parts = append(parts, "by "+p.Author)
		}
		parts = append(parts, relTime(p.Updated))
	} else {
		it := r.e.it
		lead += strings.Repeat(" ", ansi.StringWidth(it.Key)+1)
		parts = append(parts, it.Status)
		if it.Type != "" {
			parts = append(parts, it.Type)
		}
		parts = append(parts, "updated "+relTime(it.Updated))
		if it.Ghost {
			parts = append(parts, "not in your list")
		}
		if len(r.e.pulls) > 0 {
			parts = append(parts, plural(len(r.e.pulls), "PR"))
		}
		if r.merged {
			parts = append(parts, "all merged")
		}
		parts = append(parts, flagTexts(r.needs)...)
	}
	line := gutter + detail.Render(lead+strings.Join(parts, " · "))
	if selected {
		return selPad(truncate(line, width), width)
	}
	return truncate(line, width)
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
	if r == nil {
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
	return m.updatePreview()
}

// ---- bubbletea ----

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, tea.RequestBackgroundColor}
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
		if r := m.currentRow(); r != nil {
			m.openURL = r.url()
			return m, tea.Quit
		}
		return m, m.flash.fail("nothing to open")
	case key.Matches(msg, m.keys.Copy):
		if r := m.currentRow(); r != nil {
			if r.kind == rowPull {
				return m, copyCmd("asgotoissues", "", r.p.URL)
			}
			return m, copyCmd("asgotoissues", "", r.e.it.Key)
		}
		return m, copyCmd("asgotoissues", "", "")
	case key.Matches(msg, m.keys.Order):
		return m, m.setOption("order", nextValue(m.options(), "order"))
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
	return headerOf(r, w) + "\n\n" + m.prevVP.View()
}
