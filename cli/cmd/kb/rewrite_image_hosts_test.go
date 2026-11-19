package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type fakeImageHostService struct {
	mu sync.Mutex

	docs    []sdk.Knowledge
	details map[string]*sdk.Knowledge
	chunks  map[string][]sdk.Chunk

	updateErr  map[string]error
	onUpdate   func(*sdk.Knowledge)
	updates    []string
	updateReqs []*sdk.UpdateManualKnowledgeRequest
	getCalls   map[string]int
	listPages  []int
}

func newFakeImageHostService(docs ...sdk.Knowledge) *fakeImageHostService {
	f := &fakeImageHostService{
		docs:      docs,
		details:   map[string]*sdk.Knowledge{},
		chunks:    map[string][]sdk.Chunk{},
		updateErr: map[string]error{},
		getCalls:  map[string]int{},
	}
	for _, doc := range docs {
		cp := doc
		f.details[doc.ID] = &cp
	}
	return f
}

func (f *fakeImageHostService) ListKnowledgeWithFilter(
	_ context.Context, _ string, page, pageSize int, _ sdk.KnowledgeListFilter,
) ([]sdk.Knowledge, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listPages = append(f.listPages, page)
	total := len(f.docs)
	start := (page - 1) * pageSize
	if start >= total {
		return nil, int64(total), nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	out := append([]sdk.Knowledge(nil), f.docs[start:end]...)
	return out, int64(total), nil
}

func (f *fakeImageHostService) GetKnowledge(_ context.Context, id string) (*sdk.Knowledge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCalls[id]++
	doc := f.details[id]
	if doc == nil {
		return nil, fmt.Errorf("missing knowledge %s", id)
	}
	cp := *doc
	cp.Metadata = append(json.RawMessage(nil), doc.Metadata...)
	return &cp, nil
}

func (f *fakeImageHostService) ListKnowledgeChunks(
	_ context.Context, id string, page, pageSize int, types ...string,
) ([]sdk.Chunk, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var matched []sdk.Chunk
	for _, chunk := range f.chunks[id] {
		if len(types) == 0 {
			matched = append(matched, chunk)
			continue
		}
		for _, want := range types {
			if chunk.ChunkType == want {
				matched = append(matched, chunk)
				break
			}
		}
	}
	total := len(matched)
	start := (page - 1) * pageSize
	if start >= total {
		return nil, int64(total), nil
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return append([]sdk.Chunk(nil), matched[start:end]...), int64(total), nil
}

func (f *fakeImageHostService) UpdateManualKnowledge(
	_ context.Context, id string, req *sdk.UpdateManualKnowledgeRequest,
) (*sdk.Knowledge, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.updateErr[id]; err != nil {
		return nil, err
	}
	f.updates = append(f.updates, id)
	f.updateReqs = append(f.updateReqs, req)
	doc := f.details[id]
	if doc != nil {
		doc.Metadata = manualMetadata(req.Content)
		doc.ParseStatus = "completed"
		if f.onUpdate != nil {
			f.onUpdate(doc)
		}
	}
	return doc, nil
}

func manualMetadata(content string) json.RawMessage {
	data, _ := json.Marshal(map[string]any{
		"content": content, "format": "markdown", "status": "publish", "version": 1,
	})
	return data
}

func manualDoc(id, title, content string) sdk.Knowledge {
	return sdk.Knowledge{ID: id, Title: title, Type: "manual", ParseStatus: "completed", Metadata: manualMetadata(content)}
}

func TestRewriteImageHostURLsOnlyChangesHost(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		n    int
	}{
		{
			name: "markdown image",
			in:   "![](https://img2.tapimg.com/moment/a.jpg)",
			want: "![](https://img2-tc.tapimg.com/moment/a.jpg)",
			n:    1,
		},
		{
			name: "html img",
			in:   "<img src=\"https://img2.tapimg.com/moment/a.jpg\" width=\"10\">",
			want: "<img src=\"https://img2-tc.tapimg.com/moment/a.jpg\" width=\"10\">",
			n:    1,
		},
		{
			name: "bare url with query and fragment",
			in:   "see https://img2.tapimg.com/x/y?q=1#frag for details",
			want: "see https://img2-tc.tapimg.com/x/y?q=1#frag for details",
			n:    1,
		},
		{
			name: "multiple urls",
			in:   "https://img2.tapimg.com/a.jpg https://img2.tapimg.com/b.jpg",
			want: "https://img2-tc.tapimg.com/a.jpg https://img2-tc.tapimg.com/b.jpg",
			n:    2,
		},
		{
			name: "url at end of string",
			in:   "https://img2.tapimg.com",
			want: "https://img2-tc.tapimg.com",
			n:    1,
		},
		{
			name: "lookalike host is untouched",
			in:   "https://img2.tapimg.com.evil.example/a.jpg",
			want: "https://img2.tapimg.com.evil.example/a.jpg",
			n:    0,
		},
		{
			name: "other host is untouched",
			in:   "https://img2-tc.tapimg.com/a.jpg",
			want: "https://img2-tc.tapimg.com/a.jpg",
			n:    0,
		},
		{
			name: "http scheme is untouched",
			in:   "http://img2.tapimg.com/a.jpg",
			want: "http://img2.tapimg.com/a.jpg",
			n:    0,
		},
		{
			name: "path and punctuation survive",
			in:   "prefix https://img2.tapimg.com/a.jpg, suffix",
			want: "prefix https://img2-tc.tapimg.com/a.jpg, suffix",
			n:    1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, n := rewriteImageHostURLs(tc.in, "img2.tapimg.com", "img2-tc.tapimg.com")
			require.Equal(t, tc.want, got)
			require.Equal(t, tc.n, n)
			require.Equal(t, tc.n, countImageHostURLs(tc.in, "img2.tapimg.com"))
		})
	}
}

func TestRewriteImageHostsDryRunReportsWithoutWriting(t *testing.T) {
	stdout, _ := iostreams.SetForTest(t)
	a := manualDoc("a", "alpha", "text ![x](https://img2.tapimg.com/a.jpg)")
	b := manualDoc("b", "beta", "no image here")
	c := sdk.Knowledge{ID: "c", Title: "gamma", Type: "file", ParseStatus: "completed",
		Metadata: manualMetadata("https://img2.tapimg.com/c.jpg")}
	svc := newFakeImageHostService(a, b, c)
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "text", Content: "![x](https://img2.tapimg.com/a.jpg)"}}
	svc.chunks["c"] = []sdk.Chunk{{ChunkType: "text", Content: "https://img2.tapimg.com/c.jpg"}}

	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Concurrency: 2, PageSize: 2, Timeout: time.Second, Verbose: true,
	}
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))

	require.Empty(t, svc.updates, "dry-run must not write")
	require.Contains(t, stdout.String(), "mode=dry-run")
	require.Contains(t, stdout.String(), "scanned=3")
	require.Contains(t, stdout.String(), "matched=2")
	require.Contains(t, stdout.String(), "would_update=1")
	require.Contains(t, stdout.String(), "skipped=1/0")
	require.Contains(t, stdout.String(), "chunk=2")
}

func TestRewriteImageHostsApplyIsIdempotent(t *testing.T) {
	stdout, _ := iostreams.SetForTest(t)
	doc := manualDoc("a", "alpha", "text ![x](https://img2.tapimg.com/a.jpg)")
	svc := newFakeImageHostService(doc)
	svc.chunks["a"] = []sdk.Chunk{
		{ChunkType: "text", Content: "text"},
		{ChunkType: "image_caption", Content: "caption", IndexStatus: "ready"},
	}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: time.Second, Verbose: true,
	}
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Equal(t, []string{"a"}, svc.updates)
	require.Len(t, svc.updateReqs, 1)
	require.Equal(t, "alpha", svc.updateReqs[0].Title)
	require.Equal(t, "publish", svc.updateReqs[0].Status)
	require.Contains(t, svc.updateReqs[0].Content, "https://img2-tc.tapimg.com/")
	require.NotContains(t, svc.updateReqs[0].Content, "https://img2.tapimg.com/")
	require.Contains(t, stdout.String(), "updated=1")

	stdout.Reset()
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Len(t, svc.updates, 1, "a second run must not write again")
	require.Contains(t, stdout.String(), "no_match=1")
}

func TestRewriteImageHostsFailsWhenIdentityChanges(t *testing.T) {
	doc := manualDoc("a", "alpha", "https://img2.tapimg.com/a.jpg")
	doc.CustomMetadata = json.RawMessage(`{"game_id":"1"}`)
	doc.ParentKnowledgeID = "parent"
	svc := newFakeImageHostService(doc)
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	svc.onUpdate = func(updated *sdk.Knowledge) {
		updated.CustomMetadata = json.RawMessage(`{"game_id":"2"}`)
	}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: time.Second,
	}
	stdout, _ := iostreams.SetForTest(t)
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Contains(t, stdout.String(), "failed=1")
}

func TestRewriteImageHostsPreservesIdentityFields(t *testing.T) {
	doc := manualDoc("a", "alpha", "https://img2.tapimg.com/a.jpg")
	doc.CustomMetadata = json.RawMessage(`{"game_id":"1"}`)
	doc.ParentKnowledgeID = "parent"
	svc := newFakeImageHostService(doc)
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: time.Second,
	}
	stdout, _ := iostreams.SetForTest(t)
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Contains(t, stdout.String(), "updated=1")
	require.Equal(t, "parent", svc.details["a"].ParentKnowledgeID)
}

func TestRewriteImageHostsPreservesDraftStatus(t *testing.T) {
	doc := manualDoc("a", "draft doc", "![](https://img2.tapimg.com/a.jpg)")
	doc.ParseStatus = "draft"
	svc := newFakeImageHostService(doc)
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: time.Second,
	}
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Equal(t, "draft", svc.updateReqs[0].Status)
}

func TestRewriteImageHostsPaginatesAndHonoursLimit(t *testing.T) {
	var docs []sdk.Knowledge
	for i := 0; i < 250; i++ {
		docs = append(docs, manualDoc(fmt.Sprintf("doc-%03d", i), "", "plain text"))
	}
	svc := newFakeImageHostService(docs...)
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Limit: 120, Concurrency: 8, PageSize: 100, Timeout: 0,
	}
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Equal(t, []int{1, 2}, svc.listPages)
	require.Len(t, svc.getCalls, 120)
	require.Zero(t, svc.getCalls["doc-120"])
}

func TestRewriteImageHostsResumeSkipsDoneAndRetriesFailed(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	state := imageHostState{
		KB: "kb-1", From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Documents: map[string]imageHostDocResult{
			"a": {ID: "a", Status: imageHostStatusDone},
			"b": {ID: "b", Status: imageHostStatusFailed, Reason: "boom"},
		},
	}
	data, err := json.Marshal(state)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(statePath, data, 0o600))

	svc := newFakeImageHostService(
		manualDoc("a", "a", "https://img2.tapimg.com/a.jpg"),
		manualDoc("b", "b", "https://img2.tapimg.com/b.jpg"),
		manualDoc("c", "c", "https://img2.tapimg.com/c.jpg"),
	)
	for _, id := range []string{"a", "b", "c"} {
		svc.chunks[id] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 2, PageSize: 10, Timeout: time.Second, Resume: statePath,
	}
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Zero(t, svc.getCalls["a"], "done docs are skipped")
	require.Equal(t, []string{"b", "c"}, svc.updates)
	require.FileExists(t, statePath)

	var saved imageHostState
	data, err = os.ReadFile(statePath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &saved))
	require.Equal(t, imageHostStatusDone, saved.Documents["b"].Status)
	require.Equal(t, imageHostStatusDone, saved.Documents["c"].Status)
}

func TestRewriteImageHostsTimeoutMarksUnverified(t *testing.T) {
	doc := manualDoc("a", "slow", "![](https://img2.tapimg.com/a.jpg)")
	svc := newFakeImageHostService(doc)
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "pending"}}
	svc.details["a"].ParseStatus = "pending"
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: 20 * time.Millisecond,
	}
	stdout, _ := iostreams.SetForTest(t)
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Equal(t, []string{"a"}, svc.updates)
	require.Contains(t, stdout.String(), "unverified=1")
}

func TestRewriteImageHostsChunkOnlyIsNotWritten(t *testing.T) {
	doc := manualDoc("a", "chunk only", "no url in the body")
	svc := newFakeImageHostService(doc)
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "text", Content: "https://img2.tapimg.com/a.jpg"}}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: time.Second,
	}
	stdout, _ := iostreams.SetForTest(t)
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Empty(t, svc.updates)
	require.Contains(t, stdout.String(), "chunk_only=1")
}

func TestRewriteImageHostsFailureDoesNotAbortBatch(t *testing.T) {
	svc := newFakeImageHostService(
		manualDoc("a", "bad", "https://img2.tapimg.com/a.jpg"),
		manualDoc("b", "good", "https://img2.tapimg.com/b.jpg"),
	)
	svc.updateErr["a"] = fmt.Errorf("server rejected")
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	svc.chunks["b"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Apply: true, Concurrency: 1, PageSize: 10, Timeout: time.Second,
	}
	stdout, _ := iostreams.SetForTest(t)
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatText}, svc, "kb-1"))
	require.Len(t, svc.updates, 1, "only the successful document is recorded")
	require.Contains(t, stdout.String(), "failed=1")
	require.Contains(t, stdout.String(), "updated=1")
}

func TestRewriteImageHostsJSONEnvelope(t *testing.T) {
	stdout, _ := iostreams.SetForTest(t)
	svc := newFakeImageHostService(manualDoc("a", "a", "https://img2.tapimg.com/a.jpg"))
	svc.chunks["a"] = []sdk.Chunk{{ChunkType: "image_caption", IndexStatus: "ready"}}
	opts := &RewriteImageHostsOptions{
		From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
		Concurrency: 1, PageSize: 10, Timeout: time.Second,
	}
	require.NoError(t, runRewriteImageHosts(context.Background(), opts,
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, svc, "kb-1"))
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &envelope))
	require.Equal(t, true, envelope["ok"])
	data := envelope["data"].(map[string]any)
	require.Equal(t, float64(1), data["matched_documents"])
	require.Equal(t, float64(1), data["would_update"])
	require.Len(t, data["documents"], 1)
}

func TestRewriteImageHostStateRejectsDifferentHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	data, _ := json.Marshal(imageHostState{KB: "kb-1", From: "a.example", To: "b.example"})
	require.NoError(t, os.WriteFile(path, data, 0o600))
	_, err := loadImageHostState(path, "kb-1", "img2.tapimg.com", "img2-tc.tapimg.com")
	require.ErrorContains(t, err, "--from")
}

func TestValidateRewriteImageHostsOptions(t *testing.T) {
	base := func() *RewriteImageHostsOptions {
		return &RewriteImageHostsOptions{
			From: "img2.tapimg.com", To: "img2-tc.tapimg.com",
			Concurrency: 4, PageSize: 100, Timeout: time.Second,
		}
	}
	require.NoError(t, validateRewriteImageHostsOptions(base()))

	opts := base()
	opts.From = "https://img2.tapimg.com"
	require.ErrorContains(t, validateRewriteImageHostsOptions(opts), "bare host")

	opts = base()
	opts.To = opts.From
	require.ErrorContains(t, validateRewriteImageHostsOptions(opts), "different")

	opts = base()
	opts.Apply, opts.DryRun = true, true
	require.ErrorContains(t, validateRewriteImageHostsOptions(opts), "mutually exclusive")

	opts = base()
	opts.Concurrency = 0
	require.ErrorContains(t, validateRewriteImageHostsOptions(opts), "concurrency")

	opts = base()
	opts.Limit = -1
	require.ErrorContains(t, validateRewriteImageHostsOptions(opts), "limit")
}

func TestRewriteImageHostsEmptyStateIsFresh(t *testing.T) {
	state, err := loadImageHostState("", "kb-1", "a.example", "b.example")
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Empty(t, state.Documents)
	require.Equal(t, "kb-1", state.KB)
}
