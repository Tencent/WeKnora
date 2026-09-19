package types

// HostFieldSpec 描述模型插件声明的宿主公共字段显示策略。
//
// 模型插件可在 Manifest 的 model_ui.host_fields 中声明 Base URL / API Key /
// 自定义 Header / 视觉能力 / 并发上限等宿主已有字段的显示方式，宿主据此控制
// 前端公共字段的显隐、必填校验与默认值，从而让不同插件拥有差异化的配置表单，
// 而不需要为每个插件改动前端。
//
// Mode 取值：hidden / optional / required / readonly。
type HostFieldSpec struct {
	Mode    string `json:"mode" yaml:"mode"` // hidden / optional / required / readonly
	Default any    `json:"default,omitempty" yaml:"default,omitempty"`
}
