// Package configschema describes configuration with a subset of JSON Schema
// (draft 2020-12) plus a few x- keywords for the UI, and handles the secret
// fields such a schema marks: encrypting them at rest, redacting them in
// responses and keeping them when an update sends the redaction back.
//
// One schema drives both the form the frontend renders and the server-side
// validation, replacing the per-integration field descriptors (model vendor
// ExtraField, web search config fields, hardcoded connector and IM forms).
package configschema

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
)

// Types a schema node may declare.
const (
	TypeObject  = "object"
	TypeString  = "string"
	TypeNumber  = "number"
	TypeInteger = "integer"
	TypeBoolean = "boolean"
	TypeArray   = "array"
)

// Schema is one node of a configuration schema. The root is an object whose
// properties are the fields of a form.
type Schema struct {
	Type        string `json:"type,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`

	Properties map[string]*Schema `json:"properties,omitempty"`
	Required   []string           `json:"required,omitempty"`
	Items      *Schema            `json:"items,omitempty"`

	Default any   `json:"default,omitempty"`
	Const   any   `json:"const,omitempty"`
	Enum    []any `json:"enum,omitempty"`
	// OneOf lists labelled choices: each entry carries a const and a title.
	OneOf []*Schema `json:"oneOf,omitempty"`

	MinLength *int     `json:"minLength,omitempty"`
	MaxLength *int     `json:"maxLength,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Minimum   *float64 `json:"minimum,omitempty"`
	Maximum   *float64 `json:"maximum,omitempty"`
	// Format is "uri" or "email"; other formats are carried but not checked.
	Format string `json:"format,omitempty"`

	// Secret marks a string that is encrypted at rest, redacted in
	// responses, and kept when an update sends the redaction back.
	Secret bool `json:"x-secret,omitempty"`
	// Widget picks the form control (password, textarea, select, switch,
	// headers, ...). Empty means the frontend picks from the type; "hidden"
	// keeps a field out of the form while still declaring it (a secret set
	// by another flow, such as WeChat's QR binding).
	Widget      string `json:"x-widget,omitempty"`
	Placeholder string `json:"x-placeholder,omitempty"`
	// I18n carries localized title / description / placeholder:
	// {"title": {"zh-CN": "..."}}.
	I18n map[string]map[string]string `json:"x-i18n,omitempty"`
	// I18nKeys names frontend locale keys for title / description /
	// placeholder. Builtins use them; the frontend falls back to I18n and
	// then to the plain text when a key is missing.
	I18nKeys map[string]string `json:"x-i18n-keys,omitempty"`
	// VisibleIf shows the field only while every listed sibling field
	// equals the given value.
	VisibleIf map[string]any `json:"x-visible-if,omitempty"`
	Group     string         `json:"x-group,omitempty"`
	// Order sorts fields in the form (lower first); ties sort by key.
	Order int `json:"x-order,omitempty"`
	// ModelTypes limits a model vendor field to some model types.
	ModelTypes []string `json:"x-model-types,omitempty"`
}

// Object returns an empty object schema.
func Object() *Schema { return &Schema{Type: TypeObject, Properties: map[string]*Schema{}} }

// Set adds a property and returns the parent for chaining. A required
// property is also listed in Required.
func (s *Schema) Set(key string, prop *Schema, required bool) *Schema {
	if s.Properties == nil {
		s.Properties = map[string]*Schema{}
	}
	s.Properties[key] = prop
	if required {
		s.Required = append(s.Required, key)
	}
	return s
}

// OrderedKeys returns property keys sorted by x-order, then by key.
func (s *Schema) OrderedKeys() []string {
	keys := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		oi, oj := s.Properties[keys[i]].Order, s.Properties[keys[j]].Order
		if oi != oj {
			return oi < oj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// Parse decodes a schema and checks that it stays within the supported
// subset.
func Parse(data []byte) (*Schema, error) {
	var s Schema
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("decode config schema: %w", err)
	}
	if err := s.Check(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Check verifies that a schema uses only what this package understands. The
// root must be an object, since a configuration is always a set of fields.
func (s *Schema) Check() error {
	if s.Type != TypeObject {
		return errors.New("config schema root must be an object")
	}
	var errs []error
	s.check("", &errs)
	return errors.Join(errs...)
}

func (s *Schema) check(path string, errs *[]error) {
	fail := func(format string, args ...any) {
		*errs = append(*errs, fmt.Errorf("%s: %s", displayPath(path), fmt.Sprintf(format, args...)))
	}
	switch s.Type {
	case TypeObject:
		for _, key := range s.Required {
			if _, ok := s.Properties[key]; !ok {
				fail("required field %q has no property", key)
			}
		}
		for _, key := range s.OrderedKeys() {
			s.Properties[key].check(joinPath(path, key), errs)
		}
	case TypeArray:
		if s.Items == nil {
			fail("array needs items")
		} else {
			if s.Items.containsSecret() {
				fail("secrets inside arrays are not supported")
			}
			s.Items.check(path+"[]", errs)
		}
	case TypeString, TypeNumber, TypeInteger, TypeBoolean:
	default:
		fail("unsupported type %q", s.Type)
	}
	if s.Secret && s.Type != TypeString {
		fail("x-secret is only allowed on strings")
	}
	if s.Pattern != "" {
		if _, err := regexp.Compile(s.Pattern); err != nil {
			fail("invalid pattern: %v", err)
		}
	}
	for _, choice := range s.OneOf {
		if choice.Const == nil {
			fail("oneOf entries must carry a const")
		}
	}
}

func (s *Schema) containsSecret() bool {
	if s.Secret {
		return true
	}
	for _, p := range s.Properties {
		if p.containsSecret() {
			return true
		}
	}
	return s.Items != nil && s.Items.containsSecret()
}

// SecretPaths lists the dotted paths of every secret field, sorted.
func (s *Schema) SecretPaths() []string {
	var out []string
	var walk func(n *Schema, path string)
	walk = func(n *Schema, path string) {
		if n.Secret {
			out = append(out, path)
		}
		for key, p := range n.Properties {
			walk(p, joinPath(path, key))
		}
	}
	walk(s, "")
	sort.Strings(out)
	return out
}

func joinPath(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

func displayPath(path string) string {
	if path == "" {
		return "(root)"
	}
	return path
}
