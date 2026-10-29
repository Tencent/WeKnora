package vlm

import (
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"testing"
)

func TestProcessingCacheIdentity(t *testing.T) {
	cfg := &Config{Source: types.ModelSourceRemote, Provider: "openai", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1", APIKey: "secret", CustomHeaders: map[string]string{"X-Route": "a"}}
	build := func() string {
		v, e := NewVLM(cfg, nil)
		if e != nil {
			t.Fatal(e)
		}
		id, ok := v.(interface{ ProcessingCacheIdentity() string })
		if !ok {
			t.Fatal("missing identity")
		}
		return id.ProcessingCacheIdentity()
	}
	first := build()
	if len(first) != 64 {
		t.Fatal(first)
	}
	cfg.Extra = map[string]any{"temperature": "0.9"}
	if build() == first {
		t.Fatal("options ignored")
	}
	second := build()
	cfg.CustomHeaders["X-Route"] = "b"
	if build() == second {
		t.Fatal("headers ignored")
	}
}

func TestProcessingCacheEffectiveOverlayIdentity(t *testing.T) {
	snapshot := modelruntime.SnapshotCurrent()
	t.Cleanup(func() { modelruntime.RestoreSnapshot(snapshot) })
	cfg := &Config{Source: types.ModelSourceRemote, Provider: "openai", ModelName: "gpt-4o", BaseURL: "https://api.openai.com/v1"}
	build := func() VLM {
		v, e := NewVLM(cfg, nil)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	first := build()
	id := identityOf(first)
	for _, overlay := range []string{`{"providers":{"openai":{"api_key":"deployment-secret"}}}`, `{"providers":{"openai":{"headers":{"X-Route":"deployment"}}}}`, `{"providers":{"openai":{"models":[{"id":"gpt-4o","max_output_tokens":1234}]}}}`} {
		modelruntime.RestoreSnapshot(snapshot)
		if e := modelruntime.ApplyOverlay([]byte(overlay), ""); e != nil {
			t.Fatal(e)
		}
		if identityOf(build()) == id {
			t.Fatal("effective overlay ignored")
		}
		if identityOf(first) != id {
			t.Fatal("existing identity changed")
		}
	}
}
func TestProcessingCacheLocalAndCloudIdentity(t *testing.T) {
	cfg := &Config{Source: types.ModelSourceLocal, ModelName: "local"}
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
	first, _ := NewVLM(cfg, a)
	second, _ := NewVLM(cfg, b)
	old, _ := NewVLM(cfg, a)
	if identityOf(first) == identityOf(second) || identityOf(first) != identityOf(old) {
		t.Fatal("did not freeze injected endpoint")
	}
	cfg = &Config{Source: types.ModelSourceRemote, Provider: "weknoracloud", BaseURL: "https://api.openai.com", ModelName: "vision", AppID: "app", AppSecret: "secret"}
	cloud, e := NewVLM(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	id := identityOf(cloud)
	cfg.AppSecret = "new-secret"
	changed, e := NewVLM(cfg, nil)
	if e != nil {
		t.Fatal(e)
	}
	if len(id) != 64 || identityOf(changed) == id || identityOf(cloud) != id {
		t.Fatal("cloud credential identity not frozen")
	}
}
