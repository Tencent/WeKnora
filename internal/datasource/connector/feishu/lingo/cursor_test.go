package lingo

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestCursorRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	original := &lingoCursor{
		LastSyncTime: now,
		EntityTimes:  map[string]string{"e1": "100", "e2": "200"},
		EntityRepos:  map[string]string{"e1": "repo-1", "e2": "repo-2"},
	}
	encoded := original.encode()
	if encoded == nil {
		t.Fatal("encode() returned nil")
	}
	decoded := decodeLingoCursor(encoded)
	if decoded == nil {
		t.Fatal("decodeLingoCursor() returned nil")
	}
	if !decoded.LastSyncTime.Equal(now) {
		t.Fatalf("LastSyncTime = %v, want %v", decoded.LastSyncTime, now)
	}
	if decoded.EntityTimes["e1"] != "100" || decoded.EntityTimes["e2"] != "200" {
		t.Fatalf("EntityTimes = %#v", decoded.EntityTimes)
	}
	if decoded.EntityRepos["e1"] != "repo-1" {
		t.Fatalf("EntityRepos = %#v", decoded.EntityRepos)
	}
}

func TestDecodeCursorRejectsEmptyState(t *testing.T) {
	if got := decodeLingoCursor(nil); got != nil {
		t.Fatalf("nil cursor decoded to %#v", got)
	}
	if got := decodeLingoCursor(&types.SyncCursor{}); got != nil {
		t.Fatalf("empty cursor decoded to %#v", got)
	}
	if got := decodeLingoCursor(&types.SyncCursor{
		ConnectorCursor: map[string]interface{}{"entity_times": map[string]interface{}{}},
	}); got != nil {
		t.Fatalf("cursor without entity times decoded to %#v", got)
	}
}
