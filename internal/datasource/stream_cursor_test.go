package datasource

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

type testScheduledFullSyncResumer struct {
	resume bool
}

func (r testScheduledFullSyncResumer) ShouldResumeScheduledFullSync(*types.SyncCursor) bool {
	return r.resume
}

func testConnectorCursor(t *testing.T) types.JSON {
	t.Helper()
	encoded, err := json.Marshal(&types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"query": "older",
	}})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return types.JSON(encoded)
}

func TestStreamStartCursorDropsFreshFullAndResumesRetry(t *testing.T) {
	ds := &types.DataSource{LastSyncCursor: testConnectorCursor(t)}

	fresh, err := StreamStartCursor(ds, true, 0, nil)
	if err != nil || fresh != nil {
		t.Fatalf("fresh cursor = %#v, error = %v", fresh, err)
	}
	retry, err := StreamStartCursor(ds, true, 1, nil)
	if err != nil || retry == nil || retry.ConnectorCursor["query"] != "older" {
		t.Fatalf("retry cursor = %#v, error = %v", retry, err)
	}
}

func TestStreamStartCursorKeepsIncrementalCursor(t *testing.T) {
	ds := &types.DataSource{LastSyncCursor: testConnectorCursor(t)}
	cursor, err := StreamStartCursor(ds, false, 0, nil)
	if err != nil || cursor == nil || cursor.ConnectorCursor["query"] != "older" {
		t.Fatalf("cursor = %#v, error = %v", cursor, err)
	}
}

func TestStreamStartCursorResumesOnlyUnfinishedScheduledFull(t *testing.T) {
	ds := &types.DataSource{LastSyncCursor: testConnectorCursor(t)}

	resumed, err := StreamStartCursor(ds, true, 0, testScheduledFullSyncResumer{resume: true})
	if err != nil || resumed == nil {
		t.Fatalf("resumed cursor = %#v, error = %v", resumed, err)
	}
	restarted, err := StreamStartCursor(ds, true, 0, testScheduledFullSyncResumer{resume: false})
	if err != nil || restarted != nil {
		t.Fatalf("restarted cursor = %#v, error = %v", restarted, err)
	}
}
