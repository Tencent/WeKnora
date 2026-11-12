package handler

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/stretchr/testify/require"
)

func TestPreviewChunkingEstimatesWithTokenBudgetLanguage(t *testing.T) {
	cases := []struct {
		name      string
		languages []string
		want      float64
	}{
		{name: "default mixed", want: 115},
		{name: "explicit English", languages: []string{chunker.LangEnglish}, want: 86},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, body := postPreview(t, PreviewChunkingRequest{
				Text: strings.Repeat("中", 345),
				ChunkingConfig: PreviewChunkingPayload{
					ChunkSize: 512, Strategy: "recursive", TokenLimit: 128, Languages: tc.languages,
				},
			})
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			chunks := body["data"].(map[string]any)["chunks"].([]any)
			require.Len(t, chunks, 1)
			preview := chunks[0].(map[string]any)
			require.Equal(t, float64(345), preview["size_chars"])
			require.Equal(t, tc.want, preview["size_tokens_approx"])
		})
	}
}
