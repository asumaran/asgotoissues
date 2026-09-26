package main

// The pulls source: the pull requests of a stack, from GitHub through gh,
// whatever tracker its tickets live in (a Jira stack's PRs are in its GitHub
// org). Three searches under the stack's owners: the open PRs I am involved
// in, my PRs merged in the last pullsWindow (a ticket whose PR just merged
// still shows it), and, URL only, the open PRs whose review is asked of me
// (involves:@me does not cover a review request, and a request to a team
// cannot be told from the PR's own reviewer list). Linking to tickets is
// local (tree.go), by the references each PR carries (pullRefs).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	pullsPages  = 3 // pages of 100 per search
	pullsWindow = 30 * 24 * time.Hour
)

// pullsNow is the clock the merged window is measured from; the tests set it.
var pullsNow = time.Now

const ghPullsQuery = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 100, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes {
      ... on PullRequest {
        number title url body state isDraft createdAt updatedAt
        headRefName baseRefName headRefOid baseRefOid
        author { login }
        repository { name nameWithOwner }
        reviewDecision mergeable mergeStateStatus
        reviewRequests { totalCount }
        latestReviews(first: 20) { nodes { state } }
        statusCheckRollup { state }
        closingIssuesReferences(first: 10) { nodes { number repository { nameWithOwner } } }
        reviewThreads(first: 50) { nodes { isResolved comments(last: 1) { nodes { author { login } } } } }
      }
    }
  }
}`

const ghPullURLsQuery = `query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 100, after: $after) {
    pageInfo { hasNextPage endCursor }
    nodes { ... on PullRequest { url } }
  }
}`

type ghPullNode struct {
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Body        string    `json:"body"`
	State       string    `json:"state"`
	IsDraft     bool      `json:"isDraft"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
	HeadRefName string    `json:"headRefName"`
	BaseRefName string    `json:"baseRefName"`
	HeadRefOid  string    `json:"headRefOid"`
	BaseRefOid  string    `json:"baseRefOid"`
	Author      struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository       ghRepo `json:"repository"`
	ReviewDecision   string `json:"reviewDecision"`
	Mergeable        string `json:"mergeable"`
	MergeStateStatus string `json:"mergeStateStatus"`
	ReviewRequests   struct {
		TotalCount int `json:"totalCount"`
	} `json:"reviewRequests"`
	LatestReviews struct {
		Nodes []struct {
			State string `json:"state"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	StatusCheckRollup *struct {
		State string `json:"state"`
	} `json:"statusCheckRollup"`
	ClosingIssuesReferences struct {
		Nodes []struct {
			Number     int    `json:"number"`
			Repository ghRepo `json:"repository"`
		} `json:"nodes"`
	} `json:"closingIssuesReferences"`
	ReviewThreads struct {
		Nodes []struct {
			IsResolved bool `json:"isResolved"`
			Comments   struct {
				Nodes []struct {
					Author struct {
						Login string `json:"login"`
					} `json:"author"`
				} `json:"nodes"`
			} `json:"comments"`
		} `json:"nodes"`
	} `json:"reviewThreads"`
}

type ghURLNode struct {
	URL string `json:"url"`
}

type pullsProvider struct{ s stack }

func (pullsProvider) kind() string { return kindPulls }

func (p pullsProvider) fetch(ctx context.Context) (fetched, error) {
	ctx, cancel := context.WithTimeout(ctx, ghTimeout*pullsPages*3)
	defer cancel()

	got, err := p.pulls(ctx)
	if err != nil {
		return fetched{}, fmt.Errorf("%s PRs: %w", p.s.Name, err)
	}
	return fetched{pulls: got}, nil
}

func (p pullsProvider) pulls(ctx context.Context) ([]pull, error) {
	me, err := ghViewer(ctx)
	if err != nil {
		return nil, err
	}
	owners := ghOwners(p.s.Orgs)
	open, err := ghSearch[ghPullNode](ctx, ghPullsQuery, "is:pr is:open archived:false involves:@me"+owners)
	if err != nil {
		return nil, err
	}
	since := pullsNow().Add(-pullsWindow).UTC().Format("2006-01-02")
	merged, err := ghSearch[ghPullNode](ctx, ghPullsQuery, "is:pr is:merged archived:false author:@me updated:>="+since+owners)
	if err != nil {
		return nil, err
	}
	asked, err := ghSearch[ghURLNode](ctx, ghPullURLsQuery, "is:pr is:open archived:false review-requested:@me"+owners)
	if err != nil {
		return nil, err
	}
	requested := map[string]bool{}
	for _, n := range asked {
		requested[n.URL] = true
	}

	seen := map[string]bool{}
	var out []pull
	for _, n := range append(open, merged...) {
		if n.URL == "" || seen[n.URL] { // a non-PR node decodes as the zero struct
			continue
		}
		seen[n.URL] = true
		pr := n.pull(p.s.Name, me)
		pr.ReviewRequested = requested[n.URL]
		out = append(out, pr)
	}
	qualifyClashingPullKeys(out)
	ghBehind(ctx, out) // its failure leaves the counts at zero, never fails the source
	return out, nil
}

// behindChunk is how many PRs one compare query asks about.
const behindChunk = 40

// ghBehind fills in how many commits of its base each open PR lacks. The
// count comes from the base branch compared with the head, which takes the
// head as an argument, so it cannot ride in the search: one query with an
// alias per PR, in chunks. A PR whose head lives in a fork, or a query that
// fails, keeps zero.
func ghBehind(ctx context.Context, pulls []pull) {
	var open []int
	for i, p := range pulls {
		if p.State == prOpen && p.Head != "" && p.Base != "" && strings.Contains(p.Repo, "/") {
			open = append(open, i)
		}
	}
	for start := 0; start < len(open); start += behindChunk {
		chunk := open[start:min(start+behindChunk, len(open))]
		var q strings.Builder
		q.WriteString("query {")
		for n, i := range chunk {
			p := pulls[i]
			owner, name, _ := strings.Cut(p.Repo, "/")
			fmt.Fprintf(&q, " p%d: repository(owner: %q, name: %q) { pullRequest(number: %d) { baseRef { compare(headRef: %q) { behindBy } } } }",
				n, owner, name, p.Number, p.Head)
		}
		q.WriteString(" }")
		out, err := ghRun(ctx, "api", "graphql", "-f", "query="+q.String())
		if len(out) == 0 && err != nil {
			return
		}
		var resp struct {
			Data map[string]*struct {
				PullRequest *struct {
					BaseRef *struct {
						Compare *struct {
							BehindBy int `json:"behindBy"`
						} `json:"compare"`
					} `json:"baseRef"`
				} `json:"pullRequest"`
			} `json:"data"`
		}
		if json.Unmarshal(out, &resp) != nil {
			return
		}
		for n, i := range chunk {
			r := resp.Data[fmt.Sprintf("p%d", n)]
			if r != nil && r.PullRequest != nil && r.PullRequest.BaseRef != nil && r.PullRequest.BaseRef.Compare != nil {
				pulls[i].Behind = r.PullRequest.BaseRef.Compare.BehindBy
			}
		}
	}
}

// ghViewer is the login gh is signed in as: what makes a PR mine.
func ghViewer(ctx context.Context) (string, error) {
	out, err := ghRun(ctx, "api", "graphql", "-f", "query={ viewer { login } }")
	if err != nil {
		return "", err
	}
	var resp struct {
		Data struct {
			Viewer struct {
				Login string `json:"login"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", fmt.Errorf("gh: bad viewer response: %w", err)
	}
	return resp.Data.Viewer.Login, nil
}

func (n ghPullNode) pull(stackName, me string) pull {
	pr := pull{
		URL:            n.URL,
		Number:         n.Number,
		Repo:           n.Repository.NameWithOwner,
		Stack:          stackName,
		Key:            n.Repository.Name + "#" + fmt.Sprint(n.Number),
		Title:          n.Title,
		Body:           n.Body,
		State:          n.State,
		Draft:          n.IsDraft,
		Head:           n.HeadRefName,
		Base:           n.BaseRefName,
		HeadOID:        n.HeadRefOid,
		BaseOID:        n.BaseRefOid,
		Author:         n.Author.Login,
		Mine:           me != "" && strings.EqualFold(n.Author.Login, me),
		ReviewDecision: n.ReviewDecision,
		Mergeable:      n.Mergeable,
		MergeState:     n.MergeStateStatus,
		ReviewRequests: n.ReviewRequests.TotalCount,
		Created:        n.CreatedAt,
		Updated:        n.UpdatedAt,
	}
	for _, r := range n.LatestReviews.Nodes {
		switch r.State {
		case "APPROVED":
			pr.Approvals++
		case "CHANGES_REQUESTED":
			pr.ChangesRequested++
		}
	}
	if n.StatusCheckRollup != nil {
		pr.Checks = n.StatusCheckRollup.State
	}
	// A thread waits for me when nobody resolved it and the last word is not
	// mine.
	for _, t := range n.ReviewThreads.Nodes {
		c := t.Comments.Nodes
		if !t.IsResolved && len(c) > 0 && !strings.EqualFold(c[len(c)-1].Author.Login, me) {
			pr.ToAnswer = true
		}
	}
	var closing []string
	for _, c := range n.ClosingIssuesReferences.Nodes {
		if c.Repository.NameWithOwner != "" {
			closing = append(closing, c.Repository.NameWithOwner+"#"+fmt.Sprint(c.Number))
		}
	}
	pr.Refs, pr.WeakRefs = pullRefs(pr.Repo, pr.Head, pr.Title, pr.Body, closing)
	return pr
}

// qualifyClashingPullKeys is qualifyClashingKeys for PRs: owner/repo#N when
// two owners share a repo name.
func qualifyClashingPullKeys(pulls []pull) {
	var repos []string
	for _, p := range pulls {
		repos = append(repos, p.Repo)
	}
	clash := clashingRepos(repos)
	for i, p := range pulls {
		name := strings.ToLower(p.Repo[strings.LastIndexByte(p.Repo, '/')+1:])
		if clash[name] {
			pulls[i].Key = p.Repo + "#" + p.number()
		}
	}
}

// carryMergeable keeps, on a fresh PR whose merge state GitHub has not
// computed yet (UNKNOWN, it does so lazily), the values the cached copy had
// while neither branch moved.
func carryMergeable(fresh []pull, cached []pull) {
	byURL := map[string]pull{}
	for _, c := range cached {
		byURL[c.URL] = c
	}
	for i, p := range fresh {
		c, ok := byURL[p.URL]
		if !ok || c.HeadOID != p.HeadOID || c.BaseOID != p.BaseOID || p.HeadOID == "" {
			continue
		}
		if p.Mergeable == "UNKNOWN" || p.Mergeable == "" {
			fresh[i].Mergeable = c.Mergeable
		}
		if p.MergeState == "UNKNOWN" || p.MergeState == "" {
			fresh[i].MergeState = c.MergeState
		}
	}
}
