package modelcontext

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestJSONCitationsPreserveStructuredAnswers(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterChunk(ChunkReference{
		ChunkID: "chunk-real", KnowledgeBaseID: "kb-real", DocumentTitle: "说明 \"甲\"\n文档\\路径",
	})
	registry.RegisterWeb("https://example.com/a?q=1&b=2", "Web \"title\"\nnext")

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "knowledge citation",
			input: `{"answer":"保修两年。<ref id=\"chunk://1\"/>","count":2,"ok":true}`,
			want:  registry.DecodeOutputText(`<ref id="chunk://1"/>`),
		},
		{
			name:  "web citation",
			input: `{"answer":"保修两年。<ref id=\"url://1\"/>","count":2,"ok":true}`,
			want:  registry.DecodeOutputText(`<ref id="url://1"/>`),
		},
		{
			name:  "JSON escaped slashes",
			input: `{"answer":"保修两年。<ref id=\"chunk:\/\/1\"/>","count":2,"ok":true}`,
			want:  registry.DecodeOutputText(`<ref id="chunk://1"/>`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := map[string]any{"answer": "保修两年。" + tc.want, "count": float64(2), "ok": true}
			assertAnswer := func(output string) {
				t.Helper()
				var answer map[string]any
				require.NoError(t, json.Unmarshal([]byte(output), &answer), output)
				require.Equal(t, want, answer)
			}
			assertAnswer(registry.DecodeOutputText(tc.input))
			// Provider packets can split inside an escape sequence, a source
			// handle, or the surrounding UTF-8 text. Test every byte boundary.
			for split := 0; split <= len(tc.input); split++ {
				decoder := registry.StreamDecoder()
				assertAnswer(decoder.Feed(tc.input[:split]) + decoder.Feed(tc.input[split:]) + decoder.Flush())
			}
			decoder := registry.StreamDecoder()
			var output strings.Builder
			for i := range len(tc.input) {
				output.WriteString(decoder.Feed(tc.input[i : i+1]))
			}
			output.WriteString(decoder.Flush())
			assertAnswer(output.String())
		})
	}
}

func TestJSONCitationsKeepEvidencePolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		registry := NewRegistry(enabled)
		registry.RegisterChunk(ChunkReference{ChunkID: "evidence", DocumentTitle: "Doc"})
		registry.RegisterContextChunk(ChunkReference{ChunkID: "navigation", DocumentTitle: "History"})
		registry.RegisterDocument("doc-real")
		input := `{"answer":"a<ref id=\"chunk://999\"/>b<ref id=\"chunk://2\"/>c<ref id=\"doc://1\"/>d"}`
		want := `{"answer":"abcd"}`
		require.JSONEq(t, want, registry.DecodeOutputText(input))
		decoder := registry.StreamDecoder()
		require.JSONEq(t, want, decoder.Feed(input)+decoder.Flush())
		if !enabled {
			input = `{"answer":"a<ref id=\"chunk://1\"/>b"}`
			require.JSONEq(t, `{"answer":"ab"}`, registry.DecodeOutputText(input))
			decoder = registry.StreamDecoder()
			require.JSONEq(t, `{"answer":"ab"}`, decoder.Feed(input)+decoder.Flush())
		}
	}
}

func TestJSONCitationsPreserveMultipleFieldsAndPlainCitations(t *testing.T) {
	registry := NewRegistry(true)
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-real", DocumentTitle: "Doc"})
	citation := `<kb doc="Doc" chunk_id="chunk-real" />`
	input := `{"answers":["first<ref id=\"chunk://1\"/>",{"text":"second<ref id=\"chunk://1\"/>"}],` +
		`"plain":"quote: \"; newline: \n; slash: \\"}`
	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(input), &want))
	answers := want["answers"].([]any)
	answers[0] = "first" + citation
	answers[1].(map[string]any)["text"] = "second" + citation
	decoder := registry.StreamDecoder()
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(decoder.Feed(input)+decoder.Flush()), &got))
	require.Equal(t, want, got)
	require.Equal(t, "claim "+citation, registry.DecodeOutputText(`claim <ref id="chunk://1"/>`))
}
