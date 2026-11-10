package runtime_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An overlay entry under "models" that restates an existing id must patch it.
// The docs and config/models.json.example both present "models" as the way to
// correct a known model, and ModelSpec cannot tell absent from zero: before
// this, fixing max_output_tokens alone silently cleared reasoning (a plain
// bool), the thinking levels, the compat overlay and the input modalities.
func TestApplyOverlay_ModelsUpsertPatchesExistingEntry(t *testing.T) {
	registerTestVendors(t)
	overlay := `{"providers": {"acme": {"models": [
	  {"id": "acme-pro", "max_output_tokens": 8192},
	  {"id": "acme-r1", "context_window": 128000}
	]}}}`
	require.NoError(t, modelruntime.ApplyOverlay([]byte(overlay), t.TempDir()))

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro"})
	require.NoError(t, err)
	assert.True(t, r.Spec.Reasoning, "omitted reasoning must not turn thinking off")
	assert.Equal(t, []string{"text", "image"}, r.Spec.Input, "omitted input survives")
	assert.Equal(t, 200000, r.Spec.ContextWindow, "omitted context_window survives")
	assert.Equal(t, 8192, r.Spec.MaxOutputTokens, "the field that was written is applied")
	assert.False(t, r.OpenAICompletions.SupportsTemperature, "omitted compat survives")

	r, err = modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-r1"})
	require.NoError(t, err)
	assert.True(t, r.Spec.Reasoning)
	assert.Equal(t, 128000, r.Spec.ContextWindow)
	assert.False(t, r.ThinkingLevels.Supports(api.ReasoningOff),
		"omitted thinking_levels survive: acme-r1 still cannot be switched off")
}

// Patching must not write through to the built-in definition: the vendor copy
// made by ApplyOverlay is shallow, so the spec's slices and maps are shared
// with the vendor package's own (package-level) models.json data.
func TestApplyOverlay_ModelsUpsertDoesNotMutateBuiltinVendor(t *testing.T) {
	registerTestVendors(t)
	builtin, ok := modelruntime.Get("acme")
	require.True(t, ok)

	overlay := `{"providers": {"acme": {"models": [{"id": "acme-pro", "input": ["audio"]}]}}}`
	require.NoError(t, modelruntime.ApplyOverlay([]byte(overlay), t.TempDir()))

	require.Equal(t, "acme-pro", builtin.Models()[0].ID)
	assert.Equal(t, []string{"text", "image"}, builtin.Models()[0].Input,
		"the built-in spec must be untouched by the overlay")

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro"})
	require.NoError(t, err)
	assert.Equal(t, []string{"audio"}, r.Spec.Input, "the written field is still applied")
}

func TestApplyOverlay_ModelsUpsertExplicitFalseAndNewEntries(t *testing.T) {
	registerTestVendors(t)
	overlay := `{"providers": {"acme": {"models": [
	  {"id": "acme-pro", "reasoning": false},
	  {"id": "acme-ultra", "context_window": 400000}
	]}}}`
	require.NoError(t, modelruntime.ApplyOverlay([]byte(overlay), t.TempDir()))

	r, err := modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-pro"})
	require.NoError(t, err)
	assert.False(t, r.Spec.Reasoning, "an explicit false still turns reasoning off")

	r, err = modelruntime.Resolve(modelruntime.Ref{Provider: "acme", Model: "acme-ultra"})
	require.NoError(t, err)
	assert.True(t, r.Cataloged, "an unknown id creates a full entry")
	assert.Equal(t, 400000, r.Spec.ContextWindow)
	assert.False(t, r.Spec.Reasoning)
}

func TestApplyOverlay_ModelsEntryValidation(t *testing.T) {
	registerTestVendors(t)
	dir := t.TempDir()
	assert.ErrorContains(t,
		modelruntime.ApplyOverlay([]byte(`{"providers": {"acme": {"models": [{"id": "x", "bogus": 1}]}}}`), dir),
		"bogus", "unknown keys inside a model entry are still rejected")
	assert.ErrorContains(t,
		modelruntime.ApplyOverlay([]byte(`{"providers": {"acme": {"models": [{"context_window": 1}]}}}`), dir),
		"model without id")
}

const testSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"></svg>`

func overlayWithIcon(icon string) string {
	return `{"providers": {"lab": {"icon": ` + quoteJSON(icon) + `}}}`
}

// quoteJSON is enough for the paths used here (no escaping needed).
func quoteJSON(s string) string { return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"` }

func TestApplyOverlay_IconAcceptsInlineAndContainedFiles(t *testing.T) {
	registerTestVendors(t)
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "icons"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "icons", "lab.svg"), []byte(testSVG), 0o600))

	require.NoError(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("icons/lab.svg")), dir))
	lab, ok := modelruntime.Get("lab")
	require.True(t, ok)
	assert.Equal(t, testSVG, string(lab.Icon))

	require.NoError(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("<svg><rect/></svg>")), dir))
	lab, _ = modelruntime.Get("lab")
	assert.Equal(t, "<svg><rect/></svg>", string(lab.Icon))
}

// The icon bytes are served as a data: URI to every viewer of the model
// pages, so an overlay must not be able to turn the provider list into a
// file-disclosure endpoint.
func TestApplyOverlay_IconRejectsUncontainedPaths(t *testing.T) {
	registerTestVendors(t)
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.svg")
	require.NoError(t, os.WriteFile(outside, []byte(testSVG), 0o600))

	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon(outside)), dir),
		"absolute paths are not allowed")
	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("/etc/passwd")), dir),
		"absolute paths are not allowed")
	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("../secret.svg")), dir),
		"escapes the overlay directory")
	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("icons/../../secret.svg")), dir),
		"escapes the overlay directory")

	link := filepath.Join(dir, "link.svg")
	if err := os.Symlink(outside, link); err == nil {
		assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("link.svg")), dir),
			"escapes the overlay directory", "a symlink out of the directory is still out of it")
	}

	_, exists := modelruntime.Get("lab")
	assert.False(t, exists, "a rejected icon must not register the provider")
}

func TestApplyOverlay_IconRejectsNonSVGAndOversizedFiles(t *testing.T) {
	registerTestVendors(t)
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "passwd.svg"), []byte("root:x:0:0:root:/root:/bin/sh\n"), 0o600))
	assert.ErrorContains(t,
		modelruntime.ApplyOverlay([]byte(overlayWithIcon("passwd.svg")), dir), "not an SVG document")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "png.svg"), []byte("\x89PNG\r\n\x1a\n"), 0o600))
	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("png.svg")), dir), "not an SVG document")

	big := append([]byte(`<svg xmlns="http://www.w3.org/2000/svg">`), make([]byte, 256*1024)...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.svg"), big, 0o600))
	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("big.svg")), dir), "limit is")

	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dir.svg"), 0o755))
	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("dir.svg")), dir), "not a regular file")

	assert.ErrorContains(t, modelruntime.ApplyOverlay([]byte(overlayWithIcon("missing.svg")), dir), "read icon")
}

// The icon path is relative to the overlay file's own directory.
func TestLoadOverlay_ResolvesIconRelativeToConfigDir(t *testing.T) {
	registerTestVendors(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lab.svg"), []byte(testSVG), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "models.json"),
		[]byte(overlayWithIcon("lab.svg")), 0o600))
	t.Setenv("MODELS_CONFIG", "")

	require.NoError(t, modelruntime.LoadOverlay(dir))
	lab, ok := modelruntime.Get("lab")
	require.True(t, ok)
	assert.Equal(t, testSVG, string(lab.Icon))
	assert.Equal(t, []types.ModelType{types.ModelTypeKnowledgeQA}, lab.ModelTypes)
}
