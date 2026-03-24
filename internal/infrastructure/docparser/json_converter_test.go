package docparser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// ═══════════════════════════════════════════════════════════════════════════
// Helper: visual test reporter
// ═══════════════════════════════════════════════════════════════════════════

func visualReport(t *testing.T, label string, input string, result string, err error, checks []checkResult) {
	t.Helper()

	const width = 72
	bar := strings.Repeat("─", width)
	thickBar := strings.Repeat("━", width)

	t.Logf("\n%s", thickBar)
	t.Logf("📋 TEST: %s", label)
	t.Logf("%s", bar)

	// ── Input ──
	inputPreview := input
	if len(inputPreview) > 200 {
		inputPreview = inputPreview[:200] + fmt.Sprintf("... (%d bytes total)", len(input))
	}
	t.Logf("📥 INPUT (%d bytes):", len(input))
	for _, line := range strings.Split(inputPreview, "\n") {
		t.Logf("   %s", line)
	}
	t.Logf("%s", bar)

	// ── Output / Error ──
	if err != nil {
		t.Logf("❌ ERROR: %v", err)
	} else {
		blockCount := strings.Count(result, "```json")
		totalLen := len(result)
		t.Logf("📤 OUTPUT: %d code block(s), %d bytes total", blockCount, totalLen)
		t.Logf("%s", bar)

		blocks := splitBlocks(result)
		for i, blk := range blocks {
			preview := blk
			lines := strings.Split(preview, "\n")
			if len(lines) > 10 {
				preview = strings.Join(lines[:4], "\n") +
					fmt.Sprintf("\n      ... (%d lines omitted) ...\n", len(lines)-8) +
					strings.Join(lines[len(lines)-4:], "\n")
			}
			t.Logf("   📦 Block #%d (%d bytes):", i+1, len(blk))
			for _, line := range strings.Split(preview, "\n") {
				t.Logf("   │ %s", line)
			}
		}
	}
	t.Logf("%s", bar)

	// ── Checks ──
	allPass := true
	for _, c := range checks {
		icon := "✅"
		if !c.pass {
			icon = "💥"
			allPass = false
		}
		t.Logf("%s %s", icon, c.desc)
		if !c.pass {
			t.Errorf("FAIL: %s", c.desc)
		}
	}
	if allPass {
		t.Logf("🎉 ALL CHECKS PASSED")
	}
	t.Logf("%s\n", thickBar)
}

// splitBlocks extracts the content between ```json and ``` fences.
func splitBlocks(result string) []string {
	var blocks []string
	rest := result
	for {
		start := strings.Index(rest, "```json\n")
		if start < 0 {
			break
		}
		rest = rest[start+len("```json\n"):]
		end := strings.Index(rest, "\n```")
		if end < 0 {
			blocks = append(blocks, rest)
			break
		}
		blocks = append(blocks, rest[:end])
		rest = rest[end+len("\n```"):]
	}
	return blocks
}

type checkResult struct {
	desc string
	pass bool
}

func check(desc string, pass bool) checkResult { return checkResult{desc, pass} }

// blockIsValidJSON checks that each code block inside the result is valid JSON.
func allBlocksValidJSON(result string) bool {
	for _, blk := range splitBlocks(result) {
		if !json.Valid([]byte(blk)) {
			return false
		}
	}
	return true
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 1: Small inputs — kept intact, NOT split
// ═══════════════════════════════════════════════════════════════════════════

func TestJsonToMarkdown_SmallObject(t *testing.T) {
	input := `{"name": "test", "version": "1.0"}`
	result, err := jsonToMarkdown([]byte(input))
	blocks := strings.Count(result, "```json")
	visualReport(t, "Small Object → single block, kept intact", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check("contains key 'name'", strings.Contains(result, `"name"`)),
		check("contains key 'version'", strings.Contains(result, `"version"`)),
		check(fmt.Sprintf("exactly 1 block (got %d)", blocks), blocks == 1),
		check("both keys in SAME block (not split apart)",
			strings.Contains(result, `"name"`) && strings.Contains(result, `"version"`) && blocks == 1),
	})
}

func TestJsonToMarkdown_SmallArray(t *testing.T) {
	input := `[{"id": 1, "name": "a"}, {"id": 2, "name": "b"}]`
	result, err := jsonToMarkdown([]byte(input))
	blocks := strings.Count(result, "```json")
	visualReport(t, "Small Array → single block, kept intact", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("exactly 1 block (got %d)", blocks), blocks == 1),
	})
}

func TestJsonToMarkdown_EmptyObject(t *testing.T) {
	input := `{}`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Empty Object {}", input, result, err, []checkResult{
		check("no error", err == nil),
		check("contains '{}'", strings.Contains(result, "{}")),
	})
}

func TestJsonToMarkdown_EmptyArray(t *testing.T) {
	input := `[]`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Empty Array []", input, result, err, []checkResult{
		check("no error", err == nil),
		check("contains '{}'", strings.Contains(result, "{}")), // [] → {} after list-to-dict
	})
}

func TestJsonToMarkdown_PrimitiveString(t *testing.T) {
	input := `"hello world"`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Primitive String", input, result, err, []checkResult{
		check("no error", err == nil),
		check("contains 'hello world'", strings.Contains(result, "hello world")),
	})
}

func TestJsonToMarkdown_PrimitiveNumber(t *testing.T) {
	input := `42`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Primitive Number", input, result, err, []checkResult{
		check("no error", err == nil),
		check("contains '42'", strings.Contains(result, "42")),
	})
}

func TestJsonToMarkdown_PrimitiveBoolean(t *testing.T) {
	input := `true`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Primitive Boolean", input, result, err, []checkResult{
		check("no error", err == nil),
		check("contains 'true'", strings.Contains(result, "true")),
	})
}

func TestJsonToMarkdown_PrimitiveNull(t *testing.T) {
	input := `null`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Primitive Null", input, result, err, []checkResult{
		check("no error", err == nil),
		check("contains 'null'", strings.Contains(result, "null")),
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 2: Large data — smart recursive splitting
// ═══════════════════════════════════════════════════════════════════════════

func TestJsonToMarkdown_LargeObject(t *testing.T) {
	obj := make(map[string]interface{})
	for i := 0; i < 50; i++ {
		obj[fmt.Sprintf("key_%02d", i)] = strings.Repeat("value", 50)
	}
	data, _ := json.Marshal(obj)

	result, err := jsonToMarkdown(data)
	blocks := strings.Count(result, "```json")
	visualReport(t, "Large Object (50 keys) → multiple blocks", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("multiple blocks (got %d)", blocks), blocks >= 2),
	})
}

func TestJsonToMarkdown_LargeArray(t *testing.T) {
	arr := make([]interface{}, 100)
	for i := range arr {
		arr[i] = map[string]interface{}{
			"id":          i,
			"name":        strings.Repeat("name", 20),
			"description": strings.Repeat("desc", 30),
		}
	}
	data, _ := json.Marshal(arr)

	result, err := jsonToMarkdown(data)
	blocks := strings.Count(result, "```json")
	visualReport(t, "Large Array (100 elements) → multiple blocks", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("multiple blocks (got %d)", blocks), blocks >= 2),
	})
}

func TestJsonToMarkdown_RecursiveSplitLargeKey(t *testing.T) {
	inner := make(map[string]interface{})
	for i := 0; i < 30; i++ {
		inner[fmt.Sprintf("sub_%02d", i)] = strings.Repeat("val", 50)
	}
	obj := map[string]interface{}{"bigkey": inner}
	data, _ := json.Marshal(obj)

	result, err := jsonToMarkdown(data)
	blocks := strings.Count(result, "```json")
	visualReport(t, "Recursive Split: 1 big key with 30 sub-keys", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("multiple blocks (got %d)", blocks), blocks >= 2),
		check("path preserved: chunks contain 'bigkey'", strings.Contains(result, "bigkey")),
	})
}

func TestJsonToMarkdown_PathPreservation(t *testing.T) {
	// Core test: verify that nested paths are preserved in each chunk
	inner := make(map[string]interface{})
	for i := 0; i < 40; i++ {
		inner[fmt.Sprintf("field_%02d", i)] = strings.Repeat("data", 60)
	}
	obj := map[string]interface{}{
		"config": map[string]interface{}{
			"database": inner,
		},
	}
	data, _ := json.Marshal(obj)

	result, err := jsonToMarkdown(data)
	blocks := splitBlocks(result)
	// Every block should contain the full path "config" → "database"
	allHavePath := true
	for _, blk := range blocks {
		if !strings.Contains(blk, `"config"`) || !strings.Contains(blk, `"database"`) {
			allHavePath = false
			break
		}
	}
	visualReport(t, "Path Preservation: config.database.* across chunks", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("multiple blocks (got %d)", len(blocks)), len(blocks) >= 2),
		check("ALL blocks preserve path: config → database", allHavePath),
	})
}

func TestJsonToMarkdown_MixedArrayElements(t *testing.T) {
	arr := []interface{}{
		map[string]interface{}{"id": 1},
		map[string]interface{}{"id": 2},
		map[string]interface{}{"id": 3, "data": strings.Repeat("x", 2000)},
		map[string]interface{}{"id": 4},
	}
	data, _ := json.Marshal(arr)

	result, err := jsonToMarkdown(data)
	blocks := strings.Count(result, "```json")
	visualReport(t, "Mixed Array: 3 small + 1 large element", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("at least 2 blocks (got %d)", blocks), blocks >= 2),
	})
}

func TestJsonToMarkdown_DeepNested(t *testing.T) {
	input := `{"l1": {"l2": {"l3": {"data": "` + strings.Repeat("x", 2000) + `"}}}}`
	result, err := jsonToMarkdown([]byte(input))
	blocks := strings.Count(result, "```json")
	visualReport(t, "Deep Nested (3 levels, large leaf)", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("block count ≥ 1 (got %d)", blocks), blocks >= 1),
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 3: Error Handling
// ═══════════════════════════════════════════════════════════════════════════

func TestJsonToMarkdown_InvalidJSON(t *testing.T) {
	input := `{invalid json}`
	_, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Invalid JSON → error", input, "", err, []checkResult{
		check("returns error", err != nil),
		check("error mentions 'invalid JSON'",
			err != nil && strings.Contains(err.Error(), "invalid JSON")),
	})
}

func TestJsonToMarkdown_EmptyInput(t *testing.T) {
	input := ""
	_, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Empty Input → error", input, "", err, []checkResult{
		check("returns error", err != nil),
		check("error mentions 'empty'",
			err != nil && strings.Contains(err.Error(), "empty")),
	})
}

func TestJsonToMarkdown_WhitespaceOnly(t *testing.T) {
	input := "   \n\t  "
	_, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Whitespace-Only Input → error", input, "", err, []checkResult{
		check("returns error", err != nil),
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 4: Edge Cases & Encoding
// ═══════════════════════════════════════════════════════════════════════════

func TestJsonToMarkdown_BOM(t *testing.T) {
	raw := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"key": "value"}`)...)
	result, err := jsonToMarkdown(raw)
	visualReport(t, "UTF-8 BOM prefix → stripped", string(raw), result, err, []checkResult{
		check("no error", err == nil),
		check("contains key after BOM removal", strings.Contains(result, `"key"`)),
	})
}

func TestJsonToMarkdown_UnicodeContent(t *testing.T) {
	input := `{"名称": "WeKnora 知识库", "描述": "支持中文 JSON 🎉", "emoji": "🚀"}`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Unicode / Chinese / Emoji", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check("contains Chinese", strings.Contains(result, "知识库")),
		check("contains emoji", strings.Contains(result, "🚀")),
	})
}

func TestJsonToMarkdown_SpecialCharsInStrings(t *testing.T) {
	input := `{"html": "<div class=\"test\">hello</div>", "path": "C:\\Users\\test", "newline": "line1\nline2"}`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Special characters (HTML, backslash, newline)", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
	})
}

func TestJsonToMarkdown_NullValues(t *testing.T) {
	input := `{"name": "test", "value": null, "list": [null, 1, null]}`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Object with null values", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check("contains 'null'", strings.Contains(result, "null")),
	})
}

func TestJsonToMarkdown_MixedValueTypes(t *testing.T) {
	input := `{"str": "hello", "num": 42, "float": 3.14, "bool": true, "null_val": null, "arr": [1,2], "obj": {"a":1}}`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Mixed value types in one object", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check("contains string", strings.Contains(result, "hello")),
		check("contains number", strings.Contains(result, "42")),
		check("contains float", strings.Contains(result, "3.14")),
	})
}

func TestJsonToMarkdown_NestedArrayOfArrays(t *testing.T) {
	input := `[[1, 2, 3], [4, 5, 6], [7, 8, 9]]`
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Nested Array of Arrays (matrix)", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
	})
}

func TestJsonToMarkdown_LargePrimitiveString(t *testing.T) {
	bigStr := strings.Repeat("abcdefghij", 300)
	input := fmt.Sprintf(`"%s"`, bigStr)
	result, err := jsonToMarkdown([]byte(input))
	visualReport(t, "Large Primitive String (3000 chars) → fallback", input, result, err, []checkResult{
		check("no error", err == nil),
		check("content preserved", strings.Contains(result, "abcdefghij")),
	})
}

func TestJsonToMarkdown_SingleKeyObject(t *testing.T) {
	input := `{"only_key": "only_value"}`
	result, err := jsonToMarkdown([]byte(input))
	blocks := strings.Count(result, "```json")
	visualReport(t, "Single-Key Object", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("exactly 1 block (got %d)", blocks), blocks == 1),
	})
}

func TestJsonToMarkdown_SingleElementArray(t *testing.T) {
	input := `[{"id": 1, "name": "solo"}]`
	result, err := jsonToMarkdown([]byte(input))
	blocks := strings.Count(result, "```json")
	visualReport(t, "Single-Element Array", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("exactly 1 block (got %d)", blocks), blocks == 1),
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 5: Realistic Scenarios
// ═══════════════════════════════════════════════════════════════════════════

func TestJsonToMarkdown_RealisticConfig(t *testing.T) {
	input := `{
  "app": {"name": "WeKnora", "version": "2.0.0", "debug": false},
  "database": {"host": "localhost", "port": 5432, "name": "weknora_db", "pool_size": 10},
  "redis": {"host": "localhost", "port": 6379},
  "features": {"json_upload": true, "multimodel": true, "graph_extraction": false}
}`
	result, err := jsonToMarkdown([]byte(input))
	blocks := strings.Count(result, "```json")
	visualReport(t, "Realistic: App Config (fits in 1 block)", input, result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check("contains 'WeKnora'", strings.Contains(result, "WeKnora")),
		check("contains 'database'", strings.Contains(result, "database")),
		check(fmt.Sprintf("single block (fits, got %d)", blocks), blocks == 1),
	})
}

func TestJsonToMarkdown_RealisticAPIResponse(t *testing.T) {
	users := make([]interface{}, 40)
	for i := range users {
		users[i] = map[string]interface{}{
			"id":       i + 1,
			"username": fmt.Sprintf("user_%03d", i+1),
			"email":    fmt.Sprintf("user%03d@example.com", i+1),
			"role":     "editor",
			"active":   i%3 != 0,
		}
	}
	apiResp := map[string]interface{}{
		"status": "ok",
		"total":  40,
		"page":   1,
		"data":   users,
	}
	data, _ := json.Marshal(apiResp)

	result, err := jsonToMarkdown(data)
	blocks := strings.Count(result, "```json")
	visualReport(t, "Realistic: API Response (40 user records)", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check("contains user data", strings.Contains(result, "user_001")),
		check(fmt.Sprintf("multiple blocks (got %d)", blocks), blocks >= 2),
	})
}

func TestJsonToMarkdown_RealisticLargeNestedConfig(t *testing.T) {
	// A config where one section is huge and must be recursively split,
	// while other sections are small
	rules := make(map[string]interface{})
	for i := 0; i < 30; i++ {
		rules[fmt.Sprintf("rule_%02d", i)] = map[string]interface{}{
			"pattern": strings.Repeat("pattern", 10),
			"action":  "allow",
			"priority": i,
		}
	}
	config := map[string]interface{}{
		"version": "3.0",
		"metadata": map[string]interface{}{"author": "admin", "updated": "2026-03-24"},
		"firewall": map[string]interface{}{
			"enabled": true,
			"rules":   rules,
		},
	}
	data, _ := json.Marshal(config)

	result, err := jsonToMarkdown(data)
	blocks := splitBlocks(result)
	// Each block should still have the path "firewall" → "rules" where applicable
	visualReport(t, "Realistic: Nested config with 30 firewall rules", string(data), result, err, []checkResult{
		check("no error", err == nil),
		check("all blocks are valid JSON", allBlocksValidJSON(result)),
		check(fmt.Sprintf("multiple blocks needed (got %d)", len(blocks)), len(blocks) >= 2),
		check("contains 'firewall'", strings.Contains(result, "firewall")),
		check("contains 'version'", strings.Contains(result, "version")),
	})
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 6: Integration — SimpleFormatReader
// ═══════════════════════════════════════════════════════════════════════════

func TestSimpleFormatReader_JSON(t *testing.T) {
	reader := &SimpleFormatReader{}
	input := `{"test": "integration", "count": 42}`
	req := &types.ReadRequest{
		FileName:    "config.json",
		FileType:    "json",
		FileContent: []byte(input),
	}

	result, err := reader.Read(context.Background(), req)

	var resultStr string
	if result != nil {
		resultStr = result.MarkdownContent
	}
	visualReport(t, "SimpleFormatReader.Read() with .json file", input, resultStr, err, []checkResult{
		check("no error", err == nil),
		check("result not nil", result != nil),
		check("markdown contains 'test'",
			result != nil && strings.Contains(result.MarkdownContent, "test")),
		check("markdown has code block",
			result != nil && strings.Contains(result.MarkdownContent, "```json")),
		check("output is valid JSON inside block",
			result != nil && allBlocksValidJSON(result.MarkdownContent)),
	})
}

func TestSimpleFormatReader_JSON_Invalid(t *testing.T) {
	reader := &SimpleFormatReader{}
	req := &types.ReadRequest{
		FileName:    "bad.json",
		FileType:    "json",
		FileContent: []byte(`{not valid json}`),
	}

	_, err := reader.Read(context.Background(), req)
	visualReport(t, "SimpleFormatReader with invalid JSON → error", `{not valid json}`, "", err, []checkResult{
		check("returns error", err != nil),
		check("error mentions 'json'",
			err != nil && strings.Contains(strings.ToLower(err.Error()), "json")),
	})
}

func TestIsSimpleFormat_JSON(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"json", true}, {"JSON", true}, {".json", true}, {"Json", true},
		{"txt", true}, {"csv", true},
		{"pdf", false}, {"docx", false},
	}

	t.Logf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	t.Logf("📋 TEST: IsSimpleFormat() recognizes JSON")
	t.Logf("─────────────────────────────────────────────────────────")
	allPass := true
	for _, tc := range cases {
		got := IsSimpleFormat(tc.input)
		icon := "✅"
		if got != tc.want {
			icon = "💥"
			allPass = false
			t.Errorf("IsSimpleFormat(%q) = %v, want %v", tc.input, got, tc.want)
		}
		t.Logf("%s IsSimpleFormat(%q) = %v  (want %v)", icon, tc.input, got, tc.want)
	}
	if allPass {
		t.Logf("🎉 ALL CHECKS PASSED")
	}
	t.Logf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

// ═══════════════════════════════════════════════════════════════════════════
// Group 7: list-to-dict preprocessing
// ═══════════════════════════════════════════════════════════════════════════

func TestListToDictPreprocess(t *testing.T) {
	// Simulate what json.Unmarshal produces: numbers become float64
	input := []interface{}{"a", "b", map[string]interface{}{"nested": []interface{}{float64(1), float64(2)}}}
	result := listToDictPreprocess(input)

	dict, ok := result.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map, got %T", result)
	}

	t.Logf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	t.Logf("📋 TEST: listToDictPreprocess")
	t.Logf("─────────────────────────────────────────────────────────")
	t.Logf("📥 INPUT:  [\"a\", \"b\", {\"nested\": [1, 2]}]")
	t.Logf("📤 OUTPUT: %s", formatValue(dict))
	t.Logf("─────────────────────────────────────────────────────────")

	checks := []checkResult{
		check("top level is dict", ok),
		check(`key "0" = "a"`, dict["0"] == "a"),
		check(`key "1" = "b"`, dict["1"] == "b"),
	}
	// Check nested array also converted
	if nested, ok2 := dict["2"].(map[string]interface{}); ok2 {
		if innerNested, ok3 := nested["nested"].(map[string]interface{}); ok3 {
			checks = append(checks, check(`nested array converted to dict`, innerNested["0"] == float64(1)))
		} else {
			checks = append(checks, check(`nested array converted to dict`, false))
		}
	}
	allPass := true
	for _, c := range checks {
		icon := "✅"
		if !c.pass {
			icon = "💥"
			allPass = false
			t.Errorf("FAIL: %s", c.desc)
		}
		t.Logf("%s %s", icon, c.desc)
	}
	if allPass {
		t.Logf("🎉 ALL CHECKS PASSED")
	}
	t.Logf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}

func TestSetNestedDict(t *testing.T) {
	d := map[string]interface{}{}
	setNestedDict(d, []string{"config", "database", "host"}, "localhost")

	t.Logf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	t.Logf("📋 TEST: setNestedDict path preservation")
	t.Logf("─────────────────────────────────────────────────────────")
	t.Logf("📥 path:  [\"config\", \"database\", \"host\"]")
	t.Logf("📥 value: \"localhost\"")
	t.Logf("📤 result: %s", formatValue(d))
	t.Logf("─────────────────────────────────────────────────────────")

	// Verify the structure
	config, ok1 := d["config"].(map[string]interface{})
	db, ok2 := map[string]interface{}{}, false
	host := ""
	if ok1 {
		db, ok2 = config["database"].(map[string]interface{})
	}
	if ok2 {
		host, _ = db["host"].(string)
	}

	checks := []checkResult{
		check(`d["config"] is dict`, ok1),
		check(`d["config"]["database"] is dict`, ok2),
		check(`d["config"]["database"]["host"] = "localhost"`, host == "localhost"),
	}
	allPass := true
	for _, c := range checks {
		icon := "✅"
		if !c.pass {
			icon = "💥"
			allPass = false
			t.Errorf("FAIL: %s", c.desc)
		}
		t.Logf("%s %s", icon, c.desc)
	}
	if allPass {
		t.Logf("🎉 ALL CHECKS PASSED")
	}
	t.Logf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
}
