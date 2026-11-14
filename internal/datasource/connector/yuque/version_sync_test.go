package yuque

import "testing"

func TestFetchIncrementalDoesNotTreatMissingVersionsAsUnchanged(t *testing.T) {
	f := newFakeYuque()
	defer f.Close()
	f.handleJSON("/api/v2/repos/10/docs", 200, v2DocListResponse{Data: []v2Doc{
		{ID: 7, Title: "Unversioned", Slug: "note"},
	}})
	f.handleJSON("/api/v2/repos/docs/7", 200, v2DocDetailResponse{Data: v2DocDetail{
		ID: 7, Title: "Unversioned", Body: "new note contents",
	}})

	connector := NewConnector()
	cfg := makeDSConfig(f, []string{"10"})
	// The prior cursor was created before this note was listed.
	prior := &yuqueCursor{BookDocTimes: map[string]map[string]string{"10": {"8": "older"}}}
	items, cursor, err := connector.walk(t.Context(), cfg, []string{"10"}, prior, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) == 0 || items[0].ExternalID != "7" || string(items[0].Content) != "new note contents" {
		t.Fatalf("new unversioned note not imported: %+v", items)
	}

	items, _, err = connector.walk(t.Context(), cfg, []string{"10"}, cursor, true)
	if err != nil || len(items) != 1 || items[0].ExternalID != "7" {
		t.Fatalf("unversioned note not rechecked: items=%+v err=%v", items, err)
	}
}
