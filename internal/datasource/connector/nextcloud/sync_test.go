package nextcloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type recorder struct {
	items                    []types.FetchedItem
	checkpoints              []*types.SyncCursor
	failEmit, failCheckpoint bool
}

func (h *recorder) Emit(_ context.Context, item types.FetchedItem) error {
	if h.failEmit {
		return errors.New("ingest failed")
	}
	h.items = append(h.items, item)
	return nil
}

func (h *recorder) Checkpoint(_ context.Context, cur *types.SyncCursor) error {
	if h.failCheckpoint {
		return errors.New("checkpoint failed")
	}
	h.checkpoints = append(h.checkpoints, cur)
	return nil
}

func TestIncrementalMoveDeleteAndUnchanged(t *testing.T) {
	f := newFakeNextcloud(t)
	f.add("/docs/说明.pdf", "same content")
	f.add("/docs/copy.pdf", "same content")
	f.add("/docs/ignored.zip", "zip")
	c := NewConnector()
	cfg := f.config("/docs", "/docs/说明.pdf")
	items, cur, err := c.FetchIncremental(t.Context(), cfg, nil)
	if err != nil || len(items) != 2 {
		t.Fatalf("first sync = %d, %v", len(items), err)
	}
	if items[0].ExternalID == items[1].ExternalID || items[1].FileName != "Nextcloud/docs/说明.pdf" {
		t.Fatalf("identities or Unicode filenames = %+v", items)
	}
	items, next, err := c.FetchIncremental(t.Context(), cfg, cur)
	if err != nil || len(items) != 0 || f.count("GET:/docs/说明.pdf") != 1 {
		t.Fatalf("unchanged = %+v, %v", items, err)
	}
	cur = next
	f.mu.Lock()
	node := f.nodes["/docs/说明.pdf"]
	node.etag = `"v2"`
	f.nodes["/docs/说明.pdf"] = node
	f.nodes["/docs/moved.pdf"] = f.nodes["/docs/copy.pdf"]
	delete(f.nodes, "/docs/copy.pdf")
	f.mu.Unlock()
	items, _, err = c.FetchIncremental(t.Context(), cfg, cur)
	if err != nil || len(items) != 3 {
		t.Fatalf("update and move = %+v, %v", items, err)
	}
	deleted := 0
	for _, item := range items {
		if item.IsDeleted {
			deleted++
			if !strings.HasSuffix(item.ExternalID, ":/docs/copy.pdf") {
				t.Fatalf("wrong deletion: %+v", item)
			}
		}
	}
	if deleted != 1 {
		t.Fatalf("deletions = %d", deleted)
	}
}

func TestPartialScanNeverDeletesAndFailedFetchRetries(t *testing.T) {
	f := newFakeNextcloud(t)
	f.add("/docs/a.pdf", "a")
	f.add("/docs/sub/b.pdf", "b")
	c := NewConnector()
	cfg := f.config("/docs")
	_, cur, err := c.FetchIncremental(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.nodes, "/docs/a.pdf")
	f.status["PROPFIND:/docs/sub"] = 403
	f.mu.Unlock()
	h := &recorder{}
	_, err = c.FetchStream(t.Context(), cfg, cur, h)
	if err == nil {
		t.Fatal("partial scan accepted")
	}
	for _, item := range h.items {
		if item.IsDeleted {
			t.Fatal("deletion after incomplete scan")
		}
	}
	f.mu.Lock()
	delete(f.status, "PROPFIND:/docs/sub")
	n := f.nodes["/docs/sub/b.pdf"]
	n.etag = `"v2"`
	f.nodes["/docs/sub/b.pdf"] = n
	f.status["GET:/docs/sub/b.pdf"] = 403
	f.mu.Unlock()
	items, next, err := c.FetchIncremental(t.Context(), cfg, cur)
	if err != nil || len(items) != 2 {
		t.Fatalf("per-file error = %+v, %v", items, err)
	}
	state, err := decodeCursor(next)
	if err != nil || state.Files["/docs/sub/b.pdf"] != "" {
		t.Fatalf("failed file must retry: %+v, %v", state, err)
	}
	f.mu.Lock()
	delete(f.status, "GET:/docs/sub/b.pdf")
	f.mu.Unlock()
	items, _, err = c.FetchIncremental(t.Context(), cfg, next)
	if err != nil || len(items) != 1 || string(items[0].Content) != "b" {
		t.Fatalf("retry = %+v, %v", items, err)
	}
}

func TestFullSyncCheckpointResumeAndDeletionBaseline(t *testing.T) {
	f := newFakeNextcloud(t)
	for i := 0; i < 55; i++ {
		f.add(fmt.Sprintf("/docs/%03d.pdf", i), "pdf")
	}
	f.add("/docs/z/gone.pdf", "gone")
	c := NewConnector()
	cfg := f.config("/docs")
	_, cur, err := c.FetchIncremental(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.nodes, "/docs/z/gone.pdf")
	f.status["PROPFIND:/docs/z"] = 500
	f.mu.Unlock()
	h := &recorder{}
	_, err = c.FetchFullStream(t.Context(), cfg, cur, h)
	if err == nil || len(h.checkpoints) == 0 {
		t.Fatalf("interrupted full sync: %v, %d checkpoints", err, len(h.checkpoints))
	}
	for _, item := range h.items {
		if item.IsDeleted {
			t.Fatal("partial full sync deleted a file")
		}
	}
	resume := h.checkpoints[len(h.checkpoints)-1]
	f.mu.Lock()
	delete(f.status, "PROPFIND:/docs/z")
	f.mu.Unlock()
	items, next, err := c.FetchIncremental(t.Context(), cfg, resume)
	if err != nil || len(items) != 1 || !items[0].IsDeleted {
		t.Fatalf("resume = %+v, %v", items, err)
	}
	state, _ := decodeCursor(next)
	if state.FullSync || len(state.Files) != 55 || len(state.FullSyncBaseline) != 0 {
		t.Fatal("full baseline not finalized")
	}
	// A saved snapshot must not be mutated by later changes to the run.
	snapshot, _ := decodeCursor(h.checkpoints[0])
	if len(snapshot.Files) >= 55 {
		t.Fatal("earlier checkpoint was mutated")
	}
}

func TestMissingRootHandlerFailuresAndCancellation(t *testing.T) {
	f := newFakeNextcloud(t)
	f.add("/docs/a.pdf", "pdf")
	c := NewConnector()
	cfg := f.config("/docs")
	_, cur, err := c.FetchIncremental(t.Context(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	delete(f.nodes, "/docs")
	f.mu.Unlock()
	h := &recorder{}
	_, err = c.FetchStream(t.Context(), cfg, cur, h)
	if !errors.Is(err, datasource.ErrResourceNotFound) || len(h.items) != 0 {
		t.Fatalf("missing root = %v", err)
	}
	f.add("/docs/a.pdf", "pdf")
	h = &recorder{failEmit: true}
	_, err = c.FetchStream(t.Context(), cfg, nil, h)
	if err == nil || len(h.checkpoints) != 0 {
		t.Fatal("ingest failure advanced cursor")
	}
	h = &recorder{failCheckpoint: true}
	if _, err = c.FetchStream(t.Context(), cfg, nil, h); err == nil {
		t.Fatal("checkpoint failure ignored")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = c.FetchStream(ctx, cfg, cur, &recorder{}); !errors.Is(err, datasource.ErrSyncCanceled) {
		t.Fatalf("cancellation = %v", err)
	}
}

func TestSizeLimitAndExtensionFilters(t *testing.T) {
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	f := newFakeNextcloud(t)
	f.add("/docs/big.pdf", strings.Repeat("x", (1<<20)+1))
	f.add("/docs/readme.md", "md")
	cfg := f.config("/docs")
	cfg.Settings = map[string]any{"exclude_extensions": "md"}
	items, _, err := NewConnector().FetchIncremental(t.Context(), cfg, nil)
	if err != nil || len(items) != 1 || items[0].Metadata["error_reason_code"] != reasonFileTooLarge ||
		f.count("GET:/docs/big.pdf") != 0 {
		t.Fatalf("size/filter = %+v, %v", items, err)
	}
}

func TestEachFingerprintComponentInvalidatesTheCursor(t *testing.T) {
	for _, component := range []string{"etag", "modified", "fileid", "size"} {
		t.Run(component, func(t *testing.T) {
			f := newFakeNextcloud(t)
			f.add("/docs/file.pdf", "pdf")
			c, cfg := NewConnector(), f.config("/docs")
			_, cur, err := c.FetchIncremental(t.Context(), cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			f.mu.Lock()
			node := f.nodes["/docs/file.pdf"]
			switch component {
			case "etag":
				node.etag = `"v2"`
			case "modified":
				node.modified = "Thu, 22 Oct 2026 07:28:00 GMT"
			case "fileid":
				node.id = "new-file-id"
			case "size":
				node.body = []byte("changed")
			}
			f.nodes["/docs/file.pdf"] = node
			f.mu.Unlock()
			items, _, err := c.FetchIncremental(t.Context(), cfg, cur)
			if err != nil || len(items) != 1 || f.count("GET:/docs/file.pdf") != 2 {
				t.Fatalf("changed %s was skipped: %d items, %v", component, len(items), err)
			}
		})
	}
}
