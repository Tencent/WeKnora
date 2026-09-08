package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArtifactKindForFile(t *testing.T) {
	cases := map[string]ArtifactKind{
		"report.PPTX":     ArtifactPresentation,
		"report.ppt":      ArtifactOther,
		"result.html":     ArtifactWebPage,
		"table.csv":       ArtifactSpreadsheet,
		"table.xlsx":      ArtifactSpreadsheet,
		"report.pdf":      ArtifactDocument,
		"image.png":       ArtifactImage,
		"unsafe.svg":      ArtifactOther,
		"script.py":       ArtifactText,
		"README":          ArtifactOther,
		"report.pptx.exe": ArtifactOther,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, want, ArtifactKindForFile(name))
		})
	}
}

func TestMessageArtifactDisplayKindLegacyAndInconsistentHint(t *testing.T) {
	var old MessageArtifact
	require.NoError(t, json.Unmarshal([]byte(`{"file_name":"slides.pptx","file_type":".pptx"}`), &old))
	require.Equal(t, ArtifactPresentation, old.DisplayKind())
	old.Kind = ArtifactWebPage
	require.Equal(t, ArtifactPresentation, old.DisplayKind())
	old.Kind = ArtifactPresentation
	data, err := json.Marshal(old)
	require.NoError(t, err)
	require.Contains(t, string(data), `"kind":"presentation"`)
}
