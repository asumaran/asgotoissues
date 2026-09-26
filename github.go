package main

// GitHub provider. It goes through the gh CLI (`gh api graphql`), so there is
// no token to configure: whoever gh is logged in as is the user. A stack
// lists the open issues assigned to that user under its owners (orgs or
// users), the same rule the Jira search follows. An issue's parent (a
// sub-issue's) and the parent's parent ride along in the same query and,
// when they are not in the list, become ghosts the tree hangs the issue from.

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
        parent {
          number title url state createdAt updatedAt
          repository { name nameWithOwner }
          parent {
            number title url state createdAt updatedAt
            repository { name nameWithOwner }
          }
        }
      }
    }
  }
}`

type ghRepo struct {
	Name          string `json:"name"`
	NameWithOwner string `json:"nameWithOwner"`
}

// ghParentNode is an issue as its child sees it: enough for a ghost row.
type ghParentNode struct {
	Number     int           `json:"number"`
	Title      string        `json:"title"`
	URL        string        `json:"url"`
	State      string        `json:"state"` // OPEN, CLOSED
	CreatedAt  time.Time     `json:"createdAt"`
	UpdatedAt  time.Time     `json:"updatedAt"`
	Repository ghRepo        `json:"repository"`
	Parent     *ghParentNode `json:"parent"`
}

type ghIssueNode struct {
	Number     int       `json:"number"`
	Title      string    `json:"title"`
	URL        string    `json:"url"`
	Body       string    `json:"body"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	Repository ghRepo    `json:"repository"`
	Labels     struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
	} `json:"labels"`
	Milestone *struct {
		Title string `json:"title"`
	} `json:"milestone"`
	Parent *ghParentNode `json:"parent"`
}

// ghSearchResp is the shape of every search: the page and its nodes of the
// type the query selects.
type ghSearchResp[T any] struct {
	Data *struct {
		Search struct {
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
			Nodes []T `json:"nodes"`
		} `json:"search"`
	} `json:"data"`
}

type githubProvider struct{ s stack }

func (githubProvider) kind() string { return kindGitHub }

func (p githubProvider) fetch(ctx context.Context) (fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout*ghMaxPages)
	defer cancel()

	nodes, err := ghSearch[ghIssueNode](ctx, ghIssuesQuery, "is:issue is:open archived:false assignee:@me"+ghOwners(p.s.Orgs))
	if err != nil {
		return fetched{}, fmt.Errorf("%s: %w", p.s.Name, err)
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
	// The parents that are not in the list, as ghosts, and their parents.
	for _, n := range nodes {
		for parent := n.Parent; parent != nil; parent = parent.Parent {
			if parent.URL == "" || seen[parent.URL] {
				continue
			}
			seen[parent.URL] = true
			out = append(out, parent.issue(p.s.Name))
		}
	}
	qualifyClashingKeys(out)
	return fetched{issues: out}, nil
}

// ghOwners is the search's owner qualifiers.
func ghOwners(orgs []string) string {
	owners := ""
	for _, o := range orgs {
		owners += " user:" + o
	}
	return owners
}

// ghSearch pages through one search of the query's node type. A response
// with errors is still read: gh exits 1 on a GraphQL error, and one raised
// by a field of one node leaves the rest of the data in place.
func ghSearch[T any](ctx context.Context, query, q string) ([]T, error) {
	var nodes []T
	after := ""
	for page := 0; page < ghMaxPages; page++ {
		args := []string{"api", "graphql", "-f", "query=" + query, "-f", "q=" + q}
		if after != "" {
			args = append(args, "-f", "after="+after)
		}
		out, runErr := ghRun(ctx, args...)
		var resp ghSearchResp[T]
		if err := json.Unmarshal(out, &resp); err != nil || resp.Data == nil {
			if runErr != nil {
				return nil, runErr
			}
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
	it := issue{
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
	if n.Parent != nil && n.Parent.URL != "" {
		it.ParentKey = n.Parent.Repository.Name + "#" + strconv.Itoa(n.Parent.Number)
		it.ParentSummary = n.Parent.Title
		it.ParentURL = n.Parent.URL
	}
	return it
}

// issue is the parent as a ghost: what its child knows of it.
func (n ghParentNode) issue(stackName string) issue {
	it := issue{
		Key:        n.Repository.Name + "#" + strconv.Itoa(n.Number),
		Stack:      stackName,
		Source:     kindGitHub,
		URL:        n.URL,
		Summary:    n.Title,
		BodyFormat: bodyMarkdown,
		Status:     strings.ToLower(n.State),
		State:      stateTodo,
		Project:    n.Repository.NameWithOwner,
		Meta:       []string{n.Repository.NameWithOwner},
		Ghost:      true,
		Created:    n.CreatedAt,
		Updated:    n.UpdatedAt,
	}
	if it.Status == "" {
		it.Status = "open"
	}
	if n.Parent != nil && n.Parent.URL != "" {
		it.ParentKey = n.Parent.Repository.Name + "#" + strconv.Itoa(n.Parent.Number)
		it.ParentSummary = n.Parent.Title
		it.ParentURL = n.Parent.URL
	}
	return it
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

// clashingRepos names the repositories (by bare name) that show up under
// more than one owner among projects (owner/repo).
func clashingRepos(projects []string) map[string]bool {
	owners := map[string]map[string]bool{}
	for _, p := range projects {
		name := strings.ToLower(p[strings.LastIndexByte(p, '/')+1:])
		if owners[name] == nil {
			owners[name] = map[string]bool{}
		}
		owners[name][strings.ToLower(p)] = true
	}
	clash := map[string]bool{}
	for name, o := range owners {
		if len(o) > 1 {
			clash[name] = true
		}
	}
	return clash
}

// qualifyClashingKeys turns "repo#12" into "owner/repo#12" for the repos
// whose name shows up under more than one owner, and keeps every parent key
// the name its parent ends up with.
func qualifyClashingKeys(issues []issue) {
	var projects []string
	for _, it := range issues {
		projects = append(projects, it.Project)
	}
	clash := clashingRepos(projects)
	keyOf := map[string]string{}
	for i, it := range issues {
		name := strings.ToLower(it.Project[strings.LastIndexByte(it.Project, '/')+1:])
		if clash[name] {
			issues[i].Key = it.Project + "#" + it.number()
		}
		keyOf[it.URL] = issues[i].Key
	}
	for i, it := range issues {
		if k, ok := keyOf[it.ParentURL]; ok {
			issues[i].ParentKey = k
		}
	}
}
