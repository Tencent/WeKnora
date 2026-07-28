package artifact

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Tencent/WeKnora/internal/types"
)

const (
	CodecJSONV1      = "json.v1"
	CodecFloat32BEV1 = "float32be.v1"
	CodecTextUTF8V1  = "text.utf8.v1"
)

var ErrCorruptArtifact = errors.New("corrupt processing artifact")

// NewInlineArtifact freezes a successful ownership-free output. A nil payload
// is represented as a non-nil empty byte slice so successful empty output is
// distinct from an external-object manifest.
func NewInlineArtifact(key Key, codec string, payload []byte) (*types.ProcessingArtifact, error) {
	if err := key.Lookup.Validate(); err != nil {
		return nil, err
	}
	if !isSHA256(key.ProcessorDigest) {
		return nil, errors.New("artifact processor digest must be 64 lowercase hex characters")
	}
	if key.OutputSchema == "" {
		return nil, errors.New("artifact output schema must not be empty")
	}
	if codec == "" {
		return nil, errors.New("artifact codec must not be empty")
	}
	frozen := make([]byte, len(payload))
	copy(frozen, payload)
	digest := SHA256Hex(frozen)
	return &types.ProcessingArtifact{
		TenantID:        key.Lookup.TenantID,
		Stage:           key.Lookup.Stage,
		KeyVersion:      key.Lookup.KeyVersion,
		ArtifactKey:     key.Lookup.ArtifactKey,
		ProcessorDigest: key.ProcessorDigest,
		OutputDigest:    digest,
		OutputSchema:    key.OutputSchema,
		Codec:           codec,
		InlinePayload:   true,
		Payload:         frozen,
		PayloadChecksum: digest,
		SizeBytes:       int64(len(frozen)),
	}, nil
}

// EncodeJSON canonicalizes and checks ownership before constructing a manifest.
func EncodeJSON(key Key, value any) (*types.ProcessingArtifact, error) {
	if err := ValidateOwnershipFreeJSON(value); err != nil {
		return nil, err
	}
	payload, err := CanonicalJSON(value)
	if err != nil {
		return nil, err
	}
	return NewInlineArtifact(key, CodecJSONV1, payload)
}

// DecodeInline validates manifest identity, schema, size, storage checksum and
// semantic output digest before returning a detached payload.
func DecodeInline(
	artifact *types.ProcessingArtifact,
	key types.ProcessingArtifactLookup,
	outputSchema string,
	codec string,
) ([]byte, error) {
	if artifact == nil {
		return nil, fmt.Errorf("%w: manifest is nil", ErrCorruptArtifact)
	}
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptArtifact, err)
	}
	if artifact.Lookup() != key {
		return nil, fmt.Errorf("%w: manifest key mismatch", ErrCorruptArtifact)
	}
	if artifact.OutputSchema != outputSchema {
		return nil, fmt.Errorf("%w: output schema mismatch", ErrCorruptArtifact)
	}
	if artifact.Codec != codec {
		return nil, fmt.Errorf("%w: codec mismatch", ErrCorruptArtifact)
	}
	if !artifact.InlinePayload {
		return nil, fmt.Errorf("%w: object payload reader is not configured", ErrCorruptArtifact)
	}
	if int64(len(artifact.Payload)) != artifact.SizeBytes {
		return nil, fmt.Errorf("%w: payload size mismatch", ErrCorruptArtifact)
	}
	checksum := SHA256Hex(artifact.Payload)
	if checksum != artifact.PayloadChecksum {
		return nil, fmt.Errorf("%w: payload checksum mismatch", ErrCorruptArtifact)
	}
	if checksum != artifact.OutputDigest {
		return nil, fmt.Errorf("%w: output digest mismatch", ErrCorruptArtifact)
	}
	result := make([]byte, len(artifact.Payload))
	copy(result, artifact.Payload)
	return result, nil
}

// DecodeJSON additionally rejects trailing data and validates that cached
// payloads remain ownership-free.
func DecodeJSON(
	artifact *types.ProcessingArtifact,
	key types.ProcessingArtifactLookup,
	outputSchema string,
	target any,
) error {
	payload, err := DecodeInline(artifact, key, outputSchema, CodecJSONV1)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("%w: decode JSON: %v", ErrCorruptArtifact, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("%w: trailing JSON value", ErrCorruptArtifact)
	}
	if err := ValidateOwnershipFreeJSON(target); err != nil {
		return fmt.Errorf("%w: %v", ErrCorruptArtifact, err)
	}
	return nil
}

// ValidateOwnershipFreeJSON rejects identifiers that bind a reusable artifact
// to one live knowledge entity or processing attempt.
func ValidateOwnershipFreeJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	return walkOwnershipFields(decoded, "")
}

func walkOwnershipFields(value any, path string) error {
	switch typed := value.(type) {
	case []any:
		for index, item := range typed {
			if err := walkOwnershipFields(item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	case map[string]any:
		for key, item := range typed {
			normalized := normalizeFieldName(key)
			if _, forbidden := ownershipFieldNames[normalized]; forbidden {
				return fmt.Errorf("ownership field %q must not enter processing artifact payload", joinJSONPath(path, key))
			}
			if err := walkOwnershipFields(item, joinJSONPath(path, key)); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalizeFieldName(value string) string {
	result := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '_', '-', '.':
			continue
		default:
			if value[index] >= 'A' && value[index] <= 'Z' {
				result = append(result, value[index]+'a'-'A')
			} else {
				result = append(result, value[index])
			}
		}
	}
	return string(result)
}

var ownershipFieldNames = map[string]struct{}{
	"attemptid":   {},
	"attempt":     {},
	"chunkid":     {},
	"knowledgeid": {},
	"tenantid":    {},
}
