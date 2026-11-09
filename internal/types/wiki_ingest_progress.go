package types

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

// WikiSourceAttemptUnbound 表示尚未读取 Map 前的源代次，仅允许首次读取计划。
const WikiSourceAttemptUnbound = -1

var (
	// ErrWikiProgressOwnerGone 表示归属已失效或源文档已取消/删除，当前领取可不结算地清理。
	ErrWikiProgressOwnerGone = errors.New("wiki progress owner gone")
	// ErrWikiProgressClaimLost 表示任务已删除或已被其他 worker 重新领取，旧结果不得再落库。
	ErrWikiProgressClaimLost = errors.New("wiki progress claim lost")
	// ErrWikiProgressAlreadyApplied 表示本次页面贡献至少有一项已落库，调用方应重新读取进度。
	ErrWikiProgressAlreadyApplied = errors.New("wiki progress already applied")
	// ErrWikiProgressPlanMissing 表示计划尚未持久化，不能仅凭模型返回标记成功。
	ErrWikiProgressPlanMissing = errors.New("wiki progress plan missing")
	// ErrWikiProgressIncomplete 表示仍有页面或收尾操作未完成，不能消费任务。
	ErrWikiProgressIncomplete = errors.New("wiki progress incomplete")
)

// WikiProgressClaim 绑定一轮领取及其归属；重试可以沿用任务 ID，但领取代次不可沿用。
type WikiProgressClaim struct {
	PendingOpID     int64
	Epoch           int64
	TenantID        uint64
	KnowledgeBaseID string
	KnowledgeID     string
	Op              string
	Attempt         int
	// SourceAttempt 是 Map 开始前观察到的源文档代次；-1 仅用于首次读取。
	SourceAttempt int
}

// WikiIngestPlan 保存不可变的 Map 输出和最终页面分配；具体 JSON 版本由服务层解释。
type WikiIngestPlan struct {
	PendingOpID     int64           `gorm:"primaryKey;autoIncrement:false"`
	TenantID        uint64          `gorm:"not null"`
	KnowledgeBaseID string          `gorm:"type:varchar(64);not null"`
	KnowledgeID     string          `gorm:"type:varchar(128);not null"`
	Op              string          `gorm:"type:varchar(32);not null"`
	Attempt         int             `gorm:"not null;default:0"`
	SourceAttempt   int             `gorm:"not null;default:0"`
	Payload         json.RawMessage `gorm:"type:jsonb;not null"`
	CreatedAt       time.Time
	PendingOp       *TaskPendingOp `json:"-" gorm:"foreignKey:PendingOpID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName 指定不可变计划的存储表。
func (WikiIngestPlan) TableName() string { return "wiki_ingest_plans" }

// WikiIngestPageProgress 只保存小型完成回执，不复制页面正文，避免每页写入放大整个计划。
type WikiIngestPageProgress struct {
	PendingOpID int64       `gorm:"primaryKey;autoIncrement:false"`
	Slug        string      `gorm:"primaryKey;type:varchar(255)"`
	Applied     bool        `gorm:"not null;default:false"`
	EffectsDone bool        `gorm:"not null;default:false"`
	PageID      string      `gorm:"type:varchar(64);not null;default:''"`
	PageVersion int         `gorm:"not null;default:0"`
	Changed     bool        `gorm:"not null;default:false"`
	OldOutLinks StringArray `gorm:"type:json;not null"`
	NewOutLinks StringArray `gorm:"type:json;not null"`
	UpdatedAt   time.Time
	PendingOp   *TaskPendingOp `json:"-" gorm:"foreignKey:PendingOpID;references:ID;constraint:OnDelete:CASCADE"`
}

// TableName 指定小型页面回执的存储表。
func (WikiIngestPageProgress) TableName() string { return "wiki_ingest_page_progress" }

// WikiIngestPlanState 同时返回冻结的计划和小型回执；没有计划时 Plan 为 nil。
type WikiIngestPlanState struct {
	SourceAttempt int
	Plan          *WikiIngestPlan
	Pages         []WikiIngestPageProgress
}

// WikiPageProgressCommit 仅授权一个 slug 的本批贡献与页面写入原子提交。
type WikiPageProgressCommit struct {
	Claims []WikiProgressClaim
	Slug   string
}

type wikiPageProgressCommitKey struct{}

// WithWikiPageProgressCommit 复制领取集合，避免调用方之后改切片而改变事务的归属。
func WithWikiPageProgressCommit(ctx context.Context, commit *WikiPageProgressCommit) context.Context {
	if commit == nil {
		return context.WithValue(ctx, wikiPageProgressCommitKey{}, (*WikiPageProgressCommit)(nil))
	}
	copyCommit := *commit
	copyCommit.Claims = append([]WikiProgressClaim(nil), commit.Claims...)
	return context.WithValue(ctx, wikiPageProgressCommitKey{}, &copyCommit)
}

// WikiPageProgressCommitFromContext 供仓储读取本页提交要求；普通页面写入没有此要求。
func WikiPageProgressCommitFromContext(ctx context.Context) *WikiPageProgressCommit {
	commit, _ := ctx.Value(wikiPageProgressCommitKey{}).(*WikiPageProgressCommit)
	return commit
}

// WithoutWikiPageProgressCommit 清除本页上下文，反链等其他页面写入不得继承本页完成回执。
func WithoutWikiPageProgressCommit(ctx context.Context) context.Context {
	return WithWikiPageProgressCommit(ctx, nil)
}

// WikiPageProgressSnapshot 校验正文和元数据快照，不把反链维护或时间刷新误判为页面贡献变化。
// JSON 数字保留原字面值，避免大整数被浮点转换抹平；格式差异最多造成保守的冲突。
func WikiPageProgressSnapshot(page *WikiPage) (string, error) {
	if page == nil {
		return "", errors.New("wiki progress snapshot requires a page")
	}
	snapshot := *page
	snapshot.ProgressSnapshot = ""
	snapshot.CreatedAt = time.Time{}
	snapshot.UpdatedAt = time.Time{}
	snapshot.DeletedAt.Time = time.Time{}
	snapshot.DeletedAt.Valid = false
	snapshot.InLinks = nil
	snapshot.Aliases = append(StringArray{}, page.Aliases...)
	snapshot.CategoryPath = append(StringArray{}, page.CategoryPath...)
	snapshot.SourceRefs = append(StringArray{}, page.SourceRefs...)
	snapshot.ChunkRefs = append(StringArray{}, page.ChunkRefs...)
	snapshot.OutLinks = append(StringArray{}, page.OutLinks...)
	if len(page.PageMetadata) != 0 {
		if !json.Valid(page.PageMetadata) {
			return "", errors.New("wiki progress snapshot has invalid page metadata")
		}
		decoder := json.NewDecoder(bytes.NewReader(page.PageMetadata))
		decoder.UseNumber()
		var metadata interface{}
		if err := decoder.Decode(&metadata); err != nil {
			return "", err
		}
		normalized, err := json.Marshal(metadata)
		if err != nil {
			return "", err
		}
		snapshot.PageMetadata = JSON(normalized)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}
