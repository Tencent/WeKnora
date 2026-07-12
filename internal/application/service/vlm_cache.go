package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/redis/go-redis/v9"
)

const vlmArtifactCacheVersion = "v1"

type cachedVLM struct {
	inner vlm.VLM
	redis *redis.Client
}

func cacheVLM(client *redis.Client, inner vlm.VLM) vlm.VLM {
	if client == nil || inner == nil {
		return inner
	}
	return &cachedVLM{inner: inner, redis: client}
}

func (v *cachedVLM) Predict(ctx context.Context, images [][]byte, prompt string) (string, error) {
	h := sha256.New()
	for _, image := range images {
		_, _ = h.Write(image)
		_, _ = h.Write([]byte{0})
	}
	_, _ = h.Write([]byte(prompt))
	key := "weknora:artifact:vlm:" + vlmArtifactCacheVersion + ":" + artifactModelKey(v.GetModelID(), v.GetModelName()) + ":" + hex.EncodeToString(h.Sum(nil))
	if cached, err := v.redis.Get(ctx, key).Result(); err == nil {
		return cached, nil
	}
	result, err := v.inner.Predict(ctx, images, prompt)
	if err != nil {
		return "", err
	}
	_ = v.redis.Set(ctx, key, result, artifactCacheTTL).Err()
	return result, nil
}

func (v *cachedVLM) GetModelName() string { return v.inner.GetModelName() }
func (v *cachedVLM) GetModelID() string   { return v.inner.GetModelID() }
