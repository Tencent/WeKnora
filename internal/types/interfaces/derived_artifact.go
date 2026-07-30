package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	ErrArtifactNotFound          = errors.New("derived artifact cache miss")
	ErrArtifactLostOwnership     = errors.New("derived artifact ownership lost")
	ErrArtifactInvalidResult     = errors.New("derived artifact result requires payload or object URI")
	ErrArtifactCorrupt           = errors.New("derived artifact payload is corrupt")
	ErrArtifactInvalidTransition = errors.New("invalid derived artifact state transition")
)

type ArtifactClaimOutcome string

const (
	ArtifactClaimHit     ArtifactClaimOutcome = "hit"
	ArtifactClaimClaimed ArtifactClaimOutcome = "claimed"
	ArtifactClaimBusy    ArtifactClaimOutcome = "busy"
)

type ArtifactClaim struct {
	TenantID                                                             uint64
	ArtifactKey, ArtifactKind, InputDigest                               string
	ModelID, ModelRevision, PromptVersion, ConfigDigest, ProducerVersion string
	OwnerToken                                                           string
	LeaseDuration                                                        time.Duration
	Now                                                                  time.Time
}
type ArtifactClaimResult struct {
	Outcome       ArtifactClaimOutcome
	Artifact      *types.DerivedArtifact
	LeaseTakeover bool
}
type ArtifactCompletion struct {
	TenantID                                  uint64
	ArtifactKey, OwnerToken                   string
	Payload                                   []byte
	PayloadEncoding, ObjectURI, PayloadDigest string
	CompletedAt                               time.Time
}
type ArtifactFailure struct {
	TenantID                                         uint64
	ArtifactKey, OwnerToken, ErrorCode, ErrorMessage string
	FailedAt                                         time.Time
}

type DerivedArtifactRepository interface {
	GetSucceeded(context.Context, uint64, string) (*types.DerivedArtifact, error)
	Claim(context.Context, ArtifactClaim) (*ArtifactClaimResult, error)
	Complete(context.Context, ArtifactCompletion) error
	Fail(context.Context, ArtifactFailure) error
	RenewLease(context.Context, uint64, string, string, time.Time, time.Duration) error
}
