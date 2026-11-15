package types

import "testing"

func TestPendingRetractionsPreserveUnrelatedMetadata(t *testing.T) {
	page := &WikiPage{PageMetadata: JSON(`{"tags":["history"]}`)}
	for _, id := range []string{"old-a", "old-b", "old-a"} {
		if err := page.SetPendingRetraction(id, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := page.SetPendingRetraction("old-a", false); err != nil {
		t.Fatal(err)
	}
	data, _ := page.PageMetadata.Map()
	if len(data["pending_retractions"].([]interface{})) != 1 || data["tags"] == nil {
		t.Fatalf("metadata lost: %s", page.PageMetadata)
	}
	if err := page.SetPendingRetraction("old-b", false); err != nil {
		t.Fatal(err)
	}
	data, _ = page.PageMetadata.Map()
	if _, pending := data["pending_retractions"]; pending {
		t.Fatal("completed retraction still blocks page")
	}
}

func TestPendingRetractionsAcceptNullablePageMetadata(t *testing.T) {
	for _, metadata := range []JSON{nil, JSON("null"), JSON("{}")} {
		page := &WikiPage{PageMetadata: metadata}
		if err := page.SetPendingRetraction("old", true); err != nil {
			t.Fatal(err)
		}
		value, err := page.PageMetadata.Map()
		if err != nil || len(value["pending_retractions"].([]interface{})) != 1 {
			t.Fatalf("pending marker not stored: %s %v", page.PageMetadata, err)
		}
	}
}
