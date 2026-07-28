package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	docReaderArtifactStage      = "parse"
	docReaderArtifactKeyVersion = uint16(1)
	docReaderArtifactSchema     = "docreader.read-result.v1"
	docReaderArtifactMaxInline  = 16 << 20
)

var errUncacheableDocReaderResult = errors.New("uncacheable DocReader result")

type docReaderArtifactRequest struct {
	FileContentDigest     string            `json:"file_content_digest"`
	FileName              string            `json:"file_name"`
	FileType              string            `json:"file_type"`
	Title                 string            `json:"title"`
	ParserEngine          string            `json:"parser_engine"`
	ParserEngineOverrides map[string]string `json:"parser_engine_overrides"`
}

type docReaderArtifactImage struct {
	Filename    string `json:"filename"`
	OriginalRef string `json:"original_ref"`
	MimeType    string `json:"mime_type"`
	ImageData   []byte `json:"image_data"`
	IsOriginal  bool   `json:"is_original"`
}

type docReaderArtifactValue struct {
	Version         uint8                    `json:"version"`
	MarkdownContent string                   `json:"markdown_content"`
	ImageRefs       []docReaderArtifactImage `json:"image_refs"`
	Metadata        map[string]string        `json:"metadata"`
	IsAudio         bool                     `json:"is_audio"`
	AudioData       []byte                   `json:"audio_data"`
}

func (s *knowledgeService) callDocReaderWithArtifact(
	ctx context.Context,
	reader interfaces.DocReader,
	request *types.ReadRequest,
) (*types.ReadResult, error) {
	if s.artifactRuntime == nil || request == nil || request.URL != "" {
		return s.callDocReaderWithTimeout(ctx, reader, request)
	}
	expected, cacheable := s.docReaderArtifactExpected(ctx, request)
	if !cacheable {
		return s.callDocReaderWithTimeout(ctx, reader, request)
	}

	var computed *types.ReadResult
	value, err := s.artifactRuntime.LoadOrCompute(ctx, expected, func(ctx context.Context) ([]byte, error) {
		result, callErr := s.callDocReaderWithTimeout(ctx, reader, request)
		if callErr != nil {
			return nil, callErr
		}
		if result == nil {
			return nil, errors.New("DocReader returned a nil result")
		}
		computed = result
		if result.Error != "" {
			return nil, fmt.Errorf("%w: provider reported an error", errUncacheableDocReaderResult)
		}
		payload, encodeErr := encodeDocReaderArtifact(result)
		if encodeErr != nil {
			return nil, fmt.Errorf("%w: %v", errUncacheableDocReaderResult, encodeErr)
		}
		return payload, nil
	})
	if errors.Is(err, errUncacheableDocReaderResult) && computed != nil {
		return computed, nil
	}
	if err != nil {
		return nil, err
	}
	return decodeDocReaderArtifact(value.Payload)
}

func (s *knowledgeService) docReaderArtifactExpected(
	ctx context.Context,
	request *types.ReadRequest,
) (artifact.Expected, bool) {
	overrides, cacheable := sanitizedDocReaderOverrides(request.ParserEngineOverrides)
	if !cacheable {
		return artifact.Expected{}, false
	}
	endpoint := ""
	transport := ""
	if s.config != nil && s.config.DocReader != nil {
		endpoint = s.config.DocReader.Addr
		transport = s.config.DocReader.Transport
		if !safeArtifactEndpoint(endpoint) {
			return artifact.Expected{}, false
		}
	}
	inputDigest := artifact.SHA256Hex(request.FileContent)
	rendered := docReaderArtifactRequest{
		FileContentDigest:     inputDigest,
		FileName:              request.FileName,
		FileType:              request.FileType,
		Title:                 request.Title,
		ParserEngine:          request.ParserEngine,
		ParserEngineOverrides: overrides,
	}
	tenantID, _ := ctx.Value(types.TenantIDContextKey).(uint64)
	if tenantID == 0 {
		return artifact.Expected{}, false
	}
	key, err := artifact.BuildKey(tenantID, artifact.KeyMaterial{
		KeyVersion: docReaderArtifactKeyVersion,
		Stage:      docReaderArtifactStage,
		DirectInputs: []artifact.DirectInput{{
			Role:   "file_content",
			Digest: inputDigest,
		}},
		Processor: artifact.ProcessorIdentity{
			ModelName:        request.ParserEngine,
			Source:           "docreader",
			Provider:         transport,
			EndpointIdentity: endpoint,
			Revision:         "docreader-contract.v1",
			Parameters: map[string]any{
				"parser_engine":    request.ParserEngine,
				"config_overrides": overrides,
			},
		},
		RenderedRequest:      rendered,
		Options:              overrides,
		CanonicalizerVersion: artifact.CanonicalJSONVersion,
		OutputSchemaVersion:  docReaderArtifactSchema,
	})
	if err != nil {
		return artifact.Expected{}, false
	}
	return artifact.Expected{
		Key:   key,
		Codec: artifact.CodecJSONV1,
		Validate: func(payload []byte) error {
			_, err := decodeDocReaderArtifact(payload)
			return err
		},
		Cacheable: func(payload []byte) bool {
			return len(payload) <= docReaderArtifactMaxInline
		},
	}, true
}

func encodeDocReaderArtifact(result *types.ReadResult) ([]byte, error) {
	if result == nil {
		return nil, errors.New("DocReader result must not be nil")
	}
	value := docReaderArtifactValue{
		Version:         1,
		MarkdownContent: result.MarkdownContent,
		ImageRefs:       make([]docReaderArtifactImage, 0, len(result.ImageRefs)),
		Metadata:        result.Metadata,
		IsAudio:         result.IsAudio,
		AudioData:       append([]byte(nil), result.AudioData...),
	}
	if value.Metadata == nil {
		value.Metadata = map[string]string{}
	}
	if !utf8.ValidString(value.MarkdownContent) {
		return nil, errors.New("DocReader markdown is not valid UTF-8")
	}
	for _, image := range result.ImageRefs {
		if !utf8.ValidString(image.Filename) ||
			!utf8.ValidString(image.OriginalRef) ||
			!utf8.ValidString(image.MimeType) {
			return nil, errors.New("DocReader image metadata is not valid UTF-8")
		}
		value.ImageRefs = append(value.ImageRefs, docReaderArtifactImage{
			Filename:    image.Filename,
			OriginalRef: image.OriginalRef,
			MimeType:    image.MimeType,
			ImageData:   append([]byte(nil), image.ImageData...),
			IsOriginal:  image.IsOriginal,
		})
	}
	if err := artifact.ValidateOwnershipFreeJSON(value); err != nil {
		return nil, err
	}
	return artifact.CanonicalJSON(value)
}

func decodeDocReaderArtifact(payload []byte) (*types.ReadResult, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var value docReaderArtifactValue
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("DocReader artifact has trailing JSON")
	}
	if value.Version != 1 || value.ImageRefs == nil || value.Metadata == nil {
		return nil, errors.New("DocReader artifact schema is incomplete")
	}
	result := &types.ReadResult{
		MarkdownContent: value.MarkdownContent,
		Metadata:        value.Metadata,
		IsAudio:         value.IsAudio,
		AudioData:       append([]byte(nil), value.AudioData...),
		ImageRefs:       make([]types.ImageRef, 0, len(value.ImageRefs)),
	}
	for _, image := range value.ImageRefs {
		result.ImageRefs = append(result.ImageRefs, types.ImageRef{
			Filename:    image.Filename,
			OriginalRef: image.OriginalRef,
			MimeType:    image.MimeType,
			ImageData:   append([]byte(nil), image.ImageData...),
			IsOriginal:  image.IsOriginal,
		})
	}
	return result, nil
}

func sanitizedDocReaderOverrides(overrides map[string]string) (map[string]string, bool) {
	result := make(map[string]string)
	for key, value := range overrides {
		normalized := strings.NewReplacer("_", "", "-", "", ".", "").Replace(strings.ToLower(key))
		if strings.Contains(normalized, "apikey") ||
			strings.Contains(normalized, "token") ||
			strings.Contains(normalized, "secret") ||
			strings.Contains(normalized, "password") ||
			strings.Contains(normalized, "signature") {
			continue
		}
		if (strings.Contains(normalized, "endpoint") || strings.Contains(normalized, "url")) &&
			!safeArtifactEndpoint(value) {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func safeArtifactEndpoint(value string) bool {
	if value == "" || !strings.Contains(value, "://") {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil &&
		parsed.Scheme != "" &&
		parsed.Host != "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		parsed.Fragment == ""
}
