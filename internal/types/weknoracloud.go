package types

// WeKnoraCloudStatusResult 状态检查结果
type WeKnoraCloudStatusResult struct {
	HasModels   bool   `json:"has_models"`       // 是否已配置 WeKnoraCloud 模型
	NeedsReinit bool   `json:"needs_reinit"`     // 是否需要重新初始化（凭证损坏）
	Reason      string `json:"reason,omitempty"` // 需要重新初始化的原因
}

// InitializeResult 返回每个模型的操作结果
type InitializeResult struct {
	Models []ModelAction `json:"models"`
}

// ModelAction 单个模型的操作结果
type ModelAction struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Action string `json:"action"` // "created" | "updated"
}

type DocreaderCredentials struct {
	AppID  string
	APIKey string
}
