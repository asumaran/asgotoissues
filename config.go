package main

// Stack configuration. asgotoissues reuses the asdev plugin's config file
// (~/.claude/asdev.local.md, read by asdevconfig.go, the family's shared
// reader). `issues:` says which trackers a stack lists; without it a stack
// with a jira.base_url lists Jira, and a stack that lists nothing is skipped.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

func configPath() string { return asdevConfigPath("ASGOTOISSUES_CONFIG") }

// parseStacks decodes the stacks from the config document and keeps the ones
// that list an issue tracker, with their Trackers resolved.
func parseStacks(doc string) ([]stack, error) {
	all, err := parseAsdevStacks(doc)
	if err != nil {
		return nil, err
	}
	return issueStacks(all)
}

// issueStacks keeps the stacks that list an issue tracker.
func issueStacks(all []stack) ([]stack, error) {
	var out []stack
	for _, st := range all {
		trackers, err := stackTrackers(st, st.Issues)
		if err != nil {
			return nil, err
		}
		if len(trackers) == 0 {
			continue
		}
		st.Trackers = trackers
		out = append(out, st)
	}
	if len(out) == 0 {
		return nil, errors.New("config: no stack lists an issue tracker (add `issues:` or a jira.base_url)")
	}
	return out, nil
}

// stackTrackers resolves the trackers of one stack and checks that each has
// the block it needs.
func stackTrackers(st stack, listed []string) ([]string, error) {
	if listed == nil {
		if st.BaseURL != "" {
			return []string{kindJira}, nil
		}
		return nil, nil
	}
	var out []string
	for _, kind := range listed {
		kind = strings.ToLower(strings.TrimSpace(kind))
		switch {
		case kind == kindJira && st.BaseURL == "":
			return nil, fmt.Errorf("config: %s lists jira issues but has no jira.base_url", st.Name)
		case kind == kindGitHub && len(st.Orgs) == 0:
			return nil, fmt.Errorf("config: %s lists github issues but has no github.org", st.Name)
		case kind != kindJira && kind != kindGitHub:
			return nil, fmt.Errorf("config: %s: unknown issue tracker %q (jira, github)", st.Name, kind)
		}
		if !slices.Contains(out, kind) {
			out = append(out, kind)
		}
	}
	return out, nil
}

func loadStacks() ([]stack, error) {
	all, err := readAsdevStacks(configPath())
	if err != nil {
		return nil, err
	}
	return issueStacks(all)
}
