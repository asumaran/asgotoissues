package main

// Jira provider. One search per stack:
// Cloud uses POST /rest/api/2/search/jql (v2 so text fields come back as
// wiki markup instead of ADF), Server/DC uses POST /rest/api/2/search. The
// JQL is fixed: tickets assigned to the authenticated user that are not in
// the Done status category, newest activity first. Descriptions ride along in
// the search response (no second round trip), so a refresh is a single
// request per stack plus pagination.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	jiraJQL      = "assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC"
	jiraTimeout  = 10 * time.Second
	jiraPageSize = 100
	jiraMaxPages = 5 // hard cap: 500 open tickets per stack is plenty
)

var jiraFields = []string{"key", "summary", "status", "issuetype", "priority", "project", "parent", "created", "updated", "description"}

var httpClient = &http.Client{Timeout: jiraTimeout}

// searchResp covers both endpoints' response shapes.
type searchResp struct {
	Issues []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary     string `json:"summary"`
			Description string `json:"description"`
			Created     string `json:"created"`
			Updated     string `json:"updated"`
			Status      struct {
				Name     string `json:"name"`
				Category struct {
					Name string `json:"name"`
				} `json:"statusCategory"`
			} `json:"status"`
			IssueType struct {
				Name string `json:"name"`
			} `json:"issuetype"`
			Priority struct {
				Name string `json:"name"`
			} `json:"priority"`
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
			Parent struct {
				Key string `json:"key"`
			} `json:"parent"`
		} `json:"fields"`
	} `json:"issues"`
	// cloud pagination
	IsLast        bool   `json:"isLast"`
	NextPageToken string `json:"nextPageToken"`
	// server pagination
	StartAt    int `json:"startAt"`
	MaxResults int `json:"maxResults"`
	Total      int `json:"total"`
	// errors
	ErrorMessages []string `json:"errorMessages"`
	Message       string   `json:"message"`
}

func (r searchResp) errText() string {
	if len(r.ErrorMessages) > 0 {
		return strings.Join(r.ErrorMessages, "; ")
	}
	return r.Message
}

type jiraProvider struct{ s stack }

func (jiraProvider) kind() string { return kindJira }

// fetch returns every open ticket assigned to the user on one stack.
func (p jiraProvider) fetch(ctx context.Context) ([]issue, error) {
	s := p.s
	cred, err := resolveCredential(s)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, jiraTimeout*jiraMaxPages)
	defer cancel()

	var out []issue
	var pageToken string
	startAt := 0
	for page := 0; page < jiraMaxPages; page++ {
		var body map[string]any
		var endpoint string
		if s.Type == "server" {
			endpoint = s.BaseURL + "/rest/api/2/search"
			body = map[string]any{"jql": jiraJQL, "maxResults": jiraPageSize, "startAt": startAt, "fields": jiraFields}
		} else {
			endpoint = s.BaseURL + "/rest/api/2/search/jql"
			body = map[string]any{"jql": jiraJQL, "maxResults": jiraPageSize, "fields": jiraFields}
			if pageToken != "" {
				body["nextPageToken"] = pageToken
			}
		}
		resp, err := postJSON(ctx, endpoint, cred, body)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		for _, raw := range resp.Issues {
			created, _ := parseJiraTime(raw.Fields.Created)
			updated, _ := parseJiraTime(raw.Fields.Updated)
			out = append(out, issue{
				Key:         raw.Key,
				Stack:       s.Name,
				Source:      kindJira,
				State:       jiraState(raw.Fields.Status.Name, raw.Fields.Status.Category.Name),
				URL:         s.BaseURL + "/browse/" + raw.Key,
				Summary:     raw.Fields.Summary,
				Description: raw.Fields.Description,
				Status:      raw.Fields.Status.Name,
				StatusCat:   raw.Fields.Status.Category.Name,
				Type:        raw.Fields.IssueType.Name,
				Priority:    raw.Fields.Priority.Name,
				Project:     raw.Fields.Project.Key,
				ParentKey:   raw.Fields.Parent.Key,
				Created:     created,
				Updated:     updated,
			})
		}
		if s.Type == "server" {
			startAt += len(resp.Issues)
			if len(resp.Issues) == 0 || startAt >= resp.Total {
				break
			}
			continue
		}
		if resp.IsLast || resp.NextPageToken == "" || len(resp.Issues) == 0 {
			break
		}
		pageToken = resp.NextPageToken
	}
	return out, nil
}

func postJSON(ctx context.Context, endpoint string, cred credential, body map[string]any) (*searchResp, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if cred.user != "" {
		req.SetBasicAuth(cred.user, cred.secret)
	} else {
		req.Header.Set("Authorization", "Bearer "+cred.secret)
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var parsed searchResp
	jsonErr := json.Unmarshal(data, &parsed)
	if res.StatusCode/100 != 2 {
		msg := parsed.errText()
		if msg == "" {
			msg = firstLine(string(data))
		}
		if len(msg) > 120 {
			msg = msg[:120] + "…"
		}
		return nil, fmt.Errorf("HTTP %d %s", res.StatusCode, msg)
	}
	if jsonErr != nil {
		return nil, fmt.Errorf("bad response: %w", jsonErr)
	}
	return &parsed, nil
}

// parseJiraTime accepts Jira's "2026-08-17T11:40:18.035-0400" (no colon in
// the zone offset) as well as plain RFC 3339.
func parseJiraTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("bad time %q", s)
}

// jiraState folds a Jira status into the picker's three states: the status
// category says in progress or not, and the usual "blocked" naming wins.
func jiraState(status, category string) string {
	if strings.Contains(strings.ToLower(status), "block") {
		return stateBlocked
	}
	switch strings.ToLower(category) {
	case "in progress", "indeterminate":
		return stateDoing
	}
	return stateTodo
}
