package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// The model sees one text budget, max_text_chars. browserCallParams maps page
// reads onto the extension's max_tokens (about 4 characters per token).
// Session identity is server-owned.
// Method-specific requirements are also described to the model on each field.
const browserToolParameters = `{
  "type": "object",
  "properties": {
    "method": {
      "type": "string",
      "enum": [
        "observe",
        "snapshot",
        "screenshot",
        "navigate",
        "navigate_back",
        "navigate_forward",
        "reload",
        "click",
        "fill",
        "press",
        "hover",
        "wheel",
        "scroll_to",
        "focus",
        "blur",
        "select",
        "tab_list",
        "tab_create",
        "tab_select",
        "tab_close",
        "tab_borrow",
        "tab_return",
        "get_html",
        "evaluate",
        "console",
        "network",
        "wait_for_navigation",
        "wait_ms",
        "window_resize",
        "emulate",
        "request_help",
        "drag",
        "screenshot_full_page",
        "extract",
        "debug",
        "dialog_status",
        "dialog_accept",
        "dialog_dismiss",
        "operation_await",
        "operation_cancel",
        "wait_for_element",
        "record_start",
        "record_stop",
        "record_await",
        "upload",
        "download",
        "video"
      ]
    },
    "keep_open": {
      "type": "boolean",
      "description": "Keep this task open after the turn for a deliverable or human step. Default false."
    },
    "debug_surfaces": {
      "type": "boolean"
    },
    "max_depth": {
      "type": "integer",
      "minimum": 0.0
    },
    "probe_hover": {
      "type": "boolean",
      "description": "For observe: opt in for hover-only content. Moves the cursor and can take seconds."
    },
    "tab_id": {
      "type": "integer",
      "minimum": 1,
      "description": "Required for tab_select/close/borrow/return; otherwise defaults to the current task tab."
    },
    "timeout_ms": {
      "type": "integer",
      "minimum": 1.0
    },
    "url": {
      "type": "string",
      "minLength": 1,
      "description": "Required for navigate; optional initial URL for tab_create."
    },
    "wait_until": {
      "type": "string",
      "enum": [
        "load",
        "domcontentloaded",
        "networkidle",
        "commit"
      ],
      "description": "Navigation wait state; default domcontentloaded. Use load/networkidle only when needed."
    },
    "hard": {
      "type": "boolean"
    },
    "button": {
      "type": "string",
      "enum": [
        "left",
        "middle",
        "right"
      ]
    },
    "click_count": {
      "type": "integer",
      "minimum": 1.0
    },
    "modifiers": {
      "type": "array",
      "items": {
        "type": "string",
        "enum": [
          "alt",
          "ctrl",
          "meta",
          "shift"
        ]
      }
    },
    "ref": {
      "type": "string",
      "minLength": 1,
      "description": "Fresh page ref; omit selector when set. Optional screenshot element crop."
    },
    "selector": {
      "type": "string",
      "minLength": 1,
      "description": "CSS selector, alternative to ref."
    },
    "expect": {
      "type": "array",
      "description": "Optional target-policy assertions, such as [{\"target\":\"@e1\",\"field\":\"text\",\"equals\":\"OK\"}]. Use for business facts that must hold after the action.",
      "items": {
        "type": "object",
        "properties": {
          "target": {
            "type": "string",
            "minLength": 1
          },
          "field": {
            "type": "string",
            "enum": ["text", "value", "checked", "count", "attribute", "url"]
          },
          "equals": {},
          "attribute": {
            "type": "string"
          }
        },
        "required": ["target", "field"]
      }
    },
    "clear_before": {
      "type": "boolean"
    },
    "value": {
      "type": "string",
      "description": "Required for fill. Empty string clears the field."
    },
    "hold_ms": {
      "type": "integer",
      "minimum": 0.0
    },
    "key": {
      "type": "string",
      "minLength": 1,
      "description": "Required for press: a key or shortcut such as Enter, Escape, Ctrl+A. ` +
	`Use fill with value to enter text, not press."
    },
    "settle_ms": {
      "type": "integer",
      "minimum": 0.0
    },
    "delta_x": {
      "type": "number"
    },
    "delta_y": {
      "type": "number"
    },
    "values": {
      "type": "array",
      "items": {
        "type": "string"
      },
      "description": "Required for select: native select option value attributes, not visible labels. ` +
	`For custom dropdowns use click/observe. Empty clears a multiple selection."
    },
    "scope": {
      "type": "string",
      "enum": [
        "user",
        "agent",
        "all"
      ]
    },
    "active": {
      "type": "boolean",
      "description": "Omit this field: the extension's task display setting controls tab activation."
    },
    "index": {
      "type": "integer",
      "minimum": 0
    },
    "confirm": {
      "type": "boolean",
      "description": "Borrowing always requires explicit user approval."
    },
    "max_bytes": {
      "type": "integer",
      "minimum": 0.0,
      "description": "For get_html: limit returned HTML bytes."
    },
    "await_promise": {
      "type": "boolean"
    },
    "expression": {
      "type": "string",
      "minLength": 1,
      "description": "Required for evaluate: JavaScript evaluated as a script, not a function body. ` +
	`Use an expression such as document.title, or wrap statements and return in an IIFE: ` +
	`(() => { return document.title; })(). A top-level return is a syntax error. ` +
	`Use only for a specific gap after observation; return bounded JSON-serializable values, ` +
	`not DOM nodes. Inspect result ok/error."
    },
    "return_by_value": {
      "type": "boolean"
    },
    "include_stack": {
      "type": "boolean"
    },
    "limit": {
      "type": "integer",
      "minimum": 1.0
    },
    "max_text_chars": {
      "type": "integer",
      "minimum": 1.0,
      "description": "Maximum characters of text to return. For observe and snapshot this bounds ` +
	`the rendered page; for console and network it bounds each entry."
    },
    "since": {
      "type": "integer",
      "minimum": 0.0
    },
    "duration_ms": {
      "type": "integer",
      "minimum": 0.0,
      "maximum": 10000,
      "description": "Required for wait_ms; integer milliseconds, 0 to 10000."
    },
    "height": {
      "type": "integer",
      "minimum": 100,
      "maximum": 7680,
      "description": "Required with width for window_resize."
    },
    "width": {
      "type": "integer",
      "minimum": 100,
      "maximum": 7680,
      "description": "Required with height for window_resize."
    },
    "off": {
      "type": "boolean",
      "description": "For emulate: true clears overrides; mutually exclusive with overrides."
    },
    "overrides": {
      "type": "object",
      "properties": {
        "accept_language": {
          "type": "string"
        },
        "device_scale_factor": {
          "type": "number"
        },
        "height": {
          "type": "integer",
          "minimum": 0.0
        },
        "max_touch_points": {
          "type": "integer",
          "minimum": 0.0
        },
        "mobile": {
          "type": "boolean"
        },
        "touch": {
          "type": "boolean"
        },
        "user_agent": {
          "type": "string"
        },
        "user_agent_metadata": {
          "type": "object",
          "properties": {
            "architecture": {
              "type": "string"
            },
            "brands": {
              "type": "array",
              "items": {
                "type": "object",
                "required": [
                  "brand",
                  "version"
                ],
                "properties": {
                  "brand": {
                    "type": "string"
                  },
                  "version": {
                    "type": "string"
                  }
                }
              }
            },
            "full_version": {
              "type": "string"
            },
            "mobile": {
              "type": "boolean"
            },
            "model": {
              "type": "string"
            },
            "platform": {
              "type": "string"
            },
            "platform_version": {
              "type": "string"
            }
          }
        },
        "width": {
          "type": "integer",
          "minimum": 0.0
        }
      },
      "description": "For emulate: required unless off is true."
    },
    "prompt": {
      "type": "string",
      "minLength": 1,
      "description": "Required for request_help. Explain the step the user should complete."
    },
    "targets": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "ref": {
            "type": "string"
          },
          "selector": {
            "type": "string"
          }
        }
      }
    },
    "title": {
      "type": "string"
    },
    "dialog_id": {
      "type": "string",
      "minLength": 1
    },
    "text": {
      "type": "string"
    },
    "operation_id": {
      "type": "string",
      "minLength": 1
    },
    "state": {
      "type": "string",
      "enum": ["visible", "hidden", "attached", "detached"]
    },
    "poll_ms": {
      "type": "integer",
      "minimum": 0
    },
    "source": {
      "type": "object",
      "properties": {
        "ref": {"type": "string"},
        "selector": {"type": "string"},
        "offset": {
          "type": "object",
          "properties": {"x": {"type": "number"}, "y": {"type": "number"}}
        }
      }
    },
    "target": {
      "type": "object",
      "properties": {
        "ref": {"type": "string"},
        "selector": {"type": "string"},
        "offset": {
          "type": "object",
          "properties": {"x": {"type": "number"}, "y": {"type": "number"}}
        }
      }
    },
    "delta": {
      "type": "object",
      "properties": {"x": {"type": "number"}, "y": {"type": "number"}}
    },
    "mode": {
      "type": "string",
      "enum": ["press_move_release", "click_move_click", "input", "drop"]
    },
    "action": {
      "type": "string",
      "enum": [
        "discover", "table", "list",
        "performance", "aggregate", "duplicates", "capabilities", "activity", "wait",
        "pin", "unpin", "start", "stop", "status", "requests", "request",
        "operations", "operation", "console", "pages", "export", "rules",
        "rule_add", "rule_enable", "rule_disable", "rule_remove", "replay",
        "read", "discard", "exported"
      ]
    },
    "run_id": {"type": "string"},
    "id": {"type": "string"},
    "name": {"type": "string"},
    "part": {"type": "string"},
    "offset": {"type": "integer", "minimum": 0},
    "max_chars": {"type": "integer", "minimum": 1},
    "pointer": {"type": "string"},
    "rule": {"type": "object"},
    "replay": {"type": "object"},
    "budget": {"type": "integer", "minimum": 4096, "maximum": 262144},
    "slow_ms": {"type": "integer", "minimum": 0, "maximum": 60000},
    "window_ms": {"type": "integer", "minimum": 100, "maximum": 10000},
    "include_controlled": {"type": "boolean"},
    "resource_type": {"type": "string"},
    "status": {"type": "integer", "minimum": 100, "maximum": 599},
    "kind": {"type": "string"},
    "fields": {"type": "array", "items": {}},
    "command_id": {"type": "string"},
    "max_rows": {"type": "integer", "minimum": 1},
    "max_columns": {"type": "integer", "minimum": 1},
    "item_selector": {"type": "string"},
    "target_id": {"type": "string"},
    "purpose": {"type": "string"},
    "max_page_tokens": {"type": "integer", "minimum": 1},
    "redact_values": {"type": "boolean"},
    "trace_version": {"type": "integer", "enum": [2, 3]},
    "supports_tab_switch_steps": {"type": "boolean"},
    "recording_id": {"type": "string"},
    "capability": {"type": "string"},
    "request_id": {"type": "string"},
    "browser": {"type": "string"},
    "max_duration_ms": {"type": "integer", "minimum": 1},
    "quality": {"type": "string", "enum": ["standard", "clear"]},
    "files": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "transfer_id": {"type": "string"},
          "name": {"type": "string"},
          "staged_path": {"type": "string"}
        },
        "required": ["transfer_id", "name"]
      }
    },
    "browser_relative_dir": {"type": "string"},
    "max_byte_size": {"type": "integer", "minimum": 1},
    "wait_ms": {"type": "integer", "minimum": 0, "maximum": 60000}
  },
  "required": [
    "method"
  ],
  "additionalProperties": false
}`

type browserArgumentRule struct{ fields, required []string }

var browserArgumentRules = map[string]browserArgumentRule{
	"screenshot": {fields: []string{"ref", "tab_id"}},
	"observe": {
		fields:   []string{"debug_surfaces", "max_depth", "max_text_chars", "probe_hover", "tab_id"},
		required: []string{},
	},
	"snapshot": {
		fields:   []string{"max_depth", "max_text_chars", "tab_id"},
		required: []string{},
	},
	"navigate": {
		fields:   []string{"tab_id", "timeout_ms", "url", "wait_until"},
		required: []string{"url"},
	},
	"navigate_back": {
		fields:   []string{"tab_id", "timeout_ms", "wait_until"},
		required: []string{},
	},
	"navigate_forward": {
		fields:   []string{"tab_id", "timeout_ms", "wait_until"},
		required: []string{},
	},
	"reload": {
		fields:   []string{"hard", "tab_id", "timeout_ms", "wait_until"},
		required: []string{},
	},
	"click": {
		fields:   []string{"expect", "button", "click_count", "modifiers", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"fill": {
		fields:   []string{"expect", "clear_before", "ref", "selector", "tab_id", "timeout_ms", "value"},
		required: []string{"value"},
	},
	"press": {
		fields:   []string{"expect", "hold_ms", "key", "modifiers", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{"key"},
	},
	"hover": {
		fields:   []string{"expect", "modifiers", "ref", "selector", "settle_ms", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"wheel": {
		fields:   []string{"expect", "delta_x", "delta_y", "modifiers", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"scroll_to": {
		fields:   []string{"expect", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"focus": {
		fields:   []string{"expect", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"blur": {
		fields:   []string{"expect", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"select": {
		fields:   []string{"expect", "ref", "selector", "tab_id", "timeout_ms", "values"},
		required: []string{"values"},
	},
	"tab_list": {
		fields:   []string{"scope"},
		required: []string{},
	},
	"tab_create": {
		fields:   []string{"active", "index", "url"},
		required: []string{},
	},
	"tab_select": {
		fields:   []string{"tab_id"},
		required: []string{"tab_id"},
	},
	"tab_close": {
		fields:   []string{"tab_id"},
		required: []string{"tab_id"},
	},
	"tab_borrow": {
		fields:   []string{"confirm", "tab_id"},
		required: []string{"tab_id"},
	},
	"tab_return": {
		fields:   []string{"tab_id"},
		required: []string{"tab_id"},
	},
	"get_html": {
		fields:   []string{"max_bytes", "ref", "tab_id"},
		required: []string{},
	},
	"evaluate": {
		fields:   []string{"await_promise", "expression", "return_by_value", "tab_id", "timeout_ms"},
		required: []string{"expression"},
	},
	"console": {
		fields:   []string{"include_stack", "limit", "max_text_chars", "since", "tab_id"},
		required: []string{},
	},
	"network": {
		fields:   []string{"limit", "max_text_chars", "since", "tab_id"},
		required: []string{},
	},
	"wait_for_navigation": {
		fields:   []string{"tab_id", "timeout_ms", "wait_until"},
		required: []string{},
	},
	"wait_ms": {
		fields:   []string{"duration_ms", "tab_id"},
		required: []string{"duration_ms"},
	},
	"window_resize": {
		fields:   []string{"height", "width"},
		required: []string{"height", "width"},
	},
	"emulate": {
		fields:   []string{"off", "overrides", "tab_id"},
		required: []string{},
	},
	"request_help": {
		fields:   []string{"prompt", "tab_id", "targets", "timeout_ms", "title"},
		required: []string{"prompt"},
	},
	"drag": {
		fields:   []string{"button", "delta", "duration_ms", "hold_ms", "mode", "modifiers", "source", "tab_id", "target", "timeout_ms"},
		required: []string{"source"},
	},
	"screenshot_full_page": {
		fields:   []string{"tab_id"},
		required: []string{},
	},
	"extract": {
		fields:   []string{"action", "fields", "item_selector", "max_bytes", "max_columns", "max_rows", "ref", "selector", "tab_id", "target_id", "timeout_ms"},
		required: []string{"action"},
	},
	"debug": {
		fields:   []string{"action", "budget", "command_id", "fields", "id", "include_controlled", "kind", "limit", "max_chars", "method", "name", "offset", "part", "pointer", "replay", "resource_type", "rule", "run_id", "since", "slow_ms", "state", "status", "tab_id", "timeout_ms", "url", "wait_ms", "window_ms"},
		required: []string{"action"},
	},
	"dialog_status": {
		fields:   []string{"tab_id"},
		required: []string{},
	},
	"dialog_accept": {
		fields:   []string{"dialog_id", "tab_id", "text"},
		required: []string{"dialog_id"},
	},
	"dialog_dismiss": {
		fields:   []string{"dialog_id", "tab_id"},
		required: []string{"dialog_id"},
	},
	"operation_await": {
		fields:   []string{"operation_id", "tab_id", "wait_ms"},
		required: []string{"operation_id"},
	},
	"operation_cancel": {
		fields:   []string{"operation_id", "tab_id"},
		required: []string{"operation_id"},
	},
	"wait_for_element": {
		fields:   []string{"poll_ms", "ref", "selector", "state", "tab_id", "timeout_ms"},
		required: []string{"state"},
	},
	"record_start": {
		fields:   []string{"max_page_tokens", "purpose", "redact_values", "supports_tab_switch_steps", "tab_id", "trace_version", "url"},
		required: []string{},
	},
	"record_stop": {
		fields:   []string{},
		required: []string{},
	},
	"record_await": {
		fields:   []string{"timeout_ms"},
		required: []string{},
	},
	"upload": {
		fields:   []string{"files", "mode", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{"files"},
	},
	"download": {
		fields:   []string{"browser_relative_dir", "max_byte_size", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"video": {
		fields:   []string{"action", "browser", "capability", "max_duration_ms", "offset", "quality", "recording_id", "request_id", "tab_id", "timeout_ms"},
		required: []string{"action"},
	},
}

var browserInputSchema = sync.OnceValues(func() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(browserToolParameters))
	if err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(nil)
	const schemaURL = "urn:weknora:local-browser:input"
	if err := compiler.AddResource(schemaURL, doc); err != nil {
		return nil, err
	}
	return compiler.Compile(schemaURL)
})

// ValidateArguments enforces the flat schema and the selected operation's
// required fields before opening a browser or executing any page action.
// inferBrowserMethod recovers the method when a model omits it but supplies a
// field that belongs to exactly one operation. Ambiguous shapes still fail.
func inferBrowserMethod(input map[string]any) (string, bool) {
	for _, candidate := range []struct {
		method string
		fields []string
	}{
		{"evaluate", []string{"expression"}},
		{"wait_ms", []string{"duration_ms"}},
		{"request_help", []string{"prompt"}},
		{"navigate", []string{"url"}},
		{"wheel", []string{"delta_x", "delta_y"}},
		{"window_resize", []string{"width", "height"}},
		{"select", []string{"values"}},
		{"press", []string{"key"}},
		{"fill", []string{"value"}},
		{"emulate", []string{"off", "overrides"}},
		{"tab_list", []string{"scope"}},
		{"reload", []string{"hard"}},
		{"observe", []string{"debug_surfaces", "max_text_chars", "probe_hover"}},
	} {
		for _, field := range candidate.fields {
			if _, ok := input[field]; ok {
				return candidate.method, true
			}
		}
	}
	return "", false
}

// normalizeBrowserArguments rewrites an unambiguous missing method so the
// registry validator and Execute see the same canonical arguments.
func normalizeBrowserArguments(args json.RawMessage) json.RawMessage {
	var input map[string]any
	if json.Unmarshal(args, &input) != nil {
		return args
	}
	if method, ok := input["method"].(string); ok && method != "" {
		return args
	}
	method, ok := inferBrowserMethod(input)
	if !ok {
		return args
	}
	input["method"] = method
	normalized, err := json.Marshal(input)
	if err != nil {
		return args
	}
	return normalized
}

func (t *BrowserSkillTool) ValidateArguments(args json.RawMessage) error {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(args))
	if err != nil {
		return fmt.Errorf("invalid browser arguments JSON: %w", err)
	}
	input, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("browser arguments must be an object")
	}
	if _, nested := input["params"]; nested {
		return fmt.Errorf("params is not supported; place arguments alongside method, " +
			"e.g. {\"method\":\"navigate\",\"url\":\"https://example.com\"}")
	}
	method, ok := input["method"].(string)
	if !ok || method == "" {
		var inferred bool
		method, inferred = inferBrowserMethod(input)
		if !inferred {
			return fmt.Errorf("method is required and must be a string")
		}
	}
	rule, ok := browserArgumentRules[method]
	if !ok {
		return fmt.Errorf("unsupported browser method %q", method)
	}
	for name := range input {
		if name != "method" && name != "keep_open" && !slices.Contains(rule.fields, name) {
			return fmt.Errorf(
				"%s does not accept argument %q; allowed fields: %s (plus keep_open)",
				method, name, strings.Join(rule.fields, ", "),
			)
		}
	}
	for _, name := range rule.required {
		if v, found := input[name]; !found || v == nil {
			return fmt.Errorf("%s requires %s at the top level", method, name)
		}
	}
	schema, err := browserInputSchema()
	if err != nil {
		return fmt.Errorf("browser input schema cannot be validated: %w", err)
	}
	if err := schema.Validate(value); err != nil {
		return err
	}
	// Targeted interactions require exactly one locator; keyboard, blur and
	// wheel operations can also act on the current focus/viewport.
	_, ref := input["ref"]
	_, selector := input["selector"]
	switch method {
	case "click", "fill", "hover", "scroll_to", "focus", "select":
		if ref == selector {
			return fmt.Errorf("%s requires exactly one of ref or selector", method)
		}
	case "press", "wheel", "blur":
		if ref && selector {
			return fmt.Errorf("%s accepts only one of ref or selector", method)
		}
	}
	if method == "emulate" {
		off, _ := input["off"].(bool)
		_, overrides := input["overrides"]
		if off == overrides {
			return fmt.Errorf("emulate requires either off:true or overrides")
		}
	}
	return nil
}

// browserCallParams copies model arguments into the official tool.* parameter
// names. Page observations budget rendered tokens, so a character cap becomes
// max_tokens at the extension's 4-characters-per-token heuristic. Console and
// network already speak max_text_chars and are passed through.
func browserCallParams(method string, input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for name, value := range input {
		if name == "method" || name == "keep_open" {
			continue
		}
		out[name] = value
	}
	switch method {
	case "wait_ms":
		delete(out, "tab_id")
	case "observe", "snapshot":
		chars, ok := out["max_text_chars"]
		if !ok {
			break
		}
		delete(out, "max_text_chars")
		out["max_tokens"] = observationTokensFromTextChars(chars)
	}
	return out
}

// observationTokensFromTextChars matches the extension heuristic of about four
// characters per rendered token, rounding up so the page cap is not shorter
// than the character budget the model asked for.
func observationTokensFromTextChars(value any) int {
	chars := int(toFloat64(value))
	if chars < 1 {
		chars = 1
	}
	tokens := (chars + 3) / 4
	// BrowserSkill rejects very small snapshots with "max_tokens is too small"
	// instead of truncating. Keep the model's character budget as a floor, but
	// never send a budget that cannot fit the observation header plus one row.
	const minObservationTokens = 512
	if tokens < minObservationTokens {
		return minObservationTokens
	}
	return tokens
}
