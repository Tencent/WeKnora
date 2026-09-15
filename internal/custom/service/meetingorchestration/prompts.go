package meetingorchestration

import (
	"embed"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/promptreload"
	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed prompts/*.txt prompts/*.schema.json
var promptFiles embed.FS

func NewPromptBundle(dir string) *promptreload.Bundle {
	fallback := map[string]string{}
	schemas := map[string]string{}
	entries, _ := fs.Glob(promptFiles, "prompts/*.txt")
	for _, entry := range entries {
		if data, err := promptFiles.ReadFile(entry); err == nil {
			fallback[filepath.Base(entry)] = strings.TrimSpace(string(data))
		}
	}
	schemaEntries, _ := fs.Glob(promptFiles, "prompts/*.schema.json")
	for _, entry := range schemaEntries {
		if data, err := promptFiles.ReadFile(entry); err == nil {
			schemas[filepath.Base(entry)] = strings.TrimSpace(string(data))
		}
	}
	bundle := promptreload.NewWithSchemas(dir, fallback, schemas)
	bundle.ValidateSchemas = validateSchemas
	return bundle
}

// validateSchemas compiles every schema independently. A malformed or
// unsupported schema therefore rejects the whole external bundle before it
// can become visible to a generation task.
func validateSchemas(schemas map[string]string) error {
	for name, content := range schemas {
		document, err := jsonschema.UnmarshalJSON(strings.NewReader(content))
		if err != nil {
			return err
		}
		compiler := jsonschema.NewCompiler()
		if err := compiler.AddResource(name, document); err != nil {
			return err
		}
		if _, err := compiler.Compile(name); err != nil {
			return err
		}
		// Ensure the source remains valid JSON for callers that use a standard
		// decoder to pass it to a provider's structured-output API.
		var raw any
		if err := json.Unmarshal([]byte(content), &raw); err != nil {
			return err
		}
	}
	return nil
}
