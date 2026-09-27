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

// A plugin vendor is only chosen by its ID, in any case: a row naming no
// vendor never lands on one by its URL, and checks see the ID Resolve uses.
func TestPluginVendorsAreChosenByIDOnly(t *testing.T) {
	rt := New()
	def := `{"name":"ACME AI","base_url":"https://api.acme.example/v1","url_patterns":["acme.example"]}`
	if err := rt.RegisterPlugin("acme.ai/acme", []byte(def), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if got := rt.VendorID("", "https://api.acme.example/v1"); got == "acme.ai/acme" {
		t.Fatal("a row naming no vendor was taken by a plugin vendor's URL pattern")
	}
	if got := rt.DetectByURL("https://api.acme.example/v1"); got == "acme.ai/acme" {
		t.Fatal("URL detection offered a plugin vendor")
	}
	for _, provider := range []string{"acme.ai/acme", "ACME.AI/Acme", " acme.ai/acme "} {
		if got := rt.VendorID(provider, ""); got != "acme.ai/acme" {
			t.Errorf("VendorID(%q) = %q", provider, got)
		}
		resolved, err := rt.Resolve(Ref{Provider: provider, Model: "m"})
		if err != nil || resolved.Vendor.ID != "acme.ai/acme" {
			t.Errorf("Resolve(%q) used %v, %v", provider, resolved, err)
		}
	}
	if got := rt.VendorID("openai", ""); got != "openai" {
		t.Fatalf("VendorID(openai) = %q", got)
	}
}

// The console catalog compiles its generation on a runtime without plugin
// vendors and publishes it with Adopt; the plugin vendors must survive.
func TestAdoptKeepsPluginVendors(t *testing.T) {
	target := New()
	def := []byte(`{"name":"ACME","base_url":"https://a.example/v1","models":[{"id":"m"}]}`)
	if err := target.RegisterPlugin("acme.ai/acme", def, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	candidate, err := New().WithOverlay([]byte(`{"providers":{"lab":{"models":[{"id":"sample"}]}}}`), "")
	if err != nil {
		t.Fatal(err)
	}
	target.Adopt(candidate)
	if _, ok := target.Get("acme.ai/acme"); !ok {
		t.Fatal("publishing a catalog generation dropped the plugin vendor")
	}
	if _, ok := target.Get("lab"); !ok {
		t.Fatal("the candidate's own vendors were not published")
	}
	// Later overlay reloads keep it, and it can still be unregistered.
	if err := target.Reload([]byte(`{"providers":{}}`), ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := target.Get("acme.ai/acme"); !ok {
		t.Fatal("reload after adopt dropped the plugin vendor")
	}
	target.Unregister("acme.ai/acme")
	target.Adopt(candidate)
	if _, ok := target.Get("acme.ai/acme"); ok {
		t.Fatal("an unregistered plugin vendor came back")
	}
}
