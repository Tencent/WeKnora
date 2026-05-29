package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

// inspectByQueryResponse parses an _update_by_query / _delete_by_query
// response and surfaces partial failures without leaking cluster-side reason
// strings (which may embed document content). Mirrors inspectBulkResponse:
// the full reason goes to the debug log only; the returned error carries the
// bounded id + type. A non-zero version_conflicts count with no hard failures
// is logged as a warning but not treated as an error.
func inspectByQueryResponse(body io.Reader) error {
	var r struct {
		VersionConflicts int `json:"version_conflicts"`
		Failures         []struct {
			ID    string `json:"id"`
			Cause struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"cause"`
		} `json:"failures"`
	}
	if err := json.NewDecoder(body).Decode(&r); err != nil {
		return fmt.Errorf("opensearch: parse by-query response: %w", ErrTransport)
	}
	log := logger.GetLogger(context.Background())
	if len(r.Failures) == 0 {
		if r.VersionConflicts > 0 {
			log.Warnf("[OpenSearch] by-query had %d version conflicts (proceeded)", r.VersionConflicts)
		}
		return nil
	}
	var msgs []string
	for _, f := range r.Failures {
		// Full reason → debug log only (may contain document content).
		log.Debugf("[OpenSearch] by-query failure: id=%s type=%s reason=%s",
			f.ID, f.Cause.Type, f.Cause.Reason)
		if len(msgs) < 5 {
			msgs = append(msgs, fmt.Sprintf("[%s] %s", f.ID, f.Cause.Type))
		}
	}
	return fmt.Errorf("opensearch: by-query partial failure (%d failed, first 5: %s): %w",
		len(r.Failures), strings.Join(msgs, "; "), ErrTransport)
}
