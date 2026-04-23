package asr

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// Segment represents a transcribed segment with timestamps.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

// TranscriptionResult holds the full text and its segments.
type TranscriptionResult struct {
	Text     string    `json:"text"`
	Segments []Segment `json:"segments,omitempty"`
}

// ASR defines the interface for Automatic Speech Recognition model operations.
type ASR interface {
	// Transcribe sends audio bytes to the ASR model and returns the transcribed text and segments.
	Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error)

	GetModelName() string
	GetModelID() string
}

// Config holds the configuration needed to create an ASR instance.
type Config struct {
	Source    types.ModelSource
	BaseURL   string
	ModelName string
	APIKey    string
	ModelID   string
	Language  string // optional: specify language for transcription
	// CustomHeaders 允许在调用远程 API 时附加自定义 HTTP 请求头（类似 OpenAI Python SDK 的 extra_headers）。
	CustomHeaders map[string]string
}

// ConfigFromModel 根据 types.Model 构造 asr.Config。
// 生产路径（从 DB 拉起）和测试连接路径（临时表单）共享这份映射。
// 当前 ASR 不涉及 WeKnoraCloud 凭证，所以签名不含 appID/appSecret。
func ConfigFromModel(m *types.Model) *Config {
	if m == nil {
		return nil
	}
	return &Config{
		ModelID:       m.ID,
		APIKey:        m.Parameters.APIKey,
		BaseURL:       m.Parameters.BaseURL,
		ModelName:     m.Name,
		Source:        m.Source,
		CustomHeaders: m.Parameters.CustomHeaders,
	}
}

// NewASR creates an ASR instance based on the provided configuration.
// All ASR vendors use the OpenAI-compatible /v1/audio/transcriptions API.
func NewASR(config *Config) (ASR, error) {
	a, err := NewOpenAIASR(config)
	return wrapASRLangfuse(a, err)
}
