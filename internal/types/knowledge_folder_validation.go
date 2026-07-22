package types

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// ValidateKnowledgeFolderStructure validates one persisted folder row and returns its path IDs.
func ValidateKnowledgeFolderStructure(folder *KnowledgeFolder) ([]string, error) {
	if folder == nil {
		return nil, fmt.Errorf("folder is nil")
	}
	if folder.TenantID == 0 {
		return nil, fmt.Errorf("tenant id is empty")
	}
	if folder.KnowledgeBaseID == "" ||
		strings.TrimSpace(folder.KnowledgeBaseID) != folder.KnowledgeBaseID {
		return nil, fmt.Errorf("knowledge base id is invalid")
	}
	if !isCanonicalKnowledgeFolderID(folder.ID) {
		return nil, fmt.Errorf("id is not a canonical UUID")
	}
	if folder.ParentID != KnowledgeFolderRootID &&
		!isCanonicalKnowledgeFolderID(folder.ParentID) {
		return nil, fmt.Errorf("parent id is not a canonical UUID")
	}
	if !utf8.ValidString(folder.Name) {
		return nil, fmt.Errorf("name is not valid UTF-8")
	}
	if strings.TrimSpace(folder.Name) == "" {
		return nil, fmt.Errorf("name is empty")
	}
	if utf8.RuneCountInString(folder.Name) > KnowledgeFolderMaxNameRunes {
		return nil, fmt.Errorf("name exceeds %d Unicode code points", KnowledgeFolderMaxNameRunes)
	}
	if folder.Depth < 1 || folder.Depth > KnowledgeFolderMaxDepth {
		return nil, fmt.Errorf(
			"depth must be between 1 and %d",
			KnowledgeFolderMaxDepth,
		)
	}
	if !strings.HasPrefix(folder.Path, "/") ||
		!strings.HasSuffix(folder.Path, "/") {
		return nil, fmt.Errorf("path must start and end with a slash")
	}

	innerPath := strings.TrimSuffix(strings.TrimPrefix(folder.Path, "/"), "/")
	if innerPath == "" {
		return nil, fmt.Errorf("path is empty")
	}
	pathIDs := strings.Split(innerPath, "/")
	for _, pathID := range pathIDs {
		if !isCanonicalKnowledgeFolderID(pathID) {
			return nil, fmt.Errorf("path contains a non-canonical folder id")
		}
	}
	if len(pathIDs) != folder.Depth {
		return nil, fmt.Errorf("path depth does not match folder depth")
	}
	if pathIDs[len(pathIDs)-1] != folder.ID {
		return nil, fmt.Errorf("path does not end with folder id")
	}

	expectedParentID := KnowledgeFolderRootID
	if len(pathIDs) > 1 {
		expectedParentID = pathIDs[len(pathIDs)-2]
	}
	if folder.ParentID != expectedParentID {
		return nil, fmt.Errorf("path parent does not match parent id")
	}
	if folder.Depth == 1 &&
		(folder.ParentID != KnowledgeFolderRootID || folder.Path != "/"+folder.ID+"/") {
		return nil, fmt.Errorf("first-level folder structure is invalid")
	}
	return pathIDs, nil
}

func isCanonicalKnowledgeFolderID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}
