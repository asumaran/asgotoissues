package main

// GitHub provider. It goes through the gh CLI (`gh api graphql`), so there is
// no token to configure: whoever gh is logged in as is the user. A stack
// lists the open issues assigned to that user under its owners (orgs or
// users), the same rule the Jira search follows.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ghTimeout  = 15 * time.Second
	ghMaxPages = 5 // pages of 100, the same cap as Jira
)

const ghIssuesQuery = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 100, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on Issue {
        number title url body createdAt updatedAt
        repository { name nameWithOwner }
        labels(first: 10) { nodes { name } }
        milestone { title }
      }
    }
  }
}`

type ghIssueNode struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Repository struct {
		Name          string `json:"name"`
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
}

type ghIssuesResp struct {
	Data struct {
		Search struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []ghIssueNode `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

type githubProvider struct{ s stack }

func (githubProvider) kind() string { return kindGitHub }

func (p githubProvider) fetch(ctx context.Context) ([]issue, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout*ghMaxPages)
	defer cancel()

	owners := ""
	for _, o := range p.s.Orgs {
		owners += " user:" + o
	}
	nodes, err := ghSearchIssues(ctx, "is:issue is:open archived:false assignee:@me"+owners)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p.s.Name, err)
	}

	seen := map[string]bool{}
	var out []issue
	for _, n := range nodes {
		if n.URL == "" || seen[n.URL] { // a non-issue node decodes as the zero struct
			continue
		}
		seen[n.URL] = true
		out = append(out, n.issue(p.s.Name))
	}
	qualifyClashingKeys(out)
	return out, nil
}

// ghSearchIssues pages through one issue search.
func ghSearchIssues(ctx context.Context, q string) ([]ghIssueNode, error) {
	var nodes []ghIssueNode
	after := ""
	for page := 0; page < ghMaxPages; page++ {
		args := []string{"api", "graphql", "-f", "query=" + ghIssuesQuery, "-f", "q=" + q}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		out, err := ghRun(ctx, args...)
		if err != nil {
			return nil, err
		}
		var resp ghIssuesResp
		if err := json.Unmarshal(out, &resp); err != nil {
			return nil, fmt.Errorf("gh: bad search response: %w", err)
		}
		nodes = append(nodes, resp.Data.Search.Nodes...)
		info := resp.Data.Search.PageInfo
		if !info.HasNextPage || info.EndCursor == "" || len(resp.Data.Search.Nodes) == 0 {
			break
		}
		after = info.EndCursor
	}
	return nodes, nil
}

func (n ghIssueNode) issue(stackName string) issue {
	var labels []string
	for _, l := range n.Labels.Nodes {
		labels = append(labels, l.Name)
	}
	meta := []string{n.Repository.NameWithOwner}
	if len(labels) > 0 {
		meta = append(meta, strings.Join(labels, ", "))
	}
	if n.Milestone != nil && n.Milestone.Title != "" {
		meta = append(meta, "⚑ "+n.Milestone.Title)
	}
	return issue{
		Key:         n.Repository.Name + "#" + strconv.Itoa(n.Number),
		Stack:       stackName,
		Source:      kindGitHub,
		URL:         n.URL,
		Summary:     n.Title,
		Description: n.Body,
		BodyFormat:  bodyMarkdown,
		Status:      "open",
		State:       githubState(labels),
		Project:     n.Repository.NameWithOwner,
		Meta:        meta,
		Created:     n.CreatedAt,
		Updated:     n.UpdatedAt,
	}
}

var (
	reLabelBlocked = regexp.MustCompile(`(?i)block`)
	reLabelDoing   = regexp.MustCompile(`(?i)in.?progress|\bdoing\b|\bwip\b`)
)

// githubState reads the state off the labels, the only place an open GitHub
// issue can say it.
func githubState(labels []string) string {
	state := stateTodo
	for _, l := range labels {
		if reLabelBlocked.MatchString(l) {
			return stateBlocked
		}
		if reLabelDoing.MatchString(l) {
			state = stateDoing
		}
	}
	return state
}

// qualifyClashingKeys turns "repo#12" into "owner/repo#12" for the repos
// whose name shows up under more than one owner.
func qualifyClashingKeys(issues []issue) {
	owners := map[string]map[string]bool{}
	for _, it := range issues {
		name := it.Project[strings.LastIndexByte(it.Project, '/')+1:]
		if owners[name] == nil {
			owners[name] = map[string]bool{}
		}
		owners[name][it.Project] = true
	}
	for i, it := range issues {
		name := it.Project[strings.LastIndexByte(it.Project, '/')+1:]
		if len(owners[name]) > 1 {
			issues[i].Key = it.Project + "#" + it.number()
		}
	}
}
