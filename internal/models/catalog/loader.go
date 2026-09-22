package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"
)

//go:embed data/models.generated.json
var builtinData []byte

var (
	builtinOnce    sync.Once
	builtinEntries map[string][]ModelSpec
)

// BuiltinModels returns an independent copy of a provider's generated catalog.
// Provider definitions never own or embed a second model list.
func BuiltinModels(provider string) []ModelSpec {
	builtinOnce.Do(func() {
		var data struct {
			Version   int                    `json:"version"`
			Providers map[string][]ModelSpec `json:"providers"`
		}
		dec := json.NewDecoder(bytesReader(builtinData))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&data); err != nil {
			panic(fmt.Sprintf("invalid generated model catalog: %v", err))
		}
		if data.Version != 1 {
			panic("unsupported generated model catalog version")
		}
		builtinEntries = data.Providers
	})
	entries, ok := builtinEntries[provider]
	if !ok {
		panic("missing generated model catalog for " + provider)
	}
	out := make([]ModelSpec, len(entries))
	for i, model := range entries {
		out[i] = cloneModelSpec(model)
	}
	return out
}
