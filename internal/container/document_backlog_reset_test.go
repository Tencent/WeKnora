package container

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestResetPendingTasksPreservesDurableDocumentBacklog(t *testing.T) {
	t.Setenv("REDIS_ADDR", "")
	db := setupResetPendingDB(t)
	for _, status := range []string{types.ParseStatusPending, types.ParseStatusProcessing} {
		require.NoError(t, db.Exec("INSERT INTO knowledges(id,parse_status) VALUES (?,?)", status, status).Error)
		require.NoError(t, db.Create(&types.TaskPendingOp{
			TenantID: 7, TaskType: types.TypeDocumentProcess, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb-1", Op: "process", DedupKey: status, Payload: []byte("{}"),
		}).Error)
	}
	resetPendingTasks(db)
	for _, status := range []string{types.ParseStatusPending, types.ParseStatusProcessing} {
		var got string
		require.NoError(t, db.Raw("SELECT parse_status FROM knowledges WHERE id=?", status).Scan(&got).Error)
		require.Equal(t, status, got, "durable work must be resumed instead of failed at startup")
	}
}
