package interfaces

import (
	"context"
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/types"
)

// WikiIngestProgressRepository 是持久队列的可选扩展；旧队列接口与其他任务类型无需改变。
type WikiIngestProgressRepository interface {
	// LoadWikiIngestPlan 验证领取代次和归属，再读取冻结计划及页面回执。
	LoadWikiIngestPlan(ctx context.Context, claim types.WikiProgressClaim) (*types.WikiIngestPlanState, error)
	// SaveWikiIngestPlan 首次原子保存计划与全部 slug；并发调用返回首次成功写入的版本。
	SaveWikiIngestPlan(
		ctx context.Context,
		claim types.WikiProgressClaim,
		payload json.RawMessage,
		slugs []string,
	) (*types.WikiIngestPlanState, error)
	// CompleteWikiPageNoop 仅用于已确认无需写页面的成功分支，不用于模型失败或延后写入。
	CompleteWikiPageNoop(ctx context.Context, commit types.WikiPageProgressCommit) error
	// FinishWikiPageEffects 在页面已落库且反链等收尾成功后记录完成。
	FinishWikiPageEffects(ctx context.Context, commit types.WikiPageProgressCommit) error
	// ReleaseWikiIngestClaims 只释放仍属于调用方的领取，不干扰重新领取的 worker。
	ReleaseWikiIngestClaims(ctx context.Context, claims []types.WikiProgressClaim) error
	// IncrWikiIngestFailCount 只增加当前领取的失败次数；失主返回 ErrWikiProgressClaimLost。
	IncrWikiIngestFailCount(ctx context.Context, claim types.WikiProgressClaim) (int, error)
	// SettleWikiIngestClaim 原子消费任务并按需释放一个文档子任务计数。discard 仅供明确终止清理。
	// 返回 false 表示任务已删除或失主；正常消费要求所有页面及收尾已完成。
	SettleWikiIngestClaim(ctx context.Context, claim types.WikiProgressClaim, finalize, discard bool) (bool, error)
}
