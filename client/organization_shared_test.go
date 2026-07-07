package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListSharedKnowledgeBasesDecodesNestedKnowledgeBase(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/shared-knowledge-bases" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success":true,
			"data":[{
				"knowledge_base":{"id":"kb-shared","name":"Partner Docs","knowledge_count":3},
				"share_id":"share-1","organization_id":"org-1","org_name":"Partners",
				"permission":"viewer","source_tenant_id":42,"shared_at":"2026-07-08T00:00:00Z"
			}]
		}`))
	}))
	t.Cleanup(srv.Close)

	items, err := NewClient(srv.URL).ListSharedKnowledgeBases(context.Background())
	if err != nil {
		t.Fatalf("ListSharedKnowledgeBases: %v", err)
	}
	if len(items) != 1 || items[0].KnowledgeBase == nil {
		t.Fatalf("expected one nested knowledge base, got %+v", items)
	}
	if items[0].KnowledgeBase.ID != "kb-shared" || items[0].KnowledgeBase.Name != "Partner Docs" {
		t.Fatalf("unexpected knowledge base: %+v", items[0].KnowledgeBase)
	}
	if items[0].OrgName != "Partners" || items[0].Permission != "viewer" {
		t.Fatalf("unexpected sharing metadata: %+v", items[0])
	}
}

func TestListSharedAgentsDecodesNestedAgent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/shared-agents" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"success":true,
			"data":[{
				"agent":{"id":"ag-shared","name":"Partner Agent"},
				"share_id":"share-1","organization_id":"org-1","org_name":"Partners",
				"permission":"viewer","source_tenant_id":42,"shared_at":"2026-07-08T00:00:00Z"
			}]
		}`))
	}))
	t.Cleanup(srv.Close)

	items, err := NewClient(srv.URL).ListSharedAgents(context.Background())
	if err != nil {
		t.Fatalf("ListSharedAgents: %v", err)
	}
	if len(items) != 1 || items[0].Agent == nil {
		t.Fatalf("expected one nested agent, got %+v", items)
	}
	if items[0].Agent.ID != "ag-shared" || items[0].OrgName != "Partners" {
		t.Fatalf("unexpected shared agent: %+v", items[0])
	}
}
