package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiPageProgressFinisher 补齐已提交页面的派生收尾，不重新生成或覆盖页面正文。
// 调用成功后，编排层才可把对应回执标为 EffectsDone；失败时保留回执等待恢复。
type WikiPageProgressFinisher interface {
	ReconcileAppliedPage(ctx context.Context, tenantID uint64, kbID string, receipt types.WikiIngestPageProgress) error
}
