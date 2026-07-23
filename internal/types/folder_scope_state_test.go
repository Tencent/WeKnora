package types

import "testing"

func TestFolderScopesRoundTripAndReadLegacyState(t *testing.T) {
	ctx := MessageExecutionContext{FolderScopes: []FolderScope{{
		KnowledgeBaseID: "kb-1", FolderIDs: []string{"folder-a", "folder-b"},
	}}}
	value, err := ctx.Value()
	if err != nil {
		t.Fatal(err)
	}
	var decoded MessageExecutionContext
	if err := decoded.Scan(value); err != nil {
		t.Fatal(err)
	}
	if got := decoded.FolderScopes[0].FolderIDs; len(got) != 2 || got[1] != "folder-b" {
		t.Fatalf("round-trip folder IDs = %#v", got)
	}

	legacy := []byte(`{"folder_scopes":[{"knowledge_base_id":"kb-1","folder_id":"legacy-folder"}]}`)
	var message MessageExecutionContext
	if err := message.Scan(legacy); err != nil {
		t.Fatal(err)
	}
	var session SessionLastRequestState
	if err := session.Scan(legacy); err != nil {
		t.Fatal(err)
	}
	for _, scopes := range [][]FolderScope{message.FolderScopes, session.FolderScopes} {
		if len(scopes) != 1 || scopes[0].FolderID == nil || *scopes[0].FolderID != "legacy-folder" {
			t.Fatalf("legacy scopes = %#v", scopes)
		}
	}
}
