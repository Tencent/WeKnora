package types

import "strings"

// ResolveKnowledgeRelativeReference resolves a document-relative reference
// against a knowledge folder. It deliberately has stricter semantics than
// NormalizeKnowledgeFolderPath: a parent segment that would leave the
// knowledge-base root makes the reference invalid instead of being discarded.
//
// The returned values map directly to knowledges.folder_path and
// knowledges.file_name. Non-file references (URLs, anchors, absolute paths and
// Windows paths) return ok=false and must be left unchanged by callers.
func ResolveKnowledgeRelativeReference(currentFolderPath, referencePath string) (folderPath, fileName string, ok bool) {
	ref := strings.TrimSpace(referencePath)
	if ref == "" || strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "#") ||
		strings.ContainsAny(ref, "\\\x00?#") || hasReferenceScheme(ref) {
		return "", "", false
	}

	segments := make([]string, 0, MaxKnowledgeFolderDepth+1)
	if currentFolderPath != "" {
		for _, segment := range strings.Split(currentFolderPath, "/") {
			if segment == "" || segment == "." || segment == ".." {
				return "", "", false
			}
			segments = append(segments, segment)
		}
	}

	for _, segment := range strings.Split(ref, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(segments) == 0 {
				return "", "", false
			}
			segments = segments[:len(segments)-1]
		default:
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return "", "", false
	}

	fileName = segments[len(segments)-1]
	if fileName == "" || fileName == "." || fileName == ".." {
		return "", "", false
	}
	return strings.Join(segments[:len(segments)-1], "/"), fileName, true
}

// hasReferenceScheme treats every URI scheme as non-relative. In particular it
// prevents file:, resource:, data: and drive-letter paths (C:\\...) from being
// interpreted as logical knowledge-base file names.
func hasReferenceScheme(value string) bool {
	colon := strings.IndexByte(value, ':')
	if colon <= 0 {
		return false
	}
	for i, r := range value[:colon] {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (i > 0 && r >= '0' && r <= '9') || r == '+' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
