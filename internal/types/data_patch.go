package types

import "time"

// Data-patch identifiers recorded in data_patches after a one-shot repair.
const (
	DataPatchKnowledgeResourceBindingsV1 = "knowledge_resource_bindings_v1"
)

// DataPatch records that a one-shot data repair has finished. SQL schema
// migrations stay in golang-migrate; Go backfills that must share application
// scanners (for example ScanResourceReferences) write a row here so they do
// not rescan on every process start.
type DataPatch struct {
	ID        string    `json:"id" gorm:"type:varchar(64);primaryKey"`
	AppliedAt time.Time `json:"applied_at"`
}

// TableName returns the data-patch ledger table name.
func (DataPatch) TableName() string { return "data_patches" }
