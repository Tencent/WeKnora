package common

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseLLMJsonResponse(t *testing.T) {
	type payload struct {
		Key string `json:"key"`
	}

	tests := []struct {
		name    string
		content string
		want    string
		wantErr bool
	}{
		{"direct json", `{"key":"value"}`, "value", false},
		{"fenced json", "```json\n{\"key\":\"fenced\"}\n```", "fenced", false},
		{"fenced no lang", "```\n{\"key\":\"plain\"}\n```", "plain", false},
		{"prose around fence", "Here you go:\n```json\n{\"key\":\"wrapped\"}\n```\nThanks", "wrapped", false},
		{"prose around bare json", `Sure: {"key":"bare"} hope that helps`, "bare", false},
		// Trailing bracket-like prose must not truncate the payload.
		{"trailing citation", `{"key":"cited"} — see section [1].`, "cited", false},
		// Braces inside a string literal must not close the object early.
		{"braces in string", `Result: {"key":"a}b"} done`, "a}b", false},
		{"not json", "just some text", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got payload
			err := ParseLLMJsonResponse(tt.content, &got)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.Key != tt.want {
				t.Errorf("Key = %q, want %q", got.Key, tt.want)
			}
		})
	}
}

func TestCleanInvalidUTF8(t *testing.T) {
	input := "valid " + string([]byte{0xef, 0xbc, 0x2e}) + "\x00tail"

	got := CleanInvalidUTF8(input)
	if !utf8.ValidString(got) {
		t.Fatalf("CleanInvalidUTF8 returned invalid UTF-8: % x", []byte(got))
	}
	if want := "valid .tail"; got != want {
		t.Fatalf("CleanInvalidUTF8() = %q, want %q", got, want)
	}
}

func BenchmarkParseLLMJsonResponse_Fenced(b *testing.B) {
	const content = "```json\n{\"name\":\"Acme\",\"slug\":\"entity/acme\",\"aliases\":[\"A\",\"B\"]}\n```"
	type payload struct {
		Name string `json:"name"`
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var p payload
		_ = ParseLLMJsonResponse(content, &p)
	}
}

func TestExtractBalancedJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"object in prose", `see {"a":1} done`, `{"a":1}`},
		{"array in prose", `list [1, 2, {"k": "v"}] end`, `[1, 2, {"k": "v"}]`},
		{"first opener wins object", `{"a":[1,2]} [3]`, `{"a":[1,2]}`},
		{"first opener wins array", `[1,2] {"a":3}`, `[1,2]`},
		{"nested object", `x {"a":{"b":[1,{"c":2}]}} y`, `{"a":{"b":[1,{"c":2}]}}`},
		// Braces inside a string literal must not close the object early.
		{"braces in string", `{"key":"a}b"} tail`, `{"key":"a}b"}`},
		// An escaped quote must not end the string literal.
		{"escaped quote in string", `{"a":"x\"y"} `, `{"a":"x\"y"}`},
		{"no json", `just some text`, ``},
		{"unbalanced object", `{"a":1`, ``},
		{"empty input", ``, ``},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ExtractBalancedJSON(tt.input); got != tt.want {
				t.Errorf("ExtractBalancedJSON(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestPipelineLog(t *testing.T) {
	t.Run("defaults stage and action", func(t *testing.T) {
		if got := PipelineLog("", "", nil); got != "[PIPELINE] stage=PIPELINE action=info" {
			t.Errorf("PipelineLog() = %q, want %q", got, "[PIPELINE] stage=PIPELINE action=info")
		}
	})

	t.Run("fields are sorted and values quoted", func(t *testing.T) {
		got := PipelineLog("chat", "answer", map[string]interface{}{"kb": "k1", "n": 2})
		want := `[PIPELINE] stage=chat action=answer kb="k1" n=2`
		if got != want {
			t.Errorf("PipelineLog() = %q, want %q", got, want)
		}
	})

	t.Run("newlines in values are escaped", func(t *testing.T) {
		got := PipelineLog("s", "a", map[string]interface{}{"k": "a\nb"})
		// truncatePipelineValue turns the newline into backslash-n, and
		// strconv.Quote escapes the backslash, so two backslashes land in
		// the output.
		want := "[PIPELINE] stage=s action=a k=\"a\\\\nb\""
		if got != want {
			t.Errorf("PipelineLog() = %q, want %q", got, want)
		}
	})

	t.Run("long values are truncated at 300 runes", func(t *testing.T) {
		got := PipelineLog("s", "a", map[string]interface{}{"k": strings.Repeat("x", 301)})
		want := "[PIPELINE] stage=s action=a k=\"" + strings.Repeat("x", 300) + `..."`
		if got != want {
			t.Errorf("PipelineLog() len = %d, want len %d", len(got), len(want))
		}
	})
}
