package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/processingcache"
	"github.com/Tencent/WeKnora/internal/types"
)

var errImageCacheAdmission = errors.New("image output cannot be cached")

func processingModelIdentity(model any) string {
	if m, ok := model.(interface{ ProcessingCacheIdentity() string }); ok {
		return m.ProcessingCacheIdentity()
	}
	return ""
}

// Only complete action results enter the cache. A rejected cache candidate still
// follows the action's original partial-output/diagnostic behavior on this run.
func (r *runContext) cachedImagePrediction(ctx context.Context, prompt, key string, on bool) (string, error) {
	compute := func(ctx context.Context) (string, error) {
		value, err := r.model.PredictWithOptions(ctx, [][]byte{r.imageBytes}, prompt, &vlm.PredictOptions{Thinking: &on})
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return value, err
	}
	identity := processingModelIdentity(r.model)
	if identity == "" || r.payload == nil || r.processingCache == nil {
		return compute(ctx)
	}
	validateOutput := func(raw string) error {
		if !utf8.ValidString(raw) {
			return errors.New("invalid image action text")
		}
		switch {
		case strings.HasPrefix(key, "ocr."):
			_, err := validateOCRText(raw)
			return err
		case strings.HasPrefix(key, "observation."):
			obs, _ := types.ParseImageAttrsResponse(raw)
			for _, name := range types.InjectedImageAttrNames(types.PreprocessObservesGeometry(r.preprocessMode())) {
				if _, ok := obs.Attrs.Attrs[name]; !ok {
					return errors.New("incomplete image observation")
				}
			}
		default:
			if strings.TrimSpace(raw) == "" {
				return errors.New("empty image caption")
			}
		}
		return nil
	}
	validate := func(raw string) error {
		if err := validateOutput(raw); err != nil {
			return fmt.Errorf("%w: %v", errImageCacheAdmission, err)
		}
		return nil
	}
	var original string
	var computed bool
	var computeErr error
	out, err := processingcache.Value(ctx, r.processingCache, r.payload.TenantID, r.payload.KnowledgeID, "image.action.v1", struct {
		Image, Prompt, Action, Model string
		Thinking                     bool
	}{processingcache.Digest(r.imageBytes), prompt, key, identity, on}, validate, func(ctx context.Context) (string, error) {
		computed = true
		original, computeErr = compute(ctx)
		return original, computeErr
	})
	if err != nil && computed && computeErr == nil && ctx.Err() == nil {
		return original, nil
	}
	if errors.Is(err, errImageCacheAdmission) && !computed && ctx.Err() == nil {
		return compute(ctx)
	}
	return out, err
}

type imagePreprocessResult struct {
	Bytes    []byte
	Fallback bool
}

func (r *runContext) cachedImagePreprocess(ctx context.Context) ([]byte, bool, error) {
	compute := func(ctx context.Context) (imagePreprocessResult, error) {
		b, f, e := r.preprocessor.PreprocessImage(ctx, r.imageBytes)
		if ctx.Err() != nil {
			return imagePreprocessResult{}, ctx.Err()
		}
		return imagePreprocessResult{b, f}, e
	}
	identity := processingModelIdentity(r.preprocessor)
	if identity == "" || r.payload == nil || r.processingCache == nil {
		v, e := compute(ctx)
		return v.Bytes, v.Fallback, e
	}
	validateOutput := func(v imagePreprocessResult) error {
		if processingModelIdentity(r.preprocessor) != identity {
			return errors.New("preprocessor identity changed during call")
		}
		if v.Fallback {
			return errors.New("preprocessor declined: do not cache potentially transient fallback")
		}
		if len(v.Bytes) == 0 {
			return errors.New("empty preprocessed image")
		}
		_, _, e := image.Decode(bytes.NewReader(v.Bytes))
		return e
	}
	validate := func(v imagePreprocessResult) error {
		if err := validateOutput(v); err != nil {
			return fmt.Errorf("%w: %v", errImageCacheAdmission, err)
		}
		return nil
	}
	var original imagePreprocessResult
	var computed bool
	var computeErr error
	out, err := processingcache.Value(ctx, r.processingCache, r.payload.TenantID, r.payload.KnowledgeID, "image.preprocess.v1", struct{ Image, Engine string }{processingcache.Digest(r.imageBytes), identity}, validate, func(ctx context.Context) (imagePreprocessResult, error) {
		computed = true
		original, computeErr = compute(ctx)
		return original, computeErr
	})
	if err != nil && computed && computeErr == nil && ctx.Err() == nil {
		return original.Bytes, original.Fallback, nil
	}
	if errors.Is(err, errImageCacheAdmission) && !computed && ctx.Err() == nil {
		v, e := compute(ctx)
		return v.Bytes, v.Fallback, e
	}
	if !computed && processingModelIdentity(r.preprocessor) != identity {
		v, e := compute(ctx)
		return v.Bytes, v.Fallback, e
	}
	return out.Bytes, out.Fallback, err
}
