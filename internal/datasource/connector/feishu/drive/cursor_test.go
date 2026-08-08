package drive

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
)

// TestDriveCursorRoundTrip verifies driveOps.EncodeCursor/DecodeCursorTimes
// survive a JSON marshal/unmarshal cycle (snapshot isolation), mirroring the
// wiki TestFeishuCursorRoundTrip. Design §3.2: driveOps is new code that
// previously had no direct test.
func TestDriveCursorRoundTrip(t *testing.T) {
	ops := driveOps{region: core.RegionFeishuDrive}
	times := map[string]map[string]string{
		"folder1":       {"fdoc1": "100", "fdoc2": "200"},
		"folder1:fdoc1": {"fdoc1": "100"},
		"folder2:subA":  {"fdoc3": "300"},
	}
	cur := ops.EncodeCursor(times, time.Unix(1000, 0))
	if cur == nil {
		t.Fatal("EncodeCursor returned nil")
	}

	got := ops.DecodeCursorTimes(cur.ConnectorCursor)
	if len(got) != len(times) {
		t.Fatalf("decoded %d resources, want %d", len(got), len(times))
	}
	for rid, files := range times {
		gotFiles, ok := got[rid]
		if !ok {
			t.Errorf("resource %q missing from decoded cursor", rid)
			continue
		}
		for tok, mt := range files {
			if gotFiles[tok] != mt {
				t.Errorf("decoded[%q][%q] = %q, want %q", rid, tok, gotFiles[tok], mt)
			}
		}
	}
}
