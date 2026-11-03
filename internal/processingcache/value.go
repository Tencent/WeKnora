package processingcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var errValueEncoding = errors.New("processing cache value encoding")

// Digest returns a stable SHA-256 JSON fingerprint, or an empty identity when
// the input cannot be encoded. Only the fingerprint should leave model factories.
func Digest(input any) string {
	b, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	return digest(b)
}

// Value caches one validated JSON value. Missing isolation identifiers bypass
// serialization and preserve the original computation path. Validators must
// explicitly reject null when null is not a valid stage result.
func Value[T any](
	ctx context.Context,
	r *Runtime,
	tenant uint64,
	scope, stage string,
	input any,
	validate func(T) error,
	compute func(context.Context) (T, error),
) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if r == nil || r.store == nil || (!r.opts.ReadEnabled && !r.opts.WriteEnabled) || tenant == 0 || scope == "" ||
		stage == "" {
		value, err := compute(ctx)
		if err != nil {
			return zero, err
		}
		if err = ctx.Err(); err != nil {
			return zero, err
		}
		if validate != nil {
			if err = validate(value); err != nil {
				return zero, err
			}
		}
		return value, nil
	}
	decode := func(b []byte) (T, error) {
		var v T
		err := json.Unmarshal(b, &v)
		if err == nil && validate != nil {
			err = validate(v)
		}
		return v, err
	}
	var computed T
	var encodingFailure error
	payloads, _, err := r.Batch(
		ctx,
		tenant,
		scope,
		stage,
		[]any{input},
		func(b []byte) error { _, e := decode(b); return e },
		func(c context.Context, _ []int) ([][]byte, error) {
			v, e := compute(c)
			if e != nil {
				return nil, e
			}
			if validate != nil {
				if e = validate(v); e != nil {
					return nil, e
				}
			}
			b, e := json.Marshal(v)
			if e == nil {
				// Check representation separately from the stage validator: a JSON
				// round-trip fault is a cache failure, a validator error is not.
				var decoded T
				e = json.Unmarshal(b, &decoded)
			}
			if e != nil {
				computed = v
				encodingFailure = e
				return nil, fmt.Errorf("%w: %v", errValueEncoding, e)
			}
			return [][]byte{b}, nil
		},
	)
	if encodingFailure != nil && ctx.Err() == nil {
		return computed, nil
	}
	if errors.Is(err, errValueEncoding) {
		return Value(ctx, nil, tenant, scope, stage, input, validate, compute)
	}
	if err != nil {
		return zero, err
	}
	return decode(payloads[0])
}
