package modelcontext

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiteralRefTextSurvivesCitationExpansion(t *testing.T) {
	cases := []struct {
		name     string
		register bool
		in       string
		want     string
	}{
		{
			name: "unregistered legacy id stays non-citable",
			in:   `引用标签的写法是 <ref id="c1"/>。`,
			want: `引用标签的写法是 。`,
		},
		{
			name:     "registered legacy id still expands",
			register: true,
			in:       `引用标签的写法是 <ref id="c1"/>。`,
			want:     `引用标签的写法是 <kb doc="Doc" chunk_id="chunk-1" />。`,
		},
		{
			name: "weknora attribute",
			in:   `引用标签的写法是 <ref weknora_id="c1"/>。`,
			want: `引用标签的写法是 <ref weknora_id="c1"/>。`,
		},
		{
			name: "arbitrary text",
			in:   `引用标签的写法是 <ref arbitrary text>。`,
			want: `引用标签的写法是 <ref arbitrary text>。`,
		},
		{
			name:     "inline code keeps a non-handle tag",
			register: true,
			in:       "写法是 `<ref id=\"example\"/>`。",
			want:     "写法是 `<ref id=\"example\"/>`。",
		},
		{
			name:     "fenced code keeps a literal tag",
			register: true,
			in:       "示例：\n```\n<ref weknora_id=\"c1\"/>\n```\n结束",
			want:     "示例：\n```\n<ref weknora_id=\"c1\"/>\n```\n结束",
		},
		{
			name:     "genuine citation still expands",
			register: true,
			in:       `依据 <ref id="chunk://1"/>。`,
			want:     `依据 <kb doc="Doc" chunk_id="chunk-1" />。`,
		},
		{
			name: "fabricated handle is still removed",
			in:   `伪造 <ref id="chunk://999"/>。`,
			want: `伪造 。`,
		},
		{
			name: "document handle is still removed",
			in:   `文档 <ref id="doc://1"/>。`,
			want: `文档 。`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := newSourceRegistry()
			if tc.register {
				registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
				registry.RegisterWeb("https://example.com", "Example")
			}
			require.Equal(t, tc.want, registry.ExpandText(tc.in))
			var streamed strings.Builder
			decoder := newCitationStreamExpander(registry)
			for i := range len(tc.in) {
				streamed.WriteString(decoder.Feed(tc.in[i : i+1]))
			}
			streamed.WriteString(decoder.Flush())
			require.Equal(t, tc.want, streamed.String())
		})
	}
}

func TestLiteralRefSplitAcrossChunks(t *testing.T) {
	registry := newSourceRegistry()
	text := `写法是 <ref weknora_id="c1"/>，以及 <ref id="example"/>。`
	for split := 0; split <= len(text); split++ {
		decoder := newCitationStreamExpander(registry)
		got := decoder.Feed(text[:split]) + decoder.Feed(text[split:]) + decoder.Flush()
		require.Equal(t, text, got, "split at %d", split)
	}
}

func TestFencedRefSplitAcrossChunksStaysLiteral(t *testing.T) {
	registry := newSourceRegistry()
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
	text := "```html\n<ref id=\"example\"/>\n```"
	for split := 0; split <= len(text); split++ {
		decoder := newCitationStreamExpander(registry)
		got := decoder.Feed(text[:split]) + decoder.Feed(text[split:]) + decoder.Flush()
		require.Equal(t, text, got, "split at %d", split)
	}
}

func TestDisabledCitationsStillDropProtocolRefs(t *testing.T) {
	registry := newSourceRegistry(false)
	registry.RegisterChunk(ChunkReference{ChunkID: "chunk-1", DocumentTitle: "Doc"})
	require.Equal(t, "literal <ref weknora_id=\"c1\"/> done",
		registry.ExpandText("literal <ref weknora_id=\"c1\"/> done"))
	require.Equal(t, "legacy  done",
		registry.ExpandText("legacy <ref id=\"c1\"/> done"))
	require.Equal(t, "protocol  done",
		registry.ExpandText("protocol <ref id=\"chunk://1\"/> done"))
}
