package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	artifactDigestLength    = sha256.Size * 2
	maxArtifactKindLength   = 64
	maxArtifactOwnerLength  = 128
	maxArtifactErrorCode    = 128
	maxArtifactErrorMessage = 2048
)

func validateArtifactTenantKeyOwner(tenantID uint64, key, owner string) error {
	if tenantID == 0 {
		return fmt.Errorf("tenant ID must be non-zero")
	}
	if err := validateArtifactDigest("artifact key", key); err != nil {
		return err
	}
	if owner == "" || len(owner) > maxArtifactOwnerLength {
		return fmt.Errorf("owner token must contain 1 to %d bytes", maxArtifactOwnerLength)
	}
	return nil
}

func validateArtifactDigest(name, value string) error {
	if len(value) != artifactDigestLength {
		return fmt.Errorf("%s must be a %d-character SHA-256 digest", name, artifactDigestLength)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("%s must be hexadecimal SHA-256", name)
	}
	return nil
}

func digestArtifactPayload(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func validateArtifactResult(payload []byte, objectURI, digest string) (string, error) {
	if len(payload) == 0 && strings.TrimSpace(objectURI) == "" {
		return "", interfaces.ErrArtifactInvalidResult
	}
	if err := validateArtifactDigest("payload digest", digest); err != nil {
		return "", fmt.Errorf("%w: %v", interfaces.ErrArtifactInvalidResult, err)
	}
	normalized := strings.ToLower(digest)
	if len(payload) > 0 && digestArtifactPayload(payload) != normalized {
		return "", fmt.Errorf("%w: payload digest mismatch", interfaces.ErrArtifactInvalidResult)
	}
	return normalized, nil
}

func validateSucceededArtifact(artifact *types.DerivedArtifact) error {
	if artifact == nil || artifact.Status != types.DerivedArtifactSucceeded {
		return interfaces.ErrArtifactInvalidTransition
	}
	if _, err := validateArtifactResult(artifact.Payload, artifact.ObjectURI, artifact.PayloadDigest); err != nil {
		return fmt.Errorf("%w: %v", interfaces.ErrArtifactCorrupt, err)
	}
	return nil
}

func truncateArtifactErrorMessage(message string) string {
	runes := []rune(message)
	if len(runes) > maxArtifactErrorMessage {
		runes = runes[:maxArtifactErrorMessage]
	}
	return string(runes)
}
