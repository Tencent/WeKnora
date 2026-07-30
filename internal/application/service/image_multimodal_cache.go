package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/artifactkey"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	multimodalArtifactSchemaVersion = "multimodal-artifact/v1"
	multimodalPromptVersion         = "multimodal-prompt/v1"
	multimodalProducerVersion       = "multimodal-producer/v1"
	multimodalArtifactLease         = 2 * time.Minute
	multimodalArtifactWait          = 3 * time.Minute
	multimodalArtifactPoll          = 100 * time.Millisecond
	multimodalArtifactCleanup       = 2 * time.Second
)

type multimodalArtifactTiming struct {
	Lease, Wait, Poll, Cleanup time.Duration
}

func (s *ImageMultimodalService) multimodalTiming() multimodalArtifactTiming {
	t := s.artifactTiming
	if t.Lease <= 0 {
		t.Lease = multimodalArtifactLease
	}
	if t.Wait <= 0 {
		t.Wait = multimodalArtifactWait
	}
	if t.Poll <= 0 {
		t.Poll = multimodalArtifactPoll
	}
	if t.Cleanup <= 0 {
		t.Cleanup = multimodalArtifactCleanup
	}
	return t
}

type multimodalArtifactPayload struct {
	SchemaVersion string `json:"schema_version"`
	Result        string `json:"result"`
	Present       bool   `json:"present"`
}

type multimodalCacheSpec struct {
	Kind            string
	Operation       types.IngestionOperation
	Prompt          string
	PromptVersion   string
	ProducerVersion string
	Normalize       func(string) string
}

type multimodalCacheInfrastructureError struct{ err error }

func (e *multimodalCacheInfrastructureError) Error() string { return e.err.Error() }
func (e *multimodalCacheInfrastructureError) Unwrap() error { return e.err }
func cacheInfrastructureError(err error) error {
	return &multimodalCacheInfrastructureError{err: err}
}

func isMultimodalCacheInfrastructureError(err error) bool {
	var target *multimodalCacheInfrastructureError
	return errors.As(err, &target)
}

func (s *ImageMultimodalService) cachedMultimodalPredict(ctx context.Context, tenantID uint64, model vlm.VLM, modelID string, image []byte, spec multimodalCacheSpec) (string, bool, error) {
	// Directly-constructed legacy tests and embedders may omit the repository.
	// Production construction always injects it through the container.
	if s.artifactRepo == nil {
		value, err := model.Predict(types.WithIngestionOperation(ctx, spec.Operation), [][]byte{image}, spec.Prompt)
		if err != nil {
			return "", false, err
		}
		if spec.Normalize != nil {
			value = spec.Normalize(value)
		}
		return value, false, nil
	}

	inputDigest := artifactkey.DigestBytes(image)
	promptVersion := spec.PromptVersion
	if promptVersion == "" {
		promptVersion = multimodalPromptVersion
	}
	producerVersion := spec.ProducerVersion
	if producerVersion == "" {
		producerVersion = multimodalProducerVersion
	}
	configDigest, err := artifactkey.DigestConfig(map[string]string{"prompt": spec.Prompt})
	if err != nil {
		return "", false, cacheInfrastructureError(fmt.Errorf("digest multimodal config: %w", err))
	}
	key := artifactkey.Generate(artifactkey.KeyInput{
		Kind: spec.Kind, TenantScope: fmt.Sprintf("tenant:%d", tenantID), InputDigest: inputDigest,
		ModelID: modelID, PromptVersion: promptVersion, ConfigDigest: configDigest,
		ProducerVersion: producerVersion,
	})

	timing := s.multimodalTiming()
	deadline := time.Now().Add(timing.Wait)
	for {
		owner, err := newArtifactOwnerToken()
		if err != nil {
			return "", false, cacheInfrastructureError(fmt.Errorf("create artifact owner token: %w", err))
		}
		claim, err := s.artifactRepo.Claim(ctx, interfaces.ArtifactClaim{
			TenantID: tenantID, ArtifactKey: key, ArtifactKind: spec.Kind, InputDigest: inputDigest,
			ModelID: modelID, PromptVersion: promptVersion, ConfigDigest: configDigest,
			ProducerVersion: producerVersion, OwnerToken: owner, LeaseDuration: timing.Lease,
		})
		if err != nil {
			return "", false, cacheInfrastructureError(fmt.Errorf("claim %s artifact: %w", spec.Kind, err))
		}
		switch claim.Outcome {
		case interfaces.ArtifactClaimHit:
			value, err := decodeMultimodalArtifact(claim.Artifact)
			if err != nil {
				return "", false, cacheInfrastructureError(err)
			}
			return value, true, nil
		case interfaces.ArtifactClaimBusy:
			if time.Now().After(deadline) {
				return "", false, cacheInfrastructureError(fmt.Errorf("%s artifact busy wait timed out", spec.Kind))
			}
			select {
			case <-ctx.Done():
				return "", false, cacheInfrastructureError(ctx.Err())
			case <-time.After(timing.Poll):
				continue
			}
		case interfaces.ArtifactClaimClaimed:
			return s.computeMultimodalArtifact(ctx, tenantID, key, owner, model, image, spec)
		default:
			return "", false, cacheInfrastructureError(fmt.Errorf("unknown %s artifact claim outcome %q", spec.Kind, claim.Outcome))
		}
	}
}

func (s *ImageMultimodalService) computeMultimodalArtifact(ctx context.Context, tenantID uint64, key, owner string, model vlm.VLM, image []byte, spec multimodalCacheSpec) (string, bool, error) {
	timing := s.multimodalTiming()
	heartbeatCtx, stop := context.WithCancel(ctx)
	defer stop()
	var mu sync.Mutex
	var ownershipErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(timing.Lease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case now := <-ticker.C:
				if err := s.artifactRepo.RenewLease(heartbeatCtx, tenantID, key, owner, now, timing.Lease); err != nil {
					mu.Lock()
					ownershipErr = err
					mu.Unlock()
					return
				}
			}
		}
	}()

	value, predictErr := model.Predict(types.WithIngestionOperation(ctx, spec.Operation), [][]byte{image}, spec.Prompt)
	stop()
	<-done
	mu.Lock()
	lost := ownershipErr
	mu.Unlock()
	if lost != nil {
		return "", false, cacheInfrastructureError(fmt.Errorf("%s artifact lease: %w", spec.Kind, lost))
	}
	if predictErr != nil {
		failCtx := ctx
		cancel := func() {}
		if ctx.Err() != nil {
			failCtx, cancel = context.WithTimeout(context.WithoutCancel(ctx), timing.Cleanup)
		}
		defer cancel()
		failErr := s.artifactRepo.Fail(failCtx, interfaces.ArtifactFailure{TenantID: tenantID, ArtifactKey: key, OwnerToken: owner, ErrorCode: "vlm_provider_failure", ErrorMessage: "VLM provider request failed"})
		if failErr != nil && !errors.Is(failErr, interfaces.ErrArtifactLostOwnership) {
			return "", false, cacheInfrastructureError(fmt.Errorf("fail %s artifact: %w", spec.Kind, failErr))
		}
		return "", false, predictErr
	}
	if spec.Normalize != nil {
		value = spec.Normalize(value)
	}
	payload, err := json.Marshal(multimodalArtifactPayload{SchemaVersion: multimodalArtifactSchemaVersion, Result: value, Present: true})
	if err != nil {
		return "", false, cacheInfrastructureError(fmt.Errorf("encode %s artifact: %w", spec.Kind, err))
	}
	if err := s.artifactRepo.Complete(ctx, interfaces.ArtifactCompletion{TenantID: tenantID, ArtifactKey: key, OwnerToken: owner, Payload: payload, PayloadEncoding: "json", PayloadDigest: artifactkey.DigestBytes(payload)}); err != nil {
		return "", false, cacheInfrastructureError(fmt.Errorf("complete %s artifact: %w", spec.Kind, err))
	}
	return value, false, nil
}

func decodeMultimodalArtifact(artifact *types.DerivedArtifact) (string, error) {
	if artifact == nil || artifact.PayloadEncoding != "json" {
		return "", interfaces.ErrArtifactCorrupt
	}
	var payload multimodalArtifactPayload
	if err := json.Unmarshal(artifact.Payload, &payload); err != nil {
		return "", fmt.Errorf("decode multimodal artifact: %w", interfaces.ErrArtifactCorrupt)
	}
	if payload.SchemaVersion != multimodalArtifactSchemaVersion || !payload.Present {
		return "", fmt.Errorf("decode multimodal artifact schema %q: %w", payload.SchemaVersion, interfaces.ErrArtifactCorrupt)
	}
	return payload.Result, nil
}

func newArtifactOwnerToken() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func canonicalCaption(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func multimodalImageDigestPrefix(image []byte) string {
	digest := artifactkey.DigestBytes(image)
	if len(digest) > 12 {
		return digest[:12]
	}
	return digest
}
