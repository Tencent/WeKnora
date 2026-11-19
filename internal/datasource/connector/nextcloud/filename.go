package nextcloud

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
)

func knowledgeFileName(p string) string {
	segments := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, segment := range segments {
		if i == len(segments)-1 {
			ext := path.Ext(segment)
			segments[i] = datasource.SanitizeFileName(strings.TrimSuffix(segment, ext)) + ext
		} else {
			segments[i] = datasource.SanitizeFileName(segment)
		}
	}
	return "Nextcloud/" + strings.Join(segments, "/")
}

func fingerprint(e entry) string {
	// Structured encoding prevents separator collisions in ETags and file IDs.
	raw, _ := json.Marshal([]any{e.FileID, e.ETag, e.ModifiedAt, e.Size})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func externalID(scope, p string) string { return "nextcloud:" + scope + ":" + p }
