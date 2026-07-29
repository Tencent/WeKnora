package artifact

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	EventComputed     EventKind = "computed"
	EventWait         EventKind = "wait"
	EventBypass       EventKind = "bypass"
)

type Event struct {
	Kind               EventKind
	Lookup             types.ProcessingArtifactLookup
	OutputSchema       string
	Reason             string
	ProviderCall       bool
	BatchTotal         int
	BatchHits          int
	BatchMisses        int
	BatchDeduplicated  int
	SingleflightWaitMS int64
	Err                error
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
	configMu   sync.RWMutex
	read       bool
	write      bool
	stages     map[string]bool
}

func NewRuntime(repository Repository, observer Observer) *Runtime {
	return &Runtime{
		repository: repository,
		observer:   observer,
		read:       true,
		write:      true,
	}
}

func (r *Runtime) ConfigureLease(lease Lease) {
	if r != nil {
		r.lease = lease
	}
}

// ConfigureCacheMode controls artifact reads and writes without changing the
// provider path. Stages omitted from the map remain enabled.
func (r *Runtime) ConfigureCacheMode(read, write bool, stages map[string]bool) {
	if r == nil {
		return
	}
	r.configMu.Lock()
	defer r.configMu.Unlock()
	r.read = read
	r.write = write
	r.stages = make(map[string]bool, len(stages))
	for stage, enabled := range stages {
		normalized := strings.TrimSpace(stage)
		if normalized != "" {
			r.stages[normalized] = enabled
		}
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
		if !r.readEnabled(item.Key.Lookup.Stage) {
			r.emit(Event{
				Kind:         EventBypass,
				Lookup:       item.Key.Lookup,
				OutputSchema: item.Key.OutputSchema,
				Reason:       "read_disabled",
			})
			continue
		}
		keys = append(keys, item.Key.Lookup)
		byKey[item.Key.Lookup] = item
	}
	if len(keys) == 0 {
		return result
	}
	artifacts, err := r.repository.BatchGet(ctx, keys)
	if err != nil {
		for _, key := range keys {
			r.emit(Event{
				Kind:         EventStoreFailure,
				Lookup:       key,
				OutputSchema: byKey[key].Key.OutputSchema,
				Reason:       "read_error",
				Err:          err,
			})
		}
		return result
	}

	hits := make([]types.ProcessingArtifactLookup, 0, len(artifacts))
	for _, key := range keys {
		item := byKey[key]
		manifest, found := artifacts[key]
		if !found {
			r.emit(Event{
				Kind:         EventMiss,
				Lookup:       key,
				OutputSchema: item.Key.OutputSchema,
				Reason:       "not_found",
			})
			continue
		}
		payload, decodeErr := DecodeInline(manifest, key, item.Key.OutputSchema, item.Codec)
		if decodeErr == nil && item.Validate != nil {
			decodeErr = item.Validate(payload)
		}
		if decodeErr != nil {
			r.emit(Event{
				Kind:         EventCorrupt,
				Lookup:       key,
				OutputSchema: item.Key.OutputSchema,
				Reason:       corruptReason(decodeErr),
				Err:          decodeErr,
			})
			if deleteErr := r.repository.DeleteCorrupt(ctx, key, manifest.PayloadChecksum); deleteErr != nil {
				r.emit(Event{
					Kind:         EventStoreFailure,
					Lookup:       key,
					OutputSchema: item.Key.OutputSchema,
					Reason:       "corrupt_delete_error",
					Err:          deleteErr,
				})
			}
			continue
		}
		result[key] = Value{
			Payload:      payload,
			OutputDigest: manifest.OutputDigest,
			CacheHit:     true,
		}
		hits = append(hits, key)
		r.emit(Event{
			Kind:         EventHit,
			Lookup:       key,
			OutputSchema: item.Key.OutputSchema,
			Reason:       "found_valid",
		})
	}
	if len(hits) > 0 {
		if err := r.repository.TouchHits(ctx, hits); err != nil {
			for _, key := range hits {
				r.emit(Event{
					Kind:         EventStoreFailure,
					Lookup:       key,
					OutputSchema: byKey[key].Key.OutputSchema,
					Reason:       "touch_error",
					Err:          err,
				})
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

	waitStarted := time.Now()
	var computedByCaller atomic.Bool
	result := r.group.DoChan(singleflightKey(expected.Key.Lookup), func() (any, error) {
		if value, hit := r.Load(ctx, expected); hit {
			return value, nil
		}
		if r.lease != nil {
			for {
				handle, acquired, leaseErr := r.lease.TryAcquire(ctx, expected.Key.Lookup)
				if leaseErr != nil {
					r.emit(Event{
						Kind:         EventStoreFailure,
						Lookup:       expected.Key.Lookup,
						OutputSchema: expected.Key.OutputSchema,
						Reason:       "lease_error",
						Err:          leaseErr,
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
		computedByCaller.Store(true)
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
			r.emit(Event{
				Kind:         EventBypass,
				Lookup:       expected.Key.Lookup,
				OutputSchema: expected.Key.OutputSchema,
				Reason:       "output_not_cacheable",
				ProviderCall: true,
			})
			return uncachedValue(payload, expected.Validate, nil)
		}
		r.emit(Event{
			Kind:         EventComputed,
			Lookup:       expected.Key.Lookup,
			OutputSchema: expected.Key.OutputSchema,
			Reason:       "provider_success",
			ProviderCall: true,
		})
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
		if completed.Shared && !computedByCaller.Load() {
			r.emit(Event{
				Kind:               EventWait,
				Lookup:             expected.Key.Lookup,
				OutputSchema:       expected.Key.OutputSchema,
				Reason:             "singleflight",
				SingleflightWaitMS: time.Since(waitStarted).Milliseconds(),
			})
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
		key := candidate.Expected.Key.Lookup
		if candidate.Expected.Cacheable != nil && !candidate.Expected.Cacheable(candidate.Payload) {
			r.emit(Event{
				Kind:         EventBypass,
				Lookup:       key,
				OutputSchema: candidate.Expected.Key.OutputSchema,
				Reason:       "output_not_cacheable",
			})
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
		key = manifest.Lookup()
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

	filtered := manifests[:0]
	for _, manifest := range manifests {
		key := manifest.Lookup()
		if !r.writeEnabled(key.Stage) {
			r.emit(Event{
				Kind:         EventBypass,
				Lookup:       key,
				OutputSchema: byKey[key].Expected.Key.OutputSchema,
				Reason:       "write_disabled",
			})
			continue
		}
		filtered = append(filtered, manifest)
	}
	manifests = filtered
	if len(manifests) == 0 {
		return result
	}

	winners, err := r.repository.PutManyIfAbsent(ctx, manifests)
	if err != nil {
		for _, manifest := range manifests {
			key := manifest.Lookup()
			r.emit(Event{
				Kind:         EventStoreFailure,
				Lookup:       key,
				OutputSchema: byKey[key].Expected.Key.OutputSchema,
				Reason:       "write_error",
				Err:          err,
			})
		}
		return result
	}
	for _, manifest := range manifests {
		key := manifest.Lookup()
		winner := winners[key]
		candidate := byKey[key]
		if winner == nil {
			r.emit(Event{
				Kind:         EventStoreFailure,
				Lookup:       key,
				OutputSchema: candidate.Expected.Key.OutputSchema,
				Reason:       "winner_missing",
				Err:          errors.New("artifact repository omitted an inserted winner"),
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
			r.emit(Event{
				Kind:         EventCorrupt,
				Lookup:       key,
				OutputSchema: candidate.Expected.Key.OutputSchema,
				Reason:       corruptReason(decodeErr),
				Err:          decodeErr,
			})
			if winner != nil {
				if deleteErr := r.repository.DeleteCorrupt(ctx, key, winner.PayloadChecksum); deleteErr != nil {
					r.emit(Event{
						Kind:         EventStoreFailure,
						Lookup:       key,
						OutputSchema: candidate.Expected.Key.OutputSchema,
						Reason:       "corrupt_delete_error",
						Err:          deleteErr,
					})
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
	if r.repository == nil || !r.writeEnabled(candidate.Stage) {
		if r != nil && r.repository != nil {
			r.emit(Event{
				Kind:         EventBypass,
				Lookup:       candidate.Lookup(),
				OutputSchema: expected.Key.OutputSchema,
				Reason:       "write_disabled",
			})
		}
		return fallback
	}
	winner, created, err := r.repository.PutIfAbsent(ctx, candidate)
	if err != nil {
		r.emit(Event{
			Kind:         EventStoreFailure,
			Lookup:       candidate.Lookup(),
			OutputSchema: expected.Key.OutputSchema,
			Reason:       "write_error",
			Err:          err,
		})
		return fallback
	}
	if winner == nil {
		r.emit(Event{
			Kind:         EventStoreFailure,
			Lookup:       candidate.Lookup(),
			OutputSchema: expected.Key.OutputSchema,
			Reason:       "winner_missing",
			Err:          errors.New("artifact repository returned a nil winner"),
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
		r.emit(Event{
			Kind:         EventCorrupt,
			Lookup:       candidate.Lookup(),
			OutputSchema: expected.Key.OutputSchema,
			Reason:       corruptReason(err),
			Err:          err,
		})
		if deleteErr := r.repository.DeleteCorrupt(ctx, winner.Lookup(), winner.PayloadChecksum); deleteErr != nil {
			r.emit(Event{
				Kind:         EventStoreFailure,
				Lookup:       winner.Lookup(),
				OutputSchema: expected.Key.OutputSchema,
				Reason:       "corrupt_delete_error",
				Err:          deleteErr,
			})
		}
		return fallback
	}
	if created {
		r.emit(Event{
			Kind:         EventStored,
			Lookup:       candidate.Lookup(),
			OutputSchema: expected.Key.OutputSchema,
			Reason:       "stored",
		})
	} else {
		r.emit(Event{
			Kind:         EventLostRace,
			Lookup:       candidate.Lookup(),
			OutputSchema: expected.Key.OutputSchema,
			Reason:       "immutable_winner",
		})
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

// Observe records adapter-level batch metrics through the same safe observer
// used by the runtime.
func (r *Runtime) Observe(event Event) {
	r.emit(event)
}

func (r *Runtime) readEnabled(stage string) bool {
	if r == nil {
		return false
	}
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return r.read && r.stageEnabledLocked(stage)
}

func (r *Runtime) writeEnabled(stage string) bool {
	if r == nil {
		return false
	}
	r.configMu.RLock()
	defer r.configMu.RUnlock()
	return r.write && r.stageEnabledLocked(stage)
}

func (r *Runtime) stageEnabledLocked(stage string) bool {
	enabled, configured := r.stages[stage]
	return !configured || enabled
}

func corruptReason(err error) string {
	switch {
	case err == nil:
		return "decode_failed"
	case strings.Contains(err.Error(), "checksum"):
		return "checksum_mismatch"
	case strings.Contains(err.Error(), "schema"):
		return "schema_mismatch"
	default:
		return "decode_failed"
	}
}
