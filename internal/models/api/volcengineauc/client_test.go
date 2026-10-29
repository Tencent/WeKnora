package volcengineauc

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/models/api"
)

const successReply = `{
  "reqid": "req-1",
  "code": 1000,
  "message": "Success",
  "sequence": -1,
  "result": [
    {
      "text": "连接池已经调到五十。",
      "utterances": [
        {"definite": true, "start_time": 0, "end_time": 1240, "text": "连接池"},
        {"definite": true, "start_time": 1240, "end_time": 3800, "text": "已经调到五十。"}
      ]
    }
  ]
}`

type captured struct {
	path     string
	auth     string
	dataType string
	header   requestHeader
	audio    []byte
}

// serve answers every request with status and reply, recording what arrived.
// The body is a JSON header followed by the raw audio bytes, so the split is
// recovered from the decoder's input offset.
func serve(t *testing.T, status int, reply string) (*httptest.Server, *captured) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	got := &captured{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		got.dataType = r.Header.Get("Content-Type")

		var header requestHeader
		dec := json.NewDecoder(bytes.NewReader(body))
		require.NoError(t, dec.Decode(&header))
		got.header = header
		got.audio = body[dec.InputOffset():]

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server, got
}

func testClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	return New(Config{
		Endpoint: api.Endpoint{BaseURL: baseURL},
		Token:    "tok",
		AppID:    "app-1",
	})
}

func TestTranscribeBuildsHeaderAndAudioBody(t *testing.T) {
	audio := []byte("fake audio bytes")
	server, got := serve(t, http.StatusOK, successReply)

	out, err := testClient(t, server.URL).Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: audio, FileName: "clip.wav"})
	require.NoError(t, err)

	assert.Equal(t, "/api/v1/auc", got.path)
	// The semicolon is the scheme: a space-form Bearer header authenticates
	// nothing on this host.
	assert.Equal(t, "Bearer; tok", got.auth)
	assert.Equal(t, "application/json", got.dataType)
	assert.Equal(t, audio, got.audio, "the raw audio must follow the JSON header verbatim")

	assert.Equal(t, "app-1", got.header.App.AppID)
	assert.Equal(t, "tok", got.header.App.Token)
	assert.Equal(t, DefaultCluster, got.header.App.Cluster, "an unset cluster falls to the console default")
	assert.NotEmpty(t, got.header.Request.ReqID)
	assert.Equal(t, 1, got.header.Request.NBest)
	assert.True(t, got.header.Request.ShowUtterances)
	assert.Equal(t, "full", got.header.Request.ResultType)
	assert.Equal(t, "wav", got.header.Audio.Format)
	assert.Equal(t, len(audio), got.header.AudioSize)

	require.Equal(t, "连接池已经调到五十。", out.Text)
	require.Len(t, out.Segments, 2)
	assert.Equal(t, 0.0, out.Segments[0].Start)
	assert.Equal(t, 1.24, out.Segments[0].End)
	assert.Equal(t, "已经调到五十。", out.Segments[1].Text)
	assert.Equal(t, 3.8, out.Segments[1].End)
}

func TestTranscribeMapsFileExtensionToFormat(t *testing.T) {
	server, got := serve(t, http.StatusOK, successReply)

	_, err := testClient(t, server.URL).Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: []byte("x"), FileName: "clip.MP3"})
	require.NoError(t, err)
	assert.Equal(t, "mp3", got.header.Audio.Format)
}

func TestTranscribeSurfacesVendorErrorCodes(t *testing.T) {
	server, _ := serve(t, http.StatusOK,
		`{"code": 45000001, "message": "invalid audio size", "result": []}`)

	_, err := testClient(t, server.URL).Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: []byte("x"), FileName: "clip.mp3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "45000001")
	assert.Contains(t, err.Error(), "invalid audio size")
}

func TestTranscribeRefusesReplyWithoutCode(t *testing.T) {
	server, _ := serve(t, http.StatusOK, `{"reqid": "req-1"}`)

	_, err := testClient(t, server.URL).Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: []byte("x"), FileName: "clip.mp3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no code")
}

func TestTranscribeRefusesEmptyAudio(t *testing.T) {
	server, _ := serve(t, http.StatusOK, successReply)
	_, err := testClient(t, server.URL).Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: nil, FileName: "clip.mp3"})
	require.Error(t, err)
}

func TestEmptyCredentialsStillSendAndFailAtTheVendor(t *testing.T) {
	// Presence is the model row's save-time validation; a row that slipped
	// past it reaches the vendor anyway and fails with the vendor's code,
	// like every other protocol client in this tree.
	server, got := serve(t, http.StatusOK,
		`{"code": 45000079, "message": "appid invalid", "result": []}`)
	client := New(Config{Endpoint: api.Endpoint{BaseURL: server.URL}})

	_, err := client.Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: []byte("x"), FileName: "clip.mp3"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "45000079")
	assert.Contains(t, err.Error(), "appid invalid")
	assert.Equal(t, "Bearer;", got.auth, "the semicolon scheme with an empty token; the wire trims the trailing space")
	assert.Empty(t, got.header.App.AppID)
}

func TestClusterOverrideReachesTheHeader(t *testing.T) {
	audio := []byte("x")
	server, got := serve(t, http.StatusOK, successReply)
	client := New(Config{
		Endpoint: api.Endpoint{BaseURL: server.URL},
		Token:    "tok", AppID: "app-1", Cluster: "volcengine_streaming_common_custom",
	})

	_, err := client.Transcribe(context.Background(),
		api.TranscriptionRequest{Audio: audio, FileName: "clip.mp3"})
	require.NoError(t, err)
	assert.Equal(t, "volcengine_streaming_common_custom", got.header.App.Cluster)
}
