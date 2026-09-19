package main

// The bubbletea model: a filter input on top, a two-column body (grouped
// ticket list left, rendered description right) and a help footer. Modeled
// on asgotopr/asgoto: the input is focused before the program starts,
// every printable key filters, and the selected ticket is opened in the
// browser AFTER the TUI exits (quitting is what closes the popup).

import (
	"os"
	"os/exec"
	"runtime"
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

func truncate(s string, width int) string {
	if width < 1 {
		return ""
	}
	return ansi.Truncate(s, width, "…")
}

// ---- styles ----

var (
	stPrompt  = lipgloss.NewStyle().Foreground(lipgloss.Color("13")).Bold(true)
	stDev     = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
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
	Cancel   key.Binding
	PrevUp   key.Binding
	PrevDown key.Binding
	Shrink   key.Binding
	Grow     key.Binding
	Filter   key.Binding
	Help     key.Binding
}

// ShortHelp is the folded help line: the tool's own actions, the help and the
// quit keys. Moving, scrolling and resizing are in the expanded help, so the
// line stays short enough for a narrow popup (a cut line loses the quit keys
// first).
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Filter, k.Select, k.Help, k.Cancel}
}

// FullHelp is what `?` expands the help into, one column per group: the
// filter and the preview, the list, the tool's actions, help and quit.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Filter, k.PrevUp, k.Shrink},
		{k.Nav.Up, k.Nav.PageUp, k.Nav.Top},
		{k.Select},
		{k.Help, k.Cancel},
	}
}

func defaultKeys() keyMap {
	return keyMap{
		Nav:      defaultListNav(),
		Select:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open in browser")),
		Cancel:   key.NewBinding(key.WithKeys("esc", "ctrl+c"), key.WithHelp("esc/q", "quit")),
		PrevUp:   key.NewBinding(key.WithKeys("shift+up"), key.WithHelp("⇧↑/⇧↓", "scroll the description")),
		PrevDown: key.NewBinding(key.WithKeys("shift+down")),
		Shrink:   key.NewBinding(key.WithKeys("shift+left"), key.WithHelp("⇧←/⇧→", "resize the list")),
		Grow:     key.NewBinding(key.WithKeys("shift+right")),
		// Help-only entry: a binding without keys is disabled and the help
		// bubble would skip it. Nothing ever matches against it.
		Filter: key.NewBinding(key.WithKeys("type"), key.WithHelp("type", "filter")),
		Help:   helpKey,
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

	// previewStyle is the glamour standard style ("dark"/"light"). It starts
	// as "dark" and flips when the terminal answers RequestBackgroundColor.
	previewStyle string

	openURL string // opened in the browser after quit ("" = none)
}

// newModel builds the model from the cached snapshot. stale starts the
// background refresh of every stack from Init.
func newModel(stacks []stack, cache issueCache, stale bool, prompt string) model {
	m := model{
		stacks:       stacks,
		sources:      sourcesOf(stacks),
		cache:        cache,
		refreshing:   stale,
		ti:           newFilterInput(prompt),
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
func (m *model) bodyH() int { return max(1, m.height-frameRows(false)-m.footH()) }

// footH is the height of the foot: a message takes one line, the help more
// while `?` has it expanded; the main section keeps at least minBodyH.
func (m *model) footH() int {
	if m.footMsg() != "" {
		return 1
	}
	return helpHeight(m.help, m.keys, m.height-frameRows(false)-minBodyH)
}

const minBodyH = 4

func (m *model) toggleHelp() tea.Cmd {
	m.help.ShowAll = !m.help.ShowAll
	m.resize()
	m.renderList()
	return m.updatePreview()
}

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
}

// resizeList moves the divider between the list and the preview by one step.
func (m *model) resizeList(grow bool) tea.Cmd {
	m.split = stepSplit(m.split, grow)
	saveSplit(stateDir(), m.split)
	m.resize()
	m.renderList()
	return m.updatePreview()
}

func (m *model) setEntries(issues []issue) {
	m.entries = buildEntries(issues)
	m.summaries, m.keysC, m.metas = corpora(m.entries)
}

func (m *model) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(m.ti.Value()))
	m.rows = buildRows(m.entries, q, m.summaries, m.keysC, m.metas)
	if q != "" {
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
		b.WriteString(truncate(m.rowLine(r, i == m.cursor, keyW), listW))
	}
	m.listVP.SetContent(b.String())
	m.ensureVisible()
}

func (m *model) rowLine(r row, selected bool, keyW int) string {
	if r.kind == "header" {
		return stHeader.Render(r.stack)
	}
	it := r.e.it
	pad := strings.Repeat(" ", max(0, keyW-ansi.StringWidth(it.Key)))
	if selected {
		return stSel.Render("▌ "+it.Key+pad+" ") + highlight(it.Summary, r.idx, stSel)
	}
	title := it.Summary
	if r.match && len(r.idx) > 0 {
		title = highlight(title, r.idx, lipgloss.NewStyle())
	}
	return "  " + stateStyle(it.state()).Render(it.Key) + pad + " " + title
}

func (m *model) ensureVisible() {
	h := m.listVP.Height()
	if h <= 0 || m.cursor < 0 {
		return
	}
	if m.cursor < m.listVP.YOffset() {
		m.listVP.SetYOffset(m.cursor)
	} else if m.cursor >= m.listVP.YOffset()+h {
		m.listVP.SetYOffset(m.cursor - h + 1)
	}
}

// ---- preview ----

func (m *model) updatePreview() tea.Cmd {
	r := m.currentRow()
	if r == nil {
		m.prevKey = ""
		m.prevVP.SetContent("")
		return nil
	}
	key := previewKey(r.e.it, m.prevW())
	if key == m.prevKey {
		return nil
	}
	m.prevKey = key
	m.prevVP.GotoTop()
	if c, ok := m.renders[key]; ok {
		m.prevVP.SetContent(c)
		return nil
	}
	m.prevVP.SetContent(stDim.Render("rendering…"))
	return renderPreviewCmd(r.e.it, m.prevW(), m.previewStyle)
}

// setPreviewStyle switches the glamour style once the terminal background is
// known. Cached renders carry the old palette, so they are dropped and the
// current preview is rendered again.
func (m *model) setPreviewStyle(style string) tea.Cmd {
	if style == m.previewStyle {
		return nil
	}
	m.previewStyle = style
	m.renders = map[string]string{}
	m.prevKey = ""
	return m.updatePreview()
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
	if c := m.updatePreview(); c != nil {
		cmds = append(cmds, c)
	}
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
		style := "dark"
		if !msg.IsDark() {
			style = "light"
		}
		return m, m.setPreviewStyle(style)

	case previewMsg:
		if msg.style != m.previewStyle { // rendered before the style flipped
			return m, nil
		}
		if m.renders == nil {
			m.renders = map[string]string{}
		}
		m.renders[msg.key] = msg.content
		if msg.key == m.prevKey {
			m.prevVP.SetContent(msg.content)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.MouseWheelMsg:
		// Over the list the wheel moves the selection, as in asgitlog; anywhere
		// else it scrolls the preview.
		if m.overList(msg.X, msg.Y) {
			switch msg.Button {
			case tea.MouseWheelUp:
				return m.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
			case tea.MouseWheelDown:
				return m.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
			}
			return m, nil
		}
		m.prevVP, _ = m.prevVP.Update(msg)
		return m, nil

	case tea.MouseClickMsg:
		return m.handleClick(msg)

	default:
		var cmd tea.Cmd
		m.ti, cmd = m.ti.Update(msg)
		return m, cmd
	}
}

func (m model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case foldsHelp(msg, m.help):
		return m, m.toggleHelp() // esc folds the help before it quits
	case isHelpKey(msg, m.ti.Value()):
		return m, m.toggleHelp()
	case msg.String() == "q" && m.ti.Value() == "":
		// q quits only while the filter is empty; otherwise it is text.
		return m, tea.Quit
	case key.Matches(msg, m.keys.Cancel):
		return m, tea.Quit
	case key.Matches(msg, m.keys.Select):
		if r := m.currentRow(); r != nil {
			m.openURL = r.e.it.URL
			return m, tea.Quit
		}
		return m, nil
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

	var curURL string
	if r := m.currentRow(); r != nil {
		curURL = r.e.it.URL
	}
	var cmd tea.Cmd
	m.ti, cmd = m.ti.Update(msg)
	m.applyFilter()
	if strings.TrimSpace(m.ti.Value()) == "" {
		// Clearing the query rebuilt the rows; stay on the same ticket instead
		// of whatever now sits at the old cursor index.
		m.keepCursorOn(curURL)
	}
	m.renderList()
	return m, tea.Batch(cmd, m.updatePreview())
}

// overList reports whether a screen cell is inside the list.
func (m *model) overList(x, y int) bool {
	return x >= 1 && x <= m.listW() && y >= listY(false) && y < listY(false)+m.bodyH()
}

// handleClick moves the selection to the ticket row under a left click on the
// list. It never opens the ticket: that stays on enter.
func (m model) handleClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if msg.Button != tea.MouseLeft || !m.overList(msg.X, msg.Y) {
		return m, nil
	}
	i := msg.Y - listY(false) + m.listVP.YOffset()
	if i < 0 || i >= len(m.rows) || m.rows[i].kind != "issue" || i == m.cursor {
		return m, nil
	}
	m.cursor = i
	m.renderList()
	return m, m.updatePreview()
}

// View declares the screen: alt screen and cell-motion mouse reports.
func (m model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// render stacks the sections in one frame (see frame.go); the tests assert on
// it. There is no context line: the stacks already head their groups.
func (m model) render() string {
	w := m.width
	out := frameHead(w, "", m.counter(), m.ti.View())
	pos := ""
	if m.currentRow() != nil {
		pos = scrollPos(&m.prevVP)
	}
	out = append(out, splitMain(m.listLines(), strings.Split(m.rightColumn(), "\n"),
		m.listW(), m.detailsW(), listPos(&m.listVP, func(i int) bool { return i < len(m.rows) && m.rows[i].kind != "header" }), pos)...)
	for _, l := range m.footLines() {
		out = append(out, framed(w, l))
	}
	out = append(out, hline(w, "╰", "╯", "", ""))
	return strings.Join(out, "\n")
}

// counter is the matches/total count, with the refresh mark.
func (m model) counter() string {
	n := 0
	for _, r := range m.rows {
		if r.kind == "issue" {
			n++
		}
	}
	s := stCount.Render(strconv.Itoa(n) + "/" + strconv.Itoa(len(m.entries)))
	if m.refreshing {
		s += stDim.Render(" refreshing…")
	}
	return s
}

// listLines is the list as exactly bodyH lines of listW cells.
func (m model) listLines() []string {
	lines := strings.Split(m.listVP.View(), "\n")
	for len(lines) < m.bodyH() {
		lines = append(lines, "")
	}
	lines = lines[:m.bodyH()]
	for i, l := range lines {
		lines[i] = fit(l, m.listW())
	}
	return lines
}

func (m model) rightColumn() string {
	w := m.prevW()
	r := m.currentRow()
	if r == nil {
		if len(m.entries) == 0 && m.refreshing {
			return "\n" + stDim.Render("Loading issues from "+strconv.Itoa(len(m.sources))+" source(s)…")
		}
		if len(m.entries) == 0 {
			return "\n" + stDim.Render("No open issues")
		}
		return ""
	}
	return previewHeader(r.e.it, w) + "\n\n" + m.prevVP.View()
}

// footer is the key help, or the network error while there is one. The
// refresh mark lives next to the counter.
// footMsg is what takes the help's place while there is something to say.
func (m model) footMsg() string {
	if m.netErr != "" {
		return stError.Render(truncate(m.netErr, max(0, m.width-4)))
	}
	return ""
}

func (m model) footLines() []string {
	if msg := m.footMsg(); msg != "" {
		return []string{msg}
	}
	return helpLines(m.help, m.keys, m.width-4, m.footH())
}

// newFilterInput builds the focused filter textinput. The prompt string
// already carries its colors, so the prompt style is left empty.
func newFilterInput(prompt string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = prompt
	st := ti.Styles()
	st.Focused.Prompt = lipgloss.NewStyle()
	st.Blurred.Prompt = lipgloss.NewStyle()
	ti.SetStyles(st)
	ti.Focus()
	return ti
}

// promptText builds the textinput prompt, with an orange "(dev)" marker on
// non-release builds.
func promptText() string {
	if strings.HasPrefix(version, "v") {
		return stPrompt.Render("asgotoissues ❯ ")
	}
	return stPrompt.Render("asgotoissues (") + stDev.Render("dev") + stPrompt.Render(") ❯ ")
}

// openInBrowser hands the URL to the OS after the TUI has exited.
// ASGOTOISSUES_OPEN_CMD overrides the opener (tests log the argv instead).
func openInBrowser(url string) {
	if url == "" {
		return
	}
	var cmd *exec.Cmd
	switch {
	case os.Getenv("ASGOTOISSUES_OPEN_CMD") != "":
		cmd = exec.Command(os.Getenv("ASGOTOISSUES_OPEN_CMD"), url)
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Run()
}
