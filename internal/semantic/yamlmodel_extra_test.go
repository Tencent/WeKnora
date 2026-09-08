package semantic

import (
	"testing"
)

func TestValidSlug(t *testing.T) {
	valid := []string{"orders", "sn_list", "a", "model_123", "abc_def_ghi"}
	for _, s := range valid {
		if !ValidSlug(s) {
			t.Errorf("ValidSlug(%q) should be true", s)
		}
	}
	invalid := []string{"", "1abc", "Orders", "has-dash", "has space", "has/slash", "中文"}
	for _, s := range invalid {
		if ValidSlug(s) {
			t.Errorf("ValidSlug(%q) should be false", s)
		}
	}
}

func TestCubeTypeForColumn(t *testing.T) {
	cases := map[string]string{
		"bigint":           "number",
		"int":              "number",
		"decimal(10,2)":    "number",
		"float8":           "number",
		"double precision": "number",
		"numeric":          "number",
		"bool":             "boolean",
		"tinyint(1)":       "boolean",
		"varchar":          "string",
		"text":             "string",
		"char(36)":         "string",
		"uuid":             "string",
		"datetime":         "time",
		"timestamp":        "time",
		"date":             "time",
		"time":             "time",
		"unknown_type":     "string",
	}
	for dt, want := range cases {
		if got := CubeTypeForColumn(dt); got != want {
			t.Errorf("CubeTypeForColumn(%q) = %q, want %q", dt, got, want)
		}
	}
}

func TestModelDocIdentity(t *testing.T) {
	// cube identity
	doc := &ModelDoc{Cubes: []CubeDef{{Name: "orders"}}}
	kind, name, err := doc.Identity()
	if err != nil || kind != ModelKindCube || name != "orders" {
		t.Errorf("cube identity wrong: kind=%q name=%q err=%v", kind, name, err)
	}
	// view identity
	doc = &ModelDoc{Views: []ViewDef{{Name: "summary"}}}
	kind, name, err = doc.Identity()
	if err != nil || kind != ModelKindView || name != "summary" {
		t.Errorf("view identity wrong: kind=%q name=%q err=%v", kind, name, err)
	}
	// empty / multiple objects
	doc = &ModelDoc{}
	if _, _, err := doc.Identity(); err == nil {
		t.Error("empty doc should error")
	}
	doc = &ModelDoc{Cubes: []CubeDef{{Name: "a"}, {Name: "b"}}}
	if _, _, err := doc.Identity(); err == nil {
		t.Error("multiple cubes should error")
	}
}

func TestParseModelYAMLErrors(t *testing.T) {
	// not YAML
	if _, err := ParseModelYAML("{{not yaml"); err == nil {
		t.Error("invalid YAML should error")
	}
	// empty
	if _, err := ParseModelYAML(""); err == nil {
		t.Error("empty string should error")
	}
	// no cubes or views
	if _, err := ParseModelYAML("cubes: []"); err == nil {
		t.Error("no cubes should error")
	}
}

func TestKnownModelNames(t *testing.T) {
	existing := map[string]bool{"orders": true, "products": true}
	doc := &ModelDoc{Cubes: []CubeDef{{Name: "orders"}}}
	result := KnownModelNames(existing, doc)
	if !result["orders"] {
		t.Error("self should be in known names")
	}
	if !result["products"] {
		t.Error("existing should be in known names")
	}
	if len(result) != 2 {
		t.Errorf("expected 2 names, got %d", len(result))
	}
}

func TestBuildPolicyAdminAlwaysGranted(t *testing.T) {
	// admin always in the list even with empty input
	rules := BuildPolicy(nil)
	names := map[string]bool{}
	for _, r := range rules {
		names[r.Group] = true
	}
	if !names[AdminGroup] {
		t.Error("admin must always be granted")
	}
	if !names["*"] {
		t.Error("deny-all rule must exist")
	}
	if len(rules) != 2 {
		t.Errorf("expected 2 rules (deny-all + admin), got %d", len(rules))
	}
}

func TestBuildPolicyDeduplicates(t *testing.T) {
	rules := BuildPolicy([]string{"analytics", "analytics", "ops"})
	counts := map[string]int{}
	for _, r := range rules {
		counts[r.Group]++
	}
	for g, c := range counts {
		if c > 1 && g != "*" {
			t.Errorf("group %q appeared %d times (should be deduped)", g, c)
		}
	}
}

func TestGenerateModelYAMLStable(t *testing.T) {
	doc := &ModelDoc{Cubes: []CubeDef{{
		Name:       "orders",
		Title:      "Sales Orders",
		DataSource: "primary_db",
		SQL:        "SELECT * FROM public.orders",
		Measures:   []MemberDef{{Name: "count", Type: "count"}},
		Dimensions: []MemberDef{{Name: "id", Type: "number", PrimaryKey: true}},
	}}}
	out1, err := GenerateModelYAML(doc)
	if err != nil {
		t.Fatalf("GenerateModelYAML: %v", err)
	}
	out2, _ := GenerateModelYAML(doc)
	if out1 != out2 {
		t.Error("output should be deterministic (same input → same output)")
	}
}
