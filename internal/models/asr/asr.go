package asr

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// ASR defines the interface for Automatic Speech Recognition model operations.
type ASR interface {
	// Transcribe sends audio bytes to the ASR model and returns the transcribed text.
	Transcribe(ctx context.Context, audioBytes []byte, fileName string) (string, error)

	GetModelName() string
	GetModelID() string
}

// Config holds the configuration needed to create an ASR instance.
type Config struct {
	Source        types.ModelSource
	BaseURL       string
	ModelName     string
	APIKey        string
	ModelID       string
	InterfaceType string // "openai" (default)
	Language      string // optional: specify language for transcription
}

// NewASR creates an ASR instance based on the provided configuration.
func NewASR(config *Config) (ASR, error) {
	ifType := strings.ToLower(config.InterfaceType)
	if ifType == "" {
		ifType = "openai"
	}

	switch ifType {
	case "openai":
		return NewOpenAIASR(config)
	default:
		return nil, fmt.Errorf("unsupported ASR interface type: %s", ifType)
	}
}
