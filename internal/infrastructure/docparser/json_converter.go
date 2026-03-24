package docparser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// defaultJSONChunkSize is the target chunk size in bytes for JSON semantic
// splitting. Approximates 512 tokens (1 token ≈ 3-4 bytes for mixed content).
const defaultJSONChunkSize = 1536

// minJSONChunkSize is the minimum chunk size. A new chunk is only started
// when the current chunk has reached at least this size.
var minJSONChunkSize = defaultJSONChunkSize - 200

// jsonToMarkdown converts raw JSON bytes into markdown text
//
// Key properties:
//   - Every output chunk is a **valid JSON object** (not a fragment).
//   - Nested paths from root to leaf are **fully preserved** in each chunk.
//   - Arrays are converted to index-keyed dicts so the algorithm is uniform.
//   - Small objects that fit within maxChunkSize are kept intact (not split).
//   - The output is a series of fenced ```json code blocks separated by \n\n,
//     which the downstream text chunker can split at block boundaries.
func jsonToMarkdown(data []byte) (string, error) {
	data = trimBOM(data)
	if len(data) == 0 {
		return "", fmt.Errorf("empty JSON content")
	}
	if !json.Valid(data) {
		return "", fmt.Errorf("invalid JSON content")
	}

	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Normalize: convert top-level arrays to index-keyed dicts
	normalized := listToDictPreprocess(parsed)

	// If the whole thing fits in one chunk, just format it
	wholeSize := jsonSize(normalized)
	if wholeSize <= defaultJSONChunkSize {
		formatted := formatValue(normalized)
		return wrapCodeBlock(formatted), nil
	}

	// Recursive split
	chunks := recursiveJSONSplit(normalized, nil, nil)

	// Convert each chunk dict to a fenced code block
	blocks := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		if len(chunk) == 0 {
			continue
		}
		blocks = append(blocks, wrapCodeBlock(formatValue(chunk)))
	}

	if len(blocks) == 0 {
		return wrapCodeBlock(formatValue(normalized)), nil
	}
	return strings.Join(blocks, "\n\n"), nil
}

// ---------------------------------------------------------------------------
// RecursiveJsonSplitter core algorithm
// ---------------------------------------------------------------------------

// recursiveJSONSplit splits a JSON dict into a list of JSON dicts,
// each fitting within defaultJSONChunkSize. It preserves the full nested
// path from root to each leaf by using setNestedDict.
//
// This is a Go port of LangChain's RecursiveJsonSplitter._json_split.
func recursiveJSONSplit(
	data interface{},
	currentPath []string,
	chunks []map[string]interface{},
) []map[string]interface{} {
	if chunks == nil {
		chunks = []map[string]interface{}{{}}
	}

	dict, ok := data.(map[string]interface{})
	if !ok {
		// Scalar or already-processed value: place it at the current path
		if len(currentPath) > 0 && len(chunks) > 0 {
			setNestedDict(chunks[len(chunks)-1], currentPath, data)
		}
		return chunks
	}

	// Sort keys for deterministic output
	keys := sortedKeys(dict)

	for _, key := range keys {
		value := dict[key]
		newPath := append(append([]string{}, currentPath...), key)

		// Measure sizes
		chunkSize := jsonSize(chunks[len(chunks)-1])
		itemSize := jsonSize(map[string]interface{}{key: value})
		remaining := defaultJSONChunkSize - chunkSize

		if itemSize <= remaining {
			// Item fits in the current chunk — add it preserving the path
			setNestedDict(chunks[len(chunks)-1], newPath, value)
		} else {
			// Item doesn't fit
			if chunkSize >= minJSONChunkSize {
				// Current chunk is big enough, start a new one
				chunks = append(chunks, map[string]interface{}{})
			}

			// Check if the value itself is a dict/list that can be recursed into
			normalized := listToDictPreprocess(value)
			if subDict, isDict := normalized.(map[string]interface{}); isDict && canSplitDict(subDict) {
				// Recurse into the sub-object
				chunks = recursiveJSONSplit(subDict, newPath, chunks)
			} else {
				// Cannot split further (scalar or single-key dict) — place as-is
				setNestedDict(chunks[len(chunks)-1], newPath, value)
			}
		}
	}

	return chunks
}

// setNestedDict sets a value in a nested dict structure, creating
// intermediate dicts as needed. This preserves the full JSON path.
//
// Example: setNestedDict(d, ["config","db","host"], "localhost")
// produces: {"config": {"db": {"host": "localhost"}}}
func setNestedDict(d map[string]interface{}, path []string, value interface{}) {
	if len(path) == 0 {
		return
	}
	current := d
	for _, key := range path[:len(path)-1] {
		next, ok := current[key]
		if !ok {
			next = map[string]interface{}{}
			current[key] = next
		}
		if nextDict, ok := next.(map[string]interface{}); ok {
			current = nextDict
		} else {
			// Path conflict (existing value is not a dict) — overwrite
			newDict := map[string]interface{}{}
			current[key] = newDict
			current = newDict
		}
	}
	current[path[len(path)-1]] = value
}

// listToDictPreprocess recursively converts JSON arrays to index-keyed
// dicts so the splitter can treat everything uniformly.
//
// Example: ["a","b","c"] → {"0":"a", "1":"b", "2":"c"}
func listToDictPreprocess(data interface{}) interface{} {
	switch v := data.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(v))
		for k, val := range v {
			result[k] = listToDictPreprocess(val)
		}
		return result
	case []interface{}:
		result := make(map[string]interface{}, len(v))
		for i, item := range v {
			result[fmt.Sprintf("%d", i)] = listToDictPreprocess(item)
		}
		return result
	default:
		return data
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// jsonSize returns the serialized JSON byte length of a value.
func jsonSize(v interface{}) int {
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

// formatValue formats a JSON value with indentation.
func formatValue(v interface{}) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b, _ = json.Marshal(v)
	}
	return string(b)
}

// indentJSON formats raw JSON bytes with indentation.
func indentJSON(data []byte) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return string(data), err
	}
	return buf.String(), nil
}

// wrapCodeBlock wraps content in a fenced JSON code block.
func wrapCodeBlock(content string) string {
	return "```json\n" + content + "\n```"
}

// trimBOM removes a UTF-8 BOM prefix if present.
func trimBOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// canSplitDict returns true if a dict can be meaningfully split.
// A dict with multiple keys can be split by distributing keys across chunks.
// A dict with a single key can still be split if its value is a splittable dict.
func canSplitDict(d map[string]interface{}) bool {
	if len(d) > 1 {
		return true
	}
	if len(d) == 1 {
		for _, v := range d {
			if sub, ok := v.(map[string]interface{}); ok && len(sub) > 1 {
				return true
			}
		}
	}
	return false
}

// sortedKeys returns the keys of a map in sorted order.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
