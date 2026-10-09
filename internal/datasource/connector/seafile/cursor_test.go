package seafile

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestCursorRoundTrip(t *testing.T) {
	c, _, err := prepareCursor(nil, testRepoID, false)
	if err != nil {
		t.Fatal(err)
	}
	c.RepoName = "原资料库"
	c.Files = map[string]string{"/ok.pdf": "o:abc|m:1|s:3", "/retry.pdf": ""}
	c.FullSync = true
	c.FullSyncBaseline = map[string]string{"/gone.pdf": "old", "/pending.pdf": ""}
	data, err := json.Marshal(c.toSyncCursor())
	if err != nil {
		t.Fatal(err)
	}
	var wire types.SyncCursor
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "repo_id", "repo_name", "files", "full_sync", "full_sync_baseline"} {
		if _, ok := wire.ConnectorCursor[key]; !ok {
			t.Errorf("cursor missing JSON key %q", key)
		}
	}
	got, err := decodeCursor(&wire)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatalf("round trip = %+v, %v; want %+v", got, err, c)
	}
}

func TestCursorEmptyAndUnknownVersion(t *testing.T) {
	got, err := decodeCursor(nil)
	if err != nil || got.Files == nil || len(got.Files) != 0 || len(got.FullSyncBaseline) != 0 {
		t.Fatalf("nil cursor = %+v, %v", got, err)
	}
	old := &types.SyncCursor{ConnectorCursor: map[string]any{"schema_version": 999999}}
	if _, err := decodeCursor(old); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("unknown schema = %v", err)
	}
	if _, _, err := prepareCursor(old, testRepoID, false); !errors.Is(err, datasource.ErrInvalidConfig) {
		t.Fatalf("prepare unknown schema = %v", err)
	}
	// A non-empty cursor from another connector or a truncated write carries
	// no usable version and must not be reinterpreted as Seafile state.
	for name, foreign := range map[string]map[string]any{
		"missing version": {"files": map[string]any{"/a.pdf": "x"}},
		"null version":    {"schema_version": nil, "repo_id": testRepoID},
		"zero version":    {"schema_version": 0, "repo_id": testRepoID},
	} {
		_, err := decodeCursor(&types.SyncCursor{ConnectorCursor: foreign})
		if !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("%s = %v", name, err)
		}
	}
}

func TestPrepareCursorFullSync(t *testing.T) {
	old := seededCursor(t, "Library", map[string]string{"/a.pdf": "a", "/pending.pdf": ""})
	before := cursorJSON(t, old)
	c, changed, err := prepareCursor(old, testRepoID, true)
	if err != nil || !changed || !c.FullSync || len(c.Files) != 0 ||
		!reflect.DeepEqual(c.FullSyncBaseline, filesOf(old)) {
		t.Fatalf("prepare full = %+v, changed=%v, err=%v", c, changed, err)
	}
	c.FullSyncBaseline["/a.pdf"] = "mutated"
	c.Files["/new.pdf"] = "new"
	if cursorJSON(t, old) != before {
		t.Fatal("full cursor aliases the old cursor")
	}
	c.FullSyncBaseline["/a.pdf"] = "a"
	active := c.toSyncCursor()
	before = cursorJSON(t, active)
	for _, force := range []bool{false, true} {
		resumed, _, err := prepareCursor(active, testRepoID, force)
		if err != nil || !resumed.FullSync || !reflect.DeepEqual(resumed.Files, c.Files) ||
			!reflect.DeepEqual(resumed.FullSyncBaseline, c.FullSyncBaseline) {
			t.Fatalf("resume force=%v: %+v, %v", force, resumed, err)
		}
	}
	if cursorJSON(t, active) != before {
		t.Fatal("prepare mutated an active full-sync cursor")
	}
}

func TestCursorRepositoryMismatchBeforeHTTP(t *testing.T) {
	old := seededCursor(t, "原资料库", map[string]string{"/a.pdf": "saved"})
	before := cursorJSON(t, old)
	_, _, err := prepareCursor(old, otherRepoID, false)
	if !errors.Is(err, datasource.ErrInvalidConfig) || !strings.Contains(err.Error(), "原资料库") {
		t.Fatalf("prepare mismatch = %v", err)
	}
	f := newFakeSeafile(t)
	cfg := syncConfig(f)
	cfg.ResourceIDs = []string{otherRepoID + ":/"}
	h := &recorder{}
	_, err = run(t, NewConnector(), cfg, old, h, false)
	if !errors.Is(err, datasource.ErrInvalidConfig) || !strings.Contains(err.Error(), "原资料库") {
		t.Fatalf("stream mismatch = %v", err)
	}
	if len(f.counts()) != 0 || len(h.items) != 0 || len(h.checkpoints) != 0 || cursorJSON(t, old) != before {
		t.Fatalf("mismatch had side effects: requests=%v, handler=%+v", f.counts(), h)
	}
}
