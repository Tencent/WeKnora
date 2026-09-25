package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestBuildSampleDataDescriptionIncludesDataAnalysisRows(t *testing.T) {
	service := &DataTableSummaryService{}
	result := &types.ToolResult{Data: map[string]interface{}{
		"rows": []map[string]string{
			{"date": "20250101", "status": "approved"},
			{"date": "20250102", "status": "pending"},
		},
	}}

	got := service.buildSampleDataDescription(context.Background(), result, 10)
	for _, want := range []string{
		`"date":"20250101"`,
		`"status":"approved"`,
		`"date":"20250102"`,
		`"status":"pending"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("sample description missing %s:\n%s", want, got)
		}
	}
}

func TestBuildSampleDataDescriptionSupportsDecodedRows(t *testing.T) {
	service := &DataTableSummaryService{}
	result := &types.ToolResult{Data: map[string]interface{}{
		"rows": []map[string]interface{}{
			{"date": "20250101", "count": float64(3)},
		},
	}}

	got := service.buildSampleDataDescription(context.Background(), result, 10)
	for _, want := range []string{`"date":"20250101"`, `"count":3`} {
		if !strings.Contains(got, want) {
			t.Errorf("sample description missing %s:\n%s", want, got)
		}
	}
}

func TestBuildSampleDataDescriptionLimitsRows(t *testing.T) {
	service := &DataTableSummaryService{}
	result := &types.ToolResult{Data: map[string]interface{}{
		"rows": []map[string]string{
			{"id": "first"},
			{"id": "second"},
		},
	}}

	got := service.buildSampleDataDescription(context.Background(), result, 1)
	if !strings.Contains(got, `"id":"first"`) {
		t.Fatalf("first row missing:\n%s", got)
	}
	if strings.Contains(got, `"id":"second"`) {
		t.Fatalf("sample limit was ignored:\n%s", got)
	}
}
