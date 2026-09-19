package plugin

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// configSchemaSections lists the sections a plugin config_schema may declare,
// per extension type. index_config is retriever-only.
var configSchemaSections = map[string]map[string]bool{
	ExtensionDataSource: {"settings": true, "credentials": true},
	ExtensionParser:     {"settings": true, "credentials": true},
	ExtensionSearch:     {"settings": true, "credentials": true},
	ExtensionModel:      {"settings": true, "credentials": true},
	ExtensionRetriever:  {"settings": true, "credentials": true, "index_config": true},
}

// schemaSection returns the properties map and required-key set for a named
// section ("settings" / "credentials" / "index_config") of a partitioned
// config_schema. It returns nil maps when the section is absent.
func schemaSection(schema map[string]any, section string) (map[string]any, map[string]struct{}) {
	root, _ := schema["properties"].(map[string]any)
	sec, _ := root[section].(map[string]any)
	if sec == nil {
		return nil, nil
	}
	props, _ := sec["properties"].(map[string]any)
	required := map[string]struct{}{}
	if raw, ok := sec["required"].([]any); ok {
		for _, item := range raw {
			if key, ok := item.(string); ok {
				required[strings.TrimSpace(key)] = struct{}{}
			}
		}
	}
	return props, required
}

// sectionFieldPresent reports whether the field is declared in the section.
func sectionFieldPresent(props map[string]any, key string) bool {
	_, ok := props[key]
	return ok
}

// sectionFieldRequired reports whether the field is declared and required.
func sectionFieldRequired(props map[string]any, required map[string]struct{}, key string) bool {
	if !sectionFieldPresent(props, key) {
		return false
	}
	_, ok := required[key]
	return ok
}

// credentialFieldAllowlist restricts which credential fields each extension
// type may declare, so a secret never lands in a plain-text bucket. Web Search
// and Model write runtime config into extra_config (only their fixed, encrypted
// keys). Parser, DataSource and Retriever keep their own encrypted
// nested-credential models (Retriever now encrypts dynamic ExtraCredentials) and
// remain intentionally absent from this map (no field restriction).
var credentialFieldAllowlist = map[string]map[string]bool{
	ExtensionSearch: {"api_key": true},
	ExtensionModel:  {"api_key": true, "app_secret": true},
}

// validSchemaFieldType reports whether a field type is part of the supported
// subset. secret is not a type; it is expressed via the secret:true flag.
func validSchemaFieldType(typ string) bool {
	switch typ {
	case "string", "boolean", "integer", "number", "array", "string[]", "object", "directory", "path", "url":
		return true
	default:
		return false
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// validateManifestConfigSchema checks the structural rules of a config_schema:
// strict object shape, the per-extension section allow-list, legal field types,
// and that secret fields are declared under credentials only.
func validateManifestConfigSchema(extensionType string, schema map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	if t, _ := schema["type"].(string); t != "object" {
		return fmt.Errorf("%w: config_schema must be type object", ErrManifestInvalid)
	}
	rootProps, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("%w: config_schema requires a properties object", ErrManifestInvalid)
	}
	allowed := configSchemaSections[extensionType]
	if allowed == nil {
		allowed = map[string]bool{"settings": true, "credentials": true}
	}
	for section, raw := range rootProps {
		if !allowed[section] {
			return fmt.Errorf("%w: config_schema section %q is not allowed for extension_type %s", ErrManifestInvalid, section, extensionType)
		}
		sec, ok := raw.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: config_schema section %q must be an object", ErrManifestInvalid, section)
		}
		if err := validateSchemaSection(extensionType, section, sec); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaSection(extensionType, section string, sec map[string]any) error {
	if t, _ := sec["type"].(string); t != "object" {
		return fmt.Errorf("%w: config_schema section %q must be type object", ErrManifestInvalid, section)
	}
	if raw, present := sec["required"]; present {
		list, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("%w: config_schema section %q required must be an array of strings", ErrManifestInvalid, section)
		}
		for _, item := range list {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("%w: config_schema section %q required must be an array of strings", ErrManifestInvalid, section)
			}
		}
	}
	secProps, _ := sec["properties"].(map[string]any)
	if _, present := sec["properties"]; present && secProps == nil {
		return fmt.Errorf("%w: config_schema section %q properties must be an object", ErrManifestInvalid, section)
	}
	for key, rawProp := range secProps {
		prop, ok := rawProp.(map[string]any)
		if !ok {
			return fmt.Errorf("%w: config_schema field %q must be an object", ErrManifestInvalid, key)
		}
		if err := validateSchemaField(extensionType, section, key, prop); err != nil {
			return err
		}
	}
	return nil
}

func validateSchemaField(extensionType, section, key string, prop map[string]any) error {
	typ, _ := prop["type"].(string)
	if typ == "" {
		return fmt.Errorf("%w: config_schema field %q requires a type", ErrManifestInvalid, key)
	}
	if typ == "secret" {
		return fmt.Errorf("%w: config_schema field %q should use secret: true instead of type secret", ErrManifestInvalid, key)
	}
	if !validSchemaFieldType(typ) {
		return fmt.Errorf("%w: config_schema field %q has unsupported type %q", ErrManifestInvalid, key, typ)
	}
	secret, _ := prop["secret"].(bool)
	if secret {
		if section != "credentials" {
			return fmt.Errorf("%w: secret field %q must be declared under credentials", ErrManifestInvalid, key)
		}
		if allowlist, restricted := credentialFieldAllowlist[extensionType]; restricted && !allowlist[key] {
			return fmt.Errorf("%w: extension_type %s only supports credentials %v (got %q)", ErrManifestInvalid, extensionType, sortedKeys(allowlist), key)
		}
	}
	if raw, present := prop["enum"]; present {
		if _, ok := raw.([]any); !ok {
			return fmt.Errorf("%w: config_schema field %q enum must be an array", ErrManifestInvalid, key)
		}
	}
	return nil
}

// ValidateConfigSchema validates the small, portable subset of JSON Schema
// needed by plugin manifests. An absent schema intentionally means any config
// is accepted for backward compatibility.
func ValidateConfigSchema(schema map[string]any, config map[string]any) error {
	if len(schema) == 0 {
		return nil
	}
	if err := validateSchemaValue("config", schema, config); err != nil {
		return fmt.Errorf("invalid plugin config: %w", err)
	}
	return nil
}

func validateSchemaValue(path string, schema map[string]any, value any) error {
	if expected, ok := schema["type"].(string); ok && !jsonTypeMatches(expected, value) {
		return fmt.Errorf("%s must be %s", path, expected)
	}
	if t, _ := schema["type"].(string); t == "url" {
		if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
			if err := datasource.ValidateConnectorBaseURL(s); err != nil {
				return fmt.Errorf("%s failed SSRF validation: %w", path, err)
			}
		}
	}
	if enum, ok := schema["enum"].([]any); ok && value != nil {
		matched := false
		for _, candidate := range enum {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must be one of the declared enum values", path)
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		if required, ok := schema["required"].([]any); ok {
			for _, raw := range required {
				key, _ := raw.(string)
				if strings.TrimSpace(key) != "" && (object[key] == nil || (fmt.Sprint(object[key]) == "")) {
					return fmt.Errorf("%s.%s is required", path, key)
				}
			}
		}
		additional, hasAdditional := schema["additionalProperties"].(bool)
		for key, child := range object {
			raw, known := properties[key]
			if !known {
				if hasAdditional && !additional {
					return fmt.Errorf("%s.%s is not declared", path, key)
				}
				continue
			}
			childSchema, ok := raw.(map[string]any)
			if ok {
				if err := validateSchemaValue(path+"."+key, childSchema, child); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func jsonTypeMatches(expected string, value any) bool {
	if value == nil {
		return expected != "null"
	}
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array", "string[]":
		kind := reflect.ValueOf(value).Kind()
		if kind != reflect.Array && kind != reflect.Slice {
			return false
		}
		if expected == "string[]" {
			for i := 0; i < reflect.ValueOf(value).Len(); i++ {
				if _, ok := reflect.ValueOf(value).Index(i).Interface().(string); !ok {
					return false
				}
			}
		}
		return true
	case "string", "directory", "path", "url":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return value.(float64) == float64(int64(value.(float64)))
		default:
			return false
		}
	case "number":
		kind := reflect.ValueOf(value).Kind()
		return kind >= reflect.Int && kind <= reflect.Float64
	default:
		return true
	}
}
