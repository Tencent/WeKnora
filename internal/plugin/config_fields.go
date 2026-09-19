package plugin

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// configFieldsFromSchema 把一个分区式 config_schema（settings / credentials）
// 解析成前端可渲染的 config field 列表。reserved 中的 key 会被跳过——它们由宿主
// 专用表单区渲染，不重复出现。credentials 区里的字段强制标记为 secret。
//
// 字段类型映射（与前端约定一致）：
//
//	string           → text input        (secret → password input)
//	boolean          → switch
//	integer / number → number input
//	array / string[] → comma-separated text input
//	enum present     → select（options 由 enum 值派生）
func configFieldsFromSchema(schema map[string]any, reserved map[string]struct{}) []types.WebSearchProviderConfigField {
	if len(schema) == 0 {
		return nil
	}
	var fields []types.WebSearchProviderConfigField
	for _, d := range normalizeConfigSchema(schema, "settings", "credentials") {
		if _, isReserved := reserved[d.Key]; isReserved {
			continue
		}
		fields = append(fields, types.WebSearchProviderConfigField{
			Key:         d.Key,
			Label:       d.Title,
			Type:        configFieldType(d.Type, d.Secret, len(d.Enum) > 0),
			Required:    d.Required,
			Default:     configFieldDefaultString(d.Default),
			Description: d.Description,
			Options:     stringEnumOptions(d.Enum),
		})
	}
	return fields
}

// SchemaFieldDescriptor is a neutral description of one config_schema field,
// shared by every extension adapter so field semantics (type / secret / title
// fallback) are normalized once instead of re-implemented per extension.
type SchemaFieldDescriptor struct {
	Key         string
	Section     string // settings / credentials / index_config
	Type        string // raw schema type (string / boolean / integer / number / array / ...)
	Secret      bool
	Required    bool
	Title       string
	Description string
	Default     any
	Enum        []string
}

// normalizeConfigSchema parses a partitioned config_schema into neutral field
// descriptors. sections lists the sections to include. Title falls back
// uniformly: title > description > key. Credentials fields are forced secret.
func normalizeConfigSchema(schema map[string]any, sections ...string) []SchemaFieldDescriptor {
	if len(schema) == 0 {
		return nil
	}
	var out []SchemaFieldDescriptor
	for _, section := range sections {
		props, required := schemaSection(schema, section)
		keys := make([]string, 0, len(props))
		for key := range props {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			property, _ := props[key].(map[string]any)
			if property == nil {
				continue
			}
			title, _ := property["title"].(string)
			description, _ := property["description"].(string)
			if title == "" {
				title = description
			}
			if title == "" {
				title = key
			}
			jsonType, _ := property["type"].(string)
			secret, _ := property["secret"].(bool)
			if section == "credentials" {
				secret = true
			}
			enum, _ := property["enum"].([]any)
			_, isRequired := required[key]
			out = append(out, SchemaFieldDescriptor{
				Key:         key,
				Section:     section,
				Type:        jsonType,
				Secret:      secret,
				Required:    isRequired,
				Title:       title,
				Description: description,
				Default:     property["default"],
				Enum:        toStringSlice(enum),
			})
		}
	}
	return out
}

// configFieldType 把一个类型声明归一化为前端的渲染意图。enum 永远优先（渲染为
// 下拉框）。
func configFieldType(declaredType string, secret bool, hasEnum bool) string {
	if hasEnum {
		return "select"
	}
	switch declaredType {
	case "boolean":
		return "boolean"
	case "integer", "number":
		return "number"
	case "array", "string[]":
		return "array"
	}
	if secret {
		return "secret"
	}
	return "string"
}

func stringEnumOptions(values []string) []types.WebSearchProviderConfigFieldOption {
	if len(values) == 0 {
		return nil
	}
	options := make([]types.WebSearchProviderConfigFieldOption, 0, len(values))
	for _, value := range values {
		options = append(options, types.WebSearchProviderConfigFieldOption{Label: value, Value: value})
	}
	return options
}

func configFieldDefaultString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

// toStringSlice converts a schema enum ([]any of scalars) into a []string for
// the VectorStore registration UI, which only models string enums.
func toStringSlice(values []any) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, strings.TrimSpace(fmt.Sprint(value)))
	}
	return out
}
