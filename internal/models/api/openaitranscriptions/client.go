// Package openaitranscriptions implements the speech-to-text shape OpenAI
// defined and the compatible servers copied: a multipart POST to
// {base}/audio/transcriptions carrying file and model, answering {text}, or
// {text, segments} when response_format is verbose_json.
//
// response_format is the one optional field that matters, and it differs by
// model: gpt-4o-transcribe and gpt-4o-mini-transcribe accept only json,
// whisper-1 also serves verbose_json, SiliconFlow documents no such field at
// all. json is the default everywhere it is documented, so nothing is sent
// unless catalog.TranscriptionsSettings names a format.
//
// https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
package openaitranscriptions

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/catalog"
)

// Config is everything the client needs, already resolved by the catalog.
type Config struct {
	Endpoint api.Endpoint
	Settings catalog.TranscriptionsSettings
	Retry    api.RetryPolicy
}

// Client talks the OpenAI transcription shape to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := c.cfg.Settings.Path
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

// FormFields is the golden-test entry point: the text parts of the form, in
// the order they are written.
func (c *Client) FormFields() []api.FormField {
	fields := []api.FormField{{Name: "model", Value: c.cfg.Endpoint.Model}}
	if format := c.cfg.Settings.ResponseFormat; format != "" {
		fields = append(fields, api.FormField{Name: "response_format", Value: format})
	}
	return fields
}

type response struct {
	// Text is a pointer because its absence is an error, not silence. A
	// transcript of silent audio is a present, empty string; a reply without
	// the field is some other document. vox-box (GPUStack's audio backend)
	// returns its HTTPException from the route instead of raising it, so a
	// failed transcription can arrive as a 2xx whose body is the exception.
	Text     *string `json:"text"`
	Segments []struct {
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Text  string  `json:"text"`
	} `json:"segments"`
	Detail any `json:"detail"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Transcribe sends one audio file.
func (c *Client) Transcribe(ctx context.Context, audio []byte, fileName string) (*api.Transcription, error) {
	body, contentType, err := api.EncodeMultipart(c.FormFields(), api.FormFile{
		Field: "file", FileName: fileName, Data: audio,
	})
	if err != nil {
		return nil, err
	}
	var decoded response
	err = c.cfg.Endpoint.PostMultipartWithRetry(
		ctx, c.url(), body, contentType, &decoded, c.cfg.Retry, "transcription",
	)
	if err != nil {
		return nil, err
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return nil, fmt.Errorf("transcription API error: %s", decoded.Error.Message)
	}
	if decoded.Text == nil {
		if decoded.Detail != nil {
			return nil, fmt.Errorf("transcription reply carries no text: %v", decoded.Detail)
		}
		return nil, fmt.Errorf("transcription reply carries no text")
	}
	out := &api.Transcription{Text: strings.TrimSpace(*decoded.Text)}
	for _, s := range decoded.Segments {
		out.Segments = append(out.Segments, api.TranscriptionSegment{
			Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text),
		})
	}
	return out, nil
}
