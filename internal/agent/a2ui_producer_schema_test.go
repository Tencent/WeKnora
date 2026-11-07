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
			defer f.Close()
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
	arrayJSON := extractFirstJSONArray(guidance)
	if arrayJSON == "" {
		t.Fatal("no JSON array found in A2UI guidance — producer contract is empty")
	}

	var frames []map[string]any
	if err := jsonUnmarshalStrict(arrayJSON, &frames); err != nil {
		t.Fatalf("guidance A2UI array is not valid JSON: %v\n%s", err, arrayJSON)
	}
	if len(frames) < 2 {
		t.Fatalf("expected >=2 frames (createSurface + updateComponents), got %d", len(frames))
	}

	s2c := compileA2UISchema(t, "server_to_client.json")
	for i, frame := range frames {
		if err := s2c.Validate(frame); err != nil {
			t.Errorf("producer frame %d fails official server_to_client schema:\n%v\nframe: %v", i, err, frame)
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
	taught := []string{"Card", "Column", "Row", "Text", "Button"}
	for _, name := range taught {
		if _, present := comps[name]; !present {
			t.Errorf("component %q used by WeKnora A2UI guidance is not in the official basic catalog", name)
		}
	}
}
