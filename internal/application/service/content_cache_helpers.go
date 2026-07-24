package service

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/contentcache"
	"github.com/Tencent/WeKnora/internal/models/chat"
)

func stableJSONHash(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return contentcache.TextHash(fmt.Sprintf("%#v", value))
	}
	return contentcache.TextHash(string(data))
}

func chatModelCacheKey(model chat.Chat) string {
	if model == nil {
		return "unknown"
	}
	return contentcache.TextHash(stableJSONHash(struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	}{
		Type: fmt.Sprintf("%T", model),
		ID:   model.GetModelID(),
		Name: model.GetModelName(),
	}))
}
