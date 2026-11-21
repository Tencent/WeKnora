// Package providers registers Volcano Engine Speech (火山引擎语音技术) through
// its one-sentence recognition HTTP API — the file-transcription entry of the
// 豆包语音 stack.
//
// Facts (https://www.volcengine.com/docs/6561/80818, 一句话识别):
//   - the endpoint is https://openspeech.bytedance.com/api/v1/auc; the body
//     is a JSON header followed by the raw audio bytes, sized by audio_size;
//   - credentials are an AppID plus an access token from the speech console;
//     auth is `Authorization: Bearer; <token>` (semicolon, not a space), so
//     the protocol client stamps the header itself and this definition
//     declares AuthNone with RequiresAuth keeping the field mandatory;
//   - cluster selects the recognition model; the console default is
//     volcengine_streaming_common;
//   - the documented upload is one audio under 60 seconds and 10 MB, in wav
//     or mp3 for the sync interface.
//
// Unverified: the streaming sibling of this API serves bigger clips on a
// WebSocket binary protocol this build does not speak; an operator needing
// realtime streaming keeps today's proxy arrangement. The one-sentence reply
// carries no total duration, so callers see none.
package providers

import (
	_ "embed"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/types"
)

// The speech product carries the same Volcano Engine brand mark as Ark.
//
//go:embed assets/volcengine.svg
var volcengineSpeechIcon []byte

// VolcengineSpeechID is the provider identifier stored on model rows.
const VolcengineSpeechID = "volcengine-speech"

// VolcengineSpeechBaseURL is the openspeech host the one-sentence API lives on.
const VolcengineSpeechBaseURL = "https://openspeech.bytedance.com"

func newVolcengineSpeechProvider() *Definition {
	asrOnly := []types.ModelType{types.ModelTypeASR}
	return &Definition{
		ID:   VolcengineSpeechID,
		Name: "Volcano Engine Speech",
		Names: map[string]string{
			"zh-CN": "火山引擎语音",
		},
		Description: "豆包语音识别 · 一句话识别(HTTP)",
		Descriptions: map[string]string{
			"zh-CN": "豆包语音识别 · 一句话识别(HTTP)",
		},
		Website:      "https://www.volcengine.com/product/doubao",
		Icon:         volcengineSpeechIcon,
		API:          api.APIOpenAICompletions,
		Order:        13,
		RequiresAuth: true,
		// The wire auth is `Bearer; <token>` — a semicolon no shared auth
		// style expresses — so the protocol client stamps the header itself.
		// AuthNone keeps Endpoint from attaching a competing header while
		// RequiresAuth still makes the token field mandatory.
		Auth:        AuthNone,
		URLPatterns: []string{"openspeech.bytedance.com"},
		DefaultBaseURLs: map[types.ModelType]string{
			types.ModelTypeASR: VolcengineSpeechBaseURL,
		},
		TranscriptionAPI: api.TranscriptionVolcengineAUC,
		ModelTypes:       asrOnly,
		// The primary credential is the speech console's access token, not an
		// sk- style API key; calling it "API Key" invites pasting an Ark LLM
		// key that can never authenticate this host.
		CredentialLabels: []CredentialLabel{{
			Label:        "Access Token",
			Labels:       map[string]string{"zh-CN": "访问令牌（Access Token）"},
			Placeholder:  "Volcano Engine speech access token",
			Placeholders: map[string]string{"zh-CN": "火山语音控制台签发的 Access Token"},
			Hint:         "The token from the 豆包语音 console, paired with the AppID below; not an Ark LLM API key.",
			Hints: map[string]string{
				"zh-CN": "豆包语音控制台签发的令牌，与下方 AppID 配对；不是方舟大模型的 API Key。",
			},
			ModelTypes: asrOnly,
			Required:   true,
		}},
		ExtraFields: []ExtraField{
			{
				Key:          "app_id",
				Label:        "AppID",
				Labels:       map[string]string{"zh-CN": "AppID"},
				Type:         "string",
				Required:     true,
				Placeholder:  "Volcano Engine speech AppID",
				Placeholders: map[string]string{"zh-CN": "火山语音应用的 AppID"},
			},
			{
				Key:         "cluster",
				Label:       "Cluster",
				Labels:      map[string]string{"zh-CN": "识别集群（Cluster）"},
				Type:        "string",
				Default:     "volcengine_streaming_common",
				Placeholder: "volcengine_streaming_common",
			},
		},
		Compat: VendorCompat{
			Transcriptions: api.TranscriptionsCompat{
				Path: api.Ptr("/api/v1/auc"),
				// One-sentence recognition takes wav and mp3 on the sync
				// interface; larger or exotic containers belong to the
				// streaming service this build does not speak.
				Formats:      []string{"wav", "mp3"},
				MaxFileBytes: api.Ptr(10 << 20),
			},
		},
	}
}
