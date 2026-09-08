package semantic

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Order Items":      "order_items",
		"2024-sales":       "t_2024_sales",
		"  User-Profiles ": "user_profiles",
		"products":         "products",
		"数据表":              "",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
	if !ValidSlug(Slugify("Order-42")) {
		t.Errorf("Slugify output should pass ValidSlug")
	}
}

func TestBuildDraftCubeSoftDelete(t *testing.T) {
	doc, err := BuildDraftCube("primary_db", TableSchema{
		Schema: "public",
		Name:   "orders",
		Columns: []ColumnSchema{
			{Name: "id", DataType: "bigint", PrimaryKey: true},
			{Name: "order_no", DataType: "varchar"},
			{Name: "amount", DataType: "int"},
			{Name: "created_at", DataType: "datetime"},
			{Name: "deleted_at", DataType: "datetime"},
		},
	})
	if err != nil {
		t.Fatalf("BuildDraftCube: %v", err)
	}
	cube := doc.Cubes[0]
	if cube.Name != "orders" || cube.DataSource != "primary_db" {
		t.Errorf("unexpected cube identity: %+v", cube)
	}
	if !strings.Contains(cube.SQL, "FROM public.orders") ||
		!strings.Contains(cube.SQL, "WHERE deleted_at IS NULL") {
		t.Errorf("sql missing table ref/soft-delete: %s", cube.SQL)
	}
	if len(cube.Measures) != 1 || cube.Measures[0].Type != "count" {
		t.Errorf("expected single count measure, got %+v", cube.Measures)
	}
	// deleted_at is filtered out of dimensions; created_at maps to time
	types := map[string]string{}
	for _, d := range cube.Dimensions {
		types[d.Name] = d.Type
	}
	if _, ok := types["deleted_at"]; ok {
		t.Errorf("deleted_at should not become a dimension")
	}
	if types["order_no"] != "string" || types["amount"] != "number" || types["created_at"] != "time" {
		t.Errorf("dimension type mapping wrong: %v", types)
	}
	if !cube.Dimensions[0].PrimaryKey {
		t.Errorf("primary key not carried over")
	}
}

func TestModelYAMLRoundTripKeepsUnknownFields(t *testing.T) {
	src := `cubes:
  - name: orders
    title: Sales Orders
    sql: SELECT * FROM public.orders
    data_source: primary_db
    meta:
      owner: analytics
    measures:
      - name: count
        type: count
        title: Order Count
      - name: total_amount
        type: sum
        sql: amount
        format: currency
    dimensions:
      - name: id
        sql: id
        type: number
        primary_key: true
    pre_aggregations:
      - measures: [total_amount]
        dimensions: [id]
`
	doc, err := ParseModelYAML(src)
	if err != nil {
		t.Fatalf("ParseModelYAML: %v", err)
	}
	if doc.Cubes[0].Name != "orders" || doc.Cubes[0].DataSource != "primary_db" {
		t.Fatalf("identity fields not parsed: %+v", doc.Cubes[0])
	}
	out, err := GenerateModelYAML(doc)
	if err != nil {
		t.Fatalf("GenerateModelYAML: %v", err)
	}
	// unmodelled fields survive the round-trip via inline Extra maps
	for _, want := range []string{
		"meta:", "owner: analytics", "format: currency",
		"pre_aggregations:", "primary_key: true",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("round-trip lost %q;\n output:\n%s", want, out)
		}
	}
	re, err := ParseModelYAML(out)
	if err != nil {
		t.Fatalf("re-parse failed: %v", err)
	}
	if len(re.Cubes[0].PreAggregations) != 1 {
		t.Errorf("pre_aggregations not round-tripped: %+v", re.Cubes[0].PreAggregations)
	}
}

func TestBuildPolicyDefaultDeny(t *testing.T) {
	rules := BuildPolicy([]string{"analytics", "ops"})
	if len(rules) != 4 {
		t.Fatalf("expected 4 rules (deny-all + admin + 2 groups), got %d", len(rules))
	}
	if rules[0].Group != "*" {
		t.Errorf("first rule must deny all, got %q", rules[0].Group)
	}
	// admin is always granted even when not in the input list
	names := map[string]bool{}
	for _, r := range rules {
		names[r.Group] = true
	}
	if !names[AdminGroup] {
		t.Errorf("admin group must always be granted, got %v", names)
	}
	includes, ok := rules[1].MemberLevel.Includes.(string)
	if !ok || includes != "*" {
		t.Errorf("allow rule must include *, got %v", rules[1].MemberLevel.Includes)
	}
}

func TestValidateRejectsBadModels(t *testing.T) {
	doc := &ModelDoc{Cubes: []CubeDef{{Name: "Bad-Name", SQL: "SELECT 1", DataSource: "db"}}}
	if err := doc.Validate(nil); err == nil {
		t.Errorf("invalid slug must be rejected")
	}
	doc = &ModelDoc{Cubes: []CubeDef{{Name: "good", SQL: "SELECT 1"}}}
	if err := doc.Validate(nil); err == nil || !strings.Contains(err.Error(), "data_source") {
		t.Errorf("missing data_source must be rejected, got %v", err)
	}
	doc = &ModelDoc{Cubes: []CubeDef{{
		Name: "good", SQL: "SELECT 1", DataSource: "db",
		Joins: []JoinDef{{Name: "ghost", SQL: "1=1", Relationship: "many_to_one"}},
	}}}
	if err := doc.Validate(nil); err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("unknown join target must be rejected, got %v", err)
	}
	// self-join is legal when the target is the model itself
	doc.Cubes[0].Joins[0].Name = "good"
	if err := doc.Validate(KnownModelNames(nil, doc)); err != nil {
		t.Errorf("self join must pass: %v", err)
	}
}
