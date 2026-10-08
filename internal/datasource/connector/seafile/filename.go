package seafile

import (
	"fmt"
	"path"
	"strings"
	"unicode"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// knowledgeFileName maps a library file to the knowledge base folder
// convention "<library name>/<path inside the library>". Every segment is
// sanitized on its own; depth and length limits are applied by the service.
func knowledgeFileName(repoName, filePath string) string {
	segments := strings.Split(strings.TrimPrefix(filePath, "/"), "/")
	last := len(segments) - 1
	for i := 0; i < last; i++ {
		segments[i] = datasource.SanitizeFileName(segments[i])
	}
	ext := path.Ext(segments[last])
	segments[last] = datasource.SanitizeFileName(strings.TrimSuffix(segments[last], ext)) + ext
	return datasource.SanitizeFileName(repoName) + "/" + strings.Join(segments, "/")
}

func externalID(repoID, filePath string) string {
	return "seafile:" + repoID + ":" + filePath
}

// fingerprint identifies a file version; three equal parts mean unchanged.
func fingerprint(oid string, mtime, size int64) string {
	return fmt.Sprintf("o:%s|m:%d|s:%d", oid, mtime, size)
}

// validEntryName accepts a single non-empty path segment.
func validEntryName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, "/\\") && !strings.ContainsFunc(name, unicode.IsControl)
}
