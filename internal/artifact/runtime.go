package artifact

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sync/singleflight"
)

type EventKind string

const (
	EventHit          EventKind = "hit"
	EventMiss         EventKind = "miss"
	EventCorrupt      EventKind = "corrupt"
	EventStoreFailure EventKind = "store_failure"
	EventStored       EventKind = "stored"
	EventLostRace     EventKind = "lost_race"
)

type Event struct {
	Kind   EventKind
	Lookup types.ProcessingArtifactLookup
	Err    error
}

type Observer func(Event)

// Repository is declared in this leaf package to keep artifact mechanics
// independent from the broad application interfaces package (which itself
// references model packages that consume artifacts).
type Repository interface {
	Get(
		ctx context.Context,
		key types.ProcessingArtifactLookup,
	) (*types.ProcessingArtifact, error)
	BatchGet(
		ctx context.Context,
		keys []types.ProcessingArtifactLookup,
	) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error)
	PutIfAbsent(
		ctx context.Context,
		candidate *types.ProcessingArtifact,
	) (winner *types.ProcessingArtifact, created bool, err error)
	PutManyIfAbsent(
		ctx context.Context,
		candidates []*types.ProcessingArtifact,
	) (map[types.ProcessingArtifactLookup]*types.ProcessingArtifact, error)
	DeleteCorrupt(
		ctx context.Context,
		key types.ProcessingArtifactLookup,
		observedChecksum string,
	) error
	TouchHits(ctx context.Context, keys []types.ProcessingArtifactLookup) error
}

// Runtime turns repository failures into misses while preserving provider
// failures. Database uniqueness remains the correctness boundary across
// processes; singleflight only suppresses duplicate work within one process.
type Runtime struct {
	repository Repository
	observer   Observer
	lease      Lease
	group      singleflight.Group
}

func NewRuntime(repository Repository, observer Observer) *Runtime {
	return &Runtime{repository: repository, observer: observer}
}

func (r *Runtime) ConfigureLease(lease Lease) {
	if r != nil {
		r.lease = lease
	}
}

type Expected struct {
	Key       Key
	Codec     string
	Validate  func([]byte) error
	Cacheable func([]byte) bool
}

type Value struct {
	Payload      []byte
	OutputDigest string
	CacheHit     bool
}

type Candidate struct {
	Expected Expected
	Payload  []byte
}

func (r *Runtime) Load(ctx context.Context, expected Expected) (Value, bool) {
	values := r.BatchLoad(ctx, []Expected{expected})
	value, ok := values[expected.Key.Lookup]
	return value, ok
}

// BatchLoad performs one manifest query per database-sized batch, validates
// every result independently, and evicts only the corrupt row observed.
func (r *Runtime) BatchLoad(
	ctx context.Context,
	expected []Expected,
) map[types.ProcessingArtifactLookup]Value {
	result := make(map[types.ProcessingArtifactLookup]Value, len(expected))
	if r == nil || r.repository == nil || len(expected) == 0 {
		return result
	}

	keys := make([]types.ProcessingArtifactLookup, 0, len(expected))
	byKey := make(map[types.ProcessingArtifactLookup]Expected, len(expected))
	for _, item := range expected {
		keys = append(keys, item.Key.Lookup)
		byKey[item.Key.Lookup] = item
	}
	artifacts, err := r.repository.BatchGet(ctx, keys)
	if err != nil {
		for _, key := range keys {
			r.emit(Event{Kind: EventStoreFailure, Lookup: key, Err: err})
		}
		return result
	}

	hits := make([]types.ProcessingArtifactLookup, 0, len(artifacts))
	for _, key := range keys {
		item := byKey[key]
		manifest, found := artifacts[key]
		if !found {
			r.emit(Event{Kind: EventMiss, Lookup: key})
			continue
		}
		payload, decodeErr := DecodeInline(manifest, key, item.Key.OutputSchema, item.Codec)
		if decodeErr == nil && item.Validate != nil {
			decodeErr = item.Validate(payload)
		}
		if decodeErr != nil {
			r.emit(Event{Kind: EventCorrupt, Lookup: key, Err: decodeErr})
			if deleteErr := r.repository.DeleteCorrupt(ctx, key, manifest.PayloadChecksum); deleteErr != nil {
				r.emit(Event{Kind: EventStoreFailure, Lookup: key, Err: deleteErr})
			}
			continue
		}
		result[key] = Value{
			Payload:      payload,
			OutputDigest: manifest.OutputDigest,
			CacheHit:     true,
		}
		hits = append(hits, key)
		r.emit(Event{Kind: EventHit, Lookup: key})
	}
	if len(hits) > 0 {
		if err := r.repository.TouchHits(ctx, hits); err != nil {
			for _, key := range hits {
				r.emit(Event{Kind: EventStoreFailure, Lookup: key, Err: err})
			}
		}
	}
	return result
}

// LoadOrCompute caches only validated successful output. Artifact read/write
// failures are fail-open; compute errors are returned unchanged.
func (r *Runtime) LoadOrCompute(
	ctx context.Context,
	expected Expected,
	compute func(context.Context) ([]byte, error),
) (Value, error) {
	if compute == nil {
		return Value{}, errors.New("artifact compute function must not be nil")
	}
	if value, hit := r.Load(ctx, expected); hit {
		return value, nil
	}
	if r == nil {
		payload, err := compute(ctx)
		return uncachedValue(payload, expected.Validate, err)
	}

	result := r.group.DoChan(singleflightKey(expected.Key.Lookup), func() (any, error) {
		if value, hit := r.Load(ctx, expected); hit {
			return value, nil
		}
		if r.lease != nil {
			for {
				handle, acquired, leaseErr := r.lease.TryAcquire(ctx, expected.Key.Lookup)
				if leaseErr != nil {
					r.emit(Event{
						Kind:   EventStoreFailure,
						Lookup: expected.Key.Lookup,
						Err:    leaseErr,
					})
					break
				}
				if acquired {
					defer handle.Release()
					// The winner may have committed between our last read and
					// lease acquisition.
					if value, hit := r.Load(ctx, expected); hit {
						return value, nil
					}
					break
				}
				timer := time.NewTimer(100 * time.Millisecond)
				select {
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					return Value{}, ctx.Err()
				case <-timer.C:
				}
				if value, hit := r.Load(ctx, expected); hit {
					return value, nil
				}
			}
		}
		payload, err := compute(ctx)
		if err != nil {
			return Value{}, err
		}
		if expected.Validate != nil {
			if err := expected.Validate(payload); err != nil {
				return Value{}, fmt.Errorf("validate processing artifact output: %w", err)
			}
		}
		if expected.Cacheable != nil && !expected.Cacheable(payload) {
			return uncachedValue(payload, expected.Validate, nil)
		}
		candidate, err := NewInlineArtifact(expected.Key, expected.Codec, payload)
		if err != nil {
			return Value{}, err
		}
		return r.freeze(ctx, expected, candidate), nil
	})

	select {
	case <-ctx.Done():
		return Value{}, ctx.Err()
	case completed := <-result:
		if completed.Err != nil {
			return Value{}, completed.Err
		}
		return completed.Val.(Value), nil
	}
}

// BatchFreeze performs one immutable batch insert and then returns the database
// winners. Invalid winners are conditionally evicted and the caller's validated
// candidate remains usable, preserving fail-open processing.
func (r *Runtime) BatchFreeze(
	ctx context.Context,
	candidates []Candidate,
) map[types.ProcessingArtifactLookup]Value {
	result := make(map[types.ProcessingArtifactLookup]Value, len(candidates))
	manifests := make([]*types.ProcessingArtifact, 0, len(candidates))
	byKey := make(map[types.ProcessingArtifactLookup]Candidate, len(candidates))
	for _, candidate := range candidates {
		if candidate.Expected.Cacheable != nil && !candidate.Expected.Cacheable(candidate.Payload) {
			continue
		}
		if candidate.Expected.Validate != nil {
			if err := candidate.Expected.Validate(candidate.Payload); err != nil {
				continue
			}
		}
		manifest, err := NewInlineArtifact(
			candidate.Expected.Key,
			candidate.Expected.Codec,
			candidate.Payload,
		)
		if err != nil {
			continue
		}
		key := manifest.Lookup()
		manifests = append(manifests, manifest)
		byKey[key] = candidate
		result[key] = Value{
			Payload:      append([]byte(nil), manifest.Payload...),
			OutputDigest: manifest.OutputDigest,
		}
	}
	if r == nil || r.repository == nil || len(manifests) == 0 {
		return result
	}

	winners, err := r.repository.PutManyIfAbsent(ctx, manifests)
	if err != nil {
		for _, manifest := range manifests {
			r.emit(Event{Kind: EventStoreFailure, Lookup: manifest.Lookup(), Err: err})
		}
		return result
	}
	for _, manifest := range manifests {
		key := manifest.Lookup()
		winner := winners[key]
		candidate := byKey[key]
		if winner == nil {
			r.emit(Event{
				Kind:   EventStoreFailure,
				Lookup: key,
				Err:    errors.New("artifact repository omitted an inserted winner"),
			})
			continue
		}
		payload, decodeErr := DecodeInline(
			winner,
			key,
			candidate.Expected.Key.OutputSchema,
			candidate.Expected.Codec,
		)
		if decodeErr == nil && candidate.Expected.Validate != nil {
			decodeErr = candidate.Expected.Validate(payload)
		}
		if decodeErr != nil {
			r.emit(Event{Kind: EventCorrupt, Lookup: key, Err: decodeErr})
			if winner != nil {
				if deleteErr := r.repository.DeleteCorrupt(ctx, key, winner.PayloadChecksum); deleteErr != nil {
					r.emit(Event{Kind: EventStoreFailure, Lookup: key, Err: deleteErr})
				}
			}
			continue
		}
		result[key] = Value{
			Payload:      payload,
			OutputDigest: winner.OutputDigest,
			CacheHit:     winner.ID != manifest.ID,
		}
	}
	return result
}

func (r *Runtime) freeze(
	ctx context.Context,
	expected Expected,
	candidate *types.ProcessingArtifact,
) Value {
	fallback := Value{
		Payload:      append([]byte(nil), candidate.Payload...),
		OutputDigest: candidate.OutputDigest,
	}
	if r.repository == nil {
		return fallback
	}
	winner, created, err := r.repository.PutIfAbsent(ctx, candidate)
	if err != nil {
		r.emit(Event{Kind: EventStoreFailure, Lookup: candidate.Lookup(), Err: err})
		return fallback
	}
	if winner == nil {
		r.emit(Event{
			Kind:   EventStoreFailure,
			Lookup: candidate.Lookup(),
			Err:    errors.New("artifact repository returned a nil winner"),
		})
		return fallback
	}
	payload, err := DecodeInline(
		winner,
		expected.Key.Lookup,
		expected.Key.OutputSchema,
		expected.Codec,
	)
	if err == nil && expected.Validate != nil {
		err = expected.Validate(payload)
	}
	if err != nil {
		r.emit(Event{Kind: EventCorrupt, Lookup: candidate.Lookup(), Err: err})
		if deleteErr := r.repository.DeleteCorrupt(ctx, winner.Lookup(), winner.PayloadChecksum); deleteErr != nil {
			r.emit(Event{Kind: EventStoreFailure, Lookup: winner.Lookup(), Err: deleteErr})
		}
		return fallback
	}
	if created {
		r.emit(Event{Kind: EventStored, Lookup: candidate.Lookup()})
	} else {
		r.emit(Event{Kind: EventLostRace, Lookup: candidate.Lookup()})
	}
	return Value{
		Payload:      payload,
		OutputDigest: winner.OutputDigest,
		CacheHit:     !created,
	}
}

func uncachedValue(payload []byte, validate func([]byte) error, err error) (Value, error) {
	if err != nil {
		return Value{}, err
	}
	if validate != nil {
		if err := validate(payload); err != nil {
			return Value{}, fmt.Errorf("validate processing output: %w", err)
		}
	}
	frozen := append([]byte(nil), payload...)
	return Value{Payload: frozen, OutputDigest: SHA256Hex(frozen)}, nil
}

func singleflightKey(key types.ProcessingArtifactLookup) string {
	return strconv.FormatUint(key.TenantID, 10) + "\x00" +
		key.Stage + "\x00" +
		strconv.FormatUint(uint64(key.KeyVersion), 10) + "\x00" +
		key.ArtifactKey
}

func (r *Runtime) emit(event Event) {
	if r != nil && r.observer != nil {
		r.observer(event)
	}
}
