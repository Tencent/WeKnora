package provider

import "github.com/Tencent/WeKnora/internal/types"

const (
	ProviderWeKnoraCloud ProviderName = "weknoracloud"

	// WeKnoraCloudBaseURL WeKnoraCloud 服务硬编码 Base URL（统一入口，路径由各实现拼接）
	WeKnoraCloudBaseURL = "https://weknora.weixin.qq.com"
)

type WeKnoraCloudProvider struct{}

func init() {
	Register(&WeKnoraCloudProvider{})
}

func (p *WeKnoraCloudProvider) Info() ProviderInfo {
	return ProviderInfo{
		Name:        ProviderWeKnoraCloud,
		DisplayName: "WeKnoraCloud",
		Description: "WeKnoraCloud 平台，提供 Chat / Embedding / Rerank 模型，使用 APPID + API Key 签名认证",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: WeKnoraCloudBaseURL,
			types.ModelTypeEmbedding:   WeKnoraCloudBaseURL,
			types.ModelTypeRerank:      WeKnoraCloudBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
		},
		RequiresAuth: true,
	}
}

func (p *WeKnoraCloudProvider) ValidateConfig(config *Config) error {
	// AppID/AppSecret 通过专用初始化接口写入，此处仅做结构校验。
	// 其中 AppSecret 字段当前实际承载上游 API Key。
	return nil
}
