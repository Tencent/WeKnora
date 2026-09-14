package feishu

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// One budget belongs to a message in its read context, including failed parses
// and any replacement snapshot. It must not be shared between unrelated cards.
type cardParseBudget struct{ visited int }

type cardParseResult struct {
	Parts   []im.MaterialPart
	Status  string // complete, partial, unknown, empty, unreadable
	Missing []string
}

type cardParser struct {
	ctx            context.Context
	budget         *cardParseBudget
	result         cardParseResult
	bytes          int
	formattedBytes int
	meaning        bool
	locale         string
	localeLabel    string
	locales        map[string]bool
	custom         bool
	body           bool
	markdownTables bool
	facts          map[string]map[string]string
	input          string
}

// parseCard extracts the returned card and explicit image/file references.
// The worker reads these resources using its existing attachment budgets;
// ordinary links and callback payloads never become downloadable Parts.
// JSON numbers never pass through float64.
func parseCard(ctx context.Context, raw string, budget *cardParseBudget) cardParseResult {
	if budget == nil {
		budget = &cardParseBudget{}
	}
	p := cardParser{
		ctx: ctx, budget: budget, result: cardParseResult{Status: "complete"},
		facts: map[string]map[string]string{},
	}
	if len(raw) > 32<<20 {
		p.missing("partial", "卡片响应超过 32 MiB，未解析")
		return p.finish()
	}
	if ctx.Err() != nil {
		p.missing("partial", "卡片解析已取消或超时")
		return p.finish()
	}
	// ReadMessage sometimes returns a json_card envelope, with either an object
	// or a JSON string. RawMessage inspects only the envelope, not its components;
	// the decoded card root, rather than this transport wrapper, is depth one.
	var envelope map[string]json.RawMessage
	var attachment json.RawMessage
	if json.Unmarshal([]byte(raw), &envelope) == nil {
		if wrapped, ok := envelope["json_card"]; ok {
			attachment = envelope["json_attachment"]
			raw = string(wrapped)
			if len(wrapped) > 0 && wrapped[0] == '"' {
				if err := json.Unmarshal(wrapped, &raw); err != nil {
					p.missing("partial", "json_card 包装无法解析")
					return p.finish()
				}
			}
		}
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	p.input = raw
	value, err := p.decode(decoder, 1, "card")
	if err != nil {
		p.missing("partial", err.Error())
	} else if _, err := decoder.Token(); err != io.EOF {
		p.missing("partial", "card：JSON 末尾存在无效或额外内容")
	}
	root, ok := value.(map[string]any)
	if !ok {
		p.missing("partial", "card：卡片根节点不是对象")
		return p.finish()
	}
	_, p.body = root["body"]
	p.markdownTables = root["schema"] == "2.0"
	if _, exists := root["elements"]; exists {
		p.body = true
	}
	if _, exists := root["i18n_elements"]; exists {
		p.body = true
	}
	if config, ok := root["config"].(map[string]any); ok {
		config = p.localized(config)
		p.custom, _ = config["use_custom_translation"].(bool)
		if enabled, exists := config["locales"]; exists {
			p.locales = map[string]bool{}
			if languages, ok := enabled.([]any); ok {
				for _, language := range languages {
					if code, ok := language.(string); ok {
						p.locales[code] = true
					} else {
						p.missing("partial", "card.config.locales：语言代码不是字符串")
					}
				}
			} else {
				p.missing("partial", "card.config.locales：语言列表不是数组")
			}
		}
	}
	languages := map[string]bool{}
	p.findLocales(root, languages, "card")
	ordered := make([]string, 0, len(languages))
	for code := range languages {
		ordered = append(ordered, code)
	}
	sort.Strings(ordered)
	if len(ordered) > 0 {
		p.explain("card.语言版本说明", "各语种是同一卡片的语言版本，不是多份业务记录；不跨语种重复汇总。生效范围未知时不能合并为唯一事实。")
	}
	for i, code := range append([]string{""}, ordered...) {
		p.locale = code
		p.localeLabel = fmt.Sprintf("语种版本%d", i)
		if code != "" {
			p.emit("card.语种", code, false)
		}
		p.node(root, "card", "card")
	}
	p.locale = ""
	if len(attachment) > 0 && string(attachment) != "null" {
		d := json.NewDecoder(strings.NewReader(string(attachment)))
		d.UseNumber()
		p.input = string(attachment)
		resources, err := p.decode(d, 1, "json_attachment")
		if err != nil {
			p.missing("partial", err.Error())
		}
		p.attachments(resources)
	}
	for _, path := range cardKeys(p.facts) {
		values := p.facts[path]
		var prior string
		for _, code := range cardKeys(values) {
			if prior != "" && prior != values[code] {
				p.explain(path, "生效语种同一字段的原文或原值存在差异；数值或状态须按各版分别使用，不能合并为唯一事实")
				break
			}
			prior = values[code]
		}
	}
	return p.finish()
}

func (p *cardParser) finish() cardParseResult {
	if !p.meaning {
		if p.result.Status == "complete" {
			p.result.Status = "empty"
		} else {
			p.result.Status = "unreadable"
		}
	}
	return p.result
}

func (p *cardParser) missing(status, why string) {
	if len(why) > 1024 {
		end := 1024
		for !utf8.RuneStart(why[end]) {
			end--
		}
		why = why[:end] + "…（缺失位置过长）"
	}
	if status == "partial" || p.result.Status == "complete" {
		p.result.Status = status
	}
	for _, prior := range p.result.Missing {
		if prior == why {
			return
		}
	}
	// Missing descriptions are material too. Keep them bounded even if a card
	// contains thousands of malformed scalar fields in a single JSON object.
	if len(p.result.Missing) < 32 {
		p.result.Missing = append(p.result.Missing, why)
	} else if len(p.result.Missing) == 32 {
		p.result.Missing = append(p.result.Missing, "另有未逐项列出的缺失内容")
	}
}

// Count every decoded object/array, including ignored protocol/layout fields.
// Stop before allocating the 2001st container or descending into depth 33.
// Returning only completed tokens permits useful siblings to survive a failure.
func (p *cardParser) decode(d *json.Decoder, depth int, path string) (any, error) {
	if p.ctx.Err() != nil {
		return nil, fmt.Errorf("%s：卡片解析已取消或超时", path)
	}
	token, err := d.Token()
	if err != nil {
		return nil, fmt.Errorf("%s：JSON 不完整或格式错误", path)
	}
	delim, container := token.(json.Delim)
	if !container {
		// Decoder.Token may accept the numeric prefix of a cut or invalid value.
		// Require a following JSON delimiter before retaining the original number.
		if _, number := token.(json.Number); number && !p.numberDelimited(d.InputOffset()) {
			return nil, fmt.Errorf("%s：JSON 数值缺少有效结束分隔符", path)
		}
		return token, nil
	}
	if depth > 32 {
		return nil, fmt.Errorf("%s：超过卡片 JSON 32 层上限", path)
	}
	if p.budget.visited >= 2000 {
		return nil, fmt.Errorf("%s：超过卡片 JSON 2000 个对象/数组上限", path)
	}
	p.budget.visited++
	if delim == '{' {
		object := map[string]any{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return object, fmt.Errorf("%s：JSON 字段不完整", path)
			}
			name, ok := key.(string)
			if !ok {
				return object, fmt.Errorf("%s：JSON 字段名无效", path)
			}
			if _, duplicate := object[name]; duplicate {
				p.missing("partial", path+"."+strconv.Quote(name)+"：重复 JSON 字段，保留最后返回值")
			}
			child, err := p.decode(d, depth+1, path+"."+strconv.Quote(name))
			if child != nil || err == nil {
				object[name] = child
			}
			if err != nil {
				return object, err
			}
		}
		if _, err := d.Token(); err != nil {
			return object, fmt.Errorf("%s：JSON 对象未结束", path)
		}
		return object, nil
	}
	if delim == '[' {
		array := []any{}
		for d.More() {
			child, err := p.decode(d, depth+1, fmt.Sprintf("%s[%d]", path, len(array)+1))
			if child != nil || err == nil {
				array = append(array, child)
			}
			if err != nil {
				return array, err
			}
		}
		if _, err := d.Token(); err != nil {
			return array, fmt.Errorf("%s：JSON 数组未结束", path)
		}
		return array, nil
	}
	return nil, fmt.Errorf("%s：JSON 结束符位置无效", path)
}

func (p *cardParser) numberDelimited(offset int64) bool {
	return offset < int64(len(p.input)) && strings.ContainsRune(",}] \t\r\n", rune(p.input[offset]))
}

func (p *cardParser) language() string {
	if p.locale == "" {
		return "默认版；生效范围未知，可能为备用默认内容"
	}
	state := "生效范围未知"
	if p.locales != nil {
		if p.locales[p.locale] {
			state = "配置生效语种；当前客户端语种未知"
		} else {
			state = "未生效的备用语种，不能当作当前事实"
		}
	}
	if p.custom {
		state += "；可用于用户主动翻译，不代表当前显示"
	}
	return p.localeLabel + "；" + state
}

func (p *cardParser) explain(path, description string) {
	p.emit(path, description, false, 0)
}

// An explicit cost is used only for generated explanations. All source values,
// including empty strings and style/locale metadata, keep their original cost.
func (p *cardParser) emit(path string, value any, meaning bool, cost ...int) {
	if p.ctx.Err() != nil {
		p.missing("partial", "卡片解析已取消或超时")
		return
	}
	typ := "null"
	switch value.(type) {
	case string:
		typ = "string"
	case json.Number:
		typ = "number"
	case bool:
		typ = "boolean"
	case []any:
		typ = "array"
	case map[string]any:
		typ = "object"
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		p.missing("partial", path+"：原值无法序列化")
		return
	}
	originalBytes := encoded.Len() - 1 // Encoder's trailing newline is formatting.
	if original, ok := value.(string); ok {
		originalBytes = len(original)
	}
	if len(cost) != 0 {
		originalBytes = cost[0]
	}
	text := "[" + strconv.Quote(p.language()+" / "+path) + "] (" + typ + "): " + encoded.String()
	if p.bytes+originalBytes > 32<<10 {
		p.missing("partial", path+"：完整字段超过剩余卡片材料额度（单卡最多 32 KiB），未截断原值")
		return
	}
	if p.formattedBytes+len(text) > im.MaxCardFormattedTextBytes {
		p.missing("partial", path+"：完整字段超过卡片格式额度（256 KiB），未截断原值")
		return
	}
	p.result.Parts = append(p.result.Parts, im.MaterialPart{Text: text, OriginalTextBytes: &originalBytes})
	p.bytes += originalBytes
	p.formattedBytes += len(text)
	p.meaning = p.meaning || meaning
	if p.locale != "" && p.locales[p.locale] {
		p.recordFacts(path, value)
	}
}

func (p *cardParser) recordFacts(path string, value any) {
	if p.facts[path] == nil {
		p.facts[path] = map[string]string{}
	}
	// Compare the whole field so business keys never become free provenance.
	encoded, _ := json.Marshal(value)
	p.facts[path][p.locale] = string(encoded)
}

func cardKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func (p *cardParser) findLocales(value any, languages map[string]bool, role string) {
	if p.ctx.Err() != nil {
		return
	}
	switch value := value.(type) {
	case []any:
		for _, child := range value {
			p.findLocales(child, languages, role)
		}
	case map[string]any:
		if role == "link_variables" {
			for _, child := range value {
				p.findLocales(child, languages, "links")
			}
			return
		}
		m := p.localized(value)
		tag, _ := m["tag"].(string)
		if tag == "" {
			tag = role
		}
		switch tag {
		case "card_header":
			tag = "header"
		case "header_text":
			tag = "text"
		case "image_option":
			tag = "option"
		}
		for key, child := range value {
			field := strings.TrimPrefix(key, "i18n_")
			translated := field != key
			if key == "i18n" && role == "header_text" {
				field, translated = "content", true
			}
			childRole := ""
			switch {
			case field == "property" && !translated:
				childRole = tag
				if role == "header_text" || role == "table_column" {
					childRole = role
				}
			case tag == "table":
				switch field {
				case "columns":
					childRole = "table_column"
				case "rows":
					childRole = "literal"
				}
			case role == "table_column":
				switch field {
				case "name", "display_name", "data_type", "unit", "format", "date_format", "timezone":
					childRole = "literal"
				case "tooltip", "hint", "description", "hover_tips", "disabled_tips":
					childRole = "text"
				}
			case !cardKnownTag(tag):
				if field == "elements" || field == "children" || field == "body" || field == "header" {
					childRole = "component"
				} else if !translated && !cardIgnoredField(field) && field != "behaviors" {
					if component, ok := child.(map[string]any); ok {
						if kind, _ := component["tag"].(string); cardKnownTag(kind) {
							childRole = "component"
						}
					}
				}
			default:
				switch field {
				case "header", "body", "confirm", "button_area", "config", "icon", "fallback":
					childRole = field
				case "ud_icon":
					if tag == "header" {
						childRole = "standard_icon"
					}
				case "newBody":
					childRole = "body"
				case "elements", "actions", "buttons", "columns", "text_tag_list", "extra":
					childRole = "component"
				case "markdownElements":
					if tag == "markdown" || tag == "lark_md" || tag == "plain_text" || tag == "text" || tag == "md" {
						childRole = "component"
					}
				case "img_list", "image_list", "images":
					childRole = "img"
				case "fields":
					childRole = "field"
				case "items":
					if tag == "list" {
						childRole = "list_item"
					}
				case "persons":
					childRole = "person"
				case "options":
					childRole = "option"
				case "title", "subtitle", "text", "content", "label", "description", "summary", "alt", "placeholder",
					"hover_tips", "disabled_tips", "tooltip", "hint":
					if tag == "config" && field == "summary" && p.body {
						continue
					}
					childRole = "text"
					if tag == "header" && (field == "title" || field == "subtitle") {
						childRole = "header_text"
					}
				case "href", "card_link", "multi_url", "url", "android_url", "ios_url", "pc_url", "preview_url":
					childRole = "links"
					if field == "href" && (tag == "markdown" || tag == "lark_md" || tag == "md") {
						childRole = "link_variables"
					}
				case "action":
					if !translated && (tag == "column" || tag == "column_set") {
						if action, ok := child.(map[string]any); ok {
							p.findLocales(action["multi_url"], languages, "links")
						}
					}
				case "img_key", "image_key", "file_key", "file_name", "title_text", "user_id", "open_id", "union_id",
					"user_name", "duration", "duration_ms", "unit", "timezone", "format", "date_format", "language",
					"color", "text_color", "background_color", "emoji_type", "disabled", "required", "input_type",
					"max_length", "min", "max", "step", "type", "show_strikethrough", "level",
					"initial_option", "initial_options", "initial_date", "initial_time", "initial_datetime",
					"initial_user",
					"initial_users", "default_value", "checked", "selected_values", "conditions", "condition",
					"display_condition", "visible", "visibility", "trigger_conditions", "selected", "selected_option",
					"selected_options", "submitted", "submitted_value", "input_value", "status", "state", "chart_spec":
					childRole = "literal"
				case "name":
					if tag == "person" || cardResourceTag(tag) || tag == "option" {
						childRole = "literal"
					}
				case "value":
					if tag == "option" {
						childRole = "literal"
					}
				}
			}
			if childRole == "" {
				if translated && !cardIgnoredField(field) && field != "value" && field != "behaviors" {
					p.missing("partial", key+"：多语言字段不在可提取结构中")
				}
				continue
			}
			// A source string overrides this component's internal protocol copies.
			if tag == "markdown" || tag == "lark_md" || tag == "plain_text" || tag == "text" || tag == "md" {
				if content, ok := m["content"].(string); ok && content != "" &&
					(field == "text" || field == "elements" || field == "markdownElements") {
					continue
				}
			}
			if translated {
				if versions, ok := child.(map[string]any); ok {
					for code, version := range versions {
						languages[code] = true
						if childRole != "literal" {
							p.findLocales(version, languages, childRole)
						}
					}
				} else {
					p.missing("partial", key+"：多语言配置不是对象")
				}
			} else if childRole != "literal" {
				p.findLocales(child, languages, childRole)
			}
		}
	}
}

// localized selects one protocol language version without merging records.
// Common, unlocalized fields remain attached to the component they describe.
func (p *cardParser) localized(m map[string]any) map[string]any {
	result := make(map[string]any, len(m))
	for key, value := range m {
		if !strings.HasPrefix(key, "i18n_") {
			result[cardFieldName(key)] = value
		}
	}
	if p.locale != "" {
		for key, value := range m {
			if strings.HasPrefix(key, "i18n_") {
				if versions, ok := value.(map[string]any); ok {
					if translated, exists := versions[p.locale]; exists {
						result[strings.TrimPrefix(key, "i18n_")] = translated
					}
				}
			}
		}
	}
	// Readback's property object wraps the same component's semantic fields.
	// Never recursively flatten an unknown object or a callback payload.
	if property, exists := result["property"]; exists {
		if fields, ok := property.(map[string]any); ok {
			for key, value := range p.localized(fields) {
				if _, duplicate := result[key]; duplicate && key != "id" {
					p.missing("partial", "property：组件存在冲突的同名属性 "+strconv.Quote(key))
				}
				result[key] = value
			}
			delete(result, "property")
		}
	}
	return result
}

func cardFieldName(key string) string {
	switch key {
	case "streamingMode":
		return "streaming_mode"
	case "useCustomTranslation":
		return "use_custom_translation"
	case "textColor":
		return "text_color"
	case "backgroundColor":
		return "background_color"
	}
	return key
}

func (p *cardParser) node(value any, path, role string) {
	if p.ctx.Err() != nil {
		p.missing("partial", "卡片解析已取消或超时")
		return
	}
	if array, ok := value.([]any); ok {
		if role == "text" || role == "header_text" || role == "links" || role == "fallback" {
			p.missing("partial", path+"：文本或链接字段不应为数组")
		}
		for i, child := range array {
			p.node(child, fmt.Sprintf("%s[%d]", path, i+1), role)
		}
		return
	}
	object, ok := value.(map[string]any)
	if !ok {
		if role == "text" || role == "header_text" || role == "option" || role == "person" ||
			role == "links" || role == "fallback" {
			if _, isString := value.(string); !isString {
				p.missing("partial", path+"：文本或标识字段的原始类型无效")
			}
			p.emit(path, value, value != "")
			if role == "person" {
				p.explain(path+".人员名称", "名称未提供；仅保留原始人员标识，不查询通讯录")
			}
		} else {
			p.missing("partial", path+"：组件不是对象")
		}
		return
	}
	m := p.localized(object)
	if role == "header_text" {
		if value, exists := m["i18n"]; exists {
			if versions, ok := value.(map[string]any); ok {
				if p.locale != "" {
					if translated, exists := versions[p.locale]; exists {
						m["content"] = translated
					}
				} else if _, hasDefault := m["content"]; hasDefault && len(versions) > 0 {
					p.explain(path+".语言用途", "标题 i18n 优先生效；默认 content 仅作为备用原文保留")
				}
			} else {
				p.missing("partial", path+".i18n：标题多语言配置不是对象")
			}
			delete(m, "i18n")
		}
	}
	tag, _ := m["tag"].(string)
	if tag == "" {
		tag = role
		if role == "image_option" {
			tag = "option"
		}
		if role == "header_text" {
			tag = "text"
		}
	}
	if tag == "card_header" {
		tag = "header"
	}
	if role == "card" {
		for key := range m {
			switch key {
			case "schema", "config", "header", "body", "elements", "card_link", "fallback", "newBody", "source",
				"title":
			default:
				p.missing("partial", path+"."+key+"：未识别的卡片根字段")
			}
		}
		if schema, exists := m["schema"]; exists && schema != "1.0" && schema != "2.0" {
			p.missing("partial", path+"：未识别的卡片 schema")
		}
		if m["type"] == "template" {
			p.missing("partial", path+"：卡片模板尚未展开")
		}
		if _, exists := m["title"]; exists {
			p.missing("unknown", path+"：收到简化展示内容，无法确认原始卡片覆盖范围")
		}
		if elements, ok := m["elements"].([]any); ok && len(elements) > 0 {
			if _, simplified := elements[0].([]any); simplified {
				p.missing("unknown", path+"：收到简化展示内容，无法确认原始卡片覆盖范围")
			}
		}
	}
	p.continuation(m, path)
	known := cardKnownTag(tag)
	if !known {
		p.missing("partial", path+"：未识别组件 "+strconv.Quote(tag))
	}
	if tag == "table" {
		p.table(m, path+"(table)")
		return
	}
	if tag == "config" {
		if style, ok := m["style"].(map[string]any); ok {
			if colors, exists := style["color"]; exists {
				palette, valid := colors.(map[string]any)
				if !valid {
					p.missing("partial", path+".style.color：自定义颜色映射不是对象")
				}
				kept := map[string]any{}
				for _, name := range cardKeys(palette) {
					modes, ok := palette[name].(map[string]any)
					if !ok {
						p.missing("partial", path+".style.color：自定义颜色主题不是对象")
						continue
					}
					values := map[string]any{}
					for _, mode := range cardKeys(modes) {
						value, ok := modes[mode].(string)
						if ok && (mode == "light_mode" || mode == "dark_mode") {
							values[mode] = value
						} else {
							p.missing("partial", path+".style.color：颜色主题字段未识别或值不是字符串")
						}
					}
					kept[name] = values
				}
				p.emit(path+".style.color", kept, false)
			}
		}
	}
	if tag == "chart" {
		if spec, exists := m["chart_spec"]; exists {
			p.chart(spec, path+"(chart).chart_spec")
		} else {
			p.missing("partial", path+"：图表未提供 chart_spec")
		}
		delete(m, "chart_spec")
	}
	if tag != "card" && tag != "body" {
		if known {
			path += "(" + tag + ")"
		} else {
			path += "(未识别组件)"
		}
	}
	if tag == "input" || tag == "form" || tag == "checker" || strings.HasPrefix(tag, "select_") ||
		strings.HasPrefix(tag, "multi_select_") || strings.Contains(tag, "picker") || tag == "button" {
		p.explain(path+".提交状态", "未知；预填、初始勾选和候选项均不是已提交记录")
	}
	if cardResourceTag(tag) {
		p.resource(m, tag, path)
		if tag == "img" || tag == "image" || tag == "file" || tag == "audio" || tag == "video" ||
			tag == "media" || tag == "attachment" {
			found := false
			for _, key := range []string{"img_key", "image_key", "file_key", "url"} {
				if id, ok := m[key].(string); ok && id != "" {
					found = true
				}
			}
			if !found {
				p.missing("partial", path+"：资源未提供有效标识或地址")
			}
		}
	}
	if role == "image_option" {
		p.resource(m, "img", path)
	}
	if tag == "person" || tag == "at" {
		if !cardHasName(m) {
			p.explain(path+".人员名称", "名称未提供；仅保留原始人员标识，不查询通讯录")
		}
	}
	if tag == "person_list" || tag == "select_person" || tag == "multi_select_person" {
		p.explain(path+".人员字段语义", "仅返回 ID 的人员项表示名称未提供；未查询通讯录")
	}
	if tag == "markdown" || tag == "lark_md" || tag == "plain_text" || tag == "text" || tag == "md" {
		if versions, ok := m["i18n"].(map[string]any); ok && len(versions) == 0 {
			delete(m, "i18n") // JSON 1.0 readback adds empty translations to text nodes.
		}
		if _, exists := m["content"]; !exists && (tag == "markdown" || tag == "plain_text" || tag == "lark_md") {
			translated := false
			property, _ := object["property"].(map[string]any)
			for _, source := range []map[string]any{object, property} {
				for _, key := range []string{"i18n_content", "i18n_text", "i18n"} {
					if key == "i18n" && role != "header_text" {
						continue
					}
					if versions, ok := source[key].(map[string]any); ok && len(versions) > 0 {
						translated = true
					}
				}
			}
			if !translated {
				status := "partial"
				for _, key := range []string{"text", "elements", "markdownElements"} {
					switch alternate := m[key].(type) {
					case string:
						if alternate != "" {
							status = "unknown"
						}
					case []any:
						if len(alternate) > 0 {
							status = "unknown"
						}
					}
				}
				p.missing(status, path+"：缺少必填 content 原文；仅保留可识别替代内容，完整性未知")
			}
		}
		// A complete source string is authoritative over its parsed Markdown tree.
		// Copies of another component elsewhere are deliberately NOT deduplicated.
		if content, isText := m["content"].(string); isText {
			copies := []string{
				"text", "elements", "children", "md", "markdown", "text_nodes", "parsed_content",
				"content_tree", "markdown_tree", "markdownElements",
			}
			text, hasText := m["text"].(string)
			for _, key := range copies {
				hasCopy := false
				switch representation := m[key].(type) {
				case []any:
					hasCopy = len(representation) > 0
				case map[string]any:
					hasCopy = len(representation) > 0
				case string:
					hasCopy = representation != ""
				}
				if content == "" && hasCopy {
					p.missing("unknown", path+"：空源文与非空内部表示并存，已保留可识别内容，完整性未知")
					// Select one representation within this component. A plain
					// text copy remains usable even when its source is unavailable.
					if hasText && text != "" && key != "text" {
						delete(m, key)
					}
				} else {
					delete(m, key)
				}
			}
		}
		if alternate, exists := m["markdownElements"]; exists {
			if elements, ok := m["elements"].([]any); !ok || len(elements) == 0 {
				m["elements"] = alternate
			}
			delete(m, "markdownElements")
		}
		if tag == "markdown" {
			if _, hasSource := m["content"].(string); !hasSource {
				if _, internal := object["property"]; internal {
					p.missing("unknown", path+"：内部节点未提供完整 Markdown 源文；已保留可识别原文和分组，无法确认转换覆盖范围")
				}
			}
		}
	}
	if tag == "br" || tag == "hr" {
		p.emit(path+".段落结构", tag, false)
	}
	// Semantic order is independent of Go map iteration; arrays retain order.
	order := []string{
		"title", "subtitle", "header", "label", "text", "content", "description", "summary", "alt", "icon",
		"text_tag_list", "fields", "body", "elements", "columns", "actions", "button_area", "buttons",
		"placeholder", "default_value", "initial_option", "initial_options", "initial_date", "initial_time",
		"initial_datetime", "initial_user", "initial_users", "selected_values", "options", "persons",
		"image_list", "images",
		"confirm", "fallback",
	}
	seen := map[string]bool{}
	for i, key := range append(order, cardKeys(m)...) {
		v, exists := m[key]
		if !exists || seen[key] {
			continue
		}
		seen[key] = true
		if key == "action" && (tag == "column_set" || tag == "column") {
			if action, ok := v.(map[string]any); ok {
				if links, exists := action["multi_url"]; exists {
					p.node(links, path+".action.multi_url(链接目标正文未读取)", "links")
				}
			}
		}
		if cardIgnoredField(key) || key == "chart_spec" || (tag == "header" && key == "position") {
			continue
		}
		childPath := path + "." + key
		if !known {
			if key == "elements" || key == "children" || key == "body" || key == "header" {
				p.node(v, childPath, "component")
			} else if child, ok := v.(map[string]any); ok {
				if childTag, _ := child["tag"].(string); cardKnownTag(childTag) {
					fieldPath := fmt.Sprintf("%s.未知属性[%d]", path, i+1)
					p.emit(fieldPath+".属性名", key, false)
					p.node(child, fieldPath, "component")
				}
			}
			continue
		}
		switch key {
		case "schema":
		case "newBody":
			if v != nil {
				p.missing("unknown", childPath+"：存在尚未确认用途的正文表示")
				p.node(v, childPath, "body")
			}
		case "source":
			if role != "card" || v != "json" {
				p.missing("partial", childPath+"：未识别的协议来源字段")
			}
		case "textStyle":
			if style, ok := v.(map[string]any); ok {
				for _, key := range []string{"attributes", "color"} {
					if attribute, exists := style[key]; exists {
						p.emit(childPath+"."+key, attribute, false)
					}
				}
			}
		case "config":
			p.node(v, childPath, "config")
		case "header", "body", "confirm", "button_area":
			if key == "confirm" {
				childPath += "(二次确认提示，是否触发未知)"
			}
			p.node(v, childPath, key)
		case "elements", "actions", "buttons", "columns", "text_tag_list":
			p.node(v, childPath, "component")
		case "img_list", "image_list", "images":
			p.node(v, childPath, "img")
		case "extra":
			p.node(v, childPath, "component")
		case "behaviors":
			behaviors, ok := v.([]any)
			if !ok {
				p.missing("partial", childPath+"：交互配置不是数组")
				continue
			}
			for i, value := range behaviors {
				behavior, ok := value.(map[string]any)
				if !ok {
					p.missing("partial", childPath+"：交互配置不是对象")
					continue
				}
				if behavior["type"] == "open_url" {
					for _, key := range []string{"url", "default_url", "pc_url", "ios_url", "android_url"} {
						if destination, exists := behavior[key]; exists {
							p.emit(fmt.Sprintf("%s[%d].%s(链接目标正文未读取)", childPath, i+1, key), destination, true)
						}
					}
				} else if behavior["type"] != "callback" {
					p.missing("partial", childPath+"：交互类型未识别；未读取载荷或执行动作")
				}
			}
		case "fields":
			p.node(v, childPath, "field")
		case "items":
			if tag == "list" {
				p.node(v, childPath, "list_item")
			} else {
				p.missing("partial", childPath+"：未识别列表结构")
			}
		case "persons":
			p.node(v, childPath, "person")
		case "options":
			optionRole := "option"
			if tag == "select_img" {
				optionRole = "image_option"
			}
			p.node(v, childPath+"(全部候选项，非已选事实)", optionRole)
		case "title", "subtitle", "text", "content", "label", "description", "summary", "alt", "placeholder",
			"hover_tips", "disabled_tips", "tooltip", "hint":
			if key == "content" {
				if _, valid := v.(string); !valid {
					p.missing("partial", childPath+"：文本 content 不是字符串")
				}
			}
			if tag == "config" && key == "summary" {
				if p.body {
					continue
				}
				childPath += "(聊天栏预览摘要，非卡片正文)"
				p.missing("unknown", path+"：聊天栏摘要不能证明正文覆盖范围")
			}
			if key == "placeholder" {
				childPath += "(占位提示，非实际填写值)"
			}
			childRole := "text"
			if tag == "header" && (key == "title" || key == "subtitle") {
				childRole = "header_text"
			}
			p.node(v, childPath, childRole)
			if (tag == "markdown" || tag == "md") && (key == "content" || key == "text") {
				if source, ok := v.(string); ok {
					p.markdownResources(source, childPath)
				}
			}
			if (tag == "markdown" || tag == "lark_md" || tag == "md") && key == "content" {
				if text, ok := v.(string); ok &&
					(strings.Contains(text, "](") || strings.Contains(text, "<img") || strings.Contains(text, "<a ") ||
						strings.Contains(text, "<audio") || strings.Contains(text, "<video")) {
					p.explain(childPath+".关联内容读取范围", "普通链接目标正文和音视频未自动读取；图片实际取得内容与缺失见本轮附件读取结果")
				}
			}
		case "icon":
			iconRole := "icon"
			if tag == "header" {
				iconRole = "custom_icon" // JSON 1.0 header images have no tag.
				if _, exists := m["ud_icon"]; exists {
					childPath += "(备用图片图标，ud_icon优先生效)"
				}
			}
			p.node(v, childPath, iconRole)
		case "ud_icon":
			if tag != "header" {
				p.missing("partial", childPath+"：标题以外的图标字段未识别")
				continue
			}
			p.node(v, childPath, "standard_icon")
			if icon, ok := v.(map[string]any); ok {
				icon = p.localized(icon)
				if style, ok := icon["style"].(map[string]any); ok {
					if color, exists := style["color"]; exists {
						p.emit(childPath+".style.color", color, false)
					}
				}
			}
		case "fallback":
			p.node(v, childPath+"(备选降级分支，是否生效未知)", "fallback")
		case "href":
			if links, ok := v.(map[string]any); ok && (tag == "markdown" || tag == "lark_md" || tag == "md") {
				for i, variable := range cardKeys(links) {
					linkPath := fmt.Sprintf("%s[%d]", childPath, i+1)
					p.emit(linkPath+".链接变量", variable, false)
					p.node(links[variable], linkPath+"(链接目标正文未读取)", "links")
				}
			} else {
				p.node(v, childPath+"(链接目标正文未读取)", "links")
			}
		case "card_link", "multi_url", "url", "android_url", "ios_url", "pc_url", "preview_url":
			p.node(v, childPath+"(链接目标正文未读取)", "links")
		case "value":
			if tag == "option" {
				p.emit(childPath+"(候选项值)", v, true)
			}
		case "name":
			if tag == "person" || cardResourceTag(tag) || tag == "option" {
				p.emit(childPath, v, v != "")
			}
		case "initial_option", "initial_options", "initial_date", "initial_time", "initial_datetime",
			"initial_user", "initial_users", "default_value", "checked", "selected_values":
			p.emit(childPath+"(预填或初始状态，非已提交记录)", v, true)
		case "conditions", "condition", "display_condition", "visible", "visibility", "trigger_conditions":
			p.emit(childPath+"(条件或可见性原值；未求值，分支生效未知)", v, true)
		case "selected", "selected_option", "selected_options", "submitted", "submitted_value", "input_value",
			"status", "state":
			p.emit(childPath+"(卡片返回状态原值；不推断业务已发生)", v, true)
		case "locales", "use_custom_translation":
			// Each emitted field already records these language semantics.
		case "streaming_mode":
			if streaming, ok := v.(bool); !ok {
				p.missing("partial", childPath+"：流式状态不是 boolean，未推断是否结束")
			} else if streaming {
				p.missing("partial", path+"：流式卡片尚未结束")
			}
		case "color", "text_color", "background_color", "template", "token", "img_key", "image_key", "file_key",
			"file_name", "title_text", "user_id", "open_id", "union_id", "user_name", "id", "duration",
			"duration_ms", "unit", "timezone", "format", "date_format", "language", "emoji_type", "disabled",
			"required", "input_type", "max_length", "min", "max", "step", "type", "show_strikethrough", "level":
			if key == "token" && tag != "standard_icon" {
				continue
			}
			if key == "id" && tag != "person" && tag != "at" {
				continue
			}
			if key == "template" && tag != "header" {
				p.missing("partial", childPath+"：卡片模板未展开")
				continue
			}
			p.emit(childPath, v, v != "")
		default:
			p.missing("partial", childPath+"：字段未识别，未推断内容")
		}
	}
}

// Explicit component resource keys identify material bodies. An arbitrary URL,
// Drive token or internal attachment-registry ID is not an IM resource key.
func (p *cardParser) resource(m map[string]any, tag, path string) {
	part := im.MaterialPart{FileSize: -1}
	keys := []string{"file_key"}
	switch tag {
	case "img", "image", "custom_icon":
		part.Type, keys = im.MessageTypeImage, []string{"img_key", "image_key"}
	case "file", "attachment":
		part.Type = im.MessageTypeFile
	default:
		if tag == "audio" || tag == "video" || tag == "media" {
			p.explain(path+".资源读取范围", "音视频资源正文未读取；未新增转录能力")
		}
		return
	}
	for _, key := range keys {
		if value, ok := m[key].(string); ok && value != "" {
			part.FileKey = value
			break
		}
	}
	prefix := "file_"
	if part.Type == im.MessageTypeImage {
		prefix = "img_"
	}
	if !strings.HasPrefix(part.FileKey, prefix) || !feishuSafePathParam(part.FileKey) {
		p.explain(path+".资源读取范围", "资源正文未读取；未提供可直接读取的 IM 图片或文件标识")
		return
	}
	for _, key := range []string{"file_name", "name"} {
		if name, ok := m[key].(string); ok && name != "" {
			part.FileName = name
			break
		}
	}
	p.explain(path+".资源读取范围", "图片或文件正文按附件处理；实际取得内容与缺失见本轮附件读取结果")
	p.result.Parts = append(p.result.Parts, part)
	p.meaning = true
}

// Parse Markdown structure so code examples and nested image alt text never
// create downloads. Feishu supports inline HTML, but explicitly not HTML blocks.
func (p *cardParser) markdownResources(source, path string) {
	if p.ctx.Err() != nil {
		p.missing("partial", "卡片解析已取消或超时")
		return
	}
	if len(source) > 32<<10 {
		p.missing("partial", path+"：Markdown 图片读取范围受 32 KiB 材料额度限制")
		return // Cutting source could remove a code delimiter and create a false image.
	}
	blocks := parser.DefaultBlockParsers()
	for i, block := range blocks {
		if block.Value == parser.NewHTMLBlockParser() {
			blocks = append(blocks[:i], blocks[i+1:]...)
			break
		}
	}
	markdown := parser.NewParser(
		parser.WithBlockParsers(blocks...),
		parser.WithInlineParsers(parser.DefaultInlineParsers()...),
		parser.WithParagraphTransformers(parser.DefaultParagraphTransformers()...),
	)
	if p.markdownTables {
		markdown.AddOptions(
			parser.WithParagraphTransformers(util.Prioritized(extension.NewTableParagraphTransformer(), 200)),
			parser.WithASTTransformers(util.Prioritized(extension.NewTableASTTransformer(), 0)),
		)
	}
	document := markdown.Parse(text.NewReader([]byte(source)))
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if p.ctx.Err() != nil {
			p.missing("partial", "卡片解析已取消或超时")
			return ast.WalkStop, nil
		}
		if img, ok := node.(*ast.Image); ok && entering {
			key := string(util.URLEscape(img.Destination, true))
			if strings.HasPrefix(key, "img_") && feishuSafePathParam(key) {
				p.resource(map[string]any{"img_key": key}, "img", path+".图片")
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
}

func cardHasName(m map[string]any) bool {
	for _, key := range []string{"name", "user_name", "content", "text"} {
		if text, ok := m[key].(string); ok && text != "" {
			return true
		}
	}
	return false
}

func cardKnownTag(tag string) bool {
	switch tag {
	case "card", "config", "body", "header", "field", "option", "links", "icon", "confirm", "button_area",
		"fallback", "fallback_text",
		"div", "plain_text", "text", "lark_md", "markdown", "md", "code_block", "code_span", "heading", "list",
		"list_item", "br", "a", "link", "link_preview", "doc_preview", "doc", "note", "hr", "text_tag",
		"standard_icon", "custom_icon", "emotion",
		"column_set", "column", "form", "interactive_container", "collapsible_panel", "action", "button",
		"overflow", "input", "select_static", "multi_select_static", "select_person", "multi_select_person",
		"date_picker", "picker_time", "picker_datetime", "select_img", "checker",
		"img", "image", "img_combination", "file", "attachment", "audio", "video", "media", "person",
		"person_list", "at", "chart", "table":
		return true
	}
	return false
}

func cardResourceTag(tag string) bool {
	switch tag {
	case "img", "image", "img_combination", "custom_icon", "file", "attachment", "audio", "video", "media",
		"select_img":
		return true
	}
	return false
}

func cardIgnoredField(key string) bool {
	switch key {
	case "tag", "element_id", "component_id", "name_id", "callback", "callbacks", "action", "on_click",
		"on_change", "form_action", "form_action_type", "extra_data", "tracking", "tracking_params",
		"biz_params", "auth", "authorization", "token_data", "textAlign", "convertVersion",
		"enableForwardInteraction", "originTag",
		"padding", "margin", "width", "height", "size", "mode", "layout", "direction", "flex_mode",
		"vertical_spacing", "horizontal_spacing", "horizontal_align", "vertical_align", "text_align",
		"text_size", "weight", "background_style", "border", "corner_radius", "offset", "style", "styles",
		"row_height", "freeze_first_column", "header_style", "page_size", "lines", "expanded", "icon_position",
		"icon_expanded_angle", "label_position", "overall_checkable", "pc_display_rule", "checked_style",
		"opacity", "preview", "scale_type", "compact_width", "width_mode", "enable_forward", "update_multi",
		"enable_forward_interaction", "wide_screen_mode", "streaming_config", "aspect_ratio", "color_theme",
		"rows", "max_rows", "auto_resize", "show_icon", "is_short", "has_more", "has_next", "next_cursor",
		"next_page_token", "page_token", "next_page", "next_url", "total", "total_count", "total_rows",
		"is_finished", "is_complete", "loading", "truncated", "combination_mode":
		return true
	}
	return false
}

func (p *cardParser) continuation(m map[string]any, path string) {
	for _, key := range []string{"has_more", "has_next", "loading", "truncated"} {
		value, exists := m[key]
		if !exists {
			continue
		}
		if active, ok := value.(bool); !ok {
			p.missing("partial", path+"."+key+"：完整性标记不是 boolean，未推断状态")
		} else if active {
			p.missing("partial", path+"."+key+"：返回内容仍有缺失或待续，未调用关联接口")
		}
	}
	for _, key := range []string{"is_finished", "is_complete"} {
		value, exists := m[key]
		if !exists {
			continue
		}
		if finished, ok := value.(bool); !ok {
			p.missing("partial", path+"."+key+"：完整性标记不是 boolean，未推断状态")
		} else if !finished {
			p.missing("partial", path+"."+key+"：返回内容尚未结束")
		}
	}
	for _, key := range []string{"next_cursor", "next_page_token", "next_page", "next_url"} {
		if value, exists := m[key]; exists && value != nil && value != "" && value != false {
			p.missing("partial", path+"."+key+"：存在后续内容提示，未取得其内容或调用关联接口")
		}
	}
}

func (p *cardParser) table(m map[string]any, path string) {
	columns, colOK := m["columns"].([]any)
	rows, rowOK := m["rows"].([]any)
	if !colOK || !rowOK {
		p.missing("partial", path+"：表格 columns 或 rows 缺失/格式错误")
	}
	var names []string
	markdownColumns := make([]bool, len(columns))
	for i, value := range columns {
		column, ok := value.(map[string]any)
		if !ok {
			p.missing("partial", fmt.Sprintf("%s.columns[%d]：列定义不是对象", path, i+1))
			names = append(names, "")
			continue
		}
		column = p.localized(column)
		markdownColumns[i] = column["data_type"] == "markdown"
		name, ok := column["name"].(string)
		if !ok || name == "" {
			p.missing("partial", fmt.Sprintf("%s.columns[%d]：缺少列字段名称", path, i+1))
		}
		names = append(names, name)
		if kind, exists := column["data_type"]; exists {
			switch kind {
			case "text", "lark_md", "markdown", "options", "number", "persons", "date":
			default:
				p.missing("partial", fmt.Sprintf("%s.columns[%d]：列数据类型未识别", path, i+1))
			}
		}
		for _, key := range []string{"name", "display_name", "data_type", "unit", "format", "date_format", "timezone"} {
			if v, exists := column[key]; exists {
				if key == "format" {
					format, ok := v.(map[string]any)
					if !ok {
						p.missing("partial", fmt.Sprintf("%s.columns[%d].format：数字格式不是对象", path, i+1))
						continue
					}
					kept := map[string]any{}
					for _, field := range cardKeys(format) {
						switch field {
						case "symbol", "precision", "separator":
							switch format[field].(type) {
							case map[string]any, []any:
								p.missing("partial", path+"：数字格式字段格式错误")
							default:
								kept[field] = format[field]
							}
						case "auth", "headers", "token", "callback":
						default:
							p.missing("partial", path+"：数字格式字段未识别 "+strconv.Quote(field))
						}
					}
					v = kept
				}
				p.emit(fmt.Sprintf("%s.columns[%d].%s", path, i+1, key), v, v != "")
			}
		}
		if column["data_type"] == "persons" {
			p.explain(fmt.Sprintf("%s.columns[%d].人员语义", path, i+1), "仅 ID 的单元格未提供名称，不查询通讯录")
		}
		if column["data_type"] == "date" {
			p.explain(fmt.Sprintf("%s.columns[%d].日期语义", path, i+1), "Unix 毫秒原值；未返回时区时不推断客户端时区")
		}
		for _, key := range cardKeys(column) {
			switch key {
			case "name", "display_name", "data_type", "unit", "format", "date_format", "timezone":
			case "tooltip", "hint", "description", "hover_tips", "disabled_tips":
				p.node(column[key], fmt.Sprintf("%s.columns[%d].%s", path, i+1, key), "text")
			default:
				if !cardIgnoredField(key) {
					p.missing("partial", fmt.Sprintf("%s.columns[%d].%s：列属性未识别", path, i+1, key))
				}
			}
		}
	}
	for i, value := range rows {
		rowPath := fmt.Sprintf("%s.rows[%d]", path, i+1)
		row, ok := value.(map[string]any)
		if !ok {
			p.missing("partial", rowPath+"：行不是对象，无法匹配列")
			continue
		}
		used := map[string]bool{}
		for col, name := range names {
			if name == "" {
				continue
			}
			cellPath := fmt.Sprintf("%s.column[%d]", rowPath, col+1)
			if v, exists := row[name]; exists {
				p.emit(cellPath, v, v != "")
				if source, ok := v.(string); ok && markdownColumns[col] {
					p.markdownResources(source, cellPath)
				}
			} else {
				p.explain(cellPath+".字段存在性", "字段缺失（不是 0、false、空字符串或 null）")
			}
			used[name] = true
		}
		for i, key := range cardKeys(row) {
			if !used[key] {
				fieldPath := fmt.Sprintf("%s.未匹配列字段[%d]", rowPath, i+1)
				p.emit(fieldPath+".名称", key, false)
				p.emit(fieldPath+".原值", row[key], true)
				p.missing("partial", rowPath+"：含未匹配列定义的字段")
			}
		}
	}
	for _, key := range []string{"total", "total_count", "total_rows"} {
		if value, exists := m[key]; exists {
			number, ok := value.(json.Number)
			if !ok {
				p.missing("partial", path+"."+key+"：声明总量不是 number，未推断数量")
				continue
			}
			p.emit(path+"."+key+"(返回声明的数量)", number, true)
			if count, err := number.Int64(); err != nil || count != int64(len(rows)) {
				p.missing("partial", path+"：返回行数与声明总量不一致或总量无法核对")
			}
		}
	}
	for _, key := range cardKeys(m) {
		if key != "columns" && key != "rows" && !cardIgnoredField(key) {
			p.missing("partial", path+"."+key+"：表格属性未识别")
		}
	}
}

// chart_spec is a VChart data model, not arbitrary card JSON. Preserve its
// explicit data and semantic axes/series, while dropping rendering geometry.
func (p *cardParser) chart(value any, path string) {
	spec, ok := value.(map[string]any)
	if !ok {
		p.missing("partial", path+"：图表定义不是对象")
		return
	}
	p.chartField(spec, path)
	p.explain(path+".数据读取范围", "仅保留卡片内嵌数据及原始字段对应关系；外部数据源未读取，未执行表达式")
}

func (p *cardParser) chartField(value any, path string) {
	if p.ctx.Err() != nil {
		p.missing("partial", "卡片解析已取消或超时")
		return
	}
	switch value := value.(type) {
	case []any:
		for i, child := range value {
			p.chartField(child, fmt.Sprintf("%s[%d]", path, i+1))
		}
	case map[string]any:
		for _, key := range cardKeys(value) {
			childPath := path + "." + key
			switch key {
			case "values":
				// Values are explicit business records, whose arbitrary column
				// names/types belong to the card. Configuration siblings are not.
				if rows, ok := value[key].([]any); ok {
					for i, row := range rows {
						p.emit(fmt.Sprintf("%s[%d]", childPath, i+1), row, true)
					}
				} else {
					p.missing("partial", childPath+"：图表数据记录不是数组")
				}
			case "type", "title", "subtitle", "subtext", "text", "data", "dataId", "dataIndex", "xField",
				"yField", "seriesField", "valueField", "categoryField", "angleField", "radiusField",
				"nameField", "wordField", "sizeField", "textField", "sourceField", "targetField", "stack",
				"percent", "unit", "units", "axes", "legends", "label", "tooltip", "series", "name", "id",
				"url", "data_source", "source", "orient", "visible", "min", "max", "domain", "range", "tick",
				"tickCount", "items", "key", "value", "formatter", "format", "formatMethod", "content",
				"displayName", "precision", "symbol", "separator", "color", "shape", "field", "fields":
				p.chartField(value[key], childPath)
			case "style", "textStyle", "titleStyle", "labelStyle":
				if style, ok := value[key].(map[string]any); ok {
					for _, attribute := range []string{"text", "fill", "stroke", "color"} {
						if content, exists := style[attribute]; exists {
							p.chartField(content, childPath+"."+attribute)
						}
					}
				} else {
					p.missing("partial", childPath+"：图表文字样式不是对象")
				}
			case "padding", "margin", "width", "height", "background", "animation", "animationAppear",
				"animationUpdate", "animationExit", "crosshair", "theme", "direction", "bar", "line", "point",
				"area", "pie", "outerRadius", "innerRadius", "startAngle", "endAngle", "layout", "region",
				"position", "align", "offset", "zIndex":
			case "headers", "auth", "authorization", "token", "credentials", "callback", "callbacks", "events",
				"behaviors":
				// Transport authentication and callbacks are outside card content.
			case "media":
				if branches, ok := value[key].([]any); !ok || len(branches) > 0 {
					p.missing("partial", childPath+"：图表条件展示分支尚未完全识别")
				}
			default:
				p.missing("partial", childPath+"：图表属性未识别，未推断内容")
			}
		}
	default:
		p.emit(path, value, value != "")
	}
}

func (p *cardParser) attachments(value any) {
	groups, ok := value.(map[string]any)
	if !ok {
		p.missing("partial", "json_attachment：资源元信息注册表格式未知")
		return
	}
	for _, group := range cardKeys(groups) {
		if group != "images" && group != "files" && group != "audios" && group != "videos" {
			p.missing("partial", "json_attachment：未识别资源注册表 "+strconv.Quote(group))
			continue
		}
		resources, ok := groups[group].(map[string]any)
		if !ok {
			p.missing("partial", "json_attachment."+group+"：资源映射格式未知")
			continue
		}
		for i, id := range cardKeys(resources) {
			path := fmt.Sprintf("json_attachment.%s[%d]", group, i+1)
			resource, ok := resources[id].(map[string]any)
			if !ok {
				p.missing("partial", path+"：资源元信息不是对象")
				continue
			}
			p.emit(path+".资源引用", id, false)
			for _, key := range cardKeys(resource) {
				switch key {
				case "origin_key", "image_key", "img_key", "file_key", "name", "file_name", "title",
					"description", "alt", "duration", "duration_ms", "url":
					p.emit(path+"."+key+"(资源注册表元信息；正文读取结果见本轮附件)", resource[key], resource[key] != "")
				case "width", "height", "size", "auth", "token":
				default:
					p.missing("partial", path+"."+key+"：资源元信息字段未识别")
				}
			}
		}
	}
}
