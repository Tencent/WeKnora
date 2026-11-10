package seafile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestSyncScale(t *testing.T) {
	f, c, cfg := syncFixture(t, "/")
	dirTarget := 300
	if os.Getenv("SEAFILE_SCALE_TEST") == "1" {
		dirTarget = 3000
	}
	// Each group has one level-one dir, one level-two dir and eight
	// level-three dirs. All 300 (or 3000) non-root dirs have ten files.
	groups := dirTarget / 10
	for g := 0; g < groups; g++ {
		top := fmt.Sprintf("/g%03d", g)
		mid := top + "/middle"
		dirs := []string{top, mid}
		for leaf := 0; leaf < 8; leaf++ {
			dirs = append(dirs, fmt.Sprintf("%s/leaf%02d", mid, leaf))
		}
		for _, dir := range dirs {
			addNumberedFiles(f, dir, 10)
		}
	}
	directoryCount := 0
	f.mu.Lock()
	for _, node := range f.tree[testRepoID] {
		if node.Type == "dir" {
			directoryCount++
		}
	}
	f.mu.Unlock()
	if directoryCount != dirTarget+1 {
		t.Fatalf("fixture has %d directories, want %d", directoryCount, dirTarget+1)
	}
	fileCount := dirTarget * 10
	full := compactRecorder()
	cur, err := run(t, c, cfg, nil, full, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.contentItems()) != fileCount || len(full.items) != fileCount || len(filesOf(cur)) != fileCount {
		t.Fatalf("full items=%d, total=%d, cursor=%d", len(full.contentItems()), len(full.items), len(filesOf(cur)))
	}
	h := compactRecorder()
	retain := h.onCheckpoint
	h.onCheckpoint = func(cp map[string]any) {
		retain(cp)
		if len(filesOf(&types.SyncCursor{ConnectorCursor: cp})) >= fileCount/2 {
			h.cancel()
		}
	}
	_, err = run(t, c, cfg, cur, h, true)
	if !errors.Is(err, context.Canceled) || h.checkpointsAfterStop != 0 {
		t.Fatalf("midpoint cancel = %v, late checkpoints=%d", err, h.checkpointsAfterStop)
	}
	saved := checkpointCursor(t, h)
	savedFiles := filesOf(saved)
	// Checkpoints land every 50 mutations, and the full-sync switch is one of
	// them, so the saved batch ends within one batch of the midpoint.
	if len(savedFiles) < fileCount/2 || len(savedFiles) >= fileCount/2+checkpointEvery ||
		!mustCursor(t, saved).FullSync {
		t.Fatalf("midpoint saved %d/%d files", len(savedFiles), fileCount)
	}
	f.resetCounts()
	resumed := compactRecorder()
	cur, err = run(t, c, cfg, saved, resumed, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.contentItems()) != fileCount-len(savedFiles) || len(filesOf(cur)) != fileCount ||
		len(resumed.deletedItems()) != 0 || len(resumed.errorItems()) != 0 {
		t.Fatalf("resume content=%d, cursor=%d, deleted=%d, errors=%d",
			len(resumed.contentItems()), len(filesOf(cur)), len(resumed.deletedItems()), len(resumed.errorItems()))
	}
	for p := range savedFiles {
		if f.count("download:"+p) != 0 {
			t.Fatalf("resume downloaded saved file %s", p)
		}
	}
	f.resetCounts()
	cur, unchanged := mustRun(t, c, cfg, cur, false)
	if len(unchanged.items) != 0 || f.countPrefix("download:") != 0 ||
		f.countPrefix("dir:") != directoryCount || f.count("repos") != 1 || len(unchanged.checkpoints) > 1 {
		t.Fatalf("unchanged: items=%d, downloads=%d, dirs=%d/%d, repos=%d, checkpoints=%d",
			len(unchanged.items), f.countPrefix("download:"), f.countPrefix("dir:"), directoryCount,
			f.count("repos"), len(unchanged.checkpoints))
	}
	if os.Getenv("SEAFILE_SCALE_TEST") == "1" {
		data, err := json.Marshal(cur.ConnectorCursor)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("files=%d cursor_bytes=%d checkpoints: full=%d interrupted=%d resumed=%d unchanged=%d",
			fileCount, len(data), full.checkpointCalls, h.checkpointCalls, resumed.checkpointCalls,
			unchanged.checkpointCalls)
	}
}
