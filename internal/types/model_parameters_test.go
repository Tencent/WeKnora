package types

import "testing"

// TestModelParametersDualRead covers the migration-window accessors: the Chat
// shard wins when present, the deprecated top-level fields are the legacy
// fallback, and a non-nil shard is authoritative for vision even when it lists
// no image modality (so a migrated row that dropped vision does not resurrect
// the stale legacy bool).
func TestModelParametersDualRead(t *testing.T) {
	t.Run("legacy row falls back to top-level fields", func(t *testing.T) {
		p := &ModelParameters{
			ContextWindow:   128000,
			MaxOutputTokens: 4096,
			SupportsVision:  true,
		}
		if got := p.GetContextWindow(); got != 128000 {
			t.Errorf("GetContextWindow = %d, want 128000", got)
		}
		if got := p.GetMaxOutputTokens(); got != 4096 {
			t.Errorf("GetMaxOutputTokens = %d, want 4096", got)
		}
		if !p.GetSupportsVision() {
			t.Error("GetSupportsVision = false, want true (legacy bool)")
		}
	})

	t.Run("shard wins when set", func(t *testing.T) {
		p := &ModelParameters{
			ContextWindow: 128000, // stale legacy value
			Chat: &ChatParameters{
				ContextWindow:   200000,
				MaxOutputTokens: 8192,
				InputModalities: []string{"text", "image"},
			},
		}
		if got := p.GetContextWindow(); got != 200000 {
			t.Errorf("GetContextWindow = %d, want 200000 (shard)", got)
		}
		if got := p.GetMaxOutputTokens(); got != 8192 {
			t.Errorf("GetMaxOutputTokens = %d, want 8192 (shard)", got)
		}
		if !p.GetSupportsVision() {
			t.Error("GetSupportsVision = false, want true (image in modalities)")
		}
	})

	t.Run("shard without image is authoritative over stale legacy bool", func(t *testing.T) {
		p := &ModelParameters{
			SupportsVision: true, // stale legacy
			Chat:           &ChatParameters{InputModalities: []string{"text"}},
		}
		if p.GetSupportsVision() {
			t.Error("GetSupportsVision = true; non-nil shard listing no image must win")
		}
	})

	t.Run("shard zero context_window falls back to legacy", func(t *testing.T) {
		p := &ModelParameters{
			ContextWindow: 128000,
			Chat:          &ChatParameters{}, // zero ContextWindow
		}
		if got := p.GetContextWindow(); got != 128000 {
			t.Errorf("GetContextWindow = %d, want 128000 (legacy fallback when shard is 0)", got)
		}
	})

	t.Run("EnsureChat allocates once and reuses", func(t *testing.T) {
		p := &ModelParameters{}
		c1 := p.EnsureChat()
		c1.ThinkingLevel = "high"
		c2 := p.EnsureChat()
		if c1 != c2 {
			t.Error("EnsureChat returned a different shard on second call")
		}
		if c2.ThinkingLevel != "high" {
			t.Errorf("ThinkingLevel = %q, want high (shard preserved)", c2.ThinkingLevel)
		}
	})
}
