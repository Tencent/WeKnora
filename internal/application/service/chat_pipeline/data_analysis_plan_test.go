package chatpipeline

import (
	"strings"
	"testing"
)

func TestParseDataAnalysisPlanAcceptsWrappedJSON(t *testing.T) {
	const object = `{"needs_analysis": true, "sql": "SELECT COUNT(*) FROM dataset"}`

	for _, tt := range []struct {
		name    string
		content string
	}{
		{name: "plain object", content: object},
		{name: "json fence", content: "```json\n" + object + "\n```"},
		{name: "unlabelled fence", content: "```\n" + object + "\n```"},
		{name: "uppercase language tag", content: "```JSON\n" + object + "\n```"},
		{name: "space before language tag", content: "``` json\n" + object + "\n```"},
		{name: "prose before the object", content: "Here is the plan:\n" + object},
		{name: "prose around the object", content: "Sure.\n" + object + "\nLet me know if you need anything else."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := parseDataAnalysisPlan(tt.content)
			if err != nil {
				t.Fatalf("parseDataAnalysisPlan(%q) returned %v", tt.content, err)
			}
			if !plan.NeedsAnalysis {
				t.Errorf("needs_analysis = false, want true")
			}
			if plan.SQL != "SELECT COUNT(*) FROM dataset" {
				t.Errorf("sql = %q, want the query from the plan", plan.SQL)
			}
		})
	}
}

func TestParseDataAnalysisPlanKeepsTheNoAnalysisAnswer(t *testing.T) {
	plan, err := parseDataAnalysisPlan("```json\n{\"needs_analysis\": false, \"sql\": \"\"}\n```")
	if err != nil {
		t.Fatalf("parseDataAnalysisPlan returned %v", err)
	}
	if plan.NeedsAnalysis {
		t.Error("needs_analysis = true, want false")
	}
	if plan.SQL != "" {
		t.Errorf("sql = %q, want empty", plan.SQL)
	}
}

func TestParseDataAnalysisPlanRejectsContentWithoutJSON(t *testing.T) {
	if _, err := parseDataAnalysisPlan("I cannot answer that from this table."); err == nil {
		t.Fatal("expected an error when the response carries no JSON object")
	}
}

func TestParseDataAnalysisPlanSkipsAnExampleObjectInProse(t *testing.T) {
	// The example object is not a plan; the parser must keep looking.
	content := "For example {\"note\": \"just an example\"} — the plan is:\n" +
		"{\"needs_analysis\": true, \"sql\": \"SELECT 1 FROM dataset\"}"

	plan, err := parseDataAnalysisPlan(content)
	if err != nil {
		t.Fatalf("parseDataAnalysisPlan returned %v", err)
	}
	if plan.SQL != "SELECT 1 FROM dataset" {
		t.Errorf("sql = %q, want the object that carries the plan fields", plan.SQL)
	}
}

func TestParseDataAnalysisPlanKeepsAnEmptyObjectAnswer(t *testing.T) {
	plan, err := parseDataAnalysisPlan("{}")
	if err != nil {
		t.Fatalf("parseDataAnalysisPlan returned %v", err)
	}
	if plan.NeedsAnalysis || plan.SQL != "" {
		t.Errorf("plan = %+v, want the zero plan an empty object expressed", plan)
	}
}

func TestParseDataAnalysisPlanIgnoresBracketProseBeforeTheObject(t *testing.T) {
	// A bracketed aside before the answer must not become the payload.
	content := "Looking at [table 1] first.\n{\"needs_analysis\": true, \"sql\": \"SELECT 1 FROM dataset\"}"

	plan, err := parseDataAnalysisPlan(content)
	if err != nil {
		t.Fatalf("parseDataAnalysisPlan returned %v", err)
	}
	if !strings.Contains(plan.SQL, "SELECT 1 FROM dataset") {
		t.Errorf("sql = %q, want the object that follows the aside", plan.SQL)
	}
}
