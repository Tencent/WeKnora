package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateEvaluationDatasetRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/evaluation/datasets" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if body["name"] != "Tenant dataset" {
			t.Errorf("name = %v, want Tenant dataset", body["name"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"id": "dataset-1", "scope": "tenant", "owner_tenant_id": 7,
				"name": "Tenant dataset", "description": "",
				"created_at": "2026-08-28T08:00:00Z", "updated_at": "2026-08-28T08:00:00Z",
			},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	dataset, err := client.CreateEvaluationDataset(context.Background(), &CreateEvaluationDatasetRequest{
		Name: "Tenant dataset",
	})
	if err != nil {
		t.Fatalf("CreateEvaluationDataset() error = %v", err)
	}
	if dataset.ID != "dataset-1" || dataset.OwnerTenantID == nil || *dataset.OwnerTenantID != 7 {
		t.Fatalf("dataset = %+v, want dataset-1 owned by tenant 7", dataset)
	}
}

func TestCreateEvaluationDatasetVersionRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/api/v1/evaluation/datasets/dataset-1/versions"
		if r.Method != http.MethodPost || r.URL.Path != wantPath {
			t.Errorf("unexpected %s %s, want POST %s", r.Method, r.URL.Path, wantPath)
		}
		var body EvaluationDatasetVersionInput
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		if len(body.Passages) != 1 || body.Passages[0].PID != "p1" {
			t.Errorf("passages = %+v, want one passage p1", body.Passages)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"data": map[string]any{
				"id": "version-1", "dataset_id": "dataset-1", "version_number": 1,
				"schema_version": 1, "artifact_sha256": "a", "content_sha256": "b",
				"passage_count": 1, "question_count": 1, "relevance_count": 1,
				"created_at": "2026-08-28T08:00:00Z",
			},
		})
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	version, err := client.CreateEvaluationDatasetVersion(context.Background(), "dataset-1",
		&EvaluationDatasetVersionInput{
			Passages:  []EvaluationDatasetPassageInput{{PID: "p1", Content: "text"}},
			Questions: []EvaluationDatasetQuestionInput{{QID: "q1", Question: "q", Answer: "a"}},
			Relevance: []EvaluationDatasetRelevanceInput{{QID: "q1", PID: "p1", Grade: 1}},
		})
	if err != nil {
		t.Fatalf("CreateEvaluationDatasetVersion() error = %v", err)
	}
	if version.ID != "version-1" || version.VersionNumber != 1 {
		t.Fatalf("version = %+v, want version-1 number 1", version)
	}
}

func TestListEvaluationDatasetEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/evaluation/datasets":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"items": []map[string]any{{"id": "dataset-1", "scope": "system", "name": "sys"}},
				},
			})
		case "/api/v1/evaluation/datasets/dataset-1/versions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true,
				"data": map[string]any{
					"items": []map[string]any{{"id": "version-1", "dataset_id": "dataset-1", "version_number": 1}},
				},
			})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	datasets, err := client.ListEvaluationDatasets(context.Background())
	if err != nil {
		t.Fatalf("ListEvaluationDatasets() error = %v", err)
	}
	if len(datasets) != 1 || datasets[0].ID != "dataset-1" {
		t.Fatalf("datasets = %+v, want one dataset-1", datasets)
	}
	versions, err := client.ListEvaluationDatasetVersions(context.Background(), "dataset-1")
	if err != nil {
		t.Fatalf("ListEvaluationDatasetVersions() error = %v", err)
	}
	if len(versions) != 1 || versions[0].ID != "version-1" {
		t.Fatalf("versions = %+v, want one version-1", versions)
	}
}

func TestEvaluationDatasetClientValidatesInputs(t *testing.T) {
	client := NewClient("http://example.test")
	ctx := context.Background()
	if _, err := client.CreateEvaluationDataset(ctx, nil); err == nil {
		t.Fatal("nil dataset request must fail")
	}
	if _, err := client.CreateEvaluationDatasetVersion(ctx, "", &EvaluationDatasetVersionInput{}); err == nil {
		t.Fatal("empty dataset id must fail")
	}
	if _, err := client.CreateEvaluationDatasetVersion(ctx, "dataset-1", nil); err == nil {
		t.Fatal("nil version content must fail")
	}
	if _, err := client.ListEvaluationDatasetVersions(ctx, ""); err == nil {
		t.Fatal("empty dataset id must fail for version list")
	}
}
