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
	if err := os.WriteFile(file, []byte("example document"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		rules []ParserEngineRule
		want  string
	}{
		{name: "omitted inherits", want: `{}`},
		{name: "explicit empty resets", rules: []ParserEngineRule{}, want: `{"parser_engine_rules":[]}`},
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
				&KnowledgeProcessOverrides{ParserEngineRules: tc.rules}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
