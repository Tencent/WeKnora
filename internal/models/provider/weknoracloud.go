package provider

import "github.com/Tencent/WeKnora/internal/types"

// ProviderWeKnoraCloud and related constants.
const (
	ProviderWeKnoraCloud Name = "weknoracloud"

	// WeKnoraCloudBaseURL WeKnoraCloud 服务硬编码 Base URL（统一入口，路径由各实现拼接）
	WeKnoraCloudBaseURL = "https://weknora.weixin.qq.com"
)

// WeKnoraCloudProvider is an exported type.
type WeKnoraCloudProvider struct{}

func init() {
	Register(&WeKnoraCloudProvider{})
}

// Info implements the required interface method.
func (p *WeKnoraCloudProvider) Info() Info {
	return Info{
		Name:        ProviderWeKnoraCloud,
		DisplayName: "WeKnoraCloud",
		Description: "WeKnora云服务，模型：chat, embedding, rerank, vlm",
		DefaultURLs: map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: WeKnoraCloudBaseURL,
			types.ModelTypeEmbedding:   WeKnoraCloudBaseURL,
			types.ModelTypeRerank:      WeKnoraCloudBaseURL,
			types.ModelTypeVLLM:        WeKnoraCloudBaseURL,
		},
		ModelTypes: []types.ModelType{
			types.ModelTypeKnowledgeQA,
			types.ModelTypeEmbedding,
			types.ModelTypeRerank,
			types.ModelTypeVLLM,
		},
		RequiresAuth: true,
	}
}

// ValidateConfig implements the required behavior.
// WeKnoraCloudProvider is exported.
// WeKnoraCloudProvider is exported.
// WeKnoraCloudProvider is exported.
// WeKnoraCloudProvider is exported.
// WeKnoraCloudProvider is exported.
// WeKnoraCloudProvider implements the required behavior.
func (p *WeKnoraCloudProvider) ValidateConfig(_ *Config) error {
	// AppID/AppSecret 通过专用初始化接口写入，此处仅做结构校验。
	// 其中 AppSecret 字段当前实际承载上游 API Key。
	return nil
}
