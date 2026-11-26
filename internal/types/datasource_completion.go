package types

import (
	"bytes"
	"encoding/json"
	"time"
)

const pendingSyncLogKey = "_pending_sync_log"

// PendingSyncLog identifies a completed run whose cursor and summary are in
// data_sources but whose terminal sync_logs write needs to be retried.
// It is stored alongside the regular LastSyncResult fields, not in log results.
type PendingSyncLog struct {
	ID                 string    `json:"id"`
	Status             string    `json:"status"`
	FinishedAt         time.Time `json:"finished_at"`
	ErrorMessage       string    `json:"error_message,omitempty"`
	ResyncConfigDigest string    `json:"resync_config_digest,omitempty"`
}

type syncResultWithPendingLog struct {
	SyncResult
	PendingLog *PendingSyncLog `json:"_pending_sync_log"`
}

// WithPendingLog encodes the result with recovery metadata for a log write that failed.
func (r *SyncResult) WithPendingLog(log *SyncLog, resyncDigest string) (JSON, error) {
	return json.Marshal(syncResultWithPendingLog{SyncResult: *r, PendingLog: &PendingSyncLog{
		ID:                 log.ID,
		Status:             log.Status,
		FinishedAt:         *log.FinishedAt,
		ErrorMessage:       log.ErrorMessage,
		ResyncConfigDigest: resyncDigest,
	}})
}

// PendingLogResult returns a completion snapshot only for the given run.
func (d *DataSource) PendingLogResult(log *SyncLog) (*PendingSyncLog, *SyncResult) {
	if d.ID != log.DataSourceID || d.TenantID != log.TenantID ||
		d.LastSyncAt == nil || d.LastSyncAt.Before(log.StartedAt) ||
		!bytes.Contains(d.LastSyncResult, []byte(`"`+pendingSyncLogKey+`"`)) {
		return nil, nil
	}
	var stored syncResultWithPendingLog
	if err := json.Unmarshal(d.LastSyncResult, &stored); err != nil || stored.PendingLog == nil {
		return nil, nil
	}
	pending := stored.PendingLog
	if pending.ID != log.ID || pending.FinishedAt.Before(log.StartedAt) ||
		(pending.Status != SyncLogStatusSuccess && pending.Status != SyncLogStatusPartial) {
		return nil, nil
	}
	return pending, &stored.SyncResult
}

// PublicSyncResult omits the private completion snapshot while retaining the
// existing summary shape (and any other fields persisted by another version).
func (d *DataSource) PublicSyncResult() JSON {
	if !bytes.Contains(d.LastSyncResult, []byte(`"`+pendingSyncLogKey+`"`)) {
		return d.LastSyncResult
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(d.LastSyncResult, &fields); err != nil {
		return d.LastSyncResult
	}
	delete(fields, pendingSyncLogKey)
	public, err := json.Marshal(fields)
	if err != nil {
		return d.LastSyncResult
	}
	return public
}
