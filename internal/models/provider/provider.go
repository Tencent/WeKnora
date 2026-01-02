// Package provider defines the unified interface and registry for multi-vendor model API adapters.
package provider

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
)

// ProviderName 모델 서비스 공급자 이름
type ProviderName string

const (
	// OpenAI
	ProviderOpenAI ProviderName = "openai"
	// Aliyun DashScope
	ProviderAliyun ProviderName = "aliyun"
	// Zhipu AI (GLM 시리즈)
	ProviderZhipu ProviderName = "zhipu"
	// OpenRouter
	ProviderOpenRouter ProviderName = "openrouter"
	// SiliconFlow
	ProviderSiliconFlow ProviderName = "siliconflow"
	// Jina AI (Embedding and Rerank)
	ProviderJina ProviderName = "jina"
	// Generic OpenAI 호환 (사용자 정의 배포)
	ProviderGeneric ProviderName = "generic"
	// DeepSeek
	ProviderDeepSeek ProviderName = "deepseek"
	// Google Gemini
	ProviderGemini ProviderName = "gemini"
	// Volcengine Ark
	ProviderVolcengine ProviderName = "volcengine"
	// Tencent Hunyuan
	ProviderHunyuan ProviderName = "hunyuan"
	// MiniMax
	ProviderMiniMax ProviderName = "minimax"
	// Xiaomi Mimo
	ProviderMimo ProviderName = "mimo"
)

// AllProviders 등록된 모든 공급자 이름을 반환합니다
func AllProviders() []ProviderName {
	return []ProviderName{
		ProviderGeneric,
		ProviderAliyun,
		ProviderZhipu,
		ProviderVolcengine,
		ProviderHunyuan,
		ProviderSiliconFlow,
		ProviderDeepSeek,
		ProviderMiniMax,
		ProviderOpenAI,
		ProviderGemini,
		ProviderOpenRouter,
		ProviderJina,
		ProviderMimo,
	}
}

// ProviderInfo 공급자 메타데이터 포함
type ProviderInfo struct {
	Name         ProviderName               // 공급자 식별자
	DisplayName  string                     // 표시 이름
	Description  string                     // 공급자 설명
	DefaultURLs  map[types.ModelType]string // 모델 유형별 기본 BaseURL
	ModelTypes   []types.ModelType          // 지원되는 모델 유형
	RequiresAuth bool                       // API 키 필요 여부
	ExtraFields  []ExtraFieldConfig         // 추가 구성 필드
}

// GetDefaultURL 지정된 모델 유형의 기본 URL 가져오기
func (p ProviderInfo) GetDefaultURL(modelType types.ModelType) string {
	if url, ok := p.DefaultURLs[modelType]; ok {
		return url
	}
	// Chat URL로 대체
	if url, ok := p.DefaultURLs[types.ModelTypeKnowledgeQA]; ok {
		return url
	}
	return ""
}

// ExtraFieldConfig 공급자의 추가 구성 필드 정의
type ExtraFieldConfig struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Type        string `json:"type"` // "string", "number", "boolean", "select"
	Required    bool   `json:"required"`
	Default     string `json:"default"`
	Placeholder string `json:"placeholder"`
	Options     []struct {
		Label string `json:"label"`
		Value string `json:"value"`
	} `json:"options,omitempty"`
}

// Config 모델 공급자 구성
type Config struct {
	Provider  ProviderName   `json:"provider"`
	BaseURL   string         `json:"base_url"`
	APIKey    string         `json:"api_key"`
	ModelName string         `json:"model_name"`
	ModelID   string         `json:"model_id"`
	Extra     map[string]any `json:"extra,omitempty"`
}

type Provider interface {
	// Info 서비스 공급자의 메타데이터 반환
	Info() ProviderInfo

	// ValidateConfig 서비스 공급자 구성 검증
	ValidateConfig(config *Config) error
}

// registry 등록된 모든 공급자 저장
var (
	registryMu sync.RWMutex
	registry   = make(map[ProviderName]Provider)
)

// Register 전역 레지스트리에 공급자 추가
func Register(p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.Info().Name] = p
}

// Get 이름을 통해 레지스트리에서 공급자 가져오기
func Get(name ProviderName) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// GetOrDefault 이름을 통해 레지스트리에서 공급자를 가져오고, 없으면 기본 공급자 반환
func GetOrDefault(name ProviderName) Provider {
	p, ok := Get(name)
	if ok {
		return p
	}
	// 찾지 못하면 기본 공급자 반환
	p, _ = Get(ProviderGeneric)
	return p
}

// List 등록된 모든 공급자 반환 (AllProviders 정의 순서대로)
func List() []ProviderInfo {
	registryMu.RLock()
	defer registryMu.RUnlock()

	result := make([]ProviderInfo, 0, len(registry))
	for _, name := range AllProviders() {
		if p, ok := registry[name]; ok {
			result = append(result, p.Info())
		}
	}
	return result
}

// ListByModelType 지정된 모델 유형을 지원하는 모든 공급자 반환 (AllProviders 정의 순서대로)
func ListByModelType(modelType types.ModelType) []ProviderInfo {
	registryMu.RLock()
	defer registryMu.RUnlock()

	result := make([]ProviderInfo, 0)
	for _, name := range AllProviders() {
		if p, ok := registry[name]; ok {
			info := p.Info()
			for _, t := range info.ModelTypes {
				if t == modelType {
					result = append(result, info)
					break
				}
			}
		}
	}
	return result
}

// DetectProvider BaseURL을 통해 서비스 공급자 감지
func DetectProvider(baseURL string) ProviderName {
	switch {
	case containsAny(baseURL, "dashscope.aliyuncs.com"):
		return ProviderAliyun
	case containsAny(baseURL, "open.bigmodel.cn", "zhipu"):
		return ProviderZhipu
	case containsAny(baseURL, "openrouter.ai"):
		return ProviderOpenRouter
	case containsAny(baseURL, "siliconflow.cn"):
		return ProviderSiliconFlow
	case containsAny(baseURL, "api.jina.ai"):
		return ProviderJina
	case containsAny(baseURL, "api.openai.com"):
		return ProviderOpenAI
	case containsAny(baseURL, "api.deepseek.com"):
		return ProviderDeepSeek
	case containsAny(baseURL, "generativelanguage.googleapis.com"):
		return ProviderGemini
	case containsAny(baseURL, "volces.com", "volcengine"):
		return ProviderVolcengine
	case containsAny(baseURL, "hunyuan.cloud.tencent.com"):
		return ProviderHunyuan
	case containsAny(baseURL, "minimax.io", "minimaxi.com"):
		return ProviderMiniMax
	case containsAny(baseURL, "xiaomimimo.com"):
		return ProviderMimo
	default:
		return ProviderGeneric
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func NewConfigFromModel(model *types.Model) (*Config, error) {
	if model == nil {
		return nil, fmt.Errorf("model is nil")
	}

	providerName := ProviderName(model.Parameters.Provider)
	if providerName == "" {
		providerName = DetectProvider(model.Parameters.BaseURL)
	}

	return &Config{
		Provider:  providerName,
		BaseURL:   model.Parameters.BaseURL,
		APIKey:    model.Parameters.APIKey,
		ModelName: model.Name,
		ModelID:   model.ID,
	}, nil
}
