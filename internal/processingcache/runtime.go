// Package processingcache provides optional, tenant- and scope-isolated caches
// for deterministic processing stages. Stores must implement atomic insertion
// and version-conditional deletion.
package processingcache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var ErrMiss = errors.New("processing cache miss")

type Record struct {
	TenantID            uint64
	ScopeID, Stage, Key string
	Payload             []byte
	Checksum            string
	ExpiresAt           time.Time
}

type Store interface {
	Get(context.Context, uint64, string, string, string) (*Record, error)
	// PutIfAbsent returns the existing winner when another writer inserts first.
	PutIfAbsent(context.Context, *Record) (*Record, error)
	// Delete removes only the version whose checksum and expiry match the observed record.
	Delete(context.Context, *Record) error
}

type Options struct {
	ReadEnabled, WriteEnabled bool
	// MaxBytes <= 0 accepts payloads of any size; TTL <= 0 does not expire them.
	MaxBytes int
	// StageMaxBytes overrides the general budget for named large artifacts.
	StageMaxBytes map[string]int
	TTL           time.Duration
	Epoch         string
}

// Stats counts original input positions; Deduplicated counts positions sharing
// an earlier key. Bypass counts positions that cannot use the cache because it
// is disabled, inputs cannot be encoded, a store operation fails, a payload is
// oversized, or a stored record/winner is invalid. Bypass may overlap Misses;
// an ordinary cache miss is not a bypass. Each position is counted at most once.
type Stats struct{ Hits, Misses, Bypass, Deduplicated int }
type Runtime struct {
	store   Store
	opts    Options
	mu      sync.Mutex
	flights map[string]*flight
}

type flight struct {
	done  chan struct{}
	out   [][]byte
	stats Stats
	err   error
}

func New(store Store, opts Options) *Runtime {
	if opts.StageMaxBytes != nil {
		copied := make(map[string]int, len(opts.StageMaxBytes))
		for stage, limit := range opts.StageMaxBytes {
			copied[stage] = limit
		}
		opts.StageMaxBytes = copied
	}
	return &Runtime{store: store, opts: opts, flights: make(map[string]*flight)}
}

func (r *Runtime) PayloadLimit(stage string) int {
	if r == nil {
		return 0
	}
	if limit := r.opts.StageMaxBytes[stage]; limit > 0 {
		return limit
	}
	return r.opts.MaxBytes
}

// Enabled reports whether this runtime can read or write cached artifacts.
// Callers that stabilize model inputs must preserve their original requests
// when caching is disabled, including explicitly attached unusable runtimes.
func (r *Runtime) Enabled() bool {
	return r != nil && r.store != nil && (r.opts.ReadEnabled || r.opts.WriteEnabled)
}

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func identity(t uint64, s, g, k string) string {
	b, _ := json.Marshal([]any{t, s, g, k})
	return string(b)
}
func clone(b []byte) []byte { return append([]byte(nil), b...) }

func (r *Runtime) usable(rec *Record, t uint64, s, g, k string, validate func([]byte) error) bool {
	return rec != nil && rec.TenantID == t && rec.ScopeID == s && rec.Stage == g && rec.Key == k &&
		(rec.ExpiresAt.IsZero() || time.Now().Before(rec.ExpiresAt)) &&
		(r.PayloadLimit(g) <= 0 || len(rec.Payload) <= r.PayloadLimit(g)) && rec.Checksum == digest(rec.Payload) &&
		(validate == nil || validate(clone(rec.Payload)) == nil)
}

// Batch computes missing unique inputs in their original order. The compute
// callback must return one payload per supplied original index. All computed
// payloads are validated before any are persisted. Cache failures fail open;
// computation, validation, and context cancellation errors are returned.
// Identical concurrent batches on this Runtime share a result. Waiters may
// cancel independently; cancellation of the computing owner fails all waiters.
// Partially overlapping batches are not coalesced. Callers must include every
// behavior-affecting setting in inputs and use a consistent contract per stage.
func (r *Runtime) Batch(ctx context.Context, t uint64, s, g string, inputs []any, validate func([]byte) error, compute func(context.Context, []int) ([][]byte, error)) ([][]byte, Stats, error) {
	return r.batchFlight(ctx, t, s, g, inputs, validate, compute, false)
}

// BatchShared permits reuse from another scope in the same tenant. Use only
// when inputs fully describe a scope-independent result (such as embeddings).
// A reused artifact is copied into the current scope to preserve deletion rules.
func (r *Runtime) BatchShared(ctx context.Context, t uint64, s, g string, inputs []any, validate func([]byte) error, compute func(context.Context, []int) ([][]byte, error)) ([][]byte, Stats, error) {
	return r.batchFlight(ctx, t, s, g, inputs, validate, compute, true)
}
func (r *Runtime) batchFlight(ctx context.Context, t uint64, s, g string, inputs []any, validate func([]byte) error, compute func(context.Context, []int) ([][]byte, error), shared bool) ([][]byte, Stats, error) {
	if err := ctx.Err(); err != nil {
		return nil, Stats{}, err
	}
	if r.store == nil || (!r.opts.ReadEnabled && !r.opts.WriteEnabled) {
		return r.batch(ctx, t, s, g, inputs, validate, compute, shared)
	}
	material, err := json.Marshal(struct {
		Tenant              uint64
		Scope, Stage, Epoch string
		Inputs              []any
		Shared              bool
	}{t, s, g, r.opts.Epoch, inputs, shared})
	if err != nil {
		return r.batch(ctx, t, s, g, inputs, validate, compute, shared)
	}
	key := digest(material)
	r.mu.Lock()
	if f, ok := r.flights[key]; ok {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, Stats{}, ctx.Err()
		case <-f.done:
		}
		if err := ctx.Err(); err != nil {
			return nil, Stats{}, err
		}
		if f.err != nil {
			return nil, f.stats, f.err
		}
		// Each caller independently verifies its payload contract.
		for _, p := range f.out {
			if validate != nil {
				if err := validate(clone(p)); err != nil {
					return nil, f.stats, err
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, f.stats, err
		}
		return cloneBatch(f.out), f.stats, nil
	}
	f := &flight{done: make(chan struct{}), err: errors.New("processing cache: computation interrupted")}
	r.flights[key] = f
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.flights, key); close(f.done); r.mu.Unlock() }()
	f.out, f.stats, f.err = r.batch(ctx, t, s, g, inputs, validate, compute, shared)
	return cloneBatch(f.out), f.stats, f.err
}

func cloneBatch(in [][]byte) [][]byte {
	if in == nil {
		return nil
	}
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = clone(in[i])
	}
	return out
}

func (r *Runtime) batch(ctx context.Context, t uint64, s, g string, inputs []any, validate func([]byte) error, compute func(context.Context, []int) ([][]byte, error), shared bool) (result [][]byte, stats Stats, err error) {
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	out := make([][]byte, len(inputs))
	hits := make([]bool, len(inputs))
	if len(inputs) == 0 {
		return out, stats, nil
	}
	enabled := r.store != nil && (r.opts.ReadEnabled || r.opts.WriteEnabled)
	keys := make([]string, len(inputs))
	owners := make([]int, len(inputs))
	bypass := make([]bool, len(inputs))
	for i := range owners {
		owners[i] = -1
	}
	defer func() {
		for _, owner := range owners {
			if owner >= 0 && bypass[owner] {
				stats.Bypass++
			}
		}
	}()
	seen := map[string]int{}
	missing := []int{}
	for i, input := range inputs {
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
		owners[i] = i
		if !enabled {
			bypass[i] = true
			missing = append(missing, i)
			continue
		}
		material, err := json.Marshal(struct {
			Epoch, Stage string
			Input        any
		}{r.opts.Epoch, g, input})
		if err != nil {
			bypass[i] = true
			missing = append(missing, i)
			continue
		}
		key := digest(material)
		keys[i] = key
		if owner, ok := seen[key]; ok {
			owners[i] = owner
			stats.Deduplicated++
			continue
		}
		seen[key] = i
		if r.opts.ReadEnabled {
			rec, err := r.store.Get(ctx, t, s, g, key)
			if err := ctx.Err(); err != nil {
				return nil, stats, err
			}
			if err == nil && r.usable(rec, t, s, g, key, validate) {
				out[i] = clone(rec.Payload)
				hits[i] = true
				continue
			}
			if (err != nil && !errors.Is(err, ErrMiss)) || err == nil {
				bypass[i] = true
			}
			if r.opts.WriteEnabled && err == nil && rec != nil && rec.TenantID == t && rec.ScopeID == s && rec.Stage == g && rec.Key == key {
				_ = r.store.Delete(ctx, rec)
				if err := ctx.Err(); err != nil {
					return nil, stats, err
				}
			}
		}
		if shared && t != 0 && s != "" && r.opts.ReadEnabled {
			if source, ok := r.store.(interface {
				GetShared(context.Context, uint64, string, string) (*Record, error)
			}); ok {
				rec, err := source.GetShared(ctx, t, g, key)
				if ctx.Err() != nil {
					return nil, stats, ctx.Err()
				}
				if err == nil && rec != nil && rec.ScopeID != "" && r.usable(rec, t, rec.ScopeID, g, key, validate) {
					out[i], hits[i] = clone(rec.Payload), true
					if r.opts.WriteEnabled {
						copy := *rec
						copy.ScopeID, copy.Payload = s, clone(rec.Payload)
						winner, writeErr := r.store.PutIfAbsent(ctx, &copy)
						if ctx.Err() != nil {
							return nil, stats, ctx.Err()
						}
						if writeErr == nil && r.usable(winner, t, s, g, key, validate) {
							out[i] = clone(winner.Payload)
						} else {
							bypass[i] = true
						}
					}
					continue
				}
				if err != nil && !errors.Is(err, ErrMiss) || err == nil {
					bypass[i] = true
				}
			}
		}
		missing = append(missing, i)
	}
	// Record hit/miss counts before computing, including duplicate positions.
	for i := range inputs {
		if keys[i] == "" {
			continue
		}
		if hits[owners[i]] {
			stats.Hits++
		} else {
			stats.Misses++
		}
	}
	if len(missing) > 0 {
		payloads, err := compute(ctx, append([]int(nil), missing...))
		if err != nil {
			return nil, stats, err
		}
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
		if len(payloads) != len(missing) {
			return nil, stats, fmt.Errorf("processing cache: compute returned %d payloads for %d inputs", len(payloads), len(missing))
		}
		for j, payload := range payloads {
			if validate != nil {
				if err := validate(clone(payload)); err != nil {
					return nil, stats, fmt.Errorf("processing cache: invalid computed payload at input %d: %w", missing[j], err)
				}
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
		for j, i := range missing {
			payload := payloads[j]
			out[i] = clone(payload)
			if r.PayloadLimit(g) > 0 && len(payload) > r.PayloadLimit(g) {
				bypass[i] = true
			}
			if !enabled || !r.opts.WriteEnabled || keys[i] == "" || (r.PayloadLimit(g) > 0 && len(payload) > r.PayloadLimit(g)) {
				continue
			}
			rec := &Record{TenantID: t, ScopeID: s, Stage: g, Key: keys[i], Payload: clone(payload), Checksum: digest(payload)}
			if r.opts.TTL > 0 {
				rec.ExpiresAt = time.Now().Add(r.opts.TTL)
			}
			winner, err := r.store.PutIfAbsent(ctx, rec)
			if err := ctx.Err(); err != nil {
				return nil, stats, err
			}
			if err == nil && r.usable(winner, t, s, g, keys[i], validate) {
				out[i] = clone(winner.Payload)
			} else {
				bypass[i] = true
			}
		}
	}
	for i := range out {
		if owners[i] != i {
			out[i] = clone(out[owners[i]])
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	return out, stats, nil
}
