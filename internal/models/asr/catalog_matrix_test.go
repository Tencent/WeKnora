package asr

import (
	"context"
	"net/url"
	"strings"
	"testing"

	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEveryCatalogASRTransport(t *testing.T) {
	for _, v := range modelruntime.List() {
		for _, m := range v.Models() {
			if m.Type != types.ModelTypeASR {
				continue
			}
			names := append([]string{m.ID}, m.Aliases...)
			if m.ID == "" {
				names[0] = strings.ReplaceAll(m.Match, "*", "snapshot")
			}
			for _, name := range names {
				t.Run(v.ID+"/"+name, func(t *testing.T) {
					address, _, calls := upstream(t)
					base, err := url.Parse(v.GetDefaultURL(types.ModelTypeASR))
					require.NoError(t, err)
					client, err := newASR(
						&Config{
							Source:    types.ModelSourceRemote,
							Provider:  v.ID,
							ModelName: name,
							ModelID:   "row-id",
							BaseURL:   address + base.Path,
							APIKey:    "row-key",
						},
					)
					if strings.Contains(string(m.Compat), "unsupported_reason") {
						require.Error(t, err)
						require.Zero(t, calls.Load())
						return
					}
					require.NoError(t, err)
					result, err := client.Transcribe(
						WithLanguage(
							context.Background(),
							"zh",
						),
						[]byte(
							"RIFF",
						),
						"sample.wav",
					)
					require.NoError(t, err)
					require.Equal(t, "hello", strings.TrimSpace(result.Text))
					require.EqualValues(t, 1, calls.Load())
					require.Equal(t, "row-id", client.GetModelID())
				})
			}
		}
	}
}

func TestStoredASRSpecReachesWire(t *testing.T) {
	address, got, _ := upstream(t)
	row := &types.Model{
		ID:     "stable-id",
		Name:   "custom",
		Source: types.ModelSourceRemote,
		Parameters: types.ModelParameters{
			Provider: "generic",
			BaseURL:  address,
			APIKey:   "model-key",
			Spec: &types.ModelSpecOverride{
				Compat: map[string]any{
					"path":            "/custom-transcriptions",
					"response_format": "verbose_json",
					"language_param":  "header",
				},
			},
		},
	}
	client, err := newASR(ConfigFromModel(row))
	require.NoError(t, err)
	result, err := client.Transcribe(WithLanguage(context.Background(), "zh"), []byte("RIFF"), "sample.wav")
	require.NoError(t, err)
	require.Equal(t, "/custom-transcriptions", got.path)
	require.Equal(t, "verbose_json", got.fields["response_format"])
	require.Equal(t, "zh", got.language)
	require.Len(t, result.Segments, 1)
	row.Parameters.Spec.Compat = map[string]any{"misspelled_option": true}
	_, err = newASR(ConfigFromModel(row))
	require.ErrorContains(t, err, "unknown field")
}
