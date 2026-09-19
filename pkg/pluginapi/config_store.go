package pluginapi

import "sync"

// ConfigStore 是模型插件级配置的进程内缓存 helper。
//
// 模型插件有一个关键约定：base_url / api_key 等配置只在宿主「保存模型」时
// 通过 ValidateConfig RPC 传入一次，后续 Chat / ChatStream / Embed / Rerank /
// VLM / ASR 等能力调用只经 gRPC metadata 注入 ModelContext（model_id /
// model_name），不再携带配置。因此插件必须在 OnValidateConfig 里缓存配置，
// 调用时按 model_id / model_name 取回。
//
// ConfigStore 把这段样板逻辑下沉到 SDK，插件作者只需两行接入：
//
//	func (m *myModel) validateConfig(_ context.Context, cfg map[string]any) error {
//	    m.configs.PutFromValidate(cfg) // 一行缓存
//	    return nil
//	}
//
//	func (m *myModel) chat(_ context.Context, req pluginapi.ChatRequest) (pluginapi.ChatResult, error) {
//	    apiKey := m.configs.GetString(req.ModelID, req.ModelName, "api_key")
//	    baseURL := m.configs.GetString(req.ModelID, req.ModelName, "base_url")
//	    // ... 用 apiKey / baseURL 调用上游模型服务
//	}
type ConfigStore struct {
	mu     sync.RWMutex
	byID   map[string]map[string]any
	byName map[string]map[string]any
	last   map[string]any
	has    bool
}

// NewConfigStore 创建一个空的配置缓存。
func NewConfigStore() *ConfigStore {
	return &ConfigStore{
		byID:   make(map[string]map[string]any),
		byName: make(map[string]map[string]any),
	}
}

// PutFromValidate 从 OnValidateConfig 收到的 config 里提取 model_id / model_name
// 并缓存。cfg 是宿主传入的扁平 map（含 model_id、model_name、base_url、api_key
// 以及 extra_config 的键值）。
func (s *ConfigStore) PutFromValidate(cfg map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := cfg["model_id"].(string)
	name, _ := cfg["model_name"].(string)
	if id != "" {
		s.byID[id] = cfg
	}
	if name != "" {
		s.byName[name] = cfg
	}
	s.last = cfg
	s.has = true
}

// Get 按 model_id（优先）、model_name（其次）取回缓存的配置；都未命中时返回
// 最近一次缓存作为全局兜底（同一 provider 下多个模型通常共享同一份配置）。
func (s *ConfigStore) Get(modelID, modelName string) (map[string]any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if modelID != "" {
		if c, ok := s.byID[modelID]; ok {
			return c, true
		}
	}
	if modelName != "" {
		if c, ok := s.byName[modelName]; ok {
			return c, true
		}
	}
	if s.has {
		return s.last, true
	}
	return nil, false
}

// GetString 按 model_id / model_name 取回某个字符串字段的值，未命中返回空串。
func (s *ConfigStore) GetString(modelID, modelName, key string) string {
	cfg, ok := s.Get(modelID, modelName)
	if !ok {
		return ""
	}
	v, _ := cfg[key].(string)
	return v
}
