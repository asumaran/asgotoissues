package main

// The screen is one rounded frame of sections split by shared edges, the
// layout asgitlog introduced: the filter input (the edge over it carries what
// the list is scoped to and its state), the main section (list and preview,
// split by a divider; its bottom edge carries the matches/total counter under
// the list, at its right end, and, while the preview overflows, its position on the right) and
// the help. Neighbours share an edge, so no line is spent on a border of
// their own.
//
// A context line on top is optional and only for what the rest of the screen
// cannot say (asgitlog: which repository and branch; asgotonotes: which group
// is being browsed). A title is not context. Without it the status sits on
// the frame's top border:
//
//	╭────────────────── [vs main] ─╮  0          ╭──────────────────────────────╮
//	│ filter ❯                     │  1          │ context                      │
//	├───────────┬──────────────────┤  mainY      ├────────────────── [vs main] ─┤
//	│ list      │ preview          │  listY …    │ filter ❯                     │
//	├──── 3/12 ─┴─────────── 8/40 ─┤             ├───────────┬──────────────────┤
//	│ help                         │             │ list      │ preview          │
//	╰──────────────────────────────╯             …

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
)

// mainY is the edge over the main section, listY the first list line and
// frameRows every line that is not the main section's content or the help:
// the borders, the input and the edges, plus the context line and its edge
// when there is one.
func mainY(context bool) int {
	if context {
		return 4
	}
	return 2
}

func listY(context bool) int { return mainY(context) + 1 }

func frameRows(context bool) int {
	if context {
		return 7
	}
	return 5
}

// frameHead is everything above the main section: the top border, the
// optional context line, the status on the edge over the input, the input.
func frameHead(w int, context, status, input string) []string {
	if context == "" {
		return []string{hline(w, "╭", "╮", "", status), framed(w, input)}
	}
	return []string{
		hline(w, "╭", "╮", "", ""),
		framed(w, context),
		hline(w, "├", "┤", "", status),
		framed(w, input),
	}
}

// hline draws a horizontal border w cells wide between the corners l and r
// (either may be empty), with optional (already styled) texts set into it
// near each end.
func hline(w int, l, r, left, right string) string {
	inner := max(0, w-ansi.StringWidth(l)-ansi.StringWidth(r))
	if left != "" {
		left = " " + left + " "
	}
	if right != "" {
		right = " " + right + " "
	}
	if 2+ansi.StringWidth(left)+ansi.StringWidth(right) > inner {
		right = ""
	}
	if 1+ansi.StringWidth(left) > inner {
		left = ansi.Truncate(left, max(0, inner-1), "")
	}
	fill := inner - ansi.StringWidth(left) - ansi.StringWidth(right)
	lead := min(1, fill)
	tail := 0
	if right != "" {
		tail = min(1, fill-lead)
	}
	return stDim.Render(l+strings.Repeat("─", lead)) + left +
		stDim.Render(strings.Repeat("─", fill-lead-tail)) + right + stDim.Render(strings.Repeat("─", tail)+r)
}

// fit truncates or pads s to exactly w cells.
func fit(s string, w int) string {
	s = ansi.Truncate(s, w, "")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// framed sets a line, with a cell of padding, between the frame's sides.
func framed(w int, l string) string {
	side := stDim.Render("│")
	return side + fit(" "+l, w-2) + side
}

// scrollPos is the preview's position for the main section's bottom edge,
// empty while everything fits.
func scrollPos(vp *viewport.Model) string {
	total := vp.TotalLineCount()
	if total <= vp.Height() {
		return ""
	}
	return stDim.Render(strconv.Itoa(min(total, vp.YOffset()+vp.Height())) + "/" + strconv.Itoa(total))
}

// splitMain is the main section with a list of listW cells and a preview of
// prevW cells (padding included) side by side: the top edge, one line per
// list row, and the bottom edge carrying the counter under the list and pos
// under the preview. list must hold lines of exactly listW cells; preview lines are
// padded here.
func splitMain(list, preview []string, listW, prevW int, counter, pos string) []string {
	side := stDim.Render("│")
	out := make([]string, 0, len(list)+2)
	out = append(out, stDim.Render("├"+strings.Repeat("─", listW))+hline(prevW+2, "┬", "┤", "", ""))
	for i := range list {
		d := ""
		if i < len(preview) {
			d = preview[i]
		}
		out = append(out, side+list[i]+side+fit(" "+d, prevW)+side)
	}
	return append(out, hline(listW+1, "├", "", "", counter)+hline(prevW+2, "┴", "┤", "", pos))
}
