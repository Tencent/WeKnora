package runtime

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRegisterPluginVendor(t *testing.T) {
	t.Setenv("SECRET_FOR_TEST", "leaked")
	rt := New()
	def := `{"name":"ACME AI","base_url":"https://api.acme.example/${SECRET_FOR_TEST}/v1",
		"headers":{"X-Leak":"$SECRET_FOR_TEST"},"model_types":["chat","embedding"],
		"models":[{"id":"acme-large","context_window":128000}]}`
	if err := rt.RegisterPlugin("acme.ai/acme", []byte(def), t.TempDir()); err != nil {
		t.Fatalf("RegisterPlugin: %v", err)
	}
	v, ok := rt.Get("acme.ai/acme")
	if !ok {
		t.Fatal("vendor not registered")
	}
	if url := v.DefaultBaseURLs[types.ModelTypeKnowledgeQA]; strings.Contains(url, "leaked") ||
		strings.Contains(v.Headers["X-Leak"], "leaked") {
		t.Fatalf("a plugin must not read the server environment: %s %v", url, v.Headers)
	}
	if _, ok := v.FindModel("acme-large", types.ModelTypeKnowledgeQA); !ok {
		t.Fatal("catalog entry missing")
	}

	// A deployment overlay reload keeps plugin vendors.
	if err := rt.Reload([]byte(`{"providers":{}}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.Get("acme.ai/acme"); !ok {
		t.Fatal("reload dropped the plugin vendor")
	}

	if err := rt.RegisterPlugin("openai", []byte(`{"name":"Fake"}`), ""); err == nil {
		t.Fatal("a plugin must not replace a built-in vendor")
	}
	if err := rt.RegisterPlugin("acme.ai/keyed", []byte(`{"api_key":"sk-1"}`), ""); err == nil {
		t.Fatal("a plugin vendor must not carry a deployment key")
	}
	if err := rt.RegisterPlugin("acme.ai/typo", []byte(`{"nmae":"x"}`), ""); err == nil {
		t.Fatal("unknown keys must be rejected")
	}

	rt.Unregister("openai")
	if _, ok := rt.Get("openai"); !ok {
		t.Fatal("Unregister must not remove built-ins")
	}
	rt.Unregister("acme.ai/acme")
	if _, ok := rt.Get("acme.ai/acme"); ok {
		t.Fatal("Unregister left the vendor")
	}
}
