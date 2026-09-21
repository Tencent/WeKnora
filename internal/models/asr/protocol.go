package asr

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/openaichataudio"
	"github.com/Tencent/WeKnora/internal/models/api/openaitranscriptions"
	"github.com/Tencent/WeKnora/internal/models/catalog"
	// catalog.Resolve answers from the vendor catalog, which is empty until
	// the vendor packages have run their init.
	_ "github.com/Tencent/WeKnora/internal/models/vendors"
	"github.com/Tencent/WeKnora/internal/types"
)

// retryPolicy is the transport-error retry budget, which is none. The
// knowledge pipeline retries a failed transcription as a whole task, and a
// client-level retry on top would turn one hung 300-second upload into
// several.
var retryPolicy = func() api.RetryPolicy { return api.RetryPolicy{} }

// newASR resolves the catalog and returns the protocol client for the
// configured model. It mirrors rerank.newReranker and
// embedding.newRemoteEmbedder: the vendor's facts decide the protocol, the
// URL and the credential, and this function knows no vendor names.
func newASR(config *Config) (ASR, error) {
	if config == nil {
		return nil, fmt.Errorf("asr config is nil")
	}
	if strings.TrimSpace(config.ModelName) == "" {
		return nil, fmt.Errorf("model name is required")
	}
	resolved, err := catalog.Resolve(catalog.Ref{
		Provider:  config.Provider,
		Model:     config.ModelName,
		BaseURL:   config.BaseURL,
		ModelType: types.ModelTypeASR,
		Extra:     config.ExtraConfig,
	})
	if err != nil {
		return nil, err
	}
	// A vendor that does not declare speech recognition has not been checked
	// for it, and its Endpoint hook may still compute a path — Azure's does.
	// A row that named the vendor is refused; a row that named none and was
	// matched by its URL is what the pre-catalog client served: an
	// OpenAI-compatible endpoint at that URL.
	if !resolved.Vendor.SupportsType(types.ModelTypeASR) {
		if strings.TrimSpace(config.Provider) != "" {
			return nil, fmt.Errorf("%s does not offer speech recognition in this build", resolved.Vendor.Name)
		}
		resolved, err = catalog.Resolve(catalog.Ref{
			Provider:  catalog.GenericID,
			Model:     config.ModelName,
			BaseURL:   config.BaseURL,
			ModelType: types.ModelTypeASR,
			Extra:     config.ExtraConfig,
		})
		if err != nil {
			return nil, err
		}
	}
	if err := validateASRBaseURL(resolved.BaseURL); err != nil {
		return nil, err
	}

	vendor := resolved.Vendor
	creds := catalog.Credentials{APIKey: config.APIKey}
	if creds.APIKey == "" {
		creds.APIKey = vendor.DefaultAPIKey
	}
	// Vendor headers first so a user header cannot silently replace a vendor
	// beta flag, matching chat.NewRemoteChat.
	headers := make(map[string]string, len(vendor.Headers)+len(config.CustomHeaders))
	for k, v := range vendor.Headers {
		headers[k] = v
	}
	for k, v := range config.CustomHeaders {
		headers[k] = v
	}
	settings := resolved.Transcriptions
	endpoint := api.Endpoint{
		BaseURL: resolved.BaseURL,
		Model:   resolved.RemoteModel,
		ModelID: config.ModelID,
		Auth:    vendor.AuthFunc(vendor.API, creds),
		Headers: headers,
		Client:  newASRHTTPClient(time.Duration(settings.RequestTimeout) * time.Second),
	}
	if vendor.Endpoint != nil {
		if url, query := vendor.Endpoint(catalog.EndpointRequest{
			BaseURL:   resolved.BaseURL,
			Model:     resolved.RemoteModel,
			ModelType: types.ModelTypeASR,
			API:       vendor.API,
			Extra:     config.ExtraConfig,
		}); url != "" {
			if err := validateASRBaseURL(url); err != nil {
				return nil, err
			}
			endpoint.URL, endpoint.Query = url, query
		}
	}

	var client api.Transcriber
	switch resolved.TranscriptionAPI {
	case api.TranscriptionOpenAI:
		client = openaitranscriptions.New(openaitranscriptions.Config{
			Endpoint: endpoint, Settings: settings, Retry: retryPolicy(),
		})
	case api.TranscriptionChatAudio:
		client = openaichataudio.New(openaichataudio.Config{
			Endpoint: endpoint, Settings: settings, Retry: retryPolicy(),
		})
	default:
		return nil, fmt.Errorf("unsupported transcription api %q for provider %s",
			resolved.TranscriptionAPI, vendor.ID)
	}
	return &protocolASR{
		inner:     client,
		settings:  settings,
		vendor:    vendor.Name,
		endpoint:  resolved.BaseURL,
		modelName: config.ModelName,
		modelID:   config.ModelID,
	}, nil
}

// protocolASR adapts a protocol client to the ASR interface and refuses an
// upload the vendor has documented it will not take, before sending it.
type protocolASR struct {
	inner     api.Transcriber
	settings  catalog.TranscriptionsSettings
	vendor    string
	endpoint  string
	modelName string
	modelID   string
}

func (a *protocolASR) GetModelName() string { return a.modelName }
func (a *protocolASR) GetModelID() string   { return a.modelID }

func (a *protocolASR) Transcribe(ctx context.Context, audio []byte, fileName string) (*TranscriptionResult, error) {
	if len(audio) == 0 {
		return nil, fmt.Errorf("audio bytes are empty")
	}
	if limit := a.settings.MaxFileBytes; limit > 0 && len(audio) > limit {
		return nil, fmt.Errorf("%s transcription: the audio is %.1f MB; %s accepts at most %d MB per file",
			a.modelName, float64(len(audio))/(1<<20), a.vendor, limit>>20)
	}
	// The server identifies the format from the extension.
	if fileName == "" {
		fileName = "audio.mp3"
	}
	if formats := a.settings.Formats; len(formats) > 0 {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
		if !slices.Contains(formats, ext) {
			return nil, fmt.Errorf("%s transcription: %s accepts %s audio, not %q",
				a.modelName, a.vendor, strings.Join(formats, "/"), fileName)
		}
	}
	logger.Infof(ctx, "[ASR] transcribing model=%s endpoint=%s size=%d file=%s",
		a.modelName, a.endpoint, len(audio), fileName)

	out, err := a.inner.Transcribe(ctx, api.TranscriptionRequest{
		Audio: audio, FileName: fileName, Language: languageFrom(ctx),
	})
	if err != nil {
		return nil, fmt.Errorf("ASR transcription request failed: %w", err)
	}
	result := &TranscriptionResult{Text: out.Text, Duration: out.Duration}
	for _, s := range out.Segments {
		result.Segments = append(result.Segments, Segment{Start: s.Start, End: s.End, Text: s.Text})
	}
	logger.Infof(ctx, "[ASR] transcription completed, text length=%d", len(result.Text))
	return result, nil
}
