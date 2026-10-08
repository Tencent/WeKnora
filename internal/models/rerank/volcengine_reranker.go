package rerank

import (
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/api"
	"github.com/Tencent/WeKnora/internal/models/api/volcengineknowledge"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
)

const (
	volcengineRerankDefaultModel       = volcengineknowledge.DefaultModel
	volcengineRerankDefaultRegion      = volcengineknowledge.DefaultRegion
	volcengineRerankDefaultInstruction = volcengineknowledge.DefaultInstruction
)

func newVolcengineClient(config *RerankerConfig, resolved *modelruntime.Resolved) (api.Reranker, error) {
	secret := strings.TrimSpace(config.AppSecret)
	if secret == "" {
		secret = config.ExtraConfig["secret_key"]
	}
	return volcengineknowledge.New(
		volcengineknowledge.Config{
			AccessKey:   config.APIKey,
			SecretKey:   secret,
			BaseURL:     resolved.BaseURL,
			Model:       resolved.RemoteModel,
			Region:      config.ExtraConfig["region"],
			Instruction: config.ExtraConfig["instruction"],
			Client: newRerankHTTPClient(
				30 * time.Second,
			),
		},
	)
}
