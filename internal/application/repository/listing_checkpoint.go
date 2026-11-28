package repository

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// ListingCheckpointRepository stores resumable directory pages for sync jobs.
type ListingCheckpointRepository struct{ db *gorm.DB }

// NewListingCheckpointRepository connects checkpoint storage to the application database.
func NewListingCheckpointRepository(db *gorm.DB) *ListingCheckpointRepository {
	return &ListingCheckpointRepository{db}
}

// ForJob scopes reads and writes to the job and its current configuration. Neither
// credentials nor configuration are persisted by the checkpoint store.
func (r *ListingCheckpointRepository) ForJob(ds *types.DataSource, job string) *JobListingPages {
	return &JobListingPages{
		db: r.db, job: job, tenant: ds.TenantID, source: ds.ID,
		selection: fmt.Sprintf("%x", sha256.Sum256([]byte(ds.KnowledgeBaseID+"\x00"+ds.Type+"\x00"+string(ds.Config)))),
	}
}

// JobListingPages isolates a single job and selection.
type JobListingPages struct {
	db                *gorm.DB
	job               string
	tenant            uint64
	source, selection string
}

// Load returns a verified page or nil when the page has not been saved.
func (s *JobListingPages) Load(ctx context.Context, key string) ([]byte, error) {
	var data []struct{ Payload, Checksum string }
	err := s.db.WithContext(ctx).Table("sync_listing_pages").
		Select("payload, checksum").Where("job_id=? AND tenant_id=? AND source_id=? AND selection=? AND page_key=?",
		s.job, s.tenant, s.source, s.selection, key).Limit(1).Find(&data).Error
	if err != nil || len(data) == 0 {
		return nil, err
	}
	raw := []byte(data[0].Payload)
	if fmt.Sprintf("%x", sha256.Sum256(raw)) != data[0].Checksum {
		return nil, fmt.Errorf("listing checkpoint checksum mismatch")
	}
	return raw, nil
}

// Save records a continuation page while the job remains active.
func (s *JobListingPages) Save(ctx context.Context, key string, data []byte) error {
	// Serialize against cancellation/completion so late workers cannot recreate cleaned pages.
	activeLog := "SELECT id FROM sync_logs WHERE id=? AND tenant_id=? AND data_source_id=? AND status IN ?"
	if s.db.Name() == "postgres" {
		activeLog += " FOR SHARE"
	}
	result := s.db.WithContext(ctx).Exec(`INSERT INTO sync_listing_pages
        (job_id,tenant_id,source_id,selection,page_key,payload,checksum)
        SELECT ?,?,?,?,?,?,? WHERE EXISTS (`+activeLog+`)
        ON CONFLICT (job_id,tenant_id,source_id,selection,page_key)
        DO UPDATE SET payload=excluded.payload, checksum=excluded.checksum`,
		s.job, s.tenant, s.source, s.selection, key, string(data), fmt.Sprintf("%x", sha256.Sum256(data)),
		s.job, s.tenant, s.source, []string{"pending", types.SyncLogStatusRunning, types.SyncLogStatusFailed})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("listing checkpoint job is no longer active")
	}
	return nil
}
