package interfaces

import (
	"context"
)

// CfsTaskOptions 用于在跨redis传输时安全保存asynq配置
type CfsTaskOptions struct {
	TaskID   string `json:"task_id,omitempty"`
	Queue    string `json:"queue,omitempty"`
	MaxRetry int    `json:"max_retry,omitempty"`
	//如果业务需要，请直接在下方添加字段并在 tenant_scheduler.go 的 SubmitTask 方法中进行赋值和使用
}

// cfsTaskWrapper 任务包装结构体，包含任务类型、负载和成本等信息
type CfsTaskWrapper struct {
	TaskType string `json:"task_type"`
	Payload  []byte `json:"payload"`

	Cost    int64           `json:"cost"`
	Options *CfsTaskOptions `json:"options,omitempty"`
}

// TenantFairScheduler 租户调度器接口
type TenantFairScheduler interface {
	SubmitTask(ctx context.Context, tenantID uint64, taskType string, payload []byte, cost int64, opt *CfsTaskOptions) error
	Start(ctx context.Context)
}
