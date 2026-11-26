package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateKnowledgeFromFileParserRuleOverride(t *testing.T) {
	file := filepath.Join(t.TempDir(), "example.txt")
	if err := os.WriteFile(file, []byte("example document"), 0o600); err != nil {
		t.Fatal(err)
	}

	summaryDisabled := false
	for _, tc := range []struct {
		name      string
		overrides KnowledgeProcessOverrides
		want      string
	}{
		{name: "omitted inherits", want: `{}`},
		{
			name:      "explicit empty resets",
			overrides: KnowledgeProcessOverrides{ParserEngineRules: []ParserEngineRule{}},
			want:      `{"parser_engine_rules":[]}`,
		},
		{
			name: "explicit empty preserves other override fields",
			overrides: KnowledgeProcessOverrides{
				ParserEngineOverrides: map[string]string{"pdf_force_scanned": "true"},
				SummaryEnabled:        &summaryDisabled,
				ParserEngineRules:     []ParserEngineRule{},
			},
			want: `{"parser_engine_overrides":{"pdf_force_scanned":"true"},` +
				`"summary_enabled":false,"parser_engine_rules":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.FormValue("process_config"); got != tc.want {
					t.Errorf("process_config = %s, want %s", got, tc.want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"success":true,"data":{"id":"document"}}`)
			}))
			defer server.Close()

			c := NewClient(server.URL)
			if _, err := c.CreateKnowledgeFromFile(context.Background(), "kb", file, nil, nil, "", "",
				&tc.overrides); err != nil {
				t.Fatal(err)
			}
		})
	}
}
