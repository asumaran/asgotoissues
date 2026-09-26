package main

// Test helpers shared by the tree and list tests.

// trackerTitle is the title function of a list without short titles.
func trackerTitle(e *entry) string { return e.it.Summary }

// opts is the tree's options as the older tests spell them: hide says the
// finished work is left out (show pending), else everything is (show all).
func opts(order orderMode, prs prsMode, hide bool, collapsed map[string]bool) treeOpts {
	show := showAll
	if hide {
		show = showPending
	}
	return treeOpts{order: order, prs: prs, show: show, group: groupTree, collapsed: collapsed}
}
