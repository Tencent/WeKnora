package plugin

import "testing"

func TestValidateConfigSchema(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"settings": map[string]any{
				"type":     "object",
				"required": []any{"root"},
				"properties": map[string]any{
					"root": map[string]any{"type": "string"},
				},
			},
		},
	}
	if err := ValidateConfigSchema(schema, map[string]any{"settings": map[string]any{"root": "C:/docs"}}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := ValidateConfigSchema(schema, map[string]any{"settings": map[string]any{}}); err == nil {
		t.Fatal("missing required setting was accepted")
	}
	if err := ValidateConfigSchema(schema, map[string]any{"settings": map[string]any{"root": 42.0}}); err == nil {
		t.Fatal("wrong setting type was accepted")
	}
}

func manifestWithSchema(extensionType string, schema map[string]any) Manifest {
	return Manifest{
		APIVersion:      APIVersionV1,
		ID:              "test.schema",
		Name:            "Schema",
		Version:         "1.0.0",
		ExtensionType:   extensionType,
		ProtocolVersion: ProtocolVersionV1,
		ConfigSchema:    schema,
	}
}

func objectSection(props map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": props}
}

func TestConfigSchemaRequiresTopLevelObject(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"properties": map[string]any{"settings": objectSection(map[string]any{})},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("missing top-level type object was accepted")
	}
}

func TestConfigSchemaRequiresSectionObjectType(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"settings": map[string]any{"properties": map[string]any{"x": map[string]any{"type": "string"}}},
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("section missing type object was accepted")
	}
}

func TestConfigSchemaRejectsUnknownSection(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"index_config": objectSection(map[string]any{}),
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("unknown section for search was accepted")
	}
}

func TestRetrieverAllowsIndexConfig(t *testing.T) {
	m := manifestWithSchema(ExtensionRetriever, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"settings":     objectSection(map[string]any{}),
			"index_config": objectSection(map[string]any{}),
		},
	})
	if err := m.Validate("1.4.0"); err != nil {
		t.Fatalf("retriever index_config rejected: %v", err)
	}
}

func TestConfigSchemaRejectsSecretInSettings(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"settings": objectSection(map[string]any{
				"token": map[string]any{"type": "string", "secret": true},
			}),
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("secret field in settings was accepted")
	}
}

func TestWebSearchRejectsCustomCredential(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"credentials": objectSection(map[string]any{
				"custom_token": map[string]any{"type": "string", "secret": true},
			}),
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("custom credential for web search was accepted")
	}
}

func TestModelAllowsAppSecretCredential(t *testing.T) {
	m := manifestWithSchema(ExtensionModel, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"credentials": objectSection(map[string]any{
				"app_secret": map[string]any{"type": "string", "secret": true},
			}),
		},
	})
	if err := m.Validate("1.4.0"); err != nil {
		t.Fatalf("app_secret credential for model rejected: %v", err)
	}
}

func TestConfigSchemaRejectsUnsupportedFieldType(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"settings": objectSection(map[string]any{
				"x": map[string]any{"type": "any"},
			}),
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("unsupported field type was accepted")
	}
}

func TestConfigSchemaRejectsNonArrayRequired(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"settings": map[string]any{"type": "object", "required": "x", "properties": map[string]any{}},
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("non-array required was accepted")
	}
}

func TestConfigSchemaRejectsTypeSecret(t *testing.T) {
	m := manifestWithSchema(ExtensionSearch, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"credentials": objectSection(map[string]any{
				"api_key": map[string]any{"type": "secret"},
			}),
		},
	})
	if err := m.Validate("1.4.0"); err == nil {
		t.Fatal("type secret was accepted")
	}
}
