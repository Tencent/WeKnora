package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const vlmArtifactKeyVersion uint16 = 1

var (
	errVLMArtifactProvider = errors.New("VLM artifact provider failed")
	errVLMArtifactStore    = errors.New("VLM artifact store failed")
)

type vlmArtifactResultKind string

const (
	vlmArtifactOCR     vlmArtifactResultKind = "vlm.ocr"
	vlmArtifactCaption vlmArtifactResultKind = "vlm.caption"
)

type vlmArtifactRequest struct {
	tenantID             uint64
	imageBytes           []byte
	model                vlm.VLM
	modelRevision        string
	config               types.VLMConfig
	prompt               string
	promptVersion        string
	resultKind           vlmArtifactResultKind
	canonicalizerVersion string
	canonicalize         func(string) string
}

func newVLMArtifactKey(request vlmArtifactRequest) (types.ProcessingArtifactKey, error) {
	if err := validateVLMArtifactRequest(request); err != nil {
		return types.ProcessingArtifactKey{}, err
	}

	imageDigest := sha256.Sum256(request.imageBytes)
	promptDigest := sha256.Sum256([]byte(request.prompt))
	modelDigest, err := vlmArtifactModelDigest(request.model, request.modelRevision, request.config)
	if err != nil {
		return types.ProcessingArtifactKey{}, err
	}

	return types.NewProcessingArtifactKey(
		request.tenantID,
		string(request.resultKind),
		vlmArtifactKeyVersion,
		imageDigest[:],
		modelDigest[:],
		promptDigest[:],
		[]byte(request.promptVersion),
		[]byte(request.canonicalizerVersion),
	)
}

func validateVLMArtifactRequest(request vlmArtifactRequest) error {
	if request.model == nil {
		return errors.New("VLM artifact model must not be nil")
	}
	if request.resultKind != vlmArtifactOCR && request.resultKind != vlmArtifactCaption {
		return errors.New("invalid VLM artifact result kind")
	}
	if strings.TrimSpace(request.promptVersion) == "" {
		return errors.New("VLM artifact prompt version must not be empty")
	}
	if strings.TrimSpace(request.canonicalizerVersion) == "" {
		return errors.New("VLM artifact canonicalizer version must not be empty")
	}
	if request.canonicalize == nil {
		return errors.New("VLM artifact canonicalizer must not be nil")
	}
	return nil
}

func vlmArtifactModelDigest(
	model vlm.VLM,
	modelRevision string,
	config types.VLMConfig,
) ([sha256.Size]byte, error) {
	if modelID := strings.TrimSpace(model.GetModelID()); modelID != "" {
		descriptor := strings.Join([]string{
			"model-id",
			modelID,
			strings.TrimSpace(model.GetModelName()),
			strings.TrimSpace(modelRevision),
		}, "\x00")
		return sha256.Sum256([]byte(descriptor)), nil
	}

	endpoint, err := url.Parse(config.BaseURL)
	if err != nil {
		return [sha256.Size]byte{}, errors.New("invalid VLM artifact endpoint URL")
	}
	endpoint.User = nil
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	endpoint.Scheme = strings.ToLower(endpoint.Scheme)
	endpoint.Host = strings.ToLower(endpoint.Host)
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")

	interfaceType := strings.ToLower(strings.TrimSpace(config.InterfaceType))
	if interfaceType == "" {
		interfaceType = "openai"
	}
	modelName := strings.TrimSpace(model.GetModelName())
	if modelName == "" {
		modelName = strings.TrimSpace(config.ModelName)
	}
	descriptor := strings.Join([]string{interfaceType, modelName, endpoint.String()}, "\x00")
	return sha256.Sum256([]byte(descriptor)), nil
}

func predictVLMArtifact(
	ctx context.Context,
	store interfaces.ProcessingArtifactStore,
	request vlmArtifactRequest,
) (string, bool, error) {
	if err := validateVLMArtifactRequest(request); err != nil {
		return "", false, err
	}
	if store == nil {
		value, err := request.model.Predict(ctx, [][]byte{request.imageBytes}, request.prompt)
		if err != nil {
			return "", false, fmt.Errorf("%w: %w", errVLMArtifactProvider, err)
		}
		if request.canonicalize != nil {
			value = request.canonicalize(value)
		}
		return value, false, nil
	}

	key, err := newVLMArtifactKey(request)
	if err != nil {
		return "", false, err
	}
	value, hit, err := store.Get(ctx, key)
	if err != nil {
		return "", false, fmt.Errorf("%w: get: %w", errVLMArtifactStore, err)
	}
	if hit {
		return string(value), true, nil
	}

	valueText, err := request.model.Predict(ctx, [][]byte{request.imageBytes}, request.prompt)
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", errVLMArtifactProvider, err)
	}
	if request.canonicalize != nil {
		valueText = request.canonicalize(valueText)
	}
	canonical, _, err := store.PutIfAbsent(ctx, key, []byte(valueText))
	if err != nil {
		return "", false, fmt.Errorf("%w: put: %w", errVLMArtifactStore, err)
	}
	return string(canonical), false, nil
}
