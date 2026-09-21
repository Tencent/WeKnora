package jira

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type testStreamHandler struct {
	emitted     []types.FetchedItem
	checkpoints []*types.SyncCursor
}

func (h *testStreamHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.emitted = append(h.emitted, item)
	return nil
}

func (h *testStreamHandler) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	h.checkpoints = append(h.checkpoints, cursor)
	return nil
}

func TestConnectorType(t *testing.T) {
	conn := NewConnector()
	if conn.Type() != types.ConnectorTypeJira {
		t.Errorf("Type() = %q, want %q", conn.Type(), types.ConnectorTypeJira)
	}
}

func TestConnectorListResources(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/api/2/project" {
			return jsonResponse(http.StatusOK, []project{
				{ID: "100", Key: "DEMO", Name: "Demo Project"},
			}), nil
		}
		return jsonResponse(http.StatusNotFound, nil), nil
	})

	conn := &Connector{
		newClient: func(cfg config) (*client, error) {
			return &client{cfg: cfg, http: &http.Client{Transport: transport}}, nil
		},
	}

	ds := &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"base_url": "https://jira.example.com",
			"username": "user",
		},
		Credentials: map[string]interface{}{
			"password": "pwd",
		},
	}

	resources, err := conn.ListResources(context.Background(), ds, "")
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].ExternalID != "DEMO" || resources[0].Name != "[DEMO] Demo Project" {
		t.Errorf("unexpected resource: %+v", resources[0])
	}
}

func TestConnectorFetchStream(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/api/2/project" {
			return jsonResponse(http.StatusOK, []project{
				{ID: "100", Key: "DEMO", Name: "Demo Project"},
			}), nil
		}
		if r.URL.Path == "/rest/api/3/search/jql" {
			return jsonResponse(http.StatusOK, searchResponse{
				StartAt:    0,
				MaxResults: 50,
				Total:      1,
				Issues: []issue{
					{
						ID:  "1",
						Key: "DEMO-1",
						Fields: issueFields{
							Summary: "First ticket",
							Created: "2024-01-01T00:00:00.000Z",
							Updated: "2024-01-02T00:00:00.000Z",
						},
					},
				},
			}), nil
		}
		return jsonResponse(http.StatusNotFound, nil), nil
	})

	conn := &Connector{
		newClient: func(cfg config) (*client, error) {
			return &client{cfg: cfg, http: &http.Client{Transport: transport}}, nil
		},
	}

	ds := &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"base_url": "https://jira.example.com",
			"username": "user",
		},
		Credentials: map[string]interface{}{
			"password": "pwd",
		},
		ResourceIDs: []string{"DEMO"},
	}

	handler := &testStreamHandler{}
	nextCursor, err := conn.FetchStream(context.Background(), ds, nil, handler)
	if err != nil {
		t.Fatalf("FetchStream failed: %v", err)
	}

	if len(handler.emitted) != 1 {
		t.Fatalf("expected 1 emitted item, got %d", len(handler.emitted))
	}
	if handler.emitted[0].ExternalID != "DEMO-1" {
		t.Errorf("unexpected external ID: %s", handler.emitted[0].ExternalID)
	}

	dec := decodeCursor(nextCursor)
	if dec.ProjectIssues["DEMO"]["DEMO-1"] != "2024-01-02T00:00:00.000Z" {
		t.Errorf("cursor did not record issue version: %+v", dec)
	}

	// Now run incremental sync with unchanged issue
	handler2 := &testStreamHandler{}
	_, err = conn.FetchStream(context.Background(), ds, nextCursor, handler2)
	if err != nil {
		t.Fatalf("incremental FetchStream failed: %v", err)
	}
	if len(handler2.emitted) != 0 {
		t.Errorf("expected 0 emitted items for unchanged issue, got %d", len(handler2.emitted))
	}
}

func TestConnectorFetchFullStreamDeletions(t *testing.T) {
	// Baseline has DEMO-1 and DEMO-2. Remote now only has DEMO-1.
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/api/2/project" {
			return jsonResponse(http.StatusOK, []project{
				{ID: "100", Key: "DEMO", Name: "Demo Project"},
			}), nil
		}
		if r.URL.Path == "/rest/api/3/search/jql" {
			return jsonResponse(http.StatusOK, searchResponse{
				StartAt:    0,
				MaxResults: 50,
				Total:      1,
				Issues: []issue{
					{
						ID:  "1",
						Key: "DEMO-1",
						Fields: issueFields{
							Summary: "First ticket",
							Created: "2024-01-01T00:00:00.000Z",
							Updated: "2024-01-02T00:00:00.000Z",
						},
					},
				},
			}), nil
		}
		return jsonResponse(http.StatusNotFound, nil), nil
	})

	conn := &Connector{
		newClient: func(cfg config) (*client, error) {
			return &client{cfg: cfg, http: &http.Client{Transport: transport}}, nil
		},
	}

	ds := &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"base_url": "https://jira.example.com",
			"username": "user",
		},
		Credentials: map[string]interface{}{
			"password": "pwd",
		},
		ResourceIDs: []string{"DEMO"},
	}

	initialCur := cursor{
		ProjectIssues: map[string]map[string]string{
			"DEMO": {
				"DEMO-1": "2024-01-01T00:00:00.000Z",
				"DEMO-2": "2024-01-01T00:00:00.000Z",
			},
		},
	}.syncCursor()

	handler := &testStreamHandler{}
	nextCursor, err := conn.FetchFullStream(context.Background(), ds, initialCur, handler)
	if err != nil {
		t.Fatalf("FetchFullStream failed: %v", err)
	}

	// Should have emitted DEMO-1 (re-fetched in full sync) and DEMO-2 (deleted)
	if len(handler.emitted) != 2 {
		t.Fatalf("expected 2 emitted items, got %d", len(handler.emitted))
	}

	var deleted *types.FetchedItem
	for i := range handler.emitted {
		if handler.emitted[i].IsDeleted {
			deleted = &handler.emitted[i]
		}
	}
	if deleted == nil || deleted.ExternalID != "DEMO-2" {
		t.Fatalf("expected DEMO-2 to be marked as deleted, got: %+v", deleted)
	}

	dec := decodeCursor(nextCursor)
	if _, exists := dec.ProjectIssues["DEMO"]["DEMO-2"]; exists {
		t.Errorf("expected DEMO-2 to be removed from cursor")
	}
}

func TestConnectorFetchStreamWithAttachments(t *testing.T) {
	transport := roundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/rest/api/2/project" {
			return jsonResponse(http.StatusOK, []project{
				{ID: "100", Key: "DEMO", Name: "Demo Project"},
			}), nil
		}
		if r.URL.Path == "/rest/api/3/search/jql" {
			return jsonResponse(http.StatusOK, searchResponse{
				Total: 1,
				Issues: []issue{
					{
						ID:  "1",
						Key: "DEMO-1",
						Fields: issueFields{
							Summary: "Ticket with log",
							Created: "2024-01-01T00:00:00.000Z",
							Updated: "2024-01-02T00:00:00.000Z",
							Attachment: []attachment{
								{
									ID:       "att-1",
									Filename: "debug.log",
									Size:     1024,
									MimeType: "text/plain",
									Content:  "https://jira.example.com/rest/api/3/attachment/content/att-1",
								},
								{
									ID:       "att-2",
									Filename: "screenshot.png", // non-parseable
									Size:     2048,
									MimeType: "image/png",
									Content:  "https://jira.example.com/rest/api/3/attachment/content/att-2",
								},
							},
						},
					},
				},
			}), nil
		}
		if r.URL.Path == "/rest/api/3/attachment/content/att-1" {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("log line 1\nlog line 2")),
			}, nil
		}
		return jsonResponse(http.StatusNotFound, nil), nil
	})

	conn := &Connector{
		newClient: func(cfg config) (*client, error) {
			return &client{cfg: cfg, http: &http.Client{Transport: transport}}, nil
		},
	}

	ds := &types.DataSourceConfig{
		Settings: map[string]interface{}{
			"base_url": "https://jira.example.com",
			"username": "user",
		},
		Credentials: map[string]interface{}{
			"password": "pwd",
		},
		ResourceIDs: []string{"DEMO"},
	}

	handler := &testStreamHandler{}
	_, err := conn.FetchStream(context.Background(), ds, nil, handler)
	if err != nil {
		t.Fatalf("FetchStream failed: %v", err)
	}

	// Expect exactly 1 unified item for the ticket, with the attachment content embedded in Markdown
	if len(handler.emitted) != 1 {
		t.Fatalf("expected 1 unified item (embedded attachment), got %d: %+v", len(handler.emitted), handler.emitted)
	}
	item := handler.emitted[0]
	if item.ExternalID != "DEMO-1" {
		t.Errorf("expected main issue DEMO-1, got %s", item.ExternalID)
	}
	if !item.ReplacesSubtree {
		t.Errorf("expected ReplacesSubtree true to clean up legacy separate attachment rows")
	}
	if len(item.SubtreeKeep) != 0 {
		t.Errorf("expected SubtreeKeep nil to clean up legacy separate attachment rows, got %v", item.SubtreeKeep)
	}
	mdContent := string(item.Content)
	if !strings.Contains(mdContent, "### 📎 debug.log") {
		t.Errorf("expected embedded attachment heading in markdown:\n%s", mdContent)
	}
	if !strings.Contains(mdContent, "```log\nlog line 1\nlog line 2\n```") {
		t.Errorf("expected embedded attachment code block in markdown:\n%s", mdContent)
	}
}
