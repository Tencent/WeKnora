package tools

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compileInputSchema caches the compiled schema per immutable tool snapshot.
func (t *MCPTool) compileInputSchema() error {
	t.schemaOnce.Do(func() {
		var doc any
		doc, t.schemaErr = jsonschema.UnmarshalJSON(bytes.NewReader(t.Parameters()))
		if t.schemaErr != nil {
			return
		}
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(jsonschema.Draft2020)
		// MCP schemas are untrusted. Resolve embedded $defs/definitions/$refs,
		// but never fetch a remote URL or open a local file during validation.
		compiler.UseLoader(nil)
		const schemaURL = "urn:weknora:mcp:input"
		if t.schemaErr = compiler.AddResource(schemaURL, doc); t.schemaErr != nil {
			return
		}
		t.schema, t.schemaErr = compiler.Compile(schemaURL)
	})
	return t.schemaErr
}

// ValidateArguments checks arguments against the complete MCP schema.
func (t *MCPTool) ValidateArguments(args json.RawMessage) error {
	if err := t.compileInputSchema(); err != nil {
		return fmt.Errorf("MCP input schema cannot be validated: %w", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(args))
	if err != nil {
		return fmt.Errorf("invalid MCP arguments JSON: %w", err)
	}
	if _, ok := value.(map[string]any); !ok {
		return fmt.Errorf("MCP arguments must be an object")
	}
	return t.schema.Validate(value)
}
