package adapters

// asr_wire_test.go — semantic wire assertions for the ASR facet. The v1
// client (go-openai SDK) randomized the multipart boundary, so byte-goldens
// are impossible; instead the parsed field set, values, file part and
// response handling are asserted against the v1 SDK layout
// (model → response_format → [language] → file, boundary-agnostic).

import (
	"bytes"
	"encoding/json"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/stretchr/testify/require"
)

func parseMultipart(t *testing.T, req *invoke.Request) (fields map[string]string, fileName string, fileBytes []byte) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, "multipart/form-data", mediaType)
	require.Contains(t, req.ProtectedHeaders, "Content-Type", "multipart Content-Type must be protected")

	reader := multipart.NewReader(bytes.NewReader(req.Body), params["boundary"])
	form, err := reader.ReadForm(8 << 20)
	require.NoError(t, err)
	fields = map[string]string{}
	for k, vs := range form.Value {
		if len(vs) > 0 {
			fields[k] = vs[0]
		}
	}
	if form.File["file"] != nil {
		fh := form.File["file"][0]
		fileName = fh.Filename
		f, err := fh.Open()
		require.NoError(t, err)
		defer func() { _ = f.Close() }()
		buf := &bytes.Buffer{}
		_, _ = buf.ReadFrom(f)
		fileBytes = buf.Bytes()
	}
	return fields, fileName, fileBytes
}

// The transcription request carries model + response_format=verbose_json +
// optional language + the audio file with the caller's (or default) name.
func TestASRRequestWire(t *testing.T) {
	req, err := BuildASRRequest(invoke.Endpoint{
		BaseURL:     "https://api.example.com/v1",
		Credentials: invoke.Credentials{APIKey: "k"},
	}, "whisper-1", &invoke.ASROptions{Audio: []byte("AUDIO"), FileName: "clip.ogg", Language: "zh"})
	require.NoError(t, err)
	require.Equal(t, "https://api.example.com/v1/audio/transcriptions", req.URL)
	require.Equal(t, "Bearer k", req.Header.Get("Authorization"))

	fields, fileName, fileBytes := parseMultipart(t, req)
	require.Equal(t, "whisper-1", fields["model"])
	require.Equal(t, "verbose_json", fields["response_format"])
	require.Equal(t, "zh", fields["language"])
	require.Equal(t, "clip.ogg", fileName)
	require.Equal(t, []byte("AUDIO"), fileBytes)
}

// Empty filename falls back to audio.mp3 (v1 default); empty language omits
// the field.
func TestASRRequestDefaults(t *testing.T) {
	req, err := BuildASRRequest(invoke.Endpoint{BaseURL: "http://127.0.0.1:1"},
		"whisper-1", &invoke.ASROptions{Audio: []byte("A")})
	require.NoError(t, err)
	fields, fileName, _ := parseMultipart(t, req)
	require.Equal(t, "audio.mp3", fileName)
	require.NotContains(t, fields, "language")

	_, err = BuildASRRequest(invoke.Endpoint{}, "m", &invoke.ASROptions{})
	require.Error(t, err, "empty audio must be rejected (v1 guard)")
}

// ParseASRResponse trims the text and every segment text (v1 semantics).
func TestASRResponseParse(t *testing.T) {
	body := []byte(`{"text":"  hello world  ","segments":[` +
		`{"start":0,"end":1.5,"text":" hi "},{"start":1.5,"end":3,"text":"there"}]}`)
	resp, err := ParseASRResponse(200, http.Header{}, body)
	require.NoError(t, err)
	require.Equal(t, "hello world", resp.Text)
	require.Len(t, resp.Segments, 2)
	require.Equal(t, "hi", resp.Segments[0].Text)
	require.Equal(t, 3.0, resp.Segments[1].End)

	// plain (non-verbose) json still parses — segments simply stay empty
	resp, err = ParseASRResponse(200, http.Header{}, []byte(`{"text":"x"}`))
	require.NoError(t, err)
	require.Empty(t, resp.Segments)
	raw, _ := json.Marshal(resp)
	require.True(t, strings.Contains(string(raw), `"Text"`))
}
