package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WeKnoraCloudService 处理 WeKnoraCloud 厂商的初始化逻辑
type WeKnoraCloudService interface {
	Initialize(ctx context.Context, appID, appSecret string) (*types.InitializeResult, error)
	// CheckStatus 检查当前租户的 WeKnoraCloud 凭证是否可正常解密
	// needsReinit=true 表示加密状态已损坏（salt 变更等），需要用户重新填写凭证
	CheckStatus(ctx context.Context) (*types.WeKnoraCloudStatusResult, error)
}
