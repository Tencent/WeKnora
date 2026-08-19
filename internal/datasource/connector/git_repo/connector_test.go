package git_repo

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// testRepo bundles a throwaway work repo plus its bare remote so tests can push
// additional commits for incremental sync.
type testRepo struct {
	bareDir string
	workDir string
	repo    *git.Repository
	branch  string
}

// setupTestRepo creates a bare repo, initializes it from the given files, and
// returns handles for pushing later commits. Everything lives in t.TempDir().
func setupTestRepo(t *testing.T, files map[string]string) *testRepo {
	t.Helper()
	workDir := t.TempDir()
	workRepo, err := git.PlainInit(workDir, false)
	if err != nil {
		t.Fatalf("PlainInit work repo: %v", err)
	}
	bareDir := filepath.Join(t.TempDir(), "bare.git")
	if _, err := git.PlainInit(bareDir, true); err != nil {
		t.Fatalf("PlainInit bare repo: %v", err)
	}
	if _, err := workRepo.CreateRemote(&gitconfig.RemoteConfig{Name: "origin", URLs: []string{bareDir}}); err != nil {
		t.Fatalf("CreateRemote: %v", err)
	}
	commitAndPush(t, workRepo, workDir, files, nil, "init")
	head, err := workRepo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	return &testRepo{bareDir: bareDir, workDir: workDir, repo: workRepo, branch: head.Name().Short()}
}

// commitAndPush writes files (creating/mutating), optionally removes others,
// commits and pushes to the bare remote on the current branch.
func commitAndPush(t *testing.T, repo *git.Repository, workDir string, files map[string]string, removed []string, msg string) {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("Worktree: %v", err)
	}
	for rel, content := range files {
		p := filepath.Join(workDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("MkdirAll %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile %s: %v", p, err)
		}
		if _, err := wt.Add(rel); err != nil {
			t.Fatalf("Add %s: %v", rel, err)
		}
	}
	for _, rel := range removed {
		if _, err := wt.Remove(rel); err != nil {
			t.Fatalf("Remove %s: %v", rel, err)
		}
	}
	if _, err := wt.Commit(msg, &git.CommitOptions{
		Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	// Read HEAD after committing — a fresh repo has no HEAD before the first commit.
	head, err := repo.Head()
	if err != nil {
		t.Fatalf("Head: %v", err)
	}
	refSpec := gitconfig.RefSpec(head.Name().String() + ":" + head.Name().String())
	if err := repo.Push(&git.PushOptions{RemoteName: "origin", RefSpecs: []gitconfig.RefSpec{refSpec}}); err != nil {
		t.Fatalf("Push: %v", err)
	}
}

// collectingHandler buffers emitted items and checkpoints.
type collectingHandler struct {
	items []types.FetchedItem
}

func (h *collectingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	h.items = append(h.items, item)
	return nil
}

func (h *collectingHandler) Checkpoint(context.Context, *types.SyncCursor) error { return nil }

// testConfig builds a git_repo config pointing at the given repo URL/branch.
func testConfig(url, branch string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		ID:       "ds-test",
		TenantID: 1,
		Settings: map[string]interface{}{
			"repos": []interface{}{map[string]interface{}{
				"repo_url": url, "branch": branch,
			}},
		},
	}
}

// withTempStorage redirects clone storage under a temp dir and restores after.
func withTempStorage(t *testing.T) {
	t.Helper()
	prev, hadPrev := os.LookupEnv(gitRepoStorageEnv)
	if err := os.Setenv(gitRepoStorageEnv, t.TempDir()); err != nil {
		t.Fatalf("Setenv: %v", err)
	}
	t.Cleanup(func() {
		if hadPrev {
			_ = os.Setenv(gitRepoStorageEnv, prev)
		} else {
			_ = os.Unsetenv(gitRepoStorageEnv)
		}
	})
}

func TestConnectorValidate(t *testing.T) {
	tr := setupTestRepo(t, map[string]string{"docs/a.md": "# A\n"})
	conn := NewConnector()

	if err := conn.Validate(context.Background(), testConfig(tr.bareDir, tr.branch)); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := conn.Validate(context.Background(), testConfig(tr.bareDir, "does-not-exist")); err == nil {
		t.Fatal("Validate with a missing branch should fail")
	}
	if err := conn.Validate(context.Background(), testConfig(tr.bareDir+"/nope.git", tr.branch)); err == nil {
		t.Fatal("Validate against a nonexistent repo should fail")
	}
}

// TestConnectorValidateNoSettings covers the stateless validate-credentials
// path, which sends credentials only (no settings.repos) — mirroring GitLab.
func TestConnectorValidateNoSettings(t *testing.T) {
	conn := NewConnector()
	ds := &types.DataSourceConfig{ID: "ds-x", TenantID: 1, Credentials: map[string]interface{}{"access_token": "x"}}
	if err := conn.Validate(context.Background(), ds); err != nil {
		t.Fatalf("Validate without settings should pass (nothing to check yet): %v", err)
	}
}

func TestConnectorFetchStreamFullSync(t *testing.T) {
	withTempStorage(t)
	tr := setupTestRepo(t, map[string]string{
		"docs/a.md":          "# A\n![img](./images/a.png)\n",
		"docs/b.md":          "# B\n",
		"docs/images/a.png":  "\x89PNG\r\n\x1a\n" + strings.Repeat("x", 600),
		"README.md":          "# readme\n",
		"notes/data.xlsx":    "ignored",
		"standalone-img.png": "\x89PNG\r\n\x1a\n" + strings.Repeat("y", 600),
	})
	conn := NewConnector()
	ds := testConfig(tr.bareDir, tr.branch)

	h := &collectingHandler{}
	cursor, err := conn.FetchStream(context.Background(), ds, nil, h)
	if err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if cursor == nil {
		t.Fatal("expected a cursor")
	}

	// Only supported document formats are emitted; standalone .png/.xlsx are not.
	if len(h.items) != 3 {
		t.Fatalf("items = %d, want 3 (docs/a.md, docs/b.md, README.md): %+v", len(h.items), h.items)
	}
	var seenA bool
	for _, item := range h.items {
		if item.FileName == "" || item.ExternalID == "" {
			t.Fatalf("item missing identity: %+v", item)
		}
		if strings.HasSuffix(item.FileName, "docs/a.md") {
			seenA = true
			// Relative image inlined as a data URI.
			if !strings.Contains(string(item.Content), "data:image/png;base64,") {
				t.Fatalf("docs/a.md content missing inlined image: %s", item.Content)
			}
		}
		if item.Metadata["channel"] != "git_repo" {
			t.Fatalf("channel = %q", item.Metadata["channel"])
		}
	}
	if !seenA {
		t.Fatal("docs/a.md was not emitted")
	}

	// Re-sync with the same cursor → no changes.
	h2 := &collectingHandler{}
	_, err = conn.FetchStream(context.Background(), ds, cursor, h2)
	if err != nil {
		t.Fatalf("re-sync FetchStream: %v", err)
	}
	if len(h2.items) != 0 {
		t.Fatalf("re-sync emitted %d items, want 0: %+v", len(h2.items), h2.items)
	}
}

func TestConnectorFetchStreamIncremental(t *testing.T) {
	withTempStorage(t)
	tr := setupTestRepo(t, map[string]string{
		"docs/a.md": "# A\n",
		"docs/b.md": "# B\n",
		"README.md": "# readme\n",
	})
	conn := NewConnector()
	ds := testConfig(tr.bareDir, tr.branch)

	h := &collectingHandler{}
	cursor, err := conn.FetchStream(context.Background(), ds, nil, h)
	if err != nil {
		t.Fatalf("first FetchStream: %v", err)
	}
	if len(h.items) != 3 {
		t.Fatalf("first sync items = %d, want 3", len(h.items))
	}

	// Add a file, modify one, delete one.
	commitAndPush(t, tr.repo, tr.workDir, map[string]string{
		"docs/c.md": "# C\n",
		"docs/b.md": "# B modified\n",
	}, []string{"README.md"}, "second")

	h2 := &collectingHandler{}
	cursor2, err := conn.FetchStream(context.Background(), ds, cursor, h2)
	if err != nil {
		t.Fatalf("second FetchStream: %v", err)
	}
	if cursor2 == nil {
		t.Fatal("expected second cursor")
	}

	var created, modified, deleted int
	for _, item := range h2.items {
		switch {
		case item.IsDeleted:
			deleted++
			if !strings.HasSuffix(item.ExternalID, "README.md") {
				t.Fatalf("deleted external_id = %q", item.ExternalID)
			}
		case strings.HasSuffix(item.ExternalID, "docs/c.md"):
			created++
		case strings.HasSuffix(item.ExternalID, "docs/b.md"):
			modified++
		}
	}
	if created != 1 || modified != 1 || deleted != 1 {
		t.Fatalf("created=%d modified=%d deleted=%d, want 1/1/1 (items=%+v)", created, modified, deleted, h2.items)
	}
}

func TestConnectorFetchStreamHistoryRewriteFallback(t *testing.T) {
	withTempStorage(t)
	tr := setupTestRepo(t, map[string]string{"docs/a.md": "# A\n"})
	conn := NewConnector()
	ds := testConfig(tr.bareDir, tr.branch)

	h := &collectingHandler{}
	if _, err := conn.FetchStream(context.Background(), ds, nil, h); err != nil {
		t.Fatalf("first FetchStream: %v", err)
	}
	if len(h.items) != 1 {
		t.Fatalf("first sync items = %d, want 1", len(h.items))
	}

	// Simulate a history rewrite: the cursor commit no longer exists.
	rewrittenCursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"repos": map[string]interface{}{
				tr.bareDir: map[string]interface{}{
					"commit": strings.Repeat("0", 40), // garbage SHA
					"branch": tr.branch,
				},
			},
		},
	}
	h2 := &collectingHandler{}
	if _, err := conn.FetchStream(context.Background(), ds, rewrittenCursor, h2); err != nil {
		t.Fatalf("FetchStream after history rewrite: %v", err)
	}
	// Falls back to a full re-stream of the current tree.
	if len(h2.items) != 1 || !strings.HasSuffix(h2.items[0].ExternalID, "docs/a.md") {
		t.Fatalf("history rewrite fallback items = %+v, want docs/a.md re-emitted", h2.items)
	}
}

func TestConnectorFetchStreamPathsScoping(t *testing.T) {
	withTempStorage(t)
	tr := setupTestRepo(t, map[string]string{
		"docs/a.md": "# A\n",
		"blog/b.md": "# B\n",
		"README.md": "# readme\n",
	})
	conn := NewConnector()
	ds := testConfig(tr.bareDir, tr.branch)
	ds.Settings["repos"] = []interface{}{map[string]interface{}{
		"repo_url": tr.bareDir, "branch": tr.branch,
		"paths": []interface{}{"docs"},
	}}

	h := &collectingHandler{}
	if _, err := conn.FetchStream(context.Background(), ds, nil, h); err != nil {
		t.Fatalf("FetchStream: %v", err)
	}
	if len(h.items) != 1 || !strings.HasSuffix(h.items[0].ExternalID, "docs/a.md") {
		t.Fatalf("paths-scoped items = %+v, want only docs/a.md", h.items)
	}
}

func TestConnectorFetchAllDelegatesToStream(t *testing.T) {
	withTempStorage(t)
	tr := setupTestRepo(t, map[string]string{"docs/a.md": "# A\n"})
	conn := NewConnector()
	ds := testConfig(tr.bareDir, tr.branch)

	items, err := conn.FetchAll(context.Background(), ds, nil)
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("FetchAll items = %d, want 1", len(items))
	}
}

var _ datasource.StreamHandler = (*collectingHandler)(nil)
