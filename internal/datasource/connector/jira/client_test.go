package jira

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body interface{}) *http.Response {
	b, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func TestClientPing(t *testing.T) {
	called := false
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		called = true
		if r.URL.Path != "/rest/api/2/myself" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "testuser" || pass != "testpass" {
			t.Errorf("basic auth mismatch: user=%s pass=%s", user, pass)
		}
		return jsonResponse(http.StatusOK, map[string]string{"name": "testuser"}), nil
	})

	c := &client{
		cfg: config{
			edition:  editionServer,
			baseURL:  "https://jira.example.com",
			username: "testuser",
			secret:   "testpass",
		},
		http: &http.Client{Transport: transport},
	}

	if err := c.ping(context.Background()); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if !called {
		t.Fatalf("expected ping request to be called")
	}
}

func TestClientProjects(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rest/api/2/project" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		projects := []project{
			{ID: "10001", Key: "OPS", Name: "Operations", Self: "https://jira.example.com/rest/api/2/project/10001"},
			{ID: "10002", Key: "DEV", Name: "Development", Self: "https://jira.example.com/rest/api/2/project/10002"},
		}
		return jsonResponse(http.StatusOK, projects), nil
	})

	c := &client{
		cfg:  config{baseURL: "https://jira.example.com", username: "u", secret: "p"},
		http: &http.Client{Transport: transport},
	}

	projs, err := c.projects(context.Background())
	if err != nil {
		t.Fatalf("projects failed: %v", err)
	}
	if len(projs) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projs))
	}
	if projs[0].Key != "OPS" || projs[1].Key != "DEV" {
		t.Errorf("unexpected project keys: %v", projs)
	}
}

func TestClientSearchIssues(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rest/api/3/search/jql" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		var request struct { JQL string `json:"jql"` }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.JQL != "project = 'OPS' ORDER BY updated ASC" {
			t.Errorf("unexpected jql: %s", request.JQL)
		}
		resp := searchResponse{
			StartAt:    0,
			MaxResults: 50,
			Total:      1,
			Issues: []issue{
				{
					ID:  "1",
					Key: "OPS-1",
					Fields: issueFields{
						Summary: "Incident 1",
						Updated: "2024-01-01T12:00:00.000Z",
					},
				},
			},
		}
		return jsonResponse(http.StatusOK, resp), nil
	})

	c := &client{
		cfg:  config{baseURL: "https://jira.example.com", username: "u", secret: "p"},
		http: &http.Client{Transport: transport},
	}

	resp, err := c.searchIssues(context.Background(), "project = 'OPS' ORDER BY updated ASC", "", 50)
	if err != nil {
		t.Fatalf("searchIssues failed: %v", err)
	}
	if resp.Total != 1 || len(resp.Issues) != 1 || resp.Issues[0].Key != "OPS-1" {
		t.Fatalf("unexpected search response: %+v", resp)
	}
}

func TestClientEnrichCommentsIfNeeded(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rest/api/3/issue/OPS-1/comment" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		return jsonResponse(http.StatusOK, map[string]interface{}{
			"comments": []map[string]interface{}{
				{
					"id":           "c2",
					"renderedBody": "<p>Extra comment</p>",
					"author": map[string]interface{}{
						"displayName": "Support",
					},
				},
			},
		}), nil
	})

	c := &client{
		cfg:  config{baseURL: "https://jira.example.com", username: "u", secret: "p"},
		http: &http.Client{Transport: transport},
	}
	iss := &issue{
		Key: "OPS-1",
		Fields: issueFields{
			Comment: &commentList{
				Total: 2,
				Comments: []comment{
					{ID: "c1"},
				},
			},
		},
	}

	err := c.enrichCommentsIfNeeded(context.Background(), iss)
	if err != nil {
		t.Fatalf("enrichCommentsIfNeeded failed: %v", err)
	}
	if len(iss.Fields.Comment.Comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(iss.Fields.Comment.Comments))
	}
	if iss.RenderedFields.Comment.Comments[0].Body != "<p>Extra comment</p>" {
		t.Errorf("expected extra comment body")
	}
}

func TestResolveEndpointRejectsForeignOrigin(t *testing.T) {
	c := &client{cfg: config{baseURL: "https://jira.example.com"}}
	if _, err := c.resolveEndpoint("https://attacker.example/rest/api/2/myself"); err == nil {
		t.Fatal("resolveEndpoint accepted a foreign origin")
	}
}

func TestCapRetryDelay(t *testing.T) {
	if got := capRetryDelay(0); got != 100*1000*1000 { // 100ms
		t.Errorf("capRetryDelay(0) = %v", got)
	}
	if got := capRetryDelay(10 * 60 * 1000 * 1000 * 1000); got != maxRetryDelay {
		t.Errorf("capRetryDelay(10m) = %v", got)
	}
}

func TestGetIncludesClientErrorBody(t *testing.T) {
	transport := roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("invalid JQL expression")),
		}, nil
	})
	c := &client{
		cfg:  config{baseURL: "https://jira.example.com", username: "reader", secret: "secret"},
		http: &http.Client{Transport: transport},
	}

	err := c.get(context.Background(), "/rest/api/2/search", nil)
	if err == nil || !strings.Contains(err.Error(), "invalid JQL expression") {
		t.Fatalf("get() error = %v", err)
	}
}

func TestClientDownloadAttachment(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/rest/api/3/attachment/content/10001" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("log content line 1\nline 2")),
		}, nil
	})
	c := &client{
		cfg:  config{baseURL: "https://jira.example.com", username: "u", secret: "p"},
		http: &http.Client{Transport: transport},
	}
	data, err := c.downloadAttachment(context.Background(), "/rest/api/3/attachment/content/10001")
	if err != nil {
		t.Fatalf("downloadAttachment failed: %v", err)
	}
	if string(data) != "log content line 1\nline 2" {
		t.Errorf("unexpected data: %s", string(data))
	}
}
