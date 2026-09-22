// Package runtime resolves one model's effective settings and assembles its
// authenticated endpoint. Provider definitions and catalog metadata are shared;
// model credentials and connection settings are resolved independently per row.
package runtime

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/Tencent/WeKnora/internal/models/providers"
)

// Initialize is the application composition entry point. A failed deployment
// overlay leaves the previous catalog generation untouched.
func Initialize(configDir string) error {
	providers.EnsureBuiltins()
	return catalog.LoadOverlayValidated(configDir, validateDefinition)
}

// Reload replaces the deployment overlay without retaining removed overrides.
func Reload(data []byte, baseDir string) error {
	providers.EnsureBuiltins()
	return catalog.ApplyOverlayValidated(data, baseDir, validateDefinition)
}

func validateDefinition(v *catalog.Vendor) error {
	switch v.Auth {
	case catalog.AuthBearer, catalog.AuthAPIKeyHeader, catalog.AuthXAPIKey,
		catalog.AuthGoogleAPIKey, catalog.AuthNone, catalog.AuthSigned:
	default:
		return fmt.Errorf("unknown auth style %q", v.Auth)
	}
	for _, kind := range v.ModelTypes {
		_, err := resolveWithVendor(Ref{Provider: v.ID, Model: "custom-model", ModelType: kind}, v)
		var unsupported *UnsupportedModelError
		if err != nil && !errors.As(err, &unsupported) {
			return err
		}
	}
	for _, entry := range v.Models {
		name := entry.ID
		if name == "" {
			name = strings.ReplaceAll(entry.Match, "*", "snapshot")
		}
		_, err := resolveWithVendor(Ref{Provider: v.ID, Model: name, ModelType: entry.Type}, v)
		// Unsupported entries deliberately remain as tombstones for old rows; they
		// are hidden from pickers and must continue to produce a useful error.
		var unsupported *UnsupportedModelError
		if err != nil && (!errors.As(err, &unsupported) || (!entry.Deprecated && entry.ID != "")) {
			return err
		}
		if entry.ContextWindow < 0 || entry.MaxOutputTokens < 0 || entry.Dimension < 0 {
			return fmt.Errorf("model %s: negative model limit", name)
		}
		if _, valid := catalog.ParseModelType(string(entry.Type)); entry.Type != "" && !valid {
			return fmt.Errorf("model %s: unsupported type %q", name, entry.Type)
		}
	}
	return nil
}

// UnsupportedModelError records an intentional refusal for an unimplemented API.
type UnsupportedModelError struct{ Provider, Model, Reason string }

func (e *UnsupportedModelError) Error() string {
	return fmt.Sprintf(
		"catalog: %s does not serve %q through a protocol this build implements: %s",
		e.Provider,
		e.Model,
		e.Reason,
	)
}
