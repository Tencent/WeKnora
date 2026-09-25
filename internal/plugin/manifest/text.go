package manifest

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// defaultLocaleKey is the key the default text travels under in the object
// form, next to locale keys such as "zh-CN".
const defaultLocaleKey = "default"

// LocalizedText is user-facing text with optional per-locale variants.
//
// Manifests may write it as a plain string ("Jira") or as an object keyed by
// locale ({"zh-CN": "飞书", "en-US": "Feishu"}). An object without a
// "default" key falls back to en-US, then to the first locale in sort order,
// so every non-empty value has a default. The API always emits the object
// form, which keeps the frontend type to a single shape.
type LocalizedText struct {
	Default string
	Locales map[string]string
}

// Text builds a LocalizedText from a default and optional locale variants.
// Empty variants are dropped.
func Text(def string, locales map[string]string) LocalizedText {
	t := LocalizedText{Default: def}
	for locale, v := range locales {
		if v == "" {
			continue
		}
		if t.Locales == nil {
			t.Locales = make(map[string]string, len(locales))
		}
		t.Locales[locale] = v
	}
	return t
}

// IsZero reports whether the text carries nothing at all.
func (t LocalizedText) IsZero() bool {
	return t.Default == "" && len(t.Locales) == 0
}

// Resolve returns the text for a locale: the exact locale, then any variant of
// the same language ("zh" for "zh-TW"), then the default.
func (t LocalizedText) Resolve(locale string) string {
	if v, ok := t.Locales[locale]; ok {
		return v
	}
	if lang, _, _ := strings.Cut(locale, "-"); lang != "" {
		for _, key := range t.sortedLocales() {
			if keyLang, _, _ := strings.Cut(key, "-"); strings.EqualFold(keyLang, lang) {
				return t.Locales[key]
			}
		}
	}
	return t.Default
}

func (t LocalizedText) sortedLocales() []string {
	keys := make([]string, 0, len(t.Locales))
	for k := range t.Locales {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// MarshalJSON emits the object form: {"default": ..., "<locale>": ...}.
func (t LocalizedText) MarshalJSON() ([]byte, error) {
	out := make(map[string]string, len(t.Locales)+1)
	for k, v := range t.Locales {
		out[k] = v
	}
	out[defaultLocaleKey] = t.Default
	return json.Marshal(out)
}

// UnmarshalJSON accepts either a string or a locale object.
func (t *LocalizedText) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*t = LocalizedText{Default: s}
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return errors.New("localized text must be a string or an object of locale strings")
	}
	*t = fromMap(m)
	return nil
}

// UnmarshalYAML accepts either a scalar or a mapping of locale strings.
func (t *LocalizedText) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*t = LocalizedText{Default: node.Value}
		return nil
	case yaml.MappingNode:
		var m map[string]string
		if err := node.Decode(&m); err != nil {
			return err
		}
		*t = fromMap(m)
		return nil
	default:
		return errors.New("localized text must be a string or a mapping of locale strings")
	}
}

func fromMap(m map[string]string) LocalizedText {
	def := m[defaultLocaleKey]
	delete(m, defaultLocaleKey)
	t := Text(def, m)
	if t.Default == "" {
		if v, ok := t.Locales["en-US"]; ok {
			t.Default = v
		} else if keys := t.sortedLocales(); len(keys) > 0 {
			t.Default = t.Locales[keys[0]]
		}
	}
	return t
}
