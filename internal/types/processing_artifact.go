package types

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"regexp"
	"time"
)

const processingArtifactFingerprintDomain = "weknora-processing-artifact-input-v1"

var (
	processingArtifactStagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	ErrProcessingArtifactNotFound  = errors.New("processing artifact not found")
)

type ProcessingArtifactKey struct {
	TenantID         uint64
	Stage            string
	KeyVersion       uint16
	InputFingerprint string
}

type ProcessingArtifactCounter struct {
	Stage   string `json:"stage"`
	Outcome string `json:"outcome"`
	Count   uint64 `json:"count"`
}

type ProcessingArtifactPurgeResult struct {
	Scanned      uint64 `json:"scanned"`
	Deleted      uint64 `json:"deleted"`
	Failed       uint64 `json:"failed"`
	DeletedBytes int64  `json:"deleted_bytes"`
}

func IsValidProcessingArtifactStage(stage string) bool {
	return processingArtifactStagePattern.MatchString(stage)
}

func NewProcessingArtifactKey(tenantID uint64, stage string, keyVersion uint16, inputParts ...[]byte) (ProcessingArtifactKey, error) {
	if tenantID == 0 {
		return ProcessingArtifactKey{}, errors.New("processing artifact tenant ID must not be zero")
	}
	if !IsValidProcessingArtifactStage(stage) {
		return ProcessingArtifactKey{}, fmt.Errorf("invalid processing artifact stage %q", stage)
	}
	if keyVersion == 0 {
		return ProcessingArtifactKey{}, errors.New("processing artifact key version must not be zero")
	}
	if len(inputParts) == 0 {
		return ProcessingArtifactKey{}, errors.New("processing artifact input parts must not be empty")
	}

	hash := sha256.New()
	writeProcessingArtifactFingerprintPart(hash, []byte(processingArtifactFingerprintDomain))
	for _, inputPart := range inputParts {
		writeProcessingArtifactFingerprintPart(hash, inputPart)
	}

	return ProcessingArtifactKey{
		TenantID:         tenantID,
		Stage:            stage,
		KeyVersion:       keyVersion,
		InputFingerprint: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

func writeProcessingArtifactFingerprintPart(hasher hash.Hash, part []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(part)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(part)
}

type ProcessingArtifact struct {
	ID               uint64    `gorm:"primaryKey"`
	TenantID         uint64    `gorm:"not null;uniqueIndex:idx_processing_artifacts_key,priority:1"`
	Stage            string    `gorm:"size:64;not null;uniqueIndex:idx_processing_artifacts_key,priority:2"`
	KeyVersion       uint16    `gorm:"not null;uniqueIndex:idx_processing_artifacts_key,priority:3"`
	InputFingerprint string    `gorm:"size:64;not null;uniqueIndex:idx_processing_artifacts_key,priority:4"`
	Payload          []byte    `gorm:"type:bytea"`
	ObjectPath       string    `gorm:"type:text;not null;default:''"`
	ContentSHA256    string    `gorm:"size:64;not null"`
	SizeBytes        int64     `gorm:"not null"`
	CreatedAt        time.Time `gorm:"not null"`
}

func (ProcessingArtifact) TableName() string { return "processing_artifacts" }
