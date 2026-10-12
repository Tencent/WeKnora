// Package openaichataudio implements speech recognition served through the
// chat completions endpoint: one user message whose only content part is
// {"type": "input_audio", "input_audio": {"data": "data:<mime>;base64,..."}},
// answered by an assistant message whose content is the transcript.
//
// Only dedicated recognition models are addressed this way — Alibaba's
// qwen3-asr-flash and Xiaomi's mimo-v2.5-asr — so no instruction is sent: the
// model transcribes whatever it is given. A general audio-capable chat model
// would need a prompt and would answer about the audio rather than transcribe
// it, which is why this is its own protocol and not a chat request.
//
// https://help.aliyun.com/zh/model-studio/qwen-asr-api-reference
// https://mimo.mi.com/docs/en-US/quick-start/usage-guide/audio/Speech-Recognition
package openaichataudio

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// Config is everything the client needs, already resolved by the api.
type Config struct {
	Endpoint api.Endpoint
	Settings api.TranscriptionsSettings
	Retry    api.RetryPolicy
}

// Client talks chat-served speech recognition to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

const defaultPath = "/chat/completions"

func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := defaultPath
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

// audioTypes maps the file extension onto the media type of the data URI.
// The two references show audio/wav and audio/mpeg; the rest are the
// registered types for the containers Alibaba lists. A fixed table rather
// than mime.TypeByExtension, which reads the host's tables and would make the
// request depend on the machine it runs on.
var audioTypes = map[string]string{
	".wav":  "audio/wav",
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".mp4":  "audio/mp4",
	".aac":  "audio/aac",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".opus": "audio/opus",
	".webm": "audio/webm",
	".amr":  "audio/amr",
}

// DataURI is the input_audio payload for one file. Both references show an
// audio media type in the URI, so a file whose extension names no audio
// container is refused rather than labelled application/octet-stream.
func DataURI(audio []byte, fileName string) (string, error) {
	mediaType, ok := audioTypes[strings.ToLower(filepath.Ext(fileName))]
	if !ok {
		return "", fmt.Errorf("cannot tell the audio format of %q from its extension", fileName)
	}
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(audio), nil
}

// BuildRequestBody is the golden-test entry point.
func (c *Client) BuildRequestBody(req api.TranscriptionRequest) (map[string]any, error) {
	uri, err := DataURI(req.Audio, req.FileName)
	if err != nil {
		return nil, err
	}
	// The ceiling is on the whole string as sent — "encoded string size must
	// not exceed 10 MB" — so the data: prefix counts too.
	if limit := c.cfg.Settings.MaxEncodedBytes; limit > 0 && len(uri) > limit {
		return nil, fmt.Errorf(
			"the audio is %.1f MB and %.1f MB once base64-encoded; this model accepts at most %.1f MB encoded",
			float64(len(req.Audio))/(1<<20), float64(len(uri))/(1<<20), float64(limit)/(1<<20))
	}
	body := map[string]any{
		"model": c.cfg.Endpoint.Model,
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "input_audio",
				"input_audio": map[string]any{"data": uri},
			}},
		}},
	}
	if req.Language != "" && c.cfg.Settings.LanguageParam == api.LanguageASROptions {
		body["asr_options"] = map[string]any{"language": req.Language}
	}
	return body, nil
}

type response struct {
	Choices []struct {
		Message struct {
			// Content is a pointer for the same reason as the multipart
			// protocol's text: an absent transcript is an error, an empty one
			// is silence.
			Content *string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	// Usage.Seconds is the audio length, which Alibaba reports.
	Usage struct {
		Seconds float64 `json:"seconds"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Transcribe sends one audio file.
func (c *Client) Transcribe(ctx context.Context, req api.TranscriptionRequest) (*api.Transcription, error) {
	body, err := c.BuildRequestBody(req)
	if err != nil {
		return nil, err
	}
	var decoded response
	err = c.cfg.Endpoint.PostJSONWithRetry(ctx, c.url(), body, &decoded, c.cfg.Retry, "transcription")
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("transcription API error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 || decoded.Choices[0].Message.Content == nil {
		return nil, fmt.Errorf("transcription reply carries no message content")
	}
	return &api.Transcription{
		Text:     strings.TrimSpace(*decoded.Choices[0].Message.Content),
		Duration: decoded.Usage.Seconds,
	}, nil
}
