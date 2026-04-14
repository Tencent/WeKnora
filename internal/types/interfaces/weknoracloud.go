package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// WeKnoraCloudService 处理 WeKnoraCloud 凭证管理
type WeKnoraCloudService interface {
	// SaveCredentials 仅保存 APPID/APPSECRET 凭证到租户配置，不自动创建模型
	SaveCredentials(ctx context.Context, appID, appSecret string) error
	// CheckStatus 检查当前租户的 WeKnoraCloud 凭证是否可正常解密
	// needsReinit=true 表示加密状态已损坏（salt 变更等），需要用户重新填写凭证
	CheckStatus(ctx context.Context) (*types.WeKnoraCloudStatusResult, error)
}
