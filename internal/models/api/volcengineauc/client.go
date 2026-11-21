// Package volcengineauc implements Volcano Engine's one-sentence recognition
// HTTP API — the file-transcription half of the 豆包语音 speech stack, and the
// only part of it that is a plain request/response round trip.
//
// Wire facts (https://www.volcengine.com/docs/6561/80818):
//   - the request goes to POST {base}/api/v1/auc as one body that is a JSON
//     header followed immediately by the raw audio bytes; the header declares
//     the audio length in audio_size and the whole body travels as
//     application/json;
//   - auth is `Authorization: Bearer; <token>` — a semicolon, not the space
//     every other Bearer scheme on this host uses — and the same token is
//     also echoed inside the header's app object next to appid and cluster;
//   - the reply is {code, message, result: [{text, utterances}]}, where code
//     1000 means success and utterance timestamps are milliseconds.
//
// Unverified: the documented one-sentence limits (audio under 60 s and under
// 10 MB) and the accepted format list are enforced where they are declared,
// on the vendor definition; this client itself only maps the file extension
// into the audio.format field.
package volcengineauc

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/Tencent/WeKnora/internal/models/api"
)

// DefaultPath is the one-sentence recognition endpoint under the base URL.
const DefaultPath = "/api/v1/auc"

// DefaultCluster is the recognition cluster the console hands out when the
// operator has not pinned a specific one.
const DefaultCluster = "volcengine_streaming_common"

// Config is everything the client needs, already resolved by the api layer.
// Token, AppID and Cluster are Volcano-specific credentials that no shared
// auth hook can carry — see the request shape above — so they arrive
// explicitly, the way tencentlkeap takes its secret pair.
type Config struct {
	Endpoint api.Endpoint
	Settings api.TranscriptionsSettings
	// Token is the access token from the console; sent both as the Bearer;
	// header and inside the app object.
	Token string
	// AppID is the console application id; empty fails at New.
	AppID string
	// Cluster selects the recognition model; empty means DefaultCluster.
	Cluster string
}

// Client talks the one-sentence recognition shape to one endpoint.
type Client struct {
	cfg Config
}

// New builds a client. Credentials are not checked here — presence is the
// model row's validation at save time, and a wrong one is the vendor's error
// code surfacing at call time, the same contract every other protocol speaks.
func New(cfg Config) *Client { return &Client{cfg: cfg} }

func (c *Client) url() string {
	if c.cfg.Endpoint.URL != "" {
		return c.cfg.Endpoint.Resolve("")
	}
	path := c.cfg.Settings.Path
	if path == "" {
		path = DefaultPath
	}
	if strings.HasSuffix(strings.TrimRight(c.cfg.Endpoint.BaseURL, "/"), path) {
		path = ""
	}
	return c.cfg.Endpoint.Resolve(path)
}

// audioFormat maps the upload's extension into the audio.format field. The
// vendor declaration keeps anything else from reaching here, so the default
// only names the shape the transport layer already assumed.
func audioFormat(fileName string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(fileName), ".")) {
	case "wav":
		return "wav"
	default:
		return "mp3"
	}
}

type requestHeader struct {
	App struct {
		AppID   string `json:"appid"`
		Token   string `json:"token"`
		Cluster string `json:"cluster"`
	} `json:"app"`
	User struct {
		UID string `json:"uid"`
	} `json:"user"`
	Request struct {
		ReqID          string `json:"reqid"`
		NBest          int    `json:"nbest"`
		Workflow       string `json:"workflow"`
		ShowLanguage   bool   `json:"show_language"`
		ShowUtterances bool   `json:"show_utterances"`
		ResultType     string `json:"result_type"`
		Sequence       int    `json:"sequence"`
	} `json:"request"`
	Audio struct {
		Format string `json:"format"`
		// Language stays unsent: the HTTP one-sentence API documents
		// auto-detection and takes no language field (its WebSocket sibling
		// does), so the operator's hint has nowhere documented to go.
		Rate    int    `json:"rate"`
		Bits    int    `json:"bits"`
		Channel int    `json:"channel"`
		Codec   string `json:"codec"`
	} `json:"audio"`
	AudioSize int `json:"audio_size"`
}

type utterance struct {
	Definite  bool   `json:"definite"`
	StartTime int64  `json:"start_time"` // milliseconds
	EndTime   int64  `json:"end_time"`   // milliseconds
	Text      string `json:"text"`
}

type result struct {
	Text       string      `json:"text"`
	Utterances []utterance `json:"utterances"`
}

type response struct {
	// Code is a pointer: a reply without one is not a recognition result at
	// all (a gateway error page, say), and must say so rather than read as
	// the zero code.
	Code    *int     `json:"code"`
	Message string   `json:"message"`
	Result  []result `json:"result"`
}

// successCode is the only reply code the documentation calls success.
const successCode = 1000

// Transcribe sends one audio file.
func (c *Client) Transcribe(ctx context.Context, req api.TranscriptionRequest) (*api.Transcription, error) {
	if len(req.Audio) == 0 {
		return nil, fmt.Errorf("audio bytes are empty")
	}
	format := audioFormat(req.FileName)

	var header requestHeader
	header.App.AppID = c.cfg.AppID
	header.App.Token = c.cfg.Token
	header.App.Cluster = c.cfg.Cluster
	if header.App.Cluster == "" {
		header.App.Cluster = DefaultCluster
	}
	header.User.UID = "weknora"
	header.Request.ReqID = uuid.NewString()
	header.Request.NBest = 1
	header.Request.Workflow = "audio_in,resample,partition,vad,fe,decode,itn"
	header.Request.ShowUtterances = true
	header.Request.ResultType = "full"
	header.Request.Sequence = 1
	header.Audio.Format = format
	header.Audio.Rate = 16000
	header.Audio.Bits = 16
	header.Audio.Channel = 1
	header.Audio.Codec = "raw"
	header.AudioSize = len(req.Audio)

	jsonHeader, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("marshal request header: %w", err)
	}
	body := make([]byte, 0, len(jsonHeader)+len(req.Audio))
	body = append(body, jsonHeader...)
	body = append(body, req.Audio...)

	// The body travels exactly as built — header then audio — so it goes out
	// through the raw request path, not NewRequest's JSON marshal, which
	// would refuse the appended audio as trailing garbage.
	httpReq, err := c.cfg.Endpoint.NewRawRequest(ctx, c.url(), "application/json", body)
	if err != nil {
		return nil, err
	}
	// The auth scheme is `Bearer; <token>` with a semicolon — not the space
	// form Endpoint's BearerAuth stamps — so the header is set here, after
	// NewRequest applied (no-op) endpoint auth and custom headers.
	httpReq.Header.Set("Authorization", "Bearer; "+c.cfg.Token)

	resp, err := c.cfg.Endpoint.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := api.ReadResponseCapped(resp.Body, 0)
	if err != nil {
		return nil, err
	}

	var decoded response
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if decoded.Code == nil {
		return nil, fmt.Errorf("volcengine auc reply carries no code: %.200s", raw)
	}
	if *decoded.Code != successCode {
		return nil, fmt.Errorf("volcengine auc error %d: %s", *decoded.Code, decoded.Message)
	}
	if len(decoded.Result) == 0 {
		return nil, fmt.Errorf("volcengine auc reply carries no result")
	}

	out := &api.Transcription{Text: strings.TrimSpace(decoded.Result[0].Text)}
	for _, u := range decoded.Result[0].Utterances {
		out.Segments = append(out.Segments, api.TranscriptionSegment{
			Start: float64(u.StartTime) / 1000,
			End:   float64(u.EndTime) / 1000,
			Text:  strings.TrimSpace(u.Text),
		})
	}
	return out, nil
}
