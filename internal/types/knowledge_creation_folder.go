package types

import (
	"fmt"
	"strings"
	"unicode"
)

// ValidateKnowledgeRelativeFolder validates before applying the existing canonicalizer.
func ValidateKnowledgeRelativeFolder(raw string) (string, error) {
	if len(raw) > MaxKnowledgeFolderPathLength {
		return "", fmt.Errorf("folder_path is too long")
	}
	if raw != "" && len(strings.Split(raw, "/")) > MaxKnowledgeFolderDepth {
		return "", fmt.Errorf("folder_path is too deep")
	}
	if strings.HasPrefix(strings.TrimSpace(raw), "/") || strings.ContainsAny(raw, "\\:") ||
		strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", fmt.Errorf("invalid relative folder_path")
	}
	for _, segment := range strings.Split(raw, "/") {
		if len(segment) > MaxKnowledgeFolderSegmentLength {
			return "", fmt.Errorf("folder segment is too long")
		}
		if strings.TrimSpace(segment) == ".." {
			return "", fmt.Errorf("folder_path must not traverse parents")
		}
	}
	return NormalizeKnowledgeFolderPath(raw), nil
}

// KnowledgeCreationOptions carries optional creation-only metadata.
// Existing callers may omit it to retain root-directory behavior.
type KnowledgeCreationOptions struct{ FolderPath string }

// CreationFolder validates the destination before any creation side effects.
func CreationFolder(options ...KnowledgeCreationOptions) (string, error) {
	if len(options) > 1 {
		return "", fmt.Errorf("at most one creation options value is supported")
	}
	if len(options) == 0 {
		return "", nil
	}
	return ValidateKnowledgeRelativeFolder(options[0].FolderPath)
}
