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

// Flat argument fields follow BrowserSkill 0.2.1, source revision
// 5aaa36bf79a201ec40b277ce6c24f2ce23ce37ca. Session identity is server-owned.
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
        "request_help"
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
    "max_tokens": {
      "type": "integer",
      "minimum": 0.0,
      "description": "For observe/snapshot: limit the rendered page content."
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
      "description": "Required for evaluate. Use only for a specific gap after observation; ` +
	`return bounded JSON-serializable values, not DOM nodes. Inspect result ok/error."
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
      "minimum": 1.0
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
    "completion_criteria": {
      "type": "object",
      "properties": {
        "all": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "selector_exists": {
                "type": "string"
              },
              "selector_missing": {
                "type": "string"
              },
              "text_exists": {
                "type": "string"
              },
              "text_missing": {
                "type": "string"
              },
              "url_contains": {
                "type": "string"
              },
              "url_matches": {
                "type": "string"
              }
            }
          }
        },
        "any": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "selector_exists": {
                "type": "string"
              },
              "selector_missing": {
                "type": "string"
              },
              "text_exists": {
                "type": "string"
              },
              "text_missing": {
                "type": "string"
              },
              "url_contains": {
                "type": "string"
              },
              "url_matches": {
                "type": "string"
              }
            }
          }
        },
        "stable_for_ms": {
          "type": "integer",
          "minimum": 0.0
        }
      },
      "description": "For request_help: optional detector for when the user has completed the step."
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
    }
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
		fields:   []string{"debug_surfaces", "max_depth", "max_tokens", "probe_hover", "tab_id"},
		required: []string{},
	},
	"snapshot": {
		fields:   []string{"max_depth", "max_tokens", "tab_id"},
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
		fields:   []string{"button", "click_count", "modifiers", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"fill": {
		fields:   []string{"clear_before", "ref", "selector", "tab_id", "timeout_ms", "value"},
		required: []string{"value"},
	},
	"press": {
		fields:   []string{"hold_ms", "key", "modifiers", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{"key"},
	},
	"hover": {
		fields:   []string{"modifiers", "ref", "selector", "settle_ms", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"wheel": {
		fields:   []string{"delta_x", "delta_y", "modifiers", "ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"scroll_to": {
		fields:   []string{"ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"focus": {
		fields:   []string{"ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"blur": {
		fields:   []string{"ref", "selector", "tab_id", "timeout_ms"},
		required: []string{},
	},
	"select": {
		fields:   []string{"ref", "selector", "tab_id", "timeout_ms", "values"},
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
		fields:   []string{"duration_ms"},
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
		fields:   []string{"completion_criteria", "prompt", "tab_id", "targets", "timeout_ms", "title"},
		required: []string{"prompt"},
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
		return fmt.Errorf("method is required and must be a string")
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
