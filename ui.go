package main

// The bubbletea model: a filter input on top, a two-column body (grouped
// ticket list left, rendered description right) and a help footer. Modeled
// on asgotopr/asgoto: the input is focused before the program starts,
// every printable key filters, and the selected ticket is opened in the
// browser AFTER the TUI exits (quitting is what closes the popup).

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
		{k.Select, k.Copy},
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
		// Help-only entry: a binding without keys is disabled and the help
		// bubble would skip it. Nothing ever matches against it.
		Filter: key.NewBinding(key.WithKeys("type"), key.WithHelp("type", "filter")),
		Help:   helpBinding(false),
	}
}

// ---- model ----

type model struct {
	// data
	stacks    []stack
	sources   []source
	entries   []*entry
	summaries []string // parallel corpora, see filter.go
	keysC     []string
	metas     []string
	cache     issueCache

	// background refresh
	pending    int // sources still fetching
	fresh      map[string][]issue
	fetchErrs  []string
	refreshing bool
	netErr     string
	stale      bool // the last refresh failed: the list is the cached one (see status)

	// ui
	rows    []row
	cursor  int
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
// background refresh of every stack from Init.
func newModel(stacks []stack, cache issueCache, stale bool) model {
	m := model{
		stacks:       stacks,
		sources:      sourcesOf(stacks),
		cache:        cache,
		refreshing:   stale,
		ti:           newFilterInput("asgotoissues", "Search by title, key, status, repo…"),
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
	m.setEntries(cache.Issues)
	m.applyFilter()
	m.resize()
	m.renderList()
	return m
}

func (m *model) currentRow() *row {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].kind == "issue" {
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
func (m *model) bodyH() int { return max(1, m.height-frameRows(false)-1) }

func (m *model) resize() {
	m.listVP.SetWidth(m.listW())
	m.listVP.SetHeight(m.bodyH())
	m.prevVP.SetWidth(m.prevW())
	prevH := m.bodyH() - 3 // preview header (2 lines) + blank
	if prevH < 1 {
		prevH = 1
	}
	m.prevVP.SetHeight(prevH)
	m.help.SetWidth(max(0, m.width-4))
	sizeInput(&m.ti, m.width-4)
}

// resizeList moves the divider between the list and the preview by one step.
func (m *model) resizeList(grow bool) tea.Cmd {
	m.split = moveSplit(stateDir(), m.split, grow)
	m.resize()
	m.renderList()
	return m.updatePreview()
}

func (m *model) setEntries(issues []issue) {
	m.entries = buildEntries(issues)
	m.summaries, m.keysC, m.metas = corpora(m.entries)
}

func (m *model) applyFilter() {
	q := strings.TrimSpace(m.ti.Value())
	m.rows = buildRows(m.entries, q, m.summaries, m.keysC, m.metas)
	if hasTerms(q) {
		m.cursor = firstIssue(m.rows) // ranked: the best match is the first row
		return
	}
	if m.cursor < 0 || m.cursor >= len(m.rows) || m.rows[m.cursor].kind != "issue" {
		m.cursor = firstIssue(m.rows)
	}
}

func (m *model) keepCursorOn(url string) {
	if url == "" {
		return
	}
	for i, r := range m.rows {
		if r.kind == "issue" && r.e.it.URL == url {
			m.cursor = i
			return
		}
	}
	m.cursor = firstIssue(m.rows)
}

// ---- list rendering ----

// keyW is the shared key column width over all entries, so filtering doesn't
// shift columns.
func (m *model) keyW() int {
	w := 0
	for _, e := range m.entries {
		if l := ansi.StringWidth(e.it.Key); l > w {
			w = l
		}
	}
	return w
}

func (m *model) renderList() {
	keyW := m.keyW()
	listW := m.listW()
	var b strings.Builder
	for i, r := range m.rows {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.rowLine(r, i == m.cursor, keyW, listW))
	}
	m.listVP.SetContent(b.String())
	m.ensureVisible()
}

// rowLine is one row cut to width; the selected one is padded to it, so the
// highlight spans the list.
func (m *model) rowLine(r row, selected bool, keyW, width int) string {
	if r.kind == "header" {
		return truncate(stHeader.Render(r.stack), width)
	}
	it := r.e.it
	pad := strings.Repeat(" ", max(0, keyW-ansi.StringWidth(it.Key)))
	if selected {
		return selPad(truncate(stSel.Render("▌ "+it.Key+pad+" ")+highlight(it.Summary, r.idx, stSel), width), width)
	}
	title := it.Summary
	if r.match && len(r.idx) > 0 {
		title = highlight(title, r.idx, lipgloss.NewStyle())
	}
	return truncate("  "+stateStyle(it.state()).Render(it.Key)+pad+" "+title, width)
}

func (m *model) ensureVisible() {
	// Scrolling up onto the first row of a group also reveals its header.
	top := withHeader(m.cursor, len(m.rows), func(i int) bool { return m.rows[i].kind == "header" })
	m.listVP.SetYOffset(scrollTo(m.listVP.YOffset(), m.listVP.Height(), len(m.rows), m.cursor, top))
}

// ---- preview ----

func (m *model) updatePreview() tea.Cmd {
	r := m.currentRow()
	if r == nil {
		m.clearPreview()
		return nil
	}
	if !m.showRender(previewKey(r.e.it, m.prevW())) {
		return nil
	}
	return renderPreviewCmd(r.e.it, m.prevW(), m.previewStyle)
}

// ---- refresh plumbing ----

// finishRefresh swaps in the merged list. The snapshot is always persisted
// (failed stacks keep their cached tickets), but FetchedAt only advances when
// every stack succeeded so a partial failure revalidates on the next open.
func (m *model) finishRefresh() tea.Cmd {
	m.refreshing = false
	merged := mergeStacks(m.stacks, m.fresh, m.cache.Issues)
	fetchedAt := m.cache.FetchedAt
	if len(m.fetchErrs) == 0 {
		fetchedAt = time.Now()
	} else {
		m.netErr = strings.Join(m.fetchErrs, " · ")
		m.stale = true
	}
	m.cache = issueCache{FetchedAt: fetchedAt, Issues: merged}
	saveCache(m.cache)
	m.fresh = nil
	m.fetchErrs = nil

	var curURL string
	if r := m.currentRow(); r != nil {
		curURL = r.e.it.URL
	}
	m.setEntries(merged)
	m.applyFilter()
	m.keepCursorOn(curURL)
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
		}
		if msg.err != nil {
			m.fetchErrs = append(m.fetchErrs, msg.err.Error())
		} else {
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
		m.panel.update(msg, nil)
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
			m.openURL = r.e.it.URL
			return m, tea.Quit
		}
		return m, m.flash.fail("nothing to open")
	case key.Matches(msg, m.keys.Copy):
		if r := m.currentRow(); r != nil {
			return m, copyCmd("asgotoissues", "", r.e.it.Key)
		}
		return m, copyCmd("asgotoissues", "", "")
	case m.keys.Nav.matches(msg):
		m.cursor = m.keys.Nav.move(msg, m.cursor, len(m.rows), m.listVP.Height(),
			func(i int) bool { return m.rows[i].kind == "issue" })
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
	var curURL string
	if r := m.currentRow(); r != nil {
		curURL = r.e.it.URL
	}
	cmd, changed := typeInto(&m.ti, msg)
	if !changed {
		return m, cmd
	}
	m.applyFilter()
	if !hasTerms(m.ti.Value()) {
		// Clearing the query rebuilt the rows; stay on the same ticket instead
		// of whatever now sits at the old cursor index.
		m.keepCursorOn(curURL)
	}
	m.renderList()
	return m, tea.Batch(cmd, m.updatePreview())
}

// overList reports whether a screen cell is inside the list.
func (m *model) overList(x, y int) bool {
	return inList(x, y, listY(false), m.listW(), m.bodyH())
}

// handleClick moves the selection to the ticket row under a left click on the
// list. It never opens the ticket: that stays on enter.
func (m model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft || !m.overList(msg.X, msg.Y) {
		return m, nil
	}
	i, ok := rowUnder(msg.Y, listY(false), m.listVP.YOffset(), len(m.rows))
	if !ok || m.rows[i].kind != "issue" || i == m.cursor {
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
	out := frameHead(w, "", withDevMark(m.status()), m.ti.View())
	pos := ""
	if m.currentRow() != nil {
		pos = scrollPos(&m.prevVP)
	}
	out = append(out, splitMain(m.listLines(), strings.Split(m.rightColumn(), "\n"),
		m.listW(), m.detailsW(), m.counter(), pos)...)
	out = append(out, framed(w, footLine(m.flash, m.netErr, m.help, m.keys, w-4)), hline(w, "╰", "╯", "", ""))
	if m.panel.open {
		keys := keyLines(m.help, m.keys, w-10)
		out = overlay(out, panelLines(nil, m.panel.cursor, keys, w-4, len(out)-2), w)
	}
	return strings.Join(out, "\n")
}

// counter is the matches/total count, for the edge under the list.
func (m model) counter() string {
	n := 0
	for _, r := range m.rows {
		if r.kind == "issue" {
			n++
		}
	}
	return stCount.Render(strconv.Itoa(n) + "/" + strconv.Itoa(len(m.entries)))
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
	return previewHeader(r.e.it, w) + "\n\n" + m.prevVP.View()
}
