package utils

import (
	"encoding/json"
	"fmt"

	jsonschema "github.com/google/jsonschema-go/jsonschema"
)

// ToJSON converts a value to a JSON string
func ToJSON(v interface{}) string {
	json, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(json)
}

// GenerateSchema generates JSON schema for type T and returns it as a map
// This is optimized to avoid unnecessary serialization/deserialization
func GenerateSchema[T any]() json.RawMessage {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("failed to generate schema: %v", err))
	}

	// Convert schema to map directly through JSON marshaling
	// This is necessary because the schema object doesn't expose its internal structure
	schemaBytes, err := json.Marshal(schema)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal schema: %v", err))
	}

	return schemaBytes
}
