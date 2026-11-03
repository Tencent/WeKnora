package utils

import (
	"net/http"
	"regexp"
	"sort"
	"strings"
)

// reservedHeaderKeys 列出不允许被用户自定义头覆盖的关键请求头。
// 这些头由各 provider 的签名、鉴权或 SSE 流程控制，覆盖后可能直接导致调用失败。
var reservedHeaderKeys = map[string]struct{}{
	"authorization":     {},
	"api-key":           {},
	"x-api-key":         {},
	"x-goog-api-key":    {},
	"content-type":      {},
	"content-length":    {},
	"accept-encoding":   {},
	"host":              {},
	"connection":        {},
	"transfer-encoding": {},
}

// IsReservedHeader 判断某个 header key 是否为保留 header，保留 header 不允许被自定义头覆盖。
func IsReservedHeader(key string) bool {
	_, ok := reservedHeaderKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// ApplyCustomHeaders 将用户自定义的 header 写入 http.Request。
// 保留 header（Authorization、api-key、Content-Type 等）会被跳过以避免破坏鉴权/签名。
// 其它 header 会直接覆盖同名条目，允许用户替换默认值（例如 Accept）。
func ApplyCustomHeaders(req *http.Request, headers map[string]string) {
	if req == nil || len(headers) == 0 {
		return
	}
	for k, v := range headers {
		name := strings.TrimSpace(k)
		if name == "" {
			continue
		}
		if IsReservedHeader(name) {
			continue
		}
		req.Header.Set(name, v)
	}
}

// headerVarPattern 匹配自定义请求头值里的 ${name} 动态占位符。
// 名称限定为字母或下划线开头，这样正文里的 "${金额}" 之类不会被误当成变量。
var headerVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ResolveHeaderTemplates 解析自定义请求头值中的 ${var} 占位符。
//
// 规则：
//   - 命中 vars 的占位符替换为对应取值；替换进来的文本不再二次展开，
//     所以取值里出现 ${...} 也只是普通文本。
//   - 未命中的占位符替换为空字符串。上下文里没有的身份如果原样发出
//     "${user_name}"，企业网关会把它当成一个真的叫 ${user_name} 的用户名，
//     排查起来比空值更难。
//   - 解析结果会去掉 CR/LF。占位符的取值来自用户资料，不能让它顺带
//     变成一个新的请求头（header 注入）。
//
// 返回新 map，不修改入参。第二个返回值是未能解析的占位符名称（去重、排序），
// 供调用方记录日志——一个变量取不到值通常是上下文里没有对应身份，
// 而不是配置写错了，所以这里不当作错误。
func ResolveHeaderTemplates(headers map[string]string, vars map[string]string) (map[string]string, []string) {
	if len(headers) == 0 {
		return nil, nil
	}
	resolved := make(map[string]string, len(headers))
	var unresolved []string
	seen := make(map[string]struct{})
	for name, value := range headers {
		resolved[name] = resolveHeaderTemplate(value, vars, func(missing string) {
			if _, ok := seen[missing]; ok {
				return
			}
			seen[missing] = struct{}{}
			unresolved = append(unresolved, missing)
		})
	}
	sort.Strings(unresolved)
	return resolved, unresolved
}

// resolveHeaderTemplate 解析单个 header 值。onMissing 在遇到取不到值的
// 占位符时被调用一次，参数是变量名。
func resolveHeaderTemplate(value string, vars map[string]string, onMissing func(string)) string {
	if !strings.Contains(value, "${") {
		return sanitizeHeaderValue(value)
	}
	return sanitizeHeaderValue(headerVarPattern.ReplaceAllStringFunc(value, func(match string) string {
		name := match[2 : len(match)-1]
		v, ok := vars[name]
		if !ok {
			onMissing(name)
			return ""
		}
		return v
	}))
}

// sanitizeHeaderValue 去掉 header 值里的 CR/LF。HTTP 头不允许换行，
// 而占位符的取值可能来自用户资料。
func sanitizeHeaderValue(value string) string {
	if !strings.ContainsAny(value, "\r\n") {
		return value
	}
	return strings.NewReplacer("\r", "", "\n", "").Replace(value)
}
