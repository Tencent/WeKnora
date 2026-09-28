package session

import "testing"

func TestQAHostContextFormatting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		context  map[string]any
		expected string
	}{
		{name: "absent", expected: "question"},
		{name: "empty values", context: map[string]any{"empty": "", "null": nil}, expected: "question"},
		{
			name: "JSON values",
			context: map[string]any{
				"page": "/refunds", "active": false, "count": 0,
				"profile": map[string]any{"name": "visitor"}, "tags": []string{"support"},
			},
			expected: "[Host context]\nactive: false\ncount: 0\npage: /refunds\n" +
				"profile: {\"name\":\"visitor\"}\ntags: [\"support\"]\n\nquestion",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query, err := buildQueryWithHostContext("question", tc.context)
			if err != nil {
				t.Fatal(err)
			}
			if query != tc.expected {
				t.Errorf("query = %q, want %q", query, tc.expected)
			}
		})
	}
}
