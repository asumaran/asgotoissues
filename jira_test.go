package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func jiraIssueJSON(key, summary, parentKey, parentSummary string) string {
	parent := ""
	if parentKey != "" {
		parent = fmt.Sprintf(`,"parent":{"key":%q,"fields":{"summary":%q}}`, parentKey, parentSummary)
	}
	return fmt.Sprintf(`{"key":%q,"fields":{"summary":%q,"description":"h1. %s","created":"2026-09-01T10:00:00.000+0000","updated":"2026-09-02T10:00:00.000+0000",`+
		`"status":{"name":"Open","statusCategory":{"name":"To Do"}},"issuetype":{"name":"Task"},"priority":{"name":"Medium"},"project":{"key":"PLAT"}%s}}`,
		key, summary, key, parent)
}

// TestJiraFetchGhosts: the parents the tickets name that are not in the
// list are fetched with `key in (...)`, level by level up to three, and
// come back as ghosts with their own parent; the parent's summary and URL
// ride on every ticket.
func TestJiraFetchGhosts(t *testing.T) {
	var jqls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			JQL string `json:"jql"`
		}
		json.Unmarshal(body, &req)
		jqls = append(jqls, req.JQL)
		var issues []string
		switch {
		case req.JQL == jiraJQL:
			issues = []string{jiraIssueJSON("PLAT-11", "sub-task", "PLAT-10", "story"), jiraIssueJSON("PLAT-20", "mine too", "PLAT-2", "other epic"), jiraIssueJSON("PLAT-2", "other epic", "", "")}
		case req.JQL == "key in (PLAT-10)":
			issues = []string{jiraIssueJSON("PLAT-10", "story", "PLAT-1", "epic")}
		case req.JQL == "key in (PLAT-1)":
			issues = []string{jiraIssueJSON("PLAT-1", "epic", "PLAT-0", "initiative")}
		case req.JQL == "key in (PLAT-0)":
			issues = []string{jiraIssueJSON("PLAT-0", "initiative", "PLAT--1", "beyond")}
		default:
			http.Error(w, `{"errorMessages":["unexpected jql"]}`, 400)
			return
		}
		fmt.Fprintf(w, `{"issues":[%s],"isLast":true}`, strings.Join(issues, ","))
	}))
	defer srv.Close()
	t.Setenv("NETRC", filepath.Join(t.TempDir(), "netrc"))
	t.Setenv("JIRA_TEST_TOKEN", "tok")
	os.WriteFile(os.Getenv("NETRC"), nil, 0o600)

	p := jiraProvider{stack{Name: "work", BaseURL: srv.URL, Type: "cloud", Email: "me@example.com", TokenEnv: "JIRA_TEST_TOKEN"}}
	res, err := p.fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := jiraJQL + "|key in (PLAT-10)|key in (PLAT-1)|key in (PLAT-0)"; strings.Join(jqls, "|") != want {
		t.Errorf("queries = %q, want %q (three levels, never a key the list holds)", jqls, want)
	}
	var lines []string
	for _, it := range res.issues {
		lines = append(lines, fmt.Sprintf("%s ghost=%v parent=%s %q %s", it.Key, it.Ghost, it.ParentKey, it.ParentSummary, strings.TrimPrefix(it.ParentURL, srv.URL)))
	}
	want := `PLAT-11 ghost=false parent=PLAT-10 "story" /browse/PLAT-10|PLAT-20 ghost=false parent=PLAT-2 "other epic" /browse/PLAT-2|PLAT-2 ghost=false parent= "" |` +
		`PLAT-10 ghost=true parent=PLAT-1 "epic" /browse/PLAT-1|PLAT-1 ghost=true parent=PLAT-0 "initiative" /browse/PLAT-0|PLAT-0 ghost=true parent=PLAT--1 "beyond" /browse/PLAT--1`
	if got := strings.Join(lines, "|"); got != want {
		t.Errorf("issues =\n%s\nwant\n%s", strings.ReplaceAll(got, "|", "\n"), strings.ReplaceAll(want, "|", "\n"))
	}
	if g := res.issues[3]; g.URL != srv.URL+"/browse/PLAT-10" || g.Stack != "work" || g.Status != "Open" || !strings.HasPrefix(g.markdown(), "# PLAT-10") {
		t.Errorf("a ghost is a whole issue: %+v", g)
	}
}
