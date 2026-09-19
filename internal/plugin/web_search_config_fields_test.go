package plugin

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Reserved keys drive capability flags and dedicated host-side form sections;
// they must never surface as custom ConfigFields (the frontend would render
// them twice — once as a real input, once as a label-only shell).
func TestWebSearchProviderTypeInfoExcludesReservedKeysFromConfigFields(t *testing.T) {
	manifest := Manifest{
		APIVersion: APIVersionV1, ID: "test.reserved", Name: "Reserved", Version: "1.0.0",
		ExtensionType: ExtensionSearch, ProtocolVersion: ProtocolVersionV1,
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"credentials": map[string]any{
					"type":       "object",
					"required":   []any{"api_key"},
					"properties": map[string]any{"api_key": map[string]any{"type": "string", "secret": true}},
				},
				"settings": map[string]any{
					"type":     "object",
					"required": []any{"base_url"},
					"properties": map[string]any{
						"base_url":     map[string]any{"type": "string"},
						"proxy_url":    map[string]any{"type": "string"},
						"search_depth": map[string]any{"type": "string", "description": "Search depth"},
					},
				},
			},
		},
	}
	info := webSearchProviderTypeInfo(manifest, "test.reserved")
	require.True(t, info.RequiresAPIKey)
	require.True(t, info.RequiresBaseURL)
	require.True(t, info.SupportsProxy)
	require.Len(t, info.ConfigFields, 1)
	require.Equal(t, "search_depth", info.ConfigFields[0].Key)
	require.Equal(t, "Search depth", info.ConfigFields[0].Label)
}

func TestWebSearchProviderTypeInfoFromConfigSchema(t *testing.T) {
	manifest := Manifest{
		APIVersion: APIVersionV1, ID: "test.schema", Name: "Schema", Version: "1.0.0",
		ExtensionType: ExtensionSearch, ProtocolVersion: ProtocolVersionV1,
		ConfigSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"settings": map[string]any{
					"type":     "object",
					"required": []any{"search_depth"},
					"properties": map[string]any{
						"search_depth": map[string]any{
							"type":        "string",
							"title":       "Search Depth",
							"description": "basic or advanced",
							"enum":        []any{"basic", "advanced"},
							"default":     "basic",
						},
						"page_size": map[string]any{
							"type":    "integer",
							"title":   "Page Size",
							"default": 5,
						},
						"safe_mode": map[string]any{
							"type":  "boolean",
							"title": "Safe Mode",
						},
					},
				},
				"credentials": map[string]any{
					"type":     "object",
					"required": []any{"api_key"},
					"properties": map[string]any{
						// reserved keys must not surface as custom fields even when
						// declared inside the schema
						"api_key": map[string]any{"type": "string", "secret": true},
					},
				},
			},
		},
	}
	info := webSearchProviderTypeInfo(manifest, "test.schema")
	require.True(t, info.RequiresAPIKey) // capability flag still honored from credentials

	fields := info.ConfigFields
	require.Len(t, fields, 3) // sorted by key: page_size, safe_mode, search_depth

	require.Equal(t, "page_size", fields[0].Key)
	require.Equal(t, "number", fields[0].Type)
	require.Equal(t, "Page Size", fields[0].Label)
	require.Equal(t, "5", fields[0].Default)

	require.Equal(t, "safe_mode", fields[1].Key)
	require.Equal(t, "boolean", fields[1].Type)

	require.Equal(t, "search_depth", fields[2].Key)
	require.Equal(t, "select", fields[2].Type)
	require.Equal(t, "Search Depth", fields[2].Label)
	require.Equal(t, "basic or advanced", fields[2].Description)
	require.True(t, fields[2].Required)
	require.Len(t, fields[2].Options, 2)
	require.Equal(t, "basic", fields[2].Options[0].Value)
	require.Equal(t, "advanced", fields[2].Options[1].Value)
	require.Equal(t, "basic", fields[2].Default)
}

func TestWebSearchConfigFieldTypeNormalization(t *testing.T) {
	require.Equal(t, "select", configFieldType("string", false, true))
	require.Equal(t, "boolean", configFieldType("boolean", false, false))
	require.Equal(t, "number", configFieldType("integer", false, false))
	require.Equal(t, "number", configFieldType("number", false, false))
	require.Equal(t, "array", configFieldType("string[]", false, false))
	require.Equal(t, "secret", configFieldType("string", true, false))
	require.Equal(t, "string", configFieldType("string", false, false))
}
