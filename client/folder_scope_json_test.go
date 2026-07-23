package client

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFolderScopeSDKJSONShapes(t *testing.T) {
	kbID := "11111111-1111-1111-1111-111111111111"
	folderID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	multi, err := json.Marshal(KnowledgeQARequest{
		Query: "q", KnowledgeBaseIDs: []string{kbID},
		FolderScopes: []FolderScope{{KnowledgeBaseID: kbID, FolderIDs: []string{folderID, "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(multi), `"folder_ids":["`+folderID) || strings.Contains(string(multi), `"folder_id"`) {
		t.Fatalf("unexpected multi-folder payload: %s", multi)
	}

	legacy, err := json.Marshal(SearchKnowledgeRequest{
		Query: "q", KnowledgeBaseIDs: []string{kbID},
		FolderScopes: []FolderScope{{KnowledgeBaseID: kbID, FolderID: &folderID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(legacy), `"folder_id":"`+folderID+`"`) {
		t.Fatalf("legacy folder_id missing: %s", legacy)
	}

	omitted, err := json.Marshal(AgentQARequest{Query: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(omitted), "folder_scopes") {
		t.Fatalf("empty scopes must be omitted: %s", omitted)
	}
}
