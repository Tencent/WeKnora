package openaitranscriptions

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonResponse is the json example from the reference, copied from the page:
// https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
const jsonResponse = `{
  "text": "text",
  "languages": [{"code": "code"}],
  "logprobs": [{"token": "token", "bytes": [0], "logprob": 0}],
  "usage": {
    "input_tokens": 0, "output_tokens": 0, "total_tokens": 0, "type": "tokens",
    "input_token_details": {"audio_tokens": 0, "text_tokens": 0}
  }
}`

// verboseResponse is the "Segment timestamps" example from the same page,
// cut to its first segment where the page elides the rest with "...".
const verboseResponse = `{
  "task": "transcribe",
  "language": "english",
  "duration": 8.470000267028809,
  "text": "The beach was a popular spot on a hot summer day. ` +
	`People were swimming in the ocean, building sandcastles, and playing beach volleyball.",
  "segments": [
    {
      "id": 0,
      "seek": 0,
      "start": 0.0,
      "end": 3.319999933242798,
      "text": " The beach was a popular spot on a hot summer day.",
      "tokens": [50364, 440, 7534, 390, 257, 3743, 4008, 322, 257, 2368, 4266, 786, 13, 50530],
      "temperature": 0.0,
      "avg_logprob": -0.2860786020755768,
      "compression_ratio": 1.2363636493682861,
      "no_speech_prob": 0.00985979475080967
    }
  ],
  "usage": {"type": "duration", "seconds": 9}
}`

type captured struct {
	path     string
	auth     string
	fields   map[string]string
	fileName string
	fileType string
	file     []byte
}

func serve(t *testing.T, status int, reply string) (*httptest.Server, *captured) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	got := &captured{fields: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		require.NoError(t, err)
		reader := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, _ := io.ReadAll(part)
			if part.FormName() == "file" {
				got.fileName, got.fileType, got.file = part.FileName(), part.Header.Get("Content-Type"), data
				continue
			}
			got.fields[part.FormName()] = string(data)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server, got
}

func newClient(url, model string, settings catalog.TranscriptionsSettings) *Client {
	if settings.Path == "" {
		settings.Path = catalog.DefaultTranscriptions().Path
	}
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: url, Model: model, Auth: api.BearerAuth("k")},
		Settings: settings,
	})
}

// The baseline form is file and model. gpt-4o-transcribe accepts only json,
// which is the default, so no response_format is sent.
func TestBaselineFormIsFileAndModel(t *testing.T) {
	server, got := serve(t, http.StatusOK, jsonResponse)
	out, err := newClient(server.URL+"/v1", "gpt-4o-transcribe", catalog.TranscriptionsSettings{}).
		Transcribe(context.Background(), []byte("RIFF"), "meeting.wav")
	require.NoError(t, err)

	assert.Equal(t, "/v1/audio/transcriptions", got.path)
	assert.Equal(t, "Bearer k", got.auth)
	assert.Equal(t, map[string]string{"model": "gpt-4o-transcribe"}, got.fields)
	assert.Equal(t, "meeting.wav", got.fileName, "the extension is how the server identifies the format")
	assert.Empty(t, got.fileType, "a guessed type a server does not list is rejected outright")
	assert.Equal(t, []byte("RIFF"), got.file)
	assert.Equal(t, &api.Transcription{Text: "text"}, out)
}

func TestVerboseJSONCarriesSegments(t *testing.T) {
	server, got := serve(t, http.StatusOK, verboseResponse)
	settings := catalog.TranscriptionsSettings{ResponseFormat: "verbose_json"}
	out, err := newClient(server.URL+"/v1", "whisper-1", settings).
		Transcribe(context.Background(), []byte("ID3"), "a.mp3")
	require.NoError(t, err)

	assert.Equal(t, "verbose_json", got.fields["response_format"])
	require.Len(t, out.Segments, 1)
	assert.Equal(t, api.TranscriptionSegment{
		Start: 0, End: 3.319999933242798, Text: "The beach was a popular spot on a hot summer day.",
	}, out.Segments[0])
}

// Silence is an empty transcript, and that is a valid answer.
func TestEmptyTextIsSilence(t *testing.T) {
	server, _ := serve(t, http.StatusOK, `{"text": ""}`)
	out, err := newClient(server.URL, "m", catalog.TranscriptionsSettings{}).
		Transcribe(context.Background(), []byte("x"), "a.wav")
	require.NoError(t, err)
	assert.Equal(t, "", out.Text)
}

// A reply without text is not silence. vox-box returns its HTTPException
// from the route rather than raising it, so a failure can arrive as a 2xx
// body shaped like this; reading it as an empty transcript would store "no
// speech detected" for a file that was never transcribed.
func TestReplyWithoutTextIsAnError(t *testing.T) {
	server, _ := serve(t, http.StatusOK,
		`{"status_code": 500, "detail": "Failed to transcribe audio, boom", "headers": null}`)
	_, err := newClient(server.URL, "m", catalog.TranscriptionsSettings{}).
		Transcribe(context.Background(), []byte("x"), "a.wav")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to transcribe audio")
}

func TestSurfacesTheVendorErrorBody(t *testing.T) {
	// A stand-in rejection in OpenAI's error envelope.
	server, _ := serve(t, http.StatusBadRequest, `{"error":{"message":"stand-in rejection"}}`)
	_, err := newClient(server.URL, "gpt-4o-transcribe", catalog.TranscriptionsSettings{}).
		Transcribe(context.Background(), []byte("x"), "a.wav")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stand-in rejection")
}

func TestDoesNotDoubleThePath(t *testing.T) {
	server, got := serve(t, http.StatusOK, jsonResponse)
	_, err := newClient(server.URL+"/v1/audio/transcriptions", "m", catalog.TranscriptionsSettings{}).
		Transcribe(context.Background(), []byte("x"), "a.wav")
	require.NoError(t, err)
	assert.Equal(t, "/v1/audio/transcriptions", got.path)
}
