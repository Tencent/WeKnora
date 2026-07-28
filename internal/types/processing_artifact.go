package types

import (
	"errors"
	"fmt"
	"regexp"
	"time"
)

var (
	processingArtifactStagePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	processingArtifactHashPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)

	ErrProcessingArtifactNotFound = errors.New("processing artifact not found")
)

// ProcessingArtifactLookup is the complete tenant-scoped identity of an
// immutable processing artifact.
type ProcessingArtifactLookup struct {
	TenantID    uint64
	Stage       string
	KeyVersion  uint16
	ArtifactKey string
}

// Validate rejects incomplete or ambiguous artifact identities before they
// reach a database query.
func (k ProcessingArtifactLookup) Validate() error {
	if k.TenantID == 0 {
		return errors.New("processing artifact tenant ID must not be zero")
	}
	if !processingArtifactStagePattern.MatchString(k.Stage) {
		return fmt.Errorf("invalid processing artifact stage %q", k.Stage)
	}
	if k.KeyVersion == 0 {
		return errors.New("processing artifact key version must not be zero")
	}
	if !processingArtifactHashPattern.MatchString(k.ArtifactKey) {
		return errors.New("processing artifact key must be 64 lowercase hex characters")
	}
	return nil
}

// ProcessingArtifact is an ownership-free, immutable value produced by a
// processing stage. Knowledge IDs, chunk IDs and attempt IDs belong in live
// reconciliation state, never in this record or its payload.
type ProcessingArtifact struct {
	ID              uint64 `gorm:"primaryKey"`
	TenantID        uint64 `gorm:"not null;uniqueIndex:uq_processing_artifacts_key,priority:1"`
	Stage           string `gorm:"size:64;not null;uniqueIndex:uq_processing_artifacts_key,priority:2"`
	KeyVersion      uint16 `gorm:"not null;uniqueIndex:uq_processing_artifacts_key,priority:3"`
	ArtifactKey     string `gorm:"size:64;not null;uniqueIndex:uq_processing_artifacts_key,priority:4"`
	ProcessorDigest string `gorm:"size:64;not null"`
	OutputDigest    string `gorm:"size:64;not null"`
	OutputSchema    string `gorm:"size:64;not null"`
	Codec           string `gorm:"size:32;not null"`
	InlinePayload   bool   `gorm:"not null;default:true"`
	Payload         []byte `gorm:"type:bytea"`
	ObjectRef       string `gorm:"type:text;not null;default:''"`
	PayloadChecksum string `gorm:"size:64;not null"`
	SizeBytes       int64  `gorm:"not null"`
	HitCount        uint64 `gorm:"not null;default:0"`
	LastHitAt       *time.Time
	CreatedAt       time.Time `gorm:"not null"`
}

func (ProcessingArtifact) TableName() string {
	return "processing_artifacts"
}

// Lookup returns the complete immutable key represented by the row.
func (a ProcessingArtifact) Lookup() ProcessingArtifactLookup {
	return ProcessingArtifactLookup{
		TenantID:    a.TenantID,
		Stage:       a.Stage,
		KeyVersion:  a.KeyVersion,
		ArtifactKey: a.ArtifactKey,
	}
}

// Validate checks manifest invariants. It intentionally does not decode the
// payload; codec- and stage-specific validation happens when the value is read.
func (a ProcessingArtifact) Validate() error {
	if err := a.Lookup().Validate(); err != nil {
		return err
	}
	if !processingArtifactHashPattern.MatchString(a.ProcessorDigest) {
		return errors.New("processing artifact processor digest must be 64 lowercase hex characters")
	}
	if !processingArtifactHashPattern.MatchString(a.OutputDigest) {
		return errors.New("processing artifact output digest must be 64 lowercase hex characters")
	}
	if a.OutputSchema == "" {
		return errors.New("processing artifact output schema must not be empty")
	}
	if a.Codec == "" {
		return errors.New("processing artifact codec must not be empty")
	}
	if !processingArtifactHashPattern.MatchString(a.PayloadChecksum) {
		return errors.New("processing artifact payload checksum must be 64 lowercase hex characters")
	}
	if a.SizeBytes < 0 {
		return errors.New("processing artifact size must not be negative")
	}
	if a.InlinePayload {
		if a.Payload == nil {
			return errors.New("inline processing artifact payload must not be nil")
		}
		if a.ObjectRef != "" {
			return errors.New("inline processing artifact must not have an object reference")
		}
	} else {
		if a.ObjectRef == "" {
			return errors.New("object processing artifact reference must not be empty")
		}
		if a.Payload != nil {
			return errors.New("object processing artifact payload must be nil")
		}
	}
	return nil
}
