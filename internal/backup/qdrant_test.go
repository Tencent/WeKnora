package backup

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestQdrantHTTPClientCreatesDownloadsAndDeletesSnapshot(t *testing.T) {
	var created, downloaded, deleted bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("api-key") != "test-api-key" {
			t.Errorf("api-key header = %q", request.Header.Get("api-key"))
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/collections":
			_, _ = writer.Write([]byte(`{"result":{"collections":[{"name":"weknora"}]}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/collections/weknora/snapshots":
			created = true
			_, _ = writer.Write([]byte(`{"result":{"name":"snapshot-weknora.snapshot"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/collections/weknora/snapshots/snapshot-weknora.snapshot":
			downloaded = true
			_, _ = writer.Write([]byte("native qdrant snapshot"))
		case request.Method == http.MethodDelete && request.URL.Path == "/collections/weknora/snapshots/snapshot-weknora.snapshot":
			deleted = true
			_, _ = writer.Write([]byte(`{}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	client := qdrantHTTPClient{client: server.Client(), now: func() time.Time { return now }}
	directory := t.TempDir()
	snapshots, err := client.Create(context.Background(), MySQLConfig{
		LocalDir: directory, QdrantURL: server.URL, QdrantAPIKey: "test-api-key",
	}, "weknora-mysql-test-backup")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if !created || !downloaded || !deleted || len(snapshots) != 1 {
		t.Fatalf("unexpected Qdrant flow: created=%t downloaded=%t deleted=%t snapshots=%#v", created, downloaded, deleted, snapshots)
	}
	snapshot := snapshots[0]
	if snapshot.Collection != "weknora" || snapshot.File != qdrantSnapshotFileName("weknora-mysql-test-backup", "weknora") || snapshot.SizeBytes == 0 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if err := VerifyQdrantSnapshot(filepath.Join(directory, snapshot.File), snapshot); err != nil {
		t.Fatalf("VerifyQdrantSnapshot returned error: %v", err)
	}
}

func TestQdrantHTTPClientRejectsUnsafeCollectionName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"result":{"collections":[{"name":"../escape"}]}}`))
	}))
	defer server.Close()
	client := qdrantHTTPClient{client: server.Client(), now: time.Now}
	if _, err := client.Create(context.Background(), MySQLConfig{LocalDir: t.TempDir(), QdrantURL: server.URL}, "weknora-mysql-test-backup"); err == nil {
		t.Fatal("Create returned nil error for unsafe collection name")
	}
}
