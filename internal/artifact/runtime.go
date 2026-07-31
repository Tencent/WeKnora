package artifact

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/singleflight"
)

var ErrCacheMiss = errors.New("artifact cache miss")

// Outcome describes how GetOrCompute obtained the returned payload.
type Outcome string

const (
	OutcomeHit      Outcome = "hit"
	OutcomeComputed Outcome = "computed"
	OutcomeBypass   Outcome = "bypass"
	OutcomeCorrupt  Outcome = "corrupt"
)

// Record is the persisted artifact representation consumed by Runtime.
type Record struct {
	ID              string
	TenantID        uint64
	Stage           string
	KeyVersion      int
	ArtifactKey     string
	ProcessorDigest string
	OutputDigest    string
	OutputSchema    string
	Codec           string
	Payload         []byte
	PayloadChecksum string
	PayloadSize     int64
	CreatedAt       time.Time
	ExpiresAt       *time.Time
}

// Store persists validated artifact records.
type Store interface {
	PutIfAbsent(ctx context.Context, artifact *Record) (bool, error)
	Get(ctx context.Context, tenantID uint64, stage string, keyVersion int, artifactKey string) (*Record, error)
	DeleteObservedChecksum(ctx context.Context, tenantID uint64, id string, payloadChecksum string) (bool, error)
}

// RuntimeOptions controls artifact cache reads and writes.
type RuntimeOptions struct {
	ReadEnabled    bool
	WriteEnabled   bool
	MaxInlineBytes int64
	Codec          string
	Retention      time.Duration
}

// Metadata describes one runtime access.
type Metadata struct {
	Outcome       Outcome
	ArtifactKey   string
	ProviderCalls int
}

// BatchMiss is one unique cache miss passed to a batch compute function.
type BatchMiss struct {
	FirstIndex  int
	Material    KeyMaterial
	ArtifactKey string
}

// BatchMetadata summarizes a batched runtime access.
type BatchMetadata struct {
	Total         int
	Hits          int
	Misses        int
	Deduplicated  int
	ProviderCalls int
	Outcome       Outcome
}

// Runtime wraps deterministic computation with fail-open artifact reuse.
type Runtime struct {
	store Store
	opts  RuntimeOptions
	group singleflight.Group
}

// NewRuntime creates an artifact runtime backed by store.
func NewRuntime(store Store, opts RuntimeOptions) *Runtime {
	if opts.Codec == "" {
		opts.Codec = "json"
	}
	return &Runtime{store: store, opts: opts}
}

// GetOrCompute returns a cached payload when valid, otherwise computes it.
// Cache read/write errors are fail-open and do not block the provider call.
func (r *Runtime) GetOrCompute(
	ctx context.Context,
	tenantID uint64,
	material KeyMaterial,
	compute func(context.Context) ([]byte, error),
) ([]byte, Metadata, error) {
	key, err := BuildKey(material)
	if err != nil {
		return nil, Metadata{}, err
	}
	groupKey := fmt.Sprintf("%d:%s:%s", tenantID, material.Stage, key)
	value, err, _ := r.group.Do(groupKey, func() (any, error) {
		return r.getOrCompute(ctx, tenantID, material, key, compute)
	})
	if err != nil {
		return nil, Metadata{}, err
	}
	result := value.(runtimeResult)
	return append([]byte(nil), result.payload...), result.meta, nil
}

// GetOrComputeBatch caches a batch as independent artifact records while
// calling compute only for unique misses and restoring the caller's order.
func (r *Runtime) GetOrComputeBatch(
	ctx context.Context,
	tenantID uint64,
	materials []KeyMaterial,
	compute func(context.Context, []BatchMiss) ([][]byte, error),
) ([][]byte, BatchMetadata, error) {
	out := make([][]byte, len(materials))
	if len(materials) == 0 {
		return out, BatchMetadata{}, nil
	}

	type uniqueItem struct {
		material KeyMaterial
		key      string
		indices  []int
	}
	uniqueByKey := make(map[string]*uniqueItem)
	ordered := make([]*uniqueItem, 0, len(materials))
	for i, material := range materials {
		key, err := BuildKey(material)
		if err != nil {
			return nil, BatchMetadata{}, err
		}
		item := uniqueByKey[key]
		if item == nil {
			item = &uniqueItem{material: material, key: key}
			uniqueByKey[key] = item
			ordered = append(ordered, item)
		}
		item.indices = append(item.indices, i)
	}

	meta := BatchMetadata{
		Total:        len(ordered),
		Deduplicated: len(materials) - len(ordered),
		Outcome:      OutcomeHit,
	}
	cacheAvailable := r.store != nil
	misses := make([]BatchMiss, 0)
	for _, item := range ordered {
		if r.opts.ReadEnabled && cacheAvailable {
			record, err := r.store.Get(ctx, tenantID, item.material.Stage, item.material.KeyVersion, item.key)
			if err == nil {
				if validateErr := ValidatePayload(record.Payload, record.PayloadSize, record.PayloadChecksum); validateErr == nil &&
					record.OutputSchema == item.material.OutputSchema && record.Codec == r.opts.Codec {
					for _, index := range item.indices {
						out[index] = append([]byte(nil), record.Payload...)
					}
					meta.Hits++
					continue
				}
				_, _ = r.store.DeleteObservedChecksum(ctx, tenantID, record.ID, record.PayloadChecksum)
			} else if !errors.Is(err, ErrCacheMiss) {
				cacheAvailable = false
			}
		}
		misses = append(misses, BatchMiss{
			FirstIndex:  item.indices[0],
			Material:    item.material,
			ArtifactKey: item.key,
		})
	}

	if len(misses) == 0 {
		return out, meta, nil
	}

	payloads, err := compute(ctx, misses)
	if err != nil {
		return nil, BatchMetadata{}, err
	}
	if len(payloads) != len(misses) {
		return nil, BatchMetadata{}, fmt.Errorf("artifact batch compute returned %d payloads for %d misses", len(payloads), len(misses))
	}
	meta.Misses = len(misses)
	meta.ProviderCalls = 1
	meta.Outcome = OutcomeComputed
	if !cacheAvailable {
		meta.Outcome = OutcomeBypass
	}

	for i, miss := range misses {
		payload := payloads[i]
		for _, index := range uniqueByKey[miss.ArtifactKey].indices {
			out[index] = append([]byte(nil), payload...)
		}
		if !r.opts.WriteEnabled || !cacheAvailable ||
			(r.opts.MaxInlineBytes > 0 && int64(len(payload)) > r.opts.MaxInlineBytes) {
			meta.Outcome = OutcomeBypass
			continue
		}
		record := r.recordFromPayload(tenantID, miss.Material, miss.ArtifactKey, payload)
		if _, err := r.store.PutIfAbsent(ctx, record); err != nil {
			meta.Outcome = OutcomeBypass
		}
	}

	return out, meta, nil
}

type runtimeResult struct {
	payload []byte
	meta    Metadata
}

func (r *Runtime) getOrCompute(
	ctx context.Context,
	tenantID uint64,
	material KeyMaterial,
	key string,
	compute func(context.Context) ([]byte, error),
) (runtimeResult, error) {
	cacheAvailable := r.store != nil
	if r.opts.ReadEnabled && cacheAvailable {
		record, err := r.store.Get(ctx, tenantID, material.Stage, material.KeyVersion, key)
		if err == nil {
			if validateErr := ValidatePayload(record.Payload, record.PayloadSize, record.PayloadChecksum); validateErr == nil &&
				record.OutputSchema == material.OutputSchema && record.Codec == r.opts.Codec {
				return runtimeResult{payload: record.Payload, meta: Metadata{Outcome: OutcomeHit, ArtifactKey: key}}, nil
			}
			_, _ = r.store.DeleteObservedChecksum(ctx, tenantID, record.ID, record.PayloadChecksum)
		} else if !errors.Is(err, ErrCacheMiss) {
			cacheAvailable = false
		}
	}

	payload, err := compute(ctx)
	if err != nil {
		return runtimeResult{}, err
	}
	meta := Metadata{Outcome: OutcomeComputed, ArtifactKey: key, ProviderCalls: 1}
	if !cacheAvailable {
		meta.Outcome = OutcomeBypass
	}
	if !r.opts.WriteEnabled || !cacheAvailable {
		return runtimeResult{payload: payload, meta: meta}, nil
	}
	if r.opts.MaxInlineBytes > 0 && int64(len(payload)) > r.opts.MaxInlineBytes {
		meta.Outcome = OutcomeBypass
		return runtimeResult{payload: payload, meta: meta}, nil
	}

	record := r.recordFromPayload(tenantID, material, key, payload)
	if _, err := r.store.PutIfAbsent(ctx, record); err != nil {
		meta.Outcome = OutcomeBypass
	}
	return runtimeResult{payload: payload, meta: meta}, nil
}

func (r *Runtime) recordFromPayload(tenantID uint64, material KeyMaterial, key string, payload []byte) *Record {
	checksum := Checksum(payload)
	now := time.Now()
	var expiresAt *time.Time
	if r.opts.Retention > 0 {
		t := now.Add(r.opts.Retention)
		expiresAt = &t
	}
	return &Record{
		ID:              uuid.NewString(),
		TenantID:        tenantID,
		Stage:           material.Stage,
		KeyVersion:      material.KeyVersion,
		ArtifactKey:     key,
		ProcessorDigest: mustDigest(material.Processor),
		OutputDigest:    checksum,
		OutputSchema:    material.OutputSchema,
		Codec:           r.opts.Codec,
		Payload:         append([]byte(nil), payload...),
		PayloadChecksum: checksum,
		PayloadSize:     int64(len(payload)),
		CreatedAt:       now,
		ExpiresAt:       expiresAt,
	}
}

func mustDigest(v any) string {
	digest, err := CanonicalDigest(v)
	if err != nil {
		return ""
	}
	return digest
}
