package asr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	openai "github.com/sashabaranov/go-openai"
)

const (
	asrDefaultTimeout = 300 * time.Second // audio transcription can be slow
)

// OpenAIASR implements ASR via an OpenAI-compatible audio transcriptions API.
type OpenAIASR struct {
	modelName string
	modelID   string
	client    *openai.Client
	baseURL   string
	language  string
}

// NewOpenAIASR creates an OpenAI-compatible ASR instance.
func NewOpenAIASR(config *Config) (*OpenAIASR, error) {
	apiCfg := openai.DefaultConfig(config.APIKey)
	if config.BaseURL != "" {
		apiCfg.BaseURL = config.BaseURL
	}
	httpClient := &http.Client{Timeout: asrDefaultTimeout}

	// 注入用户自定义 HTTP header（类似 OpenAI Python SDK 的 extra_headers）
	if len(config.CustomHeaders) > 0 {
		apiCfg.HTTPClient = secutils.WrapHTTPClientWithHeaders(httpClient, config.CustomHeaders)
	} else {
		apiCfg.HTTPClient = httpClient
	}

	return &OpenAIASR{
		modelName: config.ModelName,
		modelID:   config.ModelID,
		client:    openai.NewClientWithConfig(apiCfg),
		baseURL:   config.BaseURL,
		language:  config.Language,
	}, nil
}

// Transcribe sends audio bytes to the OpenAI-compatible audio transcriptions API.
func (s *OpenAIASR) Transcribe(ctx context.Context, audioBytes []byte, fileName string) (*TranscriptionResult, error) {
	if len(audioBytes) == 0 {
		return nil, fmt.Errorf("audio bytes are empty")
	}

	// Ensure fileName has an extension for proper MIME type detection
	if fileName == "" {
		fileName = "audio.mp3"
	}

	logger.Infof(ctx, "[ASR] Calling OpenAI-compatible transcription API, model=%s, baseURL=%s, audioSize=%d, file=%s",
		s.modelName, s.baseURL, len(audioBytes), fileName)

	req := openai.AudioRequest{
		Model:    s.modelName,
		FilePath: fileName,
		Reader:   bytes.NewReader(audioBytes),
		Format:   openai.AudioResponseFormatVerboseJSON,
	}

	if s.language != "" {
		req.Language = s.language
	}

	resp, err := s.client.CreateTranscription(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("ASR transcription request failed: %w", err)
	}

	jsonData, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("marshal transcription response: %w", err)
	}
	logger.Debugf(ctx, "[ASR] Transcription response: %s", string(jsonData))

	text := strings.TrimSpace(resp.Text)
	logger.Infof(ctx, "[ASR] Transcription completed, text length=%d", len(text))

	var segments []Segment
	for _, s := range resp.Segments {
		segments = append(segments, Segment{
			Start: s.Start,
			End:   s.End,
			Text:  strings.TrimSpace(s.Text),
		})
	}

	return &TranscriptionResult{
		Text:     text,
		Segments: segments,
	}, nil
}

func (s *OpenAIASR) GetModelName() string { return s.modelName }
func (s *OpenAIASR) GetModelID() string   { return s.modelID }

// DetectAudioFormat returns a file extension hint for the given audio bytes.
func DetectAudioFormat(data []byte, fileName string) string {
	if fileName != "" {
		ext := strings.ToLower(filepath.Ext(fileName))
		if ext != "" {
			return ext
		}
	}
	// Try to detect from magic bytes
	if len(data) >= 4 {
		// MP3: starts with ID3 or 0xFF 0xFB
		if data[0] == 'I' && data[1] == 'D' && data[2] == '3' {
			return ".mp3"
		}
		if data[0] == 0xFF && (data[1]&0xE0) == 0xE0 {
			return ".mp3"
		}
		// FLAC: starts with "fLaC"
		if data[0] == 'f' && data[1] == 'L' && data[2] == 'a' && data[3] == 'C' {
			return ".flac"
		}
		// OGG: starts with "OggS"
		if data[0] == 'O' && data[1] == 'g' && data[2] == 'g' && data[3] == 'S' {
			return ".ogg"
		}
		// WAV: starts with "RIFF"
		if data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F' {
			return ".wav"
		}
	}
	// M4A: check for ftyp box
	if len(data) >= 8 && data[4] == 'f' && data[5] == 't' && data[6] == 'y' && data[7] == 'p' {
		return ".m4a"
	}
	return ".mp3" // default
}
