package repository

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// ListReparseBatchViews returns a page plus aggregate state in two bounded
// GROUP BY queries, independent of the number of frozen members.
func (r *reparseBatchStore) ListReparseBatchViews(
	ctx context.Context, tenant uint64, kb string, offset, limit int,
) ([]*types.ReparseBatchView, int64, error) {
	batches, total, err := r.ListReparseBatches(ctx, tenant, kb, offset, limit)
	if err != nil || len(batches) == 0 {
		return nil, total, err
	}
	out, err := r.reparseBatchViewsFor(ctx, batches)
	return out, total, err
}

// ListReparseBatchViewsByIDs aggregates an already-authorized selected page in
// the same fixed number of queries as the KB-filtered list.
func (r *reparseBatchStore) ListReparseBatchViewsByIDs(
	ctx context.Context, tenant uint64, ids []string,
) ([]*types.ReparseBatchView, error) {
	if len(ids) == 0 {
		return []*types.ReparseBatchView{}, nil
	}
	var batches []*types.ReparseBatch
	if err := r.db.WithContext(ctx).
		Where("tenant_id = ? AND id IN (?)", tenant, ids).
		Order("created_at DESC").Find(&batches).Error; err != nil {
		return nil, err
	}
	return r.reparseBatchViewsFor(ctx, batches)
}

func (r *reparseBatchStore) reparseBatchViewsFor(
	ctx context.Context, batches []*types.ReparseBatch,
) ([]*types.ReparseBatchView, error) {
	if len(batches) == 0 {
		return []*types.ReparseBatchView{}, nil
	}
	ids := make([]string, 0, len(batches))
	views := make(map[string]*types.ReparseBatchView, len(batches))
	for _, b := range batches {
		ids = append(ids, b.ID)
		views[b.ID] = &types.ReparseBatchView{ReparseBatch: b}
	}
	state := reparseMemberStateSQL()
	q := fmt.Sprintf(`SELECT b.id AS batch_id, COUNT(*) AS total,
 SUM(CASE WHEN state='waiting_start' THEN 1 ELSE 0 END) AS waiting_start,
 SUM(CASE WHEN state='queued' THEN 1 ELSE 0 END) AS queued,
 SUM(CASE WHEN state='processing' THEN 1 ELSE 0 END) AS processing,
 SUM(CASE WHEN state='finalizing' THEN 1 ELSE 0 END) AS finalizing,
 SUM(CASE WHEN state='completed' THEN 1 ELSE 0 END) AS completed,
 SUM(CASE WHEN state='failed' THEN 1 ELSE 0 END) AS failed,
 SUM(CASE WHEN state='cancelled' THEN 1 ELSE 0 END) AS cancelled,
 SUM(CASE WHEN state='missing' THEN 1 ELSE 0 END) AS missing,
 SUM(CASE WHEN state='submission_failed' THEN 1 ELSE 0 END) AS submission_failed,
 SUM(CASE WHEN state='unknown' THEN 1 ELSE 0 END) AS unknown
 FROM (SELECT b.id, %s AS state
  FROM reparse_batches b
  JOIN reparse_batch_members m ON m.batch_id=b.id
  LEFT JOIN knowledges k ON k.id=m.knowledge_id AND k.tenant_id=b.tenant_id
   AND k.knowledge_base_id=b.knowledge_base_id AND k.deleted_at IS NULL
  WHERE b.id IN (?)) b
 GROUP BY b.id`, state)
	rows := []struct {
		BatchID          string
		Total            int64
		WaitingStart     int64
		Queued           int64
		Processing       int64
		Finalizing       int64
		Completed        int64
		Failed           int64
		Cancelled        int64
		Missing          int64
		SubmissionFailed int64
		Unknown          int64
	}{}
	if err := r.db.WithContext(ctx).Raw(q, ids).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, x := range rows {
		v := views[x.BatchID]
		v.Total = x.Total
		v.Counts = types.ReparseBatchCounts{
			WaitingStart: x.WaitingStart, Queued: x.Queued, Processing: x.Processing,
			Finalizing: x.Finalizing, Completed: x.Completed, Failed: x.Failed,
			Cancelled: x.Cancelled, Missing: x.Missing, SubmissionFailed: x.SubmissionFailed,
			Unknown: x.Unknown,
		}
	}
	sq := `SELECT b.id AS batch_id,
 SUM(CASE WHEN k.summary_status='none' THEN 1 ELSE 0 END) AS none,
 SUM(CASE WHEN k.summary_status='pending' THEN 1 ELSE 0 END) AS pending,
 SUM(CASE WHEN k.summary_status='processing' THEN 1 ELSE 0 END) AS processing,
 SUM(CASE WHEN k.summary_status='completed' THEN 1 ELSE 0 END) AS completed,
 SUM(CASE WHEN k.summary_status='failed' THEN 1 ELSE 0 END) AS failed,
 SUM(CASE WHEN k.id IS NULL OR k.summary_status IS NULL OR k.summary_status=''
   OR k.summary_status NOT IN ('none','pending','processing','completed','failed')
  THEN 1 ELSE 0 END) AS unknown
 FROM reparse_batches b
 JOIN reparse_batch_members m ON m.batch_id=b.id
 LEFT JOIN knowledges k ON k.id=m.knowledge_id AND k.tenant_id=b.tenant_id
  AND k.knowledge_base_id=b.knowledge_base_id AND k.deleted_at IS NULL
 WHERE b.id IN (?)
 GROUP BY b.id`
	srows := []struct {
		BatchID    string
		None       int64
		Pending    int64
		Processing int64
		Completed  int64
		Failed     int64
		Unknown    int64
	}{}
	if err := r.db.WithContext(ctx).Raw(sq, ids).Scan(&srows).Error; err != nil {
		return nil, err
	}
	for _, x := range srows {
		views[x.BatchID].SummaryCounts = types.ReparseBatchSummaryCounts{
			None: x.None, Pending: x.Pending, Processing: x.Processing,
			Completed: x.Completed, Failed: x.Failed, Unknown: x.Unknown,
		}
	}
	out := make([]*types.ReparseBatchView, 0, len(batches))
	for _, b := range batches {
		out = append(out, views[b.ID])
	}
	return out, nil
}

// GetReparseBatchView calculates all display state from the current knowledge
// rows. It deliberately never reads Asynq/archive state.
func (r *reparseBatchStore) GetReparseBatchView(
	ctx context.Context, tenant uint64, id string,
) (*types.ReparseBatchView, error) {
	batch, err := r.GetReparseBatch(ctx, tenant, id)
	if err != nil {
		return nil, err
	}
	view := &types.ReparseBatchView{ReparseBatch: batch}
	state := reparseMemberStateSQL()
	return r.reparseBatchViewScan(ctx, view, id, state)
}

func (r *reparseBatchStore) reparseBatchViewScan(
	ctx context.Context, view *types.ReparseBatchView, id, state string,
) (*types.ReparseBatchView, error) {
	q := fmt.Sprintf(`SELECT COUNT(*) AS total,
 SUM(CASE WHEN state='waiting_start' THEN 1 ELSE 0 END) AS waiting_start,
 SUM(CASE WHEN state='queued' THEN 1 ELSE 0 END) AS queued,
 SUM(CASE WHEN state='processing' THEN 1 ELSE 0 END) AS processing,
 SUM(CASE WHEN state='finalizing' THEN 1 ELSE 0 END) AS finalizing,
 SUM(CASE WHEN state='completed' THEN 1 ELSE 0 END) AS completed,
 SUM(CASE WHEN state='failed' THEN 1 ELSE 0 END) AS failed,
 SUM(CASE WHEN state='cancelled' THEN 1 ELSE 0 END) AS cancelled,
 SUM(CASE WHEN state='missing' THEN 1 ELSE 0 END) AS missing,
 SUM(CASE WHEN state='submission_failed' THEN 1 ELSE 0 END) AS submission_failed,
 SUM(CASE WHEN state='unknown' THEN 1 ELSE 0 END) AS unknown
 FROM (SELECT %s AS state
  FROM reparse_batch_members m
  JOIN reparse_batches b ON b.id=m.batch_id
  LEFT JOIN knowledges k ON k.id=m.knowledge_id AND k.tenant_id=b.tenant_id
   AND k.knowledge_base_id=b.knowledge_base_id AND k.deleted_at IS NULL
  WHERE m.batch_id=?) x`, state)
	row := struct {
		Total            int64
		WaitingStart     int64
		Queued           int64
		Processing       int64
		Finalizing       int64
		Completed        int64
		Failed           int64
		Cancelled        int64
		Missing          int64
		SubmissionFailed int64
		Unknown          int64
	}{}
	if err := r.db.WithContext(ctx).Raw(q, id).Scan(&row).Error; err != nil {
		return nil, err
	}
	view.Total = row.Total
	view.Counts = types.ReparseBatchCounts{
		WaitingStart: row.WaitingStart, Queued: row.Queued, Processing: row.Processing,
		Finalizing: row.Finalizing, Completed: row.Completed, Failed: row.Failed,
		Cancelled: row.Cancelled, Missing: row.Missing, SubmissionFailed: row.SubmissionFailed,
		Unknown: row.Unknown,
	}
	summaryQ := `SELECT
 SUM(CASE WHEN k.summary_status='none' THEN 1 ELSE 0 END) AS none,
 SUM(CASE WHEN k.summary_status='pending' THEN 1 ELSE 0 END) AS pending,
 SUM(CASE WHEN k.summary_status='processing' THEN 1 ELSE 0 END) AS processing,
 SUM(CASE WHEN k.summary_status='completed' THEN 1 ELSE 0 END) AS completed,
 SUM(CASE WHEN k.summary_status='failed' THEN 1 ELSE 0 END) AS failed,
 SUM(CASE WHEN k.id IS NULL OR k.summary_status IS NULL OR k.summary_status=''
   OR k.summary_status NOT IN ('none','pending','processing','completed','failed')
  THEN 1 ELSE 0 END) AS unknown
 FROM reparse_batch_members m
 JOIN reparse_batches b ON b.id=m.batch_id
 LEFT JOIN knowledges k ON k.id=m.knowledge_id AND k.tenant_id=b.tenant_id
  AND k.knowledge_base_id=b.knowledge_base_id AND k.deleted_at IS NULL
 WHERE m.batch_id=?`
	summary := struct {
		None       int64
		Pending    int64
		Processing int64
		Completed  int64
		Failed     int64
		Unknown    int64
	}{}
	if err := r.db.WithContext(ctx).Raw(summaryQ, id).Scan(&summary).Error; err != nil {
		return nil, err
	}
	view.SummaryCounts = types.ReparseBatchSummaryCounts{
		None: summary.None, Pending: summary.Pending, Processing: summary.Processing,
		Completed: summary.Completed, Failed: summary.Failed, Unknown: summary.Unknown,
	}
	return view, nil
}

func (r *reparseBatchStore) ListReparseBatchMemberViews(
	ctx context.Context, tenant uint64, id, filter string, offset, limit int,
) ([]*types.ReparseBatchMemberView, int64, error) {
	if _, err := r.GetReparseBatch(ctx, tenant, id); err != nil {
		return nil, 0, err
	}
	state := reparseMemberStateSQL()
	base := ` FROM reparse_batch_members m
 JOIN reparse_batches b ON b.id=m.batch_id
 LEFT JOIN knowledges k ON k.id=m.knowledge_id AND k.tenant_id=b.tenant_id
  AND k.knowledge_base_id=b.knowledge_base_id AND k.deleted_at IS NULL
 WHERE m.batch_id=?`
	where := ""
	args := []interface{}{id}
	if filter != "" {
		where = " AND " + state + " = ?"
		args = append(args, filter)
	}
	var total int64
	if err := r.db.WithContext(ctx).Raw("SELECT COUNT(*)"+base+where, args...).Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	selectSQL := fmt.Sprintf(`SELECT m.knowledge_id, k.title, %s AS state,
 k.parse_status, k.summary_status, m.submission_error`+base+where+`
 ORDER BY m.knowledge_id LIMIT ? OFFSET ?`, state)
	args = append(args, limit, offset)
	rows := []*types.ReparseBatchMemberView{}
	err := r.db.WithContext(ctx).Raw(selectSQL, args...).Scan(&rows).Error
	return rows, total, err
}

func reparseMemberStateSQL() string {
	return `CASE
 WHEN k.id IS NULL THEN 'missing'
 WHEN b.mode='managed' AND m.submission_state IN ('','waiting_start') THEN 'waiting_start'
 WHEN b.mode='managed' AND m.submission_state='queued' THEN 'queued'
 WHEN b.mode='managed' AND m.submission_state='failed' THEN 'submission_failed'
 WHEN b.mode='managed' AND m.submission_state IN ('handoff_unknown','submitting') THEN 'unknown'
 WHEN k.parse_status='pending' THEN 'queued'
 WHEN k.parse_status='processing' THEN 'processing'
 WHEN k.parse_status='finalizing' THEN 'finalizing'
 WHEN k.parse_status='completed' THEN 'completed'
 WHEN k.parse_status='failed' THEN 'failed'
 WHEN k.parse_status='cancelled' THEN 'cancelled'
 ELSE 'unknown' END`
}
