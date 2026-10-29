package chat

import (
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

func TestProcessingCacheIdentity(t *testing.T) {
	cfg := &ChatConfig{Source: types.ModelSourceRemote, Provider: "openai", ModelName: "gpt-4o", APIKey: "secret", CustomHeaders: map[string]string{"X-Route": "a"}}
	build := func() string {
		c, e := NewChat(cfg, nil)
		if e != nil {
			t.Fatal(e)
		}
		v, ok := c.(interface{ ProcessingCacheIdentity() string })
		if !ok {
			t.Fatal("missing identity")
		}
		return v.ProcessingCacheIdentity()
	}
	first := build()
	if len(first) != 64 {
		t.Fatal(first)
	}
	cfg.CustomHeaders["X-Route"] = "b"
	if build() == first {
		t.Fatal("headers ignored")
	}
	second := build()
	cfg.APIKey = "other"
	if build() == second {
		t.Fatal("credentials ignored")
	}
	c, e := NewRemoteChat(cfg)
	if e != nil {
		t.Fatal(e)
	}
	identified, ok := c.(interface{ ProcessingCacheIdentity() string })
	if !ok {
		t.Fatal("remote identity missing")
	}
	frozen := identified.ProcessingCacheIdentity()
	cfg.CustomHeaders["X-Route"] = "c"
	if identified.ProcessingCacheIdentity() != frozen {
		t.Fatal("mutable identity")
	}
}

func TestProcessingCacheEffectiveOverlayIdentity(t *testing.T) {
	snapshot := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(snapshot) })
	cfg := &ChatConfig{Source: types.ModelSourceRemote, Provider: "openai", ModelName: "gpt-4o"}
	build := func() Chat {
		c, e := NewChat(cfg, nil)
		if e != nil {
			t.Fatal(e)
		}
		return c
	}
	first := build()
	id := cacheIdentityOf(first)
	for _, overlay := range []string{
		`{"providers":{"openai":{"base_urls":{"KnowledgeQA":"https://api.openai.com/changed"}}}}`,
		`{"providers":{"openai":{"api_key":"deployment-secret"}}}`,
		`{"providers":{"openai":{"headers":{"X-Route":"deployment"}}}}`,
		`{"providers":{"openai":{"models":[{"id":"gpt-4o","max_output_tokens":1234}]}}}`,
	} {
		modelruntime.RestoreSnapshot(snapshot)
		if e := modelruntime.ApplyOverlay([]byte(overlay), ""); e != nil {
			t.Fatal(e)
		}
		if cacheIdentityOf(build()) == id {
			t.Fatalf("overlay ignored: %s", overlay)
		}
		if cacheIdentityOf(first) != id {
			t.Fatal("existing identity changed")
		}
	}
}
func TestProcessingCacheLocalIdentity(t *testing.T) {
	cfg := &ChatConfig{Source: types.ModelSourceLocal, ModelName: "local"}
	t.Setenv("OLLAMA_BASE_URL", "http://ollama-a:11434")
	a, e := ollama.GetOllamaService()
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv("OLLAMA_BASE_URL", "http://ollama-b:11434")
	b, e := ollama.GetOllamaService()
	if e != nil {
		t.Fatal(e)
	}
	first, _ := NewChat(cfg, a)
	second, _ := NewChat(cfg, b)
	old, _ := NewChat(cfg, a)
	if cacheIdentityOf(first) == cacheIdentityOf(second) || cacheIdentityOf(first) != cacheIdentityOf(old) {
		t.Fatal("did not freeze effective endpoint")
	}
	missing, _ := NewChat(cfg, nil)
	if cacheIdentityOf(missing) != "" {
		t.Fatal("missing endpoint needs bypass")
	}
}
