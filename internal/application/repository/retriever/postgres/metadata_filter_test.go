package postgres

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm/clause"
)

func TestCompileMetadataFilterPreservesNestedBooleanTree(t *testing.T) {
	filter := &types.MetadataFilter{Or: []types.MetadataFilter{
		{And: []types.MetadataFilter{
			{Field: "employee_nature", Op: types.MetadataFilterOpEqual, Value: "formal"},
			{Field: "department", Op: types.MetadataFilterOpIn, Values: []any{"research", "finance"}},
		}},
		{Field: "level", Op: types.MetadataFilterOpEqual, Value: 3},
	}}

	sql, args, err := compileMetadataFilter(filter, questionMarkMetadataPlaceholder, 0)
	if err != nil {
		t.Fatalf("compile metadata filter: %v", err)
	}

	wantSQL := "(((access_metadata @> jsonb_build_object(?, ?::jsonb) OR access_metadata @> jsonb_build_object(?, jsonb_build_array(?::jsonb))) AND ((access_metadata @> jsonb_build_object(?, ?::jsonb) OR access_metadata @> jsonb_build_object(?, jsonb_build_array(?::jsonb))) OR (access_metadata @> jsonb_build_object(?, ?::jsonb) OR access_metadata @> jsonb_build_object(?, jsonb_build_array(?::jsonb))))) OR (access_metadata @> jsonb_build_object(?, ?::jsonb) OR access_metadata @> jsonb_build_object(?, jsonb_build_array(?::jsonb))))"
	if sql != wantSQL {
		t.Fatalf("compiled SQL = %s\nwant %s", sql, wantSQL)
	}
	wantArgs := []interface{}{
		"employee_nature", `"formal"`, "employee_nature", `"formal"`,
		"department", `"research"`, "department", `"research"`,
		"department", `"finance"`, "department", `"finance"`,
		"level", "3", "level", "3",
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("compiled args = %#v, want %#v", args, wantArgs)
	}
}

func TestCompileMetadataFilterBindsInjectionValuesAndHonorsOffset(t *testing.T) {
	field := "department'); DROP TABLE embeddings; --"
	filter := &types.MetadataFilter{Field: field, Op: types.MetadataFilterOpEqual, Value: "research'); --"}

	sql, args, err := compileMetadataFilter(filter, postgresMetadataPlaceholder, 7)
	if err != nil {
		t.Fatalf("compile metadata filter: %v", err)
	}
	if strings.Contains(sql, field) || strings.Contains(sql, "research") {
		t.Fatalf("untrusted value was interpolated into SQL: %s", sql)
	}
	wantSQL := "(access_metadata @> jsonb_build_object($8, $9::jsonb) OR access_metadata @> jsonb_build_object($10, jsonb_build_array($11::jsonb)))"
	if sql != wantSQL {
		t.Fatalf("compiled SQL = %s, want %s", sql, wantSQL)
	}
	wantArgs := []interface{}{field, `"research'); --"`, field, `"research'); --"`}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("compiled args = %#v, want %#v", args, wantArgs)
	}
}

func TestCompileMetadataFilterRejectsUnexpectedOperator(t *testing.T) {
	filter := &types.MetadataFilter{Field: "department", Op: types.MetadataFilterOperator("contains"), Value: "research"}

	_, _, err := compileMetadataFilter(filter, questionMarkMetadataPlaceholder, 0)
	if err == nil || !strings.Contains(err.Error(), "unsupported metadata filter operator") {
		t.Fatalf("unexpected operator error = %v", err)
	}
}

func TestKeywordsRetrieveAppliesMetadataFilterBeforeLimit(t *testing.T) {
	filter := &types.MetadataFilter{Field: "department", Op: types.MetadataFilterOpEqual, Value: "research"}

	conds, err := buildKeywordRetrieveConditions(types.RetrieveParams{
		Query:          "expense",
		TopK:           2,
		MetadataFilter: filter,
	})
	if err != nil {
		t.Fatalf("build keyword conditions: %v", err)
	}
	if len(conds) != 3 {
		t.Fatalf("condition count = %d, want metadata, content, enabled", len(conds))
	}
	metadata, ok := conds[0].(clause.Expr)
	if !ok {
		t.Fatalf("first keyword condition = %T, want metadata filter expression", conds[0])
	}
	if !strings.Contains(metadata.SQL, "access_metadata @>") {
		t.Fatalf("metadata predicate missing from keyword conditions: %s", metadata.SQL)
	}
	if metadata.SQL == "" || len(metadata.Vars) != 4 {
		t.Fatalf("metadata predicate was not bound before keyword top-K: %+v", metadata)
	}
}

func TestVectorRetrieveAppliesMetadataFilterBeforeCandidateLimit(t *testing.T) {
	filter := &types.MetadataFilter{Field: "roles", Op: types.MetadataFilterOpIn, Values: []any{"reviewer", "admin"}}

	querySQL, args, err := buildVectorRetrieveQuery(types.RetrieveParams{
		Embedding:      []float32{0.1, 0.2},
		TopK:           3,
		Threshold:      0.5,
		MetadataFilter: filter,
	})
	if err != nil {
		t.Fatalf("build vector query: %v", err)
	}
	metadataAt := strings.Index(querySQL, "access_metadata @> jsonb_build_object($4, $5::jsonb)")
	candidateLimitAt := strings.Index(querySQL, "LIMIT $12")
	if metadataAt < 0 || candidateLimitAt < 0 || metadataAt > candidateLimitAt {
		t.Fatalf("metadata predicate must precede candidate limit:\n%s", querySQL)
	}
	if !strings.Contains(querySQL, "WHERE distance <= $13") || !strings.Contains(querySQL, "LIMIT $14") {
		t.Fatalf("vector threshold/final limit placeholders changed:\n%s", querySQL)
	}
	wantArgs := []interface{}{
		2, true,
		"roles", `"reviewer"`, "roles", `"reviewer"`,
		"roles", `"admin"`, "roles", `"admin"`,
		100, 0.5, 3,
	}
	if len(args) != len(wantArgs)+1 || !reflect.DeepEqual(args[1:], wantArgs) {
		t.Fatalf("vector args after query vector = %#v, want %#v", args[1:], wantArgs)
	}
}
