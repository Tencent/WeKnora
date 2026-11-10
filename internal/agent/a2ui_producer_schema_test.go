// Package agent holds the agent runtime. This file pins the official A2UI
// v0.9 producer schema that the prompt teaches the model to emit.
package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// jsonUnmarshalStrict decodes a strict JSON document (rejects trailing bytes)
// into v. Used to parse the producer frame array out of the guidance text.
func jsonUnmarshalStrict(raw string, v any) error {
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("invalid character after top-level value")
	}
	return nil
}

// loadJSONFile reads a JSON file into an interface{} tree.
func loadJSONFile(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// a2uiProducerSchemaTest is the producer-side core acceptance: the
// offline realization of the "message compliance" and "external client
// consumption" acceptance rows.
//
// It breaks the self-produces/self-consumes same-source trap: instead of
// validating WeKnora's own frames with WeKnora's own parser
// (ExtractA2UIMessages), it extracts the exact JSON frame the model is taught
// to emit and validates it against the official A2UI v0.9 JSON Schema
// published by Google. The official schema is an independent frame of
// reference: the moment a frame we produce deviates from the official spec
// this test turns red, regardless of whether our own parser accepts it.
//
// The schema files are vendored under ../types/testdata/a2ui/, taken from
// specification/v0_9/ (main branch) of the official a2ui-project/a2ui
// repository; all of them declare v0.9.

const a2uiSpecTestdata = "../types/testdata/a2ui"

// a2uiSpecLoader maps every absolute a2ui.org $ref that appears in the
// official schema — and every URL variant a relative $ref resolves to — onto
// the local vendored copy by filename suffix, keeping validation entirely
// offline. Any ref that is not recognized falls through to FileLoader (which
// only accepts file://), so it fails loudly instead of being skipped silently.
type a2uiSpecLoader struct {
	file jsonschema.FileLoader
}

var a2uiSpecFilenames = []string{
	"server_to_client.json",
	"client_to_server.json",
	"catalog.json",
	"common_types.json",
}

func (l *a2uiSpecLoader) Load(rawURL string) (any, error) {
	for _, name := range a2uiSpecFilenames {
		if strings.HasSuffix(rawURL, name) {
			f, err := os.Open(filepath.Join(a2uiSpecTestdata, name))
			if err != nil {
				return nil, err
			}
			defer func() { _ = f.Close() }()
			return jsonschema.UnmarshalJSON(f)
		}
	}
	return l.file.Load(rawURL)
}

// compileA2UISchema compiles the official A2UI schema rooted at the given
// vendored filename (e.g. "server_to_client.json"), resolving every $ref via
// the offline spec loader.
func compileA2UISchema(t *testing.T, rootFile string) *jsonschema.Schema {
	t.Helper()
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(&a2uiSpecLoader{})
	s, err := c.Compile(filepath.Join(a2uiSpecTestdata, rootFile))
	if err != nil {
		t.Fatalf("compile %s: %v", rootFile, err)
	}
	return s
}

// extractFirstJSONArray returns the first balanced top-level JSON array in s,
// doing a bracket-depth walk that is quote-aware (string literals cannot nest
// brackets in the frames the model is taught to emit).
func extractFirstJSONArray(s string) string {
	start := strings.Index(s, "[")
	if start < 0 {
		return ""
	}
	depth := 0
	inStr := false
	for i := start; i < len(s); i++ {
		switch ch := s[i]; ch {
		case '"':
			if !inStr {
				inStr = true
			} else if i > 0 && s[i-1] != '\\' {
				inStr = false
			}
		case '[':
			if !inStr {
				depth++
			}
		case ']':
			if !inStr {
				depth--
				if depth == 0 {
					return s[start : i+1]
				}
			}
		}
	}
	return ""
}

// extractJSONArrays returns every balanced top-level JSON array in s. It
// re-scans from just past the end of the previous array, so the prose between
// the two taught example cards is never scanned as candidate array content —
// which is what makes bracket-free prose safe to interleave.
func extractJSONArrays(s string) []string {
	var out []string
	for {
		start := strings.Index(s, "[")
		if start < 0 {
			return out
		}
		depth := 0
		inStr := false
		for i := start; i < len(s); i++ {
			switch ch := s[i]; ch {
			case '"':
				if !inStr {
					inStr = true
				} else if i > 0 && s[i-1] != '\\' {
					inStr = false
				}
			case '[':
				if !inStr {
					depth++
				}
			case ']':
				if !inStr {
					depth--
					if depth == 0 {
						out = append(out, s[start:i+1])
						s = s[i+1:]
						i = len(s) // break the for by advancing past end
					}
				}
			}
			if depth == 0 && i >= start {
				break
			}
		}
	}
}

// TestA2UIProducerFramesValidateAgainstOfficialSchema is the core acceptance
// check: it extracts, verbatim, the JSON frame that agentA2UIGuidance()
// teaches the model to emit (extractFirstJSONArray takes the array by bracket
// depth only, independent of the outer wrapper form — the teaching text now
// uses the official <a2ui-json> tags, but a fence form would be extracted the
// same way), then validates every frame against the official server_to_client
// schema. Any output that violates the official v0.9 spec (wrong field names,
// missing required fields, illegal components) surfaces here: the items of
// updateComponents.components are catalog.json#/$defs/anyComponent, so every
// taught component is simultaneously validated field by field against the
// official basic catalog (including its additionalProperties: false "no extra
// keys" constraint).
func TestA2UIProducerFramesValidateAgainstOfficialSchema(t *testing.T) {
	guidance := agentA2UIGuidance()
	arrays := extractJSONArrays(guidance)
	if len(arrays) < 2 {
		t.Fatalf("expected >=2 JSON arrays (noop example + form example), got %d", len(arrays))
	}

	s2c := compileA2UISchema(t, "server_to_client.json")
	for arrIdx, arrayJSON := range arrays {
		var frames []map[string]any
		if err := jsonUnmarshalStrict(arrayJSON, &frames); err != nil {
			t.Fatalf("guidance A2UI array %d is not valid JSON: %v\n%s", arrIdx, err, arrayJSON)
		}
		if len(frames) < 2 {
			t.Fatalf("array %d: expected >=2 frames (createSurface + updateComponents), got %d", arrIdx, len(frames))
		}
		for i, frame := range frames {
			if err := s2c.Validate(frame); err != nil {
				t.Errorf("array %d, frame %d fails official server_to_client schema:\n%v\nframe: %v",
					arrIdx, i, err, frame)
			}
		}
	}
}

// TestA2UIReflowedActionValidatesAgainstOfficialSchema validates that the
// action message the client sends back to the server after a button click
// conforms to the official client_to_server schema — the protocol conformance
// of the button-reflow leg of "external client consumption".
func TestA2UIReflowedActionValidatesAgainstOfficialSchema(t *testing.T) {
	action := map[string]any{
		"version": "v0.9",
		"action": map[string]any{
			"name":              "noop",
			"surfaceId":         "s1",
			"sourceComponentId": "b1",
			"timestamp":         "2026-11-04T12:00:00Z",
			"context":           map[string]any{},
		},
	}
	c2s := compileA2UISchema(t, "client_to_server.json")
	if err := c2s.Validate(action); err != nil {
		t.Fatalf("reflowed action message fails official client_to_server schema: %v", err)
	}
}

// TestA2UITaughtComponentsExistInBasicCatalog asserts that every component
// type used in the teaching genuinely exists in the official basic catalog's
// component enumeration, preventing the teaching from naming a component that
// the catalog never defines (a "ghost component" no renderer can map).
func TestA2UITaughtComponentsExistInBasicCatalog(t *testing.T) {
	catalogDoc, err := loadJSONFile(filepath.Join(a2uiSpecTestdata, "catalog.json"))
	if err != nil {
		t.Fatalf("load catalog.json: %v", err)
	}
	root, ok := catalogDoc.(map[string]any)
	if !ok {
		t.Fatal("catalog.json is not a JSON object")
	}
	comps, ok := root["components"].(map[string]any)
	if !ok {
		t.Fatal("catalog.json has no components map")
	}
	taught := []string{
		"Card", "Column", "Row", "Text", "Button", "TextField",
		"CheckBox", "ChoicePicker", "Slider", "DateTimeInput",
	}
	for _, name := range taught {
		if _, present := comps[name]; !present {
			t.Errorf("component %q used by WeKnora A2UI guidance is not in the official basic catalog", name)
		}
	}
}

// TestA2UISpecFormExampleValidatesAgainstOfficialSchema validates the
// verbatim reference form card against the official server-to-client schema.
// This is the "external consumer can read our output" proof for the form
// card shape: every input control, binding, and confirm action the model is
// taught to emit must satisfy the official schema, not just our own parser.
func TestA2UISpecFormExampleValidatesAgainstOfficialSchema(t *testing.T) {
	specCard := `[
  {"version":"v0.9","createSurface":{"surfaceId":"s1",
    "catalogId":"https://a2ui.org/specification/v0_9/catalogs/basic/catalog.json"}},
  {"version":"v0.9","updateComponents":{"surfaceId":"s1","components":[
    {"id":"root","component":"Card","child":"col"},
    {"id":"col","component":"Column","children":["head","f_name","f_room","btn"]},
    {"id":"head","component":"Text","text":"请填写会议预约信息","variant":"h3"},
    {"id":"f_name","component":"TextField","label":"姓名","value":{"path":"/name"}},
    {"id":"f_room","component":"ChoicePicker","label":"会议室","variant":"mutuallyExclusive",
     "options":[{"label":"3F 大会议室","value":"3f-hall"},{"label":"5F 视频室","value":"5f-video"}],
     "value":{"path":"/room"}},
    {"id":"btn_lbl","component":"Text","text":"提交"},
    {"id":"btn","component":"Button","child":"btn_lbl","variant":"primary",
     "checks":[
       {"condition":{"call":"required","args":{"value":{"path":"/name"}},"returnType":"boolean"},"message":"姓名不能为空"},
       {"condition":{"call":"required","args":{"value":{"path":"/room"}},"returnType":"boolean"},"message":"请选择会议室"}],
     "action":{"event":{"name":"confirm","context":{
       "message":"根据下面用户提交的会议信息继续办理预约。",
       "name":{"path":"/name"},
       "room":{"path":"/room"}}}}}
  ]}}
]`
	var frames []map[string]any
	if err := jsonUnmarshalStrict(specCard, &frames); err != nil {
		t.Fatalf("reference card card is not valid JSON: %v", err)
	}
	if len(frames) < 2 {
		t.Fatalf("reference card card has %d frames, want >=2", len(frames))
	}
	s2c := compileA2UISchema(t, "server_to_client.json")
	for i, frame := range frames {
		if err := s2c.Validate(frame); err != nil {
			t.Fatalf("reference card frame %d fails official server_to_client schema:\n%v", i, err)
		}
	}
	// Anchor: our own parser must also accept this card, and the button must
	// be re-derivable with the correct action and bindings. The parser
	// routes through ExtractA2UIMessages, which requires the <a2ui-json>
	// tag wrapper, so wrap the bare spec array before the lookup.
	wrapped := types.A2UITagOpen + "\n" + specCard + "\n" + types.A2UITagClose
	btn := types.FindA2UIButton(wrapped, "s1", "btn")
	if btn == nil {
		t.Fatal("reference card button not re-derivable by our parser")
	}
	if btn.ActionName != "confirm" {
		t.Errorf("reference card button action = %q, want confirm", btn.ActionName)
	}
	if len(btn.BoundInputPaths) != 2 {
		t.Errorf("reference card button has %d bound paths, want 2", len(btn.BoundInputPaths))
	}
}
